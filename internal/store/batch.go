package store

import (
	"context"
	"fmt"
	"strings"
)

// CreateMany makes documents of one class in one transaction, one commit
// each with its own history row and event, all or none: a bulk load pays
// one fsync for the batch (ADR-0035). The options apply to every
// document; a batch takes no idempotency key, since a key names one
// command, and no caller's id, since the documents are many. An error
// names the document by its position, "[3].number".
func (s *Store) CreateMany(ctx context.Context, class string, docs []Values, opts ...Option) ([]*Document, error) {
	c, e := s.class(class)
	if e != nil {
		return nil, e
	}
	o := apply(opts)
	if o.key != "" {
		return nil, hintf(CodeNotSupported, "", "a batch takes no idempotency key; a key names one command (ADR-0026)", "give each document its own commit with Create, or load without a key")
	}
	if err := refuseAt(o, "batch"); err != nil {
		return nil, err
	}
	if o.id != "" {
		return nil, hintf(CodeBadValue, "", "a batch takes no document id", "ids are generated for every document of a batch")
	}
	if len(docs) == 0 {
		return []*Document{}, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errf(CodeClosed, "", "the store is closed")
	}
	now := s.now()
	if _, err := s.writer.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return nil, storageErr("begin", err)
	}
	out := make([]*Document, 0, len(docs))
	queued := 0
	for i, values := range docs {
		res, err := s.run(ctx, command{op: opCreate, c: c, values: values, o: o}, now)
		if err != nil {
			_, _ = s.writer.ExecContext(ctx, "ROLLBACK")
			if se, ok := err.(*Error); ok {
				at := fmt.Sprintf("[%d]", i)
				if se.Variable != "" {
					at += "." + se.Variable
				}
				se.Variable = at
			}
			return nil, err
		}
		out = append(out, res.doc)
		queued += res.queued
	}
	if _, err := s.writer.ExecContext(ctx, "COMMIT"); err != nil {
		_, _ = s.writer.ExecContext(ctx, "ROLLBACK")
		return nil, storageErr("commit", err)
	}
	s.wakeQueued(queued > 0, false)
	return out, nil
}

// MaxMany is the most documents one TransitionMany or PreviewMany takes
// (ADR-0048, item 10): the cap of a list (ADR-0013).
const MaxMany = MaxElements

// manyKeySep stands between the key of a batch of moves and the id of a
// document in the key of that document's commit (ADR-0048, item 9).
const manyKeySep = "#"

// MoveOutcome is what became of one document of a batch of moves
// (ADR-0048, item 4): the document after its move, or the refusal.
type MoveOutcome struct {
	ID       string    `json:"id"`
	Document *Document `json:"document,omitempty"`
	Refusal  *Error    `json:"refusal,omitempty"`
}

// Moved says whether the document moved.
func (m *MoveOutcome) Moved() bool { return m.Refusal == nil && m.Document != nil }

// TransitionMany moves the documents of one class named by ids along the
// named transition, in the order given, in one transaction (ADR-0048).
// Each move is the commit Transition makes, with the assignments in
// values, which may be nil, and its own version, history row and event.
// A document that cannot move is refused with the code Transition would
// give it and left as it was, and the others move: the outcomes say
// which, one for each id in the order given. The error is the call's
// own: unknown_class, unknown_transition, too_many_elements for more
// than MaxMany ids, bad_value for an id named twice, not_allowed for At,
// or a failure of the database, after which nothing has moved.
//
// Key gives the commit of each document the key with the document's id
// after it, so that the batch sent again moves no document twice. As and
// BusinessDate apply to every document.
func (s *Store) TransitionMany(ctx context.Context, class, name string, ids []string, values Values, opts ...Option) ([]MoveOutcome, error) {
	return s.moveMany(ctx, class, name, ids, values, apply(opts), false)
}

// PreviewMany runs the batch TransitionMany would run and rolls it back
// (ADR-0048, item 11): the outcomes the call would have now, or at the
// moment given with At, and nothing written, no action queued. It writes
// inside its transaction before taking everything back, so that a
// document sees the ones moved before it as it would in the batch; it
// holds the writer as long as the batch would.
func (s *Store) PreviewMany(ctx context.Context, class, name string, ids []string, values Values, opts ...Option) ([]MoveOutcome, error) {
	return s.moveMany(ctx, class, name, ids, values, apply(opts), true)
}

func (s *Store) moveMany(ctx context.Context, class, name string, ids []string, values Values, o options, preview bool) ([]MoveOutcome, error) {
	c, e := s.class(class)
	if e != nil {
		return nil, e
	}
	if _, e := namedTransition(c, name); e != nil {
		return nil, e
	}
	if e := checkPrincipal(o.principal); e != nil {
		return nil, e
	}
	if o.principal.For != "" {
		if e := s.actsFor(ctx, &o, c.cls.Name); e != nil {
			return nil, e
		}
	}
	if !preview {
		if err := refuseAt(o, "batch"); err != nil {
			return nil, err
		}
	}
	if strings.HasPrefix(o.key, outboxKeyPrefix) {
		return nil, hintf(CodeBadValue, "", "a key may not start with "+outboxKeyPrefix+", the prefix of the commits the outbox makes", "choose another key")
	}
	if len(ids) > MaxMany {
		return nil, hintf(CodeTooManyElements, "", fmt.Sprintf("a batch of %d documents; one call takes at most %d", len(ids), MaxMany), "send them in several calls; 1,000 documents a call is a good size")
	}
	if len(ids) == 0 {
		return []MoveOutcome{}, nil
	}
	seen := make(map[string]int, len(ids))
	for i, id := range ids {
		if j, twice := seen[id]; twice {
			return nil, hintf(CodeBadValue, "", fmt.Sprintf("the document %s is named twice, at [%d] and [%d]", id, j, i), "name each document once")
		}
		seen[id] = i
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errf(CodeClosed, "", "the store is closed")
	}
	now := s.now()
	if _, err := s.writer.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return nil, storageErr("begin", err)
	}
	// undo takes the whole batch back. The moment of the earliest wake-up
	// its commits queued goes with it.
	undo := func() {
		_, _ = s.writer.ExecContext(ctx, "ROLLBACK")
		s.wakeAt = 0
	}
	out := make([]MoveOutcome, len(ids))
	cmd := command{op: opTransition, c: c, transition: name, values: values, o: o}
	queued := 0
	// The mark is the savepoint before a document. A refused document
	// returns to it and leaves it standing for the next one.
	marked := false
	for i, id := range ids {
		out[i].ID = id
		if !marked {
			if _, err := s.stmts.mark.ExecContext(ctx); err != nil {
				undo()
				return nil, storageErr("mark the batch", err)
			}
			marked = true
		}
		cmd.id = id
		if o.key != "" {
			cmd.o.key = o.key + manyKeySep + id
		}
		res, err := s.run(ctx, cmd, now)
		if err == nil && s.testMoved != nil {
			if late := s.testMoved(id); late != nil {
				err = late
			}
		}
		if err != nil {
			refusal, ok := err.(*Error)
			if !ok || refusal.Code == CodeStorage || ctx.Err() != nil {
				undo()
				return nil, err
			}
			if _, err := s.stmts.toMark.ExecContext(ctx); err != nil {
				undo()
				return nil, storageErr("take a refused document back", err)
			}
			out[i].Refusal = refusal
			continue
		}
		if _, err := s.stmts.unmark.ExecContext(ctx); err != nil {
			undo()
			return nil, storageErr("release the mark", err)
		}
		marked = false
		out[i].Document = res.doc
		queued += res.queued
	}
	if preview {
		undo()
		return out, nil
	}
	if _, err := s.writer.ExecContext(ctx, "COMMIT"); err != nil {
		undo()
		return nil, storageErr("commit", err)
	}
	s.wakeQueued(queued > 0, false)
	return out, nil
}
