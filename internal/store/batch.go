package store

import (
	"context"
	"fmt"
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
	if queued > 0 {
		s.wakeWorker()
	}
	return out, nil
}
