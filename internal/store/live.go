package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// LiveValue is what a live variable is for one reader at one moment
// (ADR-0076): a single value in the wire shape of its type.
type LiveValue struct {
	Value any `json:"value"`
}

// Live works a live variable of a document out for its caller, and
// keeps nothing (ADR-0076, items 1, 3 and 7). A live variable says
// "mode": "live" in its class: it has no column, no history and no
// event, so no other call answers it and no commit recalculates it. It
// is read by whoever may read the document, and the variable by its
// read right in the document's state; a content administrator reads
// past either only with a reason, which the log of reads keeps
// (ADR-0067). The formula runs over the stored document on the writer's
// VM, which reads what the document's life says of its states, so the
// read takes the writer's turn; it is short.
func (s *Store) Live(ctx context.Context, class, id, variable string, opts ...Option) (*LiveValue, error) {
	c, e := s.class(class)
	if e != nil {
		return nil, e
	}
	o, e := s.asked(ctx, opts, c.cls.Name)
	if e != nil {
		return nil, e
	}
	cv, ok := c.exact[variable]
	if !ok {
		cv, ok = c.byName[strings.ToUpper(variable)]
	}
	if !ok {
		return nil, errf(CodeUnknownVariable, variable, "the class %s has no variable %s", c.cls.Name, variable)
	}
	if !cv.v.Live() {
		return nil, hintf(CodeNotAllowed, cv.v.Name, cv.v.Name+" is no live variable: its value is kept with the document",
			`read the document with get; a live variable says "mode": "live" in its class`)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errf(CodeClosed, "", "the store is closed")
	}
	row, err := c.scanW(c.w.selectCurrent.QueryRowContext(ctx, id), false)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, noDocument(c, id)
		}
		return nil, storageErr("read the document", err)
	}
	ctxOf := contextOf(o.principal, s.clock(o))
	evalCtx := &ctxOf
	evalCtx.State, evalCtx.ID = row.state, row.id
	// A document the caller may not see does not exist for them
	// (ADR-0049, item 3).
	var r *readLog
	past := func(what, path string) *Error {
		if !o.principal.content() || c.brought {
			return nil
		}
		if o.reason == "" {
			return reasonToRead(o.principal, what, path)
		}
		if r == nil {
			r = newRead("live", c)
		}
		r.saw(id, path)
		return nil
	}
	if ok, _ := s.wev.may(c, ruleRead, row, evalCtx); !ok && !(c.brought && o.principal.content()) {
		if !o.principal.content() {
			return nil, noDocument(c, id)
		}
		if e := past("the document "+id+" of "+c.cls.Name, "/access/read"); e != nil {
			return nil, e
		}
	}
	// The read right of the variable, in the document's state (ADR-0041).
	if c.rights != nil {
		if rule := c.cells(row.state)[cv.idx].read; rule != nil {
			if held, rerr := s.wev.allowed(c, rule, row, evalCtx); !held {
				if !o.principal.content() || c.brought {
					msg := fmt.Sprintf("%s may not read %s", who(o.principal), cv.v.Name)
					if row.state != "" {
						msg += " while the document is in " + row.state
					}
					if rerr != nil {
						msg += ": the read right failed, " + guardFailure(rerr)
					}
					return nil, hintf(CodeForbidden, cv.v.Name, msg, "the read right of "+cv.v.Name+", from the cell "+rule.Cell()+": "+ruleText(rule))
				}
				if e := past(cv.v.Name+" of the document "+id+" of "+c.cls.Name, rightRead(row.state, cv.v.Name)); e != nil {
					return nil, e
				}
			}
		}
	}
	val, err := s.wev.run(c, cv.v.Formula, row, evalCtx)
	if err != nil {
		return nil, evalErr(cv.v.Name, err)
	}
	if err := s.logReadHeld(ctx, o, r); err != nil {
		return nil, err
	}
	return &LiveValue{Value: valueOut(val)}, nil
}
