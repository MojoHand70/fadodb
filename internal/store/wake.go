package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"time"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/state"
)

// The wake-up (ADR-0047, items 10 to 12). A timed transition fires in the
// chain of any commit made once its time has come. What the engine adds
// is that commit: when a document enters a state, the same transaction
// queues a row of the outbox for each timed transition that leaves it,
// due at its moment, and when a commit changes a value an at reads it
// queues one for the new moment. When a row is due the worker looks at
// the document. If it is gone, in another state or in a later stay, the
// row is done and nothing is written. Otherwise the worker runs the
// chain: what fires is written, each transition a version with its event,
// and when nothing fires, because the moment has moved to later or a
// guard beside the timer does not hold, nothing is written either. The
// touch and the mark are one transaction, so a timer fires exactly once;
// and since any commit fires a timer that has come, one that the worker
// missed fires at the next commit, and a stopped worker loses nothing.

// wakeArgs is what a wake-up row says of itself: the state whose stay it
// was queued for, and the transition whose moment it waits for, by its
// target and its name when it has one. The moment is the row's due_at.
type wakeArgs struct {
	State string `json:"state"`
	To    string `json:"to"`
	Name  string `json:"name,omitempty"`
}

// queueWakes writes, in the commit's transaction, the wake-ups of the
// stay the command leaves the document in: a row for each timed
// transition leaving its state whose moment is still to come. A moment
// that has come was asked in the command's own chain. For a stay the
// command began, fresh, that is every such transition. For a stay it
// found, only what the command changed: an at whose moment is another
// than it was, and every timer of a document the command adopted from an
// older version of the class, which may not have had it. The row carries
// the command's principal: a wake-up acts as the person whose commit
// queued it (ADR-0044, item 11). eventID is the event of the command's
// last commit, final the row it wrote and queued how many rows of the
// outbox that event has already; it returns how many it added.
func (s *Store) queueWakes(ctx context.Context, c *class, cmd *command, beforeClass *class, before, final *docRow, fresh bool, evalCtx *formula.Context, eventID string, queued int, secs int64) (int, error) {
	if c.cls.States == nil || c.cls.States.IsFinal(final.state) {
		return 0, nil
	}
	evalCtx.State = final.state
	var groups []byte
	n := 0
	for i, t := range c.cls.Transitions {
		if !t.Timed() || !state.From(t, final.state) {
			continue
		}
		at, ok := s.timerOf(c, t, final, evalCtx, false)
		if !ok || at <= evalCtx.Now {
			continue
		}
		if !fresh && beforeClass == c {
			if t.After > 0 {
				// The stay and its length are what they were.
				continue
			}
			if was, ok := s.timerOf(c, t, before, evalCtx, false); ok && was == at {
				continue
			}
		}
		args, err := json.Marshal(wakeArgs{State: final.state, To: t.To, Name: t.Name})
		if err != nil {
			return n, storageErr("queue the wake-up", err)
		}
		p := cmd.o.principal
		if groups == nil {
			groups, _ = json.Marshal(p.Groups)
		}
		admin := 0
		if p.Admin {
			admin = 1
		}
		// Time breaks a chain of actions: a wake-up has depth 0.
		if _, err := s.stmts.insertOutbox.ExecContext(ctx, eventID, queued+n, c.cls.Name, final.id, final.version, c.cls.Version, "/transitions/"+strconv.Itoa(i), ActionTimer, string(args),
			nullText(p.ID), string(groups), admin, 0, ActionPending, at, nil, nil, secs, nil, nil, nullText(p.Kind), nullText(p.For)); err != nil {
			return n, storageErr("queue the wake-up", err)
		}
		if s.wakeAt == 0 || at < s.wakeAt {
			s.wakeAt = at
		}
		n++
	}
	return n, nil
}

// wakeQueued tells the worker of what a transaction queued, once it is
// committed, under the writer's lock. An action is due at once, so the
// worker is always told. A wake-up waits for its moment, and the worker
// is told only when that comes before it next looks: it is not when the
// worker sleeps until an earlier second, at which it will find the row,
// nor when the transaction was the worker's own, own, since it asks what
// falls due next after every look. A commit of a class with timers so
// costs the worker nothing, where waking it costs three queries.
func (s *Store) wakeQueued(actions, own bool) {
	at := s.wakeAt
	s.wakeAt = 0
	if actions {
		s.wakeWorker()
		return
	}
	if at == 0 || own {
		return
	}
	if until := s.sleepUntil.Load(); until == 0 || at < until {
		s.wakeWorker()
	}
}

// touch is what a wake-up row stands for, inside the worker's
// transaction (ADR-0047, item 11): the touch of the document the row was
// queued for, as the principal the row carries. It returns the event of
// the first transition that fired, "" when none did, and reports whether
// the touch queued further rows. A document that is gone, in another
// state or in a later stay is left alone: the stay the row waited in has
// ended, and a later one has wake-ups of its own.
func (s *Store) touch(ctx context.Context, row *outboxRow, now time.Time) (event string, queuedMore bool, failure *Error) {
	var args wakeArgs
	if err := json.Unmarshal(row.args, &args); err != nil {
		return "", false, errf(CodeBadValue, "", "the arguments of the wake-up do not read: %v", err)
	}
	c, e := s.class(row.class)
	if e != nil {
		// The class is gone, and its documents with it.
		return "", false, nil
	}
	// The document as it stands, and what the row says of the commit
	// that queued it: when, and which version it made. Only a wake-up
	// reads those, so the worker's look at what is due does not carry
	// them for every row.
	var in sql.NullString
	var entered sql.NullInt64
	var version, created int64
	err := s.writer.QueryRowContext(ctx, `SELECT d."state", d."state_entered_at", o.version, o.created_at FROM outbox o JOIN `+quote(c.table)+` d ON d."id" = o.doc_id WHERE o.event_id = ? AND o.idx = ?`,
		row.eventID, row.idx).Scan(&in, &entered, &version, &created)
	switch {
	case err == sql.ErrNoRows:
		return "", false, nil
	case err != nil:
		return "", false, asError(storageErr("read the document", err))
	case in.String != args.State || entered.Int64 > created:
		return "", false, nil
	case entered.Int64 == created:
		// The stay began in the second the row was queued in: it is the
		// row's own unless the document has moved since the version the
		// row was queued with, back into the same state.
		var moved int
		if err := s.writer.QueryRowContext(ctx, `SELECT count(*) FROM events WHERE class = ? AND doc_id = ? AND ts >= ? AND kind = ? AND after_version > ?`,
			c.cls.Name, row.docID, created, EventTransition, version).Scan(&moved); err != nil {
			return "", false, asError(storageErr("read the document's moves", err))
		}
		if moved > 0 {
			return "", false, nil
		}
	}
	// The touch: no commit of its own, and the chain. The business date
	// of what fires is the day it fires, by the database's clock.
	cmd := command{op: opWake, c: c, id: row.docID, o: options{principal: row.principal, key: outboxKeyPrefix + row.key()},
		cause: row.eventID, depth: row.depth + 1, internal: true}
	res, rerr := s.run(ctx, cmd, now)
	if rerr != nil {
		return "", false, asError(rerr)
	}
	if res.noop {
		return "", false, nil
	}
	return res.eventID, res.queued > 0, nil
}

// woke marks a wake-up done, in the worker's transaction. It appends no
// event of its own (ADR-0047, item 11): when a transition fired, the
// event of that move is the record, and the row names it as its outcome;
// when nothing was to do, nothing happened to the document.
func (s *Store) woke(ctx context.Context, row *outboxRow, attempts int, event string, now time.Time) error {
	if _, err := s.writer.ExecContext(ctx, `UPDATE outbox SET status = ?, attempts = ?, finished_at = ?, error_code = NULL, error_message = NULL, outcome_event = ?, into_code = NULL, into_message = NULL WHERE event_id = ? AND idx = ?`,
		ActionDone, attempts, now.Unix(), nullText(event), row.eventID, row.idx); err != nil {
		return storageErr("mark the wake-up", err)
	}
	return nil
}
