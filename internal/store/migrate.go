package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/model"
)

// Class versions and migrations (ADR-0039). A redefinition makes a new
// version of the class and touches no document: every row records the
// class version that computed it and is read under that version. A live
// document adopts the current version at its next commit; Migrate does
// it on purpose for every live document. A terminal document never
// changes. What a redefinition may do is decided against the documents
// the store holds, in redefinitionDiagnostics.

// redefinitionDiagnostics says what a new version may not do to the
// class's documents: remove or retype a variable that holds values,
// remove a state documents are in, or make a state live again while
// documents are in it. The counts read through the pool, so that
// Validate needs no writer.
func (s *Store) redefinitionDiagnostics(ctx context.Context, old *class, cls *model.Class) model.Diagnostics {
	var diags model.Diagnostics
	count := func(query string, args ...any) int64 {
		var n int64
		if err := s.db.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
			return -1
		}
		return n
	}
	// Values of a variable in the current rows, and in the history for a
	// type change, since history is read under its version and a column
	// cannot hold two types.
	valuesOf := func(name string, history bool) int64 {
		cv := old.byName[strings.ToUpper(name)]
		if cv == nil {
			return 0
		}
		if cv.child != nil {
			if history {
				return count("SELECT count(*) FROM " + quote(cv.child.table))
			}
			return count(`SELECT count(*) FROM ` + quote(cv.child.table) + ` c JOIN ` + quote(old.table) + ` d ON d."id" = c."doc_id" AND d."version" = c."version"`)
		}
		var terms []string
		for _, li := range cv.leaves {
			terms = append(terms, quote(old.leaves[li].column)+" IS NOT NULL")
		}
		if len(terms) == 0 {
			return 0
		}
		where := " WHERE " + strings.Join(terms, " OR ")
		n := count("SELECT count(*) FROM " + quote(old.table) + where)
		if history {
			n += count("SELECT count(*) FROM " + quote(old.history) + where)
		}
		return n
	}
	for _, ch := range model.Diff(old.cls, cls) {
		switch ch.Kind {
		case model.ChangeRemoveVariable:
			if n := valuesOf(ch.Name, false); n != 0 {
				diags = append(diags, model.Diagnostic{Code: model.CodeNotAllowed, Path: ch.Path, Pos: -1,
					Message: fmt.Sprintf("%d documents hold a value for %s; a variable with values is not removed", n, ch.Name),
					Hint:    "blank the values first, or keep the variable"})
			}
		case model.ChangeRetypeVariable:
			if n := valuesOf(ch.Name, true); n != 0 {
				diags = append(diags, model.Diagnostic{Code: model.CodeNotAllowed, Path: ch.Path, Pos: -1,
					Message: fmt.Sprintf("%d current or past rows hold a value for %s; a variable with values, now or in history, does not change type", n, ch.Name),
					Hint:    "add a variable of the new type beside it"})
			}
		case model.ChangeRemoveState:
			if n := count("SELECT count(*) FROM "+quote(old.table)+` WHERE "state" = ?`, ch.Name); n != 0 {
				diags = append(diags, model.Diagnostic{Code: model.CodeNotAllowed, Path: ch.Path, Pos: -1,
					Message: fmt.Sprintf("%d documents are in the state %s; a state with documents is not removed", n, ch.Name)})
			}
		case model.ChangeUnfinalState:
			if n := count("SELECT count(*) FROM "+quote(old.table)+` WHERE "state" = ?`, ch.Name); n != 0 {
				diags = append(diags, model.Diagnostic{Code: model.CodeNotAllowed, Path: ch.Path, Pos: -1,
					Message: fmt.Sprintf("%d documents are in the terminal state %s; a terminal state stays terminal while documents are in it", n, ch.Name)})
			}
		}
	}
	return diags
}

// brokenRules says which rules of other classes a new version of a class,
// of a table or of an action definition would break (ADR-0044, item 7),
// and which of their formulas that read the class (ADR-0046, item 11).
// with names the classes one call defines together (ADR-0046, item 12):
// they compile against each other, so none of them is among the others:
// an action of theirs names a variable, a transition, a column or a
// parameter the new version lacks, or gives one a value its new type does
// not take. Each is refused with not_allowed naming the rule, and the fix
// is to change that rule first. It compiles every class whose rules act
// on the target against the stored definitions and against the new one,
// and reports what only the new one breaks. An input the new version
// requires and a create does not give, and a parameter it requires and a
// call does not give, are not among them: that rule cannot be changed
// first, since the variable or the parameter does not exist yet, and its
// action is refused with required when it runs. One of cls, tbl and act
// is given. The caller holds cmu.
func (s *Store) brokenRules(cls *model.Class, tbl *model.Table, act *model.ActionSpec, with ...string) model.Diagnostics {
	env := s.env()
	next, target := env, ""
	switch {
	case cls != nil:
		target = cls.Name
		next.Classes = func(name string) (*model.Class, bool) {
			if strings.EqualFold(name, cls.Name) {
				return cls, true
			}
			return env.Classes(name)
		}
	case tbl != nil:
		target = tbl.Name
		set := formula.NewSourceSet()
		for _, name := range s.tableOrder {
			set.Add(s.tables[strings.ToUpper(name)].src)
		}
		set.Add(tbl.Source())
		next.Sources = set
	default:
		target = act.Name
		next.Actions = func(name string) (*model.ActionSpec, bool) {
			if strings.EqualFold(name, act.Name) {
				return act, true
			}
			return env.Actions(name)
		}
	}
	var out model.Diagnostics
	for _, name := range s.order {
		dep := s.classes[strings.ToUpper(name)]
		// The rules of the class itself compile with its new definition,
		// and so do those of the other classes of the same call.
		if cls != nil && strings.EqualFold(name, target) || definedAmong(with, name) {
			continue
		}
		// A class that reads the documents of the redefined one has its
		// formulas compiled against the new columns too (ADR-0046).
		reader := cls != nil && reads(dep.cls, target)
		if !reader && (act != nil && !calls(dep.cls, target) || act == nil && !actsOn(dep.cls, target, tbl != nil)) {
			continue
		}
		_, before := model.DefineIn(dep.json, env)
		_, after := model.DefineIn(dep.json, next)
		had := map[string]bool{}
		for _, d := range before {
			had[d.Code+" "+d.Path] = true
		}
		for _, d := range after {
			if had[d.Code+" "+d.Path] {
				continue
			}
			if !strings.HasPrefix(d.Path, "/rules/") {
				// Outside the rules only a formula can break: a variable,
				// a default, a guard, an access rule or a right that reads
				// the class.
				if reader && (d.Code == model.CodeBadFormula || d.Code == model.CodeTypeMismatch) {
					out = append(out, model.Diagnostic{Code: model.CodeNotAllowed, Path: "/variables", Pos: -1,
						Message: "it would break a formula of the class " + dep.cls.Name + ", at " + d.Path + ": " + d.Message,
						Hint:    "change that formula first; a redefinition may not break a formula of another class that reads this one (ADR-0046)"})
				}
				continue
			}
			if d.Code == model.CodeMissing && (strings.HasSuffix(d.Path, "/values") || strings.HasSuffix(d.Path, "/args")) {
				continue
			}
			out = append(out, model.Diagnostic{Code: model.CodeNotAllowed, Path: changedPath(d.Path), Pos: -1,
				Message: "it would break a rule of the class " + dep.cls.Name + ", at " + d.Path + ": " + d.Message,
				Hint:    "change that rule first; a redefinition may not break a rule of another class (ADR-0044)"})
		}
	}
	return out
}

// definedAmong reports whether a class of that name is among the ones a
// call defines.
func definedAmong(names []string, name string) bool {
	for _, n := range names {
		if strings.EqualFold(n, name) {
			return true
		}
	}
	return false
}

// reads reports whether a formula of the class reads the documents of the
// class of that name.
func reads(cls *model.Class, target string) bool {
	for _, name := range cls.Reads {
		if strings.EqualFold(name, target) {
			return true
		}
	}
	return false
}

// actsOn reports whether a rule of the class has an outbox action on the
// class, or with table on the table, of that name.
func actsOn(cls *model.Class, target string, table bool) bool {
	for _, r := range cls.Rules {
		for _, a := range r.Actions {
			if a.Queued && strings.EqualFold(a.Target, target) && table == strings.HasPrefix(a.Name, "table.") {
				return true
			}
		}
	}
	return false
}

// calls reports whether a rule of the class calls the defined action of
// that name.
func calls(cls *model.Class, action string) bool {
	for _, r := range cls.Rules {
		for _, a := range r.Actions {
			if a.Call && strings.EqualFold(a.Name, action) {
				return true
			}
		}
	}
	return false
}

// changedPath is where, in the definition of a class, a table or an
// action, the thing is that a finding at path in another class's action
// is about: /rules/0/do/1/values/amount is about /variables/amount.
func changedPath(path string) string {
	parts := strings.Split(path, "/") // "", rules, i, do, j, part, name
	if len(parts) < 6 {
		return ""
	}
	name := ""
	if len(parts) > 6 {
		name = "/" + parts[6]
	}
	switch parts[5] {
	case "values":
		return "/variables" + name
	case "variables":
		return "/variables"
	case "name":
		return "/transitions"
	case "class":
		return "/states"
	case "row", "key", "set":
		if name == "" {
			return "/key"
		}
		return "/columns" + name
	case "args":
		return "/params" + name
	}
	return ""
}

// alterDDL is the DDL that takes the tables of the old version to the
// new one without touching a row: columns added for new variables, a
// child table for a new composite, indexes made or dropped, and for a
// variable that changed type with no value anywhere, its columns dropped
// and made again. Columns of removed variables stay, holding the history.
func alterDDL(old, c *class) []string {
	var ddl []string
	oldCols := map[string]model.Column{}
	for _, col := range old.cls.Columns {
		oldCols[strings.ToUpper(col.Name)] = col
	}
	newCols := map[string]model.Column{}
	for _, col := range c.cls.Columns {
		newCols[strings.ToUpper(col.Name)] = col
	}
	retyped := map[string]bool{}
	for _, ch := range model.Diff(old.cls, c.cls) {
		if ch.Kind == model.ChangeRetypeVariable {
			retyped[strings.ToUpper(ch.Name)] = true
		}
	}
	for _, table := range []string{old.table, old.history} {
		// The moment columns of a triggered variable (ADR-0043) keep their
		// types whatever the variable becomes, and the version that had
		// them still reads them, so a retype leaves them in place.
		for _, col := range old.cls.Columns {
			if retyped[strings.ToUpper(col.Variable)] && col.Moment == "" {
				if col.Index && table == old.table {
					ddl = append(ddl, "DROP INDEX IF EXISTS "+quote(old.table+"_"+col.Name))
				}
				ddl = append(ddl, "ALTER TABLE "+quote(table)+" DROP COLUMN "+quote(col.Name))
			}
		}
		for _, col := range c.cls.Columns {
			if _, had := oldCols[strings.ToUpper(col.Name)]; !had || retyped[strings.ToUpper(col.Variable)] && col.Moment == "" {
				ddl = append(ddl, "ALTER TABLE "+quote(table)+" ADD COLUMN "+quote(col.Name)+" "+sqlType(col.Type.Kind))
			}
		}
	}
	for _, col := range old.cls.Columns {
		if nc, ok := newCols[strings.ToUpper(col.Name)]; col.Index && (!ok || !nc.Index) && !retyped[strings.ToUpper(col.Variable)] {
			ddl = append(ddl, "DROP INDEX IF EXISTS "+quote(old.table+"_"+col.Name))
		}
	}
	for _, col := range c.cls.Columns {
		if oc, had := oldCols[strings.ToUpper(col.Name)]; col.Index && (!had || !oc.Index || retyped[strings.ToUpper(col.Variable)]) {
			ddl = append(ddl, "CREATE INDEX "+quote(c.table+"_"+col.Name)+" ON "+quote(c.table)+" ("+quote(col.Name)+")")
		}
	}
	oldChildren := map[string]*child{}
	for _, ch := range old.children {
		oldChildren[strings.ToUpper(ch.v.Name)] = ch
	}
	for _, ch := range c.children {
		key := strings.ToUpper(ch.v.Name)
		if prev, had := oldChildren[key]; !had {
			ddl = append(ddl, ch.tableDDL())
		} else if retyped[key] {
			ddl = append(ddl, "DROP TABLE "+quote(prev.table), ch.tableDDL())
		}
	}
	return ddl
}

// Migrate brings every live document of a class that an older version
// computed to the current version, as commits of their own in batches of
// a thousand per transaction, each a history row and a migrated event
// (ADR-0039). Terminal documents are left alone. It returns how many
// documents it moved. It is never automatic.
func (s *Store) Migrate(ctx context.Context, class string, opts ...Option) (int, error) {
	c, e := s.class(class)
	if e != nil {
		return 0, e
	}
	o := apply(opts)
	if o.key != "" {
		return 0, hintf(CodeNotSupported, "", "a migration takes no idempotency key", "running it again moves nothing: every live document is already at the current version")
	}
	if err := refuseAt(o, "migration"); err != nil {
		return 0, err
	}
	where := `WHERE "class_version" < ?`
	var args []any
	args = append(args, c.cls.Version)
	if c.cls.States != nil && len(c.cls.States.Final) > 0 {
		where += ` AND ("state" IS NULL OR "state" NOT IN (` + marks(len(c.cls.States.Final)) + "))"
		for _, st := range c.cls.States.Final {
			args = append(args, st)
		}
	}
	moved := 0
	const batch = 1000
	for {
		rows, err := s.db.QueryContext(ctx, "SELECT \"id\" FROM "+quote(c.table)+" "+where+` ORDER BY "id" LIMIT `+fmt.Sprint(batch), args...)
		if err != nil {
			return moved, storageErr("find the documents to migrate", err)
		}
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return moved, storageErr("find the documents to migrate", err)
			}
			ids = append(ids, id)
		}
		rows.Close()
		if len(ids) == 0 {
			return moved, nil
		}
		if err := s.migrateBatch(ctx, c, ids, o); err != nil {
			return moved, err
		}
		moved += len(ids)
	}
}

func (s *Store) migrateBatch(ctx context.Context, c *class, ids []string, o options) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errf(CodeClosed, "", "the store is closed")
	}
	now := s.now()
	if _, err := s.writer.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return storageErr("begin", err)
	}
	for _, id := range ids {
		if _, err := s.run(ctx, command{op: opMigrate, c: c, id: id, o: o}, now); err != nil {
			_, _ = s.writer.ExecContext(ctx, "ROLLBACK")
			return err
		}
	}
	if _, err := s.writer.ExecContext(ctx, "COMMIT"); err != nil {
		_, _ = s.writer.ExecContext(ctx, "ROLLBACK")
		return storageErr("commit", err)
	}
	return nil
}
