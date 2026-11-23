package store

import (
	"context"
	"database/sql"
	"slices"
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
	DefinitionInlet  = "inlet"
	DefinitionApp    = "app"
)

// DefinitionWords is declared with the words, in words.go.

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
	// Removed says who took this version away and when (ADR-0083): it is
	// the version a table or words had when they were removed. It is
	// nil for every other version, and for a caller who is not answered
	// who removed a thing (Store.Definitions says who is).
	Removed *Defined `json:"removed,omitempty"`
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// recordSQL writes a line of the log of definitions.
const recordSQL = "INSERT INTO definitions (what, name, version, defined_at, defined_by, defined_kind, defined_for) VALUES (?, ?, ?, ?, ?, ?, ?)" +
	" ON CONFLICT (what, name, version) DO UPDATE SET defined_at = excluded.defined_at, defined_by = excluded.defined_by, defined_kind = excluded.defined_kind, defined_for = excluded.defined_for"

// recordDefinition writes the row of a version that is being stored, with
// the statements that store it, and returns what it wrote.
func recordDefinition(ctx context.Context, w execer, what, name string, version int, by Principal, now int64) (Defined, error) {
	d := Defined{By: by.ID, Kind: by.Kind, For: by.For, At: now}
	if _, err := w.ExecContext(ctx, recordSQL, what, name, version, now, nullText(by.ID), nullText(by.Kind), nullText(by.For)); err != nil {
		return d, storageErr("record who defined the "+what, err)
	}
	return d, nil
}

// A removal is a line of its own in the same log (ADR-0083): what was
// removed with " removed" after it, so "table removed", its name, the
// version it had, and in the four columns of who and when, who removed it
// and when. No reader of the versions asks for such a line, so the log
// keeps its shape and a database of engine schema 13 opens as it is.
const removedSuffix = " removed"

// recordRemoval writes the line of a version that is being removed, with
// the statements that remove it.
func recordRemoval(ctx context.Context, w execer, what, name string, version int, by Principal, now int64) error {
	if _, err := w.ExecContext(ctx, recordSQL, what+removedSuffix, name, version, now, nullText(by.ID), nullText(by.Kind), nullText(by.For)); err != nil {
		return storageErr("record who removed the "+what, err)
	}
	return nil
}

// Definitions lists the versions of one class, schema, table or action,
// oldest first, each with who defined it and when. It is open to every
// caller, as reading a definition is (ADR-0051, item 4). A name nothing
// was defined under answers an empty list. The version a table or words
// had when they were removed also says who removed them and when, to a
// caller seesRemovals lets see it (ADR-0083); every other caller gets the
// same list without it, and is not refused.
func (s *Store) Definitions(ctx context.Context, what, name string, opts ...Option) ([]Definition, error) {
	switch what {
	case DefinitionClass, DefinitionSchema, DefinitionTable, DefinitionAction, DefinitionInlet, DefinitionApp, DefinitionWords:
	default:
		return nil, noSuchDefinition(what)
	}
	removals := s.seesRemovals(ctx, opts)
	rows, err := s.db.QueryContext(ctx, "SELECT d.name, d.version, d.defined_at, d.defined_by, d.defined_kind, d.defined_for, r.defined_at, r.defined_by, r.defined_kind, r.defined_for"+
		" FROM definitions d LEFT JOIN definitions r ON r.what = ? AND r.name = d.name AND r.version = d.version WHERE d.what = ? AND d.name = ? ORDER BY d.version",
		what+removedSuffix, what, name)
	if err != nil {
		return nil, storageErr("read who defined", err)
	}
	defer rows.Close()
	out := []Definition{}
	for rows.Next() {
		d := Definition{What: what}
		var by, kind, acts, rby, rkind, racts sql.NullString
		var rat sql.NullInt64
		if err := rows.Scan(&d.Name, &d.Version, &d.At, &by, &kind, &acts, &rat, &rby, &rkind, &racts); err != nil {
			return nil, storageErr("read who defined", err)
		}
		d.By, d.Kind, d.For = by.String, kind.String, acts.String
		if removals && rat.Valid {
			d.Removed = &Defined{By: rby.String, Kind: rkind.String, For: racts.String, At: rat.Int64}
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, storageErr("read who defined", err)
	}
	return out, nil
}

// seesRemovals says whether the caller is answered who removed a table
// or words. The owner, 2026-11-23: "administrators and designers only,
// Agents if work on Administrator behalf". So it is a caller in the
// system group administrators or designers who is no agent, a person, a
// service or a caller whose kind is not given; or an agent that acts for
// a user the database keeps, who is in administrators and not disabled.
// An agent's own system groups give it nothing, as his words read, and in
// a database that does not keep its users no agent is answered it, since
// the engine cannot tell whom it works for. A call that names nobody is
// nobody (ADR-0041, item 6), and a caller whose principal the engine
// refuses elsewhere, such as one whose "for" no substitution holds, is
// answered as nobody is: the versions without the removals.
func (s *Store) seesRemovals(ctx context.Context, opts []Option) bool {
	if len(opts) == 0 {
		return false
	}
	o, e := s.asked(ctx, opts, "")
	if e != nil {
		return false
	}
	p := o.principal
	if p.Kind != ActorAgent {
		return p.in(GroupAdministrators) || p.in(GroupDesigners)
	}
	if p.For == "" || !s.people.on {
		return false
	}
	s.people.mu.RLock()
	u := s.people.users[p.For]
	s.people.mu.RUnlock()
	return u != nil && !u.Disabled && slices.Contains(u.System, GroupAdministrators)
}

// noSuchDefinition is the refusal of a kind of definition that is none.
func noSuchDefinition(what string) *Error {
	return hintf(CodeBadValue, "", "a definition is a "+DefinitionClass+", a "+DefinitionSchema+", a "+DefinitionTable+", an "+DefinitionAction+", an "+DefinitionInlet+", an "+DefinitionApp+" or "+DefinitionWords+", got "+strings.TrimSpace(what), "name one of the seven")
}

// loadDefined gives every class, table, action and inlet that was loaded the
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
		case DefinitionInlet:
			if in, ok := s.inlets[key]; ok && in.spec.Version == version {
				in.defined = d
			}
		}
	}
}
