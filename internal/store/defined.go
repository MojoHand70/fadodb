package store

import (
	"context"
	"database/sql"
	"strings"

	"github.com/MojoHand70/fadodb/internal/model"
)

// Every version of a class, a schema, a table and an action records who
// defined it, what they were and for whom they acted, and when
// (ADR-0052, item 7; ADR-0051, item 5). A designer can read through a
// class they write and can rewrite a rule; this record does not stop
// that, it keeps it in sight.

// What a definition is, as Definitions takes it and Definition.What says
// it.
const (
	DefinitionClass  = "class"
	DefinitionSchema = "schema"
	DefinitionTable  = "table"
	DefinitionAction = "action"
)

// Defined says who defined a version and when: By, the id of the caller,
// Kind, what it was, For, the id it acted for, and At, the moment in
// seconds since the epoch.
type Defined = model.Defined

// Definition is one version of a class, a schema, a table or an action
// in the record of who defined what.
type Definition struct {
	What    string `json:"what"`
	Name    string `json:"name"`
	Version int    `json:"version"`
	Defined
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// recordDefinition writes the row of a version that is being stored, with
// the statements that store it, and returns what it wrote.
func recordDefinition(ctx context.Context, w execer, what, name string, version int, by Principal, now int64) (Defined, error) {
	d := Defined{By: by.ID, Kind: by.Kind, For: by.For, At: now}
	if _, err := w.ExecContext(ctx, "INSERT INTO definitions (what, name, version, defined_at, defined_by, defined_kind, defined_for) VALUES (?, ?, ?, ?, ?, ?, ?)"+
		" ON CONFLICT (what, name, version) DO UPDATE SET defined_at = excluded.defined_at, defined_by = excluded.defined_by, defined_kind = excluded.defined_kind, defined_for = excluded.defined_for",
		what, name, version, now, nullText(by.ID), nullText(by.Kind), nullText(by.For)); err != nil {
		return d, storageErr("record who defined the "+what, err)
	}
	return d, nil
}

// Definitions lists the versions of one class, schema, table or action,
// oldest first, each with who defined it and when. It is open to every
// caller, as reading a definition is (ADR-0051, item 4). A name nothing
// was defined under answers an empty list.
func (s *Store) Definitions(ctx context.Context, what, name string) ([]Definition, error) {
	switch what {
	case DefinitionClass, DefinitionSchema, DefinitionTable, DefinitionAction:
	default:
		return nil, hintf(CodeBadValue, "", "a definition is a "+DefinitionClass+", a "+DefinitionSchema+", a "+DefinitionTable+" or an "+DefinitionAction+", got "+strings.TrimSpace(what), "name one of the four")
	}
	rows, err := s.db.QueryContext(ctx, "SELECT name, version, defined_at, defined_by, defined_kind, defined_for FROM definitions WHERE what = ? AND name = ? ORDER BY version", what, name)
	if err != nil {
		return nil, storageErr("read who defined", err)
	}
	defer rows.Close()
	out := []Definition{}
	for rows.Next() {
		d := Definition{What: what}
		var by, kind, acts sql.NullString
		if err := rows.Scan(&d.Name, &d.Version, &d.At, &by, &kind, &acts); err != nil {
			return nil, storageErr("read who defined", err)
		}
		d.By, d.Kind, d.For = by.String, kind.String, acts.String
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, storageErr("read who defined", err)
	}
	return out, nil
}

// loadDefined gives every class, table and action that was loaded the
// record of its current version. The caller is Open; a database that
// cannot be read here was refused before.
func (s *Store) loadDefined(ctx context.Context) {
	rows, err := s.db.QueryContext(ctx, "SELECT what, name, version, defined_at, defined_by, defined_kind, defined_for FROM definitions ORDER BY what, name, version")
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var what, name string
		var version int
		var d Defined
		var by, kind, acts sql.NullString
		if rows.Scan(&what, &name, &version, &d.At, &by, &kind, &acts) != nil {
			return
		}
		d.By, d.Kind, d.For = by.String, kind.String, acts.String
		key := strings.ToUpper(name)
		switch what {
		case DefinitionClass:
			if c, ok := s.classes[key]; ok && c.cls.Version == version {
				c.cls.Defined = d
			}
		case DefinitionTable:
			if t, ok := s.tables[key]; ok && t.def.Version == version {
				t.def.Defined = d
			}
		case DefinitionAction:
			if a, ok := s.actions[key]; ok && a.spec.Version == version {
				a.defined = d
			}
		}
	}
}
