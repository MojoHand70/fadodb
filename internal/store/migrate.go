package store

import (
	"context"
	"fmt"
	"strings"

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
		for _, col := range old.cls.Columns {
			if retyped[strings.ToUpper(col.Variable)] {
				if col.Index && table == old.table {
					ddl = append(ddl, "DROP INDEX IF EXISTS "+quote(old.table+"_"+col.Name))
				}
				ddl = append(ddl, "ALTER TABLE "+quote(table)+" DROP COLUMN "+quote(col.Name))
			}
		}
		for _, col := range c.cls.Columns {
			if _, had := oldCols[strings.ToUpper(col.Name)]; !had || retyped[strings.ToUpper(col.Variable)] {
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
