package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/model"
)

// The result of an action comes back to the document that caused it
// (ADR-0045, item 8). A rule's action may carry into, a map from input
// variables to formulas over what the action returns, with OK and ERROR.
// When the row is done or dead, the worker evaluates them and writes the
// values as a set on the document, in the transaction that marks the
// row: a commit of its own with an event of kind set, caused by the
// event of the action's outcome. It is the engine's write, as a rule's
// set is, so no write right is consulted. Like any set it raises
// values_change and is followed by the eventless transitions, so a
// process goes on from the answer, and from a failure too. A document
// that is frozen or gone, or that refuses a value, leaves the action's
// outcome as it stands, and the row says why the result was not stored.

// intoOf finds the action of a row in the version of the class whose
// rule queued it, when that action writes its result back; nil
// otherwise. A row of a class no version of which writes anything back,
// which is most rows, costs one look at a map: the place of the action in
// the definition is read only for the others.
func (s *Store) intoOf(ctx context.Context, row *outboxRow) (*model.Action, error) {
	s.cmu.RLock()
	may := s.writesBack[row.class]
	s.cmu.RUnlock()
	if !may {
		return nil, nil
	}
	var version int
	var rule string
	if err := s.writer.QueryRowContext(ctx, `SELECT class_version, rule FROM outbox WHERE event_id = ? AND idx = ?`, row.eventID, row.idx).Scan(&version, &rule); err != nil {
		return nil, storageErr("read the action's place in its class", err)
	}
	c := s.classAt(row.class, version)
	if c == nil {
		return nil, nil
	}
	var ri, ai int
	if n, _ := fmt.Sscanf(rule, "/rules/%d/do/%d", &ri, &ai); n != 2 || ri < 0 || ri >= len(c.cls.Rules) {
		return nil, nil
	}
	for _, a := range c.cls.Rules[ri].Actions {
		if a.Index == ai && len(a.Into) > 0 {
			return a, nil
		}
	}
	return nil, nil
}

// applyInto writes what an action returned back to the document whose
// commit queued it, inside the transaction that has just marked the row
// done or dead with the event outcomeEvent. made is the document the
// commit of an engine action left, from which what a create and a
// transition return is read. It reports whether the write queued further
// actions. An error is the store's: the caller undoes the transaction. A
// refusal of the document is none: it is recorded on the row and the
// action's outcome stands.
func (s *Store) applyInto(ctx context.Context, row *outboxRow, outcomeEvent string, did outcome, made *Document, day int64, now time.Time) (bool, error) {
	a, err := s.intoOf(ctx, row)
	if a == nil || err != nil {
		return false, err
	}
	if made != nil {
		switch row.action {
		case model.ActionCreate:
			did.result = Values{"id": made.ID}
		case model.ActionTransition:
			did.result = Values{"state": made.State, "version": float64(made.Version)}
		}
	}
	// What the formulas read: the returned values, typed as the action
	// declares them and blank when it did not run; OK; and ERROR, the code
	// it failed with, or bad_result when it ran and what it returned could
	// not be read.
	var refused *Error
	code := ""
	switch {
	case did.failure != nil:
		code = did.failure.Code
	case did.badResult != nil:
		code, refused = did.badResult.Code, did.badResult
	}
	names := make([]string, 0, len(a.Returns)+2)
	vals := make([]formula.Value, 0, len(a.Returns)+2)
	for _, r := range a.Returns {
		val := formula.Blank()
		if code == "" {
			if v, err := toValue(did.result[r.Name], r.Type, "", r.Name, s.limits); err == nil {
				val = v
			}
		}
		names, vals = append(names, r.Name), append(vals, val)
	}
	failedWith := formula.Blank()
	if code != "" {
		failedWith = formula.Text(code)
	}
	names = append(names, model.ResultOK, model.ResultError)
	vals = append(vals, formula.Bool(did.failure == nil), failedWith)

	e := s.reader()
	values := make(Values, len(a.Into))
	for _, iv := range a.Into {
		val, err := e.over(iv.Formula, names, vals)
		if err != nil {
			refused = evalErr(iv.Name, err)
			values = nil
			break
		}
		values[iv.Name] = valueOut(val)
	}
	s.release(e)
	queued := false
	if values != nil {
		var wrote *Error
		var err error
		if queued, wrote, err = s.writeBack(ctx, row, outcomeEvent, values, day, now); err != nil {
			return false, err
		}
		if wrote != nil {
			refused = wrote
		}
	}
	if refused != nil {
		message := refused.Message
		if refused.Variable != "" {
			message = refused.Variable + ": " + message
		}
		if _, err := s.writer.ExecContext(ctx, `UPDATE outbox SET into_code = ?, into_message = ? WHERE event_id = ? AND idx = ?`, refused.Code, message, row.eventID, row.idx); err != nil {
			return false, storageErr("record why the result was not stored", err)
		}
	}
	return queued, nil
}

// writeBack makes the commit that stores a result: a set on the document,
// under the class as it is now, caused by the event of the action's
// outcome, on that event's business day, one deeper in the chain than the
// action (ADR-0044, item 6). A refusal of the set undoes the set alone.
func (s *Store) writeBack(ctx context.Context, row *outboxRow, outcomeEvent string, values Values, day int64, now time.Time) (bool, *Error, error) {
	c, e := s.class(row.class)
	if e != nil {
		return false, e, nil
	}
	cmd := command{op: opSet, c: c, id: row.docID, values: values,
		o:     options{principal: row.principal, key: outboxKeyPrefix + "into:" + outcomeEvent},
		cause: outcomeEvent, depth: row.depth + 1, internal: true, engine: true, day: day, hasDay: true}
	w := s.writer
	if _, err := w.ExecContext(ctx, "SAVEPOINT fado_into"); err != nil {
		return false, nil, storageErr("begin the write of the result", err)
	}
	res, err := s.run(ctx, cmd, now)
	if err != nil {
		if _, rerr := w.ExecContext(ctx, "ROLLBACK TO fado_into"); rerr != nil {
			return false, nil, storageErr("undo the write of the result", rerr)
		}
		if _, rerr := w.ExecContext(ctx, "RELEASE fado_into"); rerr != nil {
			return false, nil, storageErr("undo the write of the result", rerr)
		}
		if refused := asError(err); !noAnswer(refused) {
			return false, refused, nil
		}
		return false, nil, err
	}
	if _, err := w.ExecContext(ctx, "RELEASE fado_into"); err != nil {
		return false, nil, storageErr("end the write of the result", err)
	}
	return res.queued > 0, nil, nil
}

// over runs a pure formula whose slots are the names given, with their
// values; a slot of any other name is blank.
func (e *evaluator) over(prog *formula.Program, names []string, vals []formula.Value) (formula.Value, error) {
	if cap(e.vars) < len(prog.Slots) {
		e.vars = make([]formula.Value, len(prog.Slots))
	}
	vars := e.vars[:len(prog.Slots)]
	for i, slot := range prog.Slots {
		vars[i] = formula.Blank()
		for j, name := range names {
			if strings.EqualFold(name, slot.Name) {
				vars[i] = vals[j]
				break
			}
		}
	}
	return e.vm.Run(prog, vars, &e.ctx)
}
