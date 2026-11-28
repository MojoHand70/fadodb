package store

import (
	"context"
	"database/sql"
	"strings"

	"github.com/MojoHand70/fadodb/internal/model"
)

// The app System and its tables (ADR-0092). Every database has the app
// System, the engine's own, and four tables in it: SystemApps,
// SystemClasses, SystemUsers and SystemGroups. Each is a view, under the
// table's own name, over the records the engine keeps of its apps,
// classes, users and groups, so its rows are what the database holds at
// the moment it is read, and no copy waits to be brought up to date. A
// formula, a page and an agent read one as they read any table: LOOKUP,
// QUERY, table_rows, get_table. Nobody writes its rows, a content
// administrator neither: an import, a delete, a rule's table action and a
// definition under its name are refused.

// systemViews is what each system table's view selects, its columns in
// the order of the table's definition.
var systemViews = map[string]string{
	model.SystemApps: `SELECT name AS "name", json_extract(json, '$.label') AS "label", json_extract(json, '$.description') AS "description",` +
		` json_extract(json, '$.icon') AS "icon", json_extract(json, '$.color') AS "color", version AS "version",` +
		` created_at AS "created_at", updated_at AS "updated_at" FROM apps`,
	model.SystemClasses: `SELECT name AS "name", json_extract(json, '$.app') AS "app", json_extract(json, '$.label') AS "label",` +
		` json_extract(json, '$.description') AS "description", version AS "version", created_at AS "created_at", updated_at AS "updated_at" FROM classes`,
	model.SystemUsers: `SELECT u.id AS "id", u.kind AS "kind", u.name AS "name", e.email AS "email", u.groups AS "groups",` +
		` u.system_groups AS "system_groups", u.disabled AS "disabled", u.created_at AS "created_at", u.updated_at AS "updated_at"` +
		` FROM fado_users AS u LEFT JOIN fado_user_emails AS e ON e.user = u.id`,
	model.SystemGroups: `SELECT g.name AS "name", g.label AS "label", g.description AS "description",` +
		` (SELECT COUNT(*) FROM fado_users AS u, json_each(u.groups) AS m WHERE m.value = g.name) AS "members",` +
		` g.created_at AS "created_at", g.updated_at AS "updated_at" FROM fado_groups AS g`,
}

// keepSystem gives the database the app System and its tables, once what
// is stored is loaded and before the classes are, since a class may read
// a system table. A database that has no app System gets the engine's;
// one that has it, made by a designer before this engine, keeps it with
// its words. A view is made when the database has none of the name, and
// made again when this engine's differs. A table or a class of the
// database's own that took a system table's name before this engine
// keeps it, and that system table is left out of this database.
func (s *Store) keepSystem(ctx context.Context) error {
	if _, ok := s.apps[strings.ToUpper(model.SystemApp)]; !ok {
		if err := s.bringSystemApp(ctx); err != nil {
			return err
		}
	}
	for _, name := range model.SystemTableNames() {
		if _, taken := s.tables[strings.ToUpper(name)]; taken {
			continue
		}
		var classes int
		if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM classes WHERE name = ?", name).Scan(&classes); err != nil {
			return storageErr("read the classes", err)
		}
		if classes > 0 {
			continue
		}
		src := model.SystemTableDefinition(name)
		def, diags := model.DefineTable(src)
		if len(diags) > 0 {
			return &Error{Code: CodeStorage, Message: "the system table " + name + " does not compile: " + diags.Error()}
		}
		def.Defined = Defined{By: engineActor.ID, Kind: engineActor.Kind}
		if err := s.keepView(ctx, name, "CREATE VIEW "+quote(name)+" AS "+systemViews[name]); err != nil {
			return err
		}
		// Who defined the version is written once, as for any table, so
		// that get_table and definitions answer it: the engine.
		var known int
		if err := s.writer.QueryRowContext(ctx, "SELECT COUNT(*) FROM definitions WHERE what = ? AND name = ? AND version = ?", DefinitionTable, name, def.Version).Scan(&known); err != nil {
			return storageErr("read who defined "+name, err)
		}
		if known == 0 {
			d, err := recordDefinition(ctx, s.writer, DefinitionTable, name, def.Version, engineActor, s.now().Unix())
			if err != nil {
				return err
			}
			def.Defined = d
		}
		t := newTable(def, src)
		t.name, t.system = name, true
		if err := t.prepare(ctx, s.writer, s.db); err != nil {
			return err
		}
		s.registerTable(t)
	}
	return nil
}

// keepView makes the view when the database has none of the name, or one
// of another text; it writes nothing when the view is this engine's.
func (s *Store) keepView(ctx context.Context, name, ddl string) error {
	var have string
	err := s.writer.QueryRowContext(ctx, "SELECT sql FROM sqlite_master WHERE type = 'view' AND name = ? COLLATE NOCASE", name).Scan(&have)
	switch {
	case err == nil && have == ddl:
		return nil
	case err == nil:
		if _, err := s.writer.ExecContext(ctx, "DROP VIEW "+quote(name)); err != nil {
			return storageErr("replace the view of "+name, err)
		}
	case err != sql.ErrNoRows:
		return storageErr("read the view of "+name, err)
	}
	if _, err := s.writer.ExecContext(ctx, ddl); err != nil {
		return storageErr("make the view of "+name, err)
	}
	return nil
}

// bringSystemApp defines the app System as the engine's, version 1,
// with who defined it written down as for any app.
func (s *Store) bringSystemApp(ctx context.Context) error {
	data := model.SystemAppDefinition()
	def, diags := model.ParseApp(data)
	if len(diags) > 0 {
		return &Error{Code: CodeStorage, Message: "the app " + model.SystemApp + " does not compile: " + diags.Error()}
	}
	w := s.writer
	if err := s.beginWriter(ctx, "BEGIN IMMEDIATE"); err != nil {
		return storageErr("begin", err)
	}
	now := s.now().Unix()
	if _, err := w.ExecContext(ctx, "INSERT INTO apps (name, version, json, created_at, updated_at) VALUES (?, 1, ?, ?, ?)", def.Name, string(data), now, now); err != nil {
		_, _ = w.ExecContext(ctx, "ROLLBACK")
		return storageErr("store the app "+def.Name, err)
	}
	if _, err := recordDefinition(ctx, w, DefinitionApp, def.Name, 1, engineActor, now); err != nil {
		_, _ = w.ExecContext(ctx, "ROLLBACK")
		return err
	}
	if err := s.commitWriter(ctx); err != nil {
		_, _ = w.ExecContext(ctx, "ROLLBACK")
		return storageErr("commit", err)
	}
	s.apps[strings.ToUpper(def.Name)] = &app{App: App{Name: def.Name, Version: 1, Def: def}, json: data}
	s.appOrder = append(s.appOrder, def.Name)
	return nil
}

// systemWrite is the refusal of a write to a system table.
func systemWrite(t *table, what string) *Error {
	e := hintf(CodeReadOnly, "", t.def.Name+" is a system table: the engine keeps its rows, and nobody "+what,
		"change what it shows by its own command: define_app, define_class, create_user, set_user, create_group, set_group")
	e.Path = "/table"
	return e
}
