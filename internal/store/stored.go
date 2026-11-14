package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/MojoHand70/fadodb/internal/model"
)

// A definition is read back as it was given (ADR-0055): the JSON a
// designer wrote, so that it can be changed and defined again, with what
// the database added to it beside it and not inside it. Class, Table and
// Action answer the engine's compiled form, which a Go program works
// with and which is not the text anybody wrote.

// StoredDefinition is one version of a class, a schema, a table or an
// action as the database keeps it: Definition is the JSON as it was
// given, Version the version the database gave it, Defined who defined
// that version and when (ADR-0052), and Notes the findings that refused
// nothing when a class was defined. Predefined marks a schema of the
// engine's vocabulary, which nobody defined and which has no version.
type StoredDefinition struct {
	Name       string            `json:"name"`
	Version    int               `json:"version,omitempty"`
	Predefined bool              `json:"predefined,omitempty"`
	Defined    *Defined          `json:"defined,omitempty"`
	Notes      model.Diagnostics `json:"notes,omitempty"`
	Definition json.RawMessage   `json:"definition"`
}

// Definition reads one class, schema, table or action back as it was
// given, with its version, who defined it and, for a class, its notes.
// what is DefinitionClass, DefinitionSchema, DefinitionTable or
// DefinitionAction. version 0 is the current one; the database keeps
// every version of a class and of an action, and the current one of a
// schema and of a table. It is open to every caller, as reading a
// definition is (ADR-0051, item 4). An action of the engine, such as
// set, has no definition: Actions describes it.
func (s *Store) Definition(ctx context.Context, what, name string, version int) (*StoredDefinition, error) {
	if version < 0 {
		return nil, hintf(CodeBadValue, "", "a version is 1 or more, got "+strconv.Itoa(version), "leave the version out for the current one")
	}
	out, e := s.stored(ctx, what, name, version)
	if e != nil {
		return nil, e
	}
	if out.Predefined {
		return out, nil
	}
	var d Defined
	var by, kind, acts sql.NullString
	err := s.db.QueryRowContext(ctx, "SELECT defined_at, defined_by, defined_kind, defined_for FROM definitions WHERE what = ? AND name = ? AND version = ?",
		what, out.Name, out.Version).Scan(&d.At, &by, &kind, &acts)
	switch {
	case err == sql.ErrNoRows:
		// A version stored before the record of who defined was kept.
	case err != nil:
		return nil, storageErr("read who defined", err)
	default:
		d.By, d.Kind, d.For = by.String, kind.String, acts.String
		out.Defined = &d
	}
	return out, nil
}

// stored finds the text and the version, from what the store holds in
// memory, and for an earlier version of an action from its table.
func (s *Store) stored(ctx context.Context, what, name string, version int) (*StoredDefinition, *Error) {
	key := strings.ToUpper(name)
	s.cmu.RLock()
	defer s.cmu.RUnlock()
	switch what {
	case DefinitionClass:
		current, ok := s.classes[key]
		if !ok {
			return nil, errf(CodeUnknownClass, "", "unknown class %q", name)
		}
		c := current
		if version != 0 && version != current.cls.Version {
			if c, ok = s.versions[key][version]; !ok {
				// A version the store no longer holds compiled is read from
				// its table, without its notes.
				var text string
				err := s.db.QueryRowContext(ctx, "SELECT json FROM class_versions WHERE class = ? AND version = ?", current.cls.Name, version).Scan(&text)
				if err == sql.ErrNoRows {
					return nil, hintf(CodeUnknownClass, "", "the class "+current.cls.Name+" has no version "+strconv.Itoa(version),
						"its current version is "+strconv.Itoa(current.cls.Version)+"; definitions lists every version with who defined it")
				}
				if err != nil {
					return nil, asError(storageErr("read the version of the class", err))
				}
				return &StoredDefinition{Name: current.cls.Name, Version: version, Definition: json.RawMessage(text)}, nil
			}
		}
		return &StoredDefinition{Name: c.cls.Name, Version: c.cls.Version, Notes: c.cls.Notes, Definition: copyJSON(c.json)}, nil
	case DefinitionSchema:
		sch, ok := s.schemas.Lookup(name)
		if !ok {
			return nil, hintf(CodeUnknownSchema, "", "unknown schema "+strconv.Quote(name), "the schemas are "+strings.Join(s.schemas.Names(), ", "))
		}
		if version != 0 && version != sch.Version {
			return nil, onlyCurrent("schema", sch.Name, sch.Version)
		}
		if sch.Predefined {
			return &StoredDefinition{Name: sch.Name, Predefined: true, Definition: schemaJSON(sch.Def)}, nil
		}
		return &StoredDefinition{Name: sch.Name, Version: sch.Version, Definition: copyJSON(s.schemaJSON[key])}, nil
	case DefinitionTable:
		t, ok := s.tables[key]
		if !ok {
			return nil, errf(CodeUnknownTable, "", "unknown table %q", name)
		}
		if version != 0 && version != t.def.Version {
			return nil, onlyCurrent("table", t.def.Name, t.def.Version)
		}
		return &StoredDefinition{Name: t.def.Name, Version: t.def.Version, Definition: copyJSON(t.json)}, nil
	case DefinitionAction:
		if model.EngineAction(name) || strings.EqualFold(name, model.ActionHTTPCall) {
			return nil, hintf(CodeUnknownAction, "", "the action "+name+" is the engine's own and has no definition", "Actions describes it: its parameters and what it returns")
		}
		a, ok := s.actions[key]
		if !ok {
			return nil, errf(CodeUnknownAction, "", "unknown action %q", name)
		}
		if version == 0 || version == a.spec.Version {
			return &StoredDefinition{Name: a.spec.Name, Version: a.spec.Version, Definition: copyJSON(a.json)}, nil
		}
		var text string
		err := s.db.QueryRowContext(ctx, "SELECT json FROM action_versions WHERE action = ? AND version = ?", a.spec.Name, version).Scan(&text)
		if err == sql.ErrNoRows {
			return nil, hintf(CodeUnknownAction, "", "the action "+a.spec.Name+" has no version "+strconv.Itoa(version),
				"its current version is "+strconv.Itoa(a.spec.Version)+"; definitions lists every version with who defined it")
		}
		if err != nil {
			return nil, asError(storageErr("read the version of the action", err))
		}
		return &StoredDefinition{Name: a.spec.Name, Version: version, Definition: json.RawMessage(text)}, nil
	}
	return nil, hintf(CodeBadValue, "", "a definition is a "+DefinitionClass+", a "+DefinitionSchema+", a "+DefinitionTable+" or an "+DefinitionAction+", got "+strings.TrimSpace(what), "name one of the four")
}

// onlyCurrent is the refusal of an earlier version of a schema or a
// table, of which the database keeps the current one.
func onlyCurrent(what, name string, current int) *Error {
	return hintf(CodeNotSupported, "", "the database keeps the current version of a "+what+", and "+name+" is at version "+strconv.Itoa(current),
		"leave the version out; definitions lists every version with who defined it")
}

func copyJSON(text []byte) json.RawMessage { return append(json.RawMessage(nil), text...) }

// schemaJSON writes a schema of the vocabulary as a designer would have
// written it, its fields in their order.
func schemaJSON(def *model.SchemaDef) json.RawMessage {
	var b bytes.Buffer
	b.WriteString(`{"name":`)
	b.Write(mustMarshal(def.Name))
	if def.Extends != "" {
		b.WriteString(`,"extends":`)
		b.Write(mustMarshal(def.Extends))
	}
	b.WriteString(`,"fields":{`)
	for i, field := range def.FieldOrder {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(mustMarshal(field))
		b.WriteByte(':')
		b.Write(mustMarshal(def.Fields[field]))
	}
	b.WriteByte('}')
	if def.Presentation != nil {
		b.WriteString(`,"presentation":`)
		b.Write(mustMarshal(def.Presentation))
	}
	b.WriteByte('}')
	return b.Bytes()
}

func mustMarshal(v any) []byte {
	text, _ := json.Marshal(v)
	return text
}

// SchemaNames lists the schemas in definition order, the engine's
// vocabulary first.
func (s *Store) SchemaNames() []string {
	s.cmu.RLock()
	defer s.cmu.RUnlock()
	return s.schemas.Names()
}

// Schema returns a resolved schema by name.
func (s *Store) Schema(name string) (*model.Schema, bool) {
	s.cmu.RLock()
	defer s.cmu.RUnlock()
	return s.schemas.Lookup(name)
}
