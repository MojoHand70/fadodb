package model

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// MaxObjectDepth is how deep objects may nest below a variable: a schema
// of scalars is one deep, a schema with an object field two (ADR-0031).
const MaxObjectDepth = 4

// MaxColumns is the most columns a generated table may hold (ADR-0031).
const MaxColumns = 1000

// SchemaDef is a schema as written: a named list of fields, each a
// scalar type other than json or object<Name> of another schema, with an
// optional base to extend and a presentation block (ADR-0031, ADR-0032).
type SchemaDef struct {
	Name         string            `json:"name"`
	Extends      string            `json:"extends,omitempty"`
	Fields       map[string]string `json:"fields"`
	Presentation *PresentationDef  `json:"presentation,omitempty"`
	// FieldOrder lists the fields in the order written; ParseSchema fills
	// it, and a caller building a SchemaDef in code may.
	FieldOrder []string `json:"-"`
}

// PresentationDef says how a frontend shows an object of the schema
// (ADR-0032): Title and Subtitle are formulas over the fields, pure
// functions only; Avatar names a text field holding an image URL or a
// file hash; Icon and Color are as in the class header.
type PresentationDef struct {
	Title    string `json:"title,omitempty"`
	Subtitle string `json:"subtitle,omitempty"`
	Avatar   string `json:"avatar,omitempty"`
	Icon     string `json:"icon,omitempty"`
	Color    string `json:"color,omitempty"`
}

// Schema is a resolved schema: its object type with every field's type
// in place, how deep it nests, and its compiled presentation.
type Schema struct {
	Name string
	// Version counts the definitions of a user schema; a predefined one
	// is versioned with the engine.
	Version    int
	Predefined bool
	Extends    string
	// Type is the object type, fields in order, base fields first.
	Type formula.Type
	// Depth is 1 for a schema of scalars and one more per level of
	// object fields.
	Depth        int
	Presentation *Presentation
	Def          *SchemaDef
}

// Presentation is a compiled presentation block.
type Presentation struct {
	Title, Subtitle *formula.Program
	Avatar          string
	Icon, Color     string
}

// SchemaSet holds the schemas a database knows: the vocabulary and the
// user's. Names are unique in any case.
type SchemaSet struct {
	byKey map[string]*Schema
	order []string
}

// NewSchemaSet makes a set holding the predefined vocabulary.
func NewSchemaSet() *SchemaSet {
	s := &SchemaSet{byKey: map[string]*Schema{}}
	for _, def := range vocabulary {
		sch, diags := s.define(def, true)
		if len(diags) > 0 {
			panic("model: the predefined vocabulary does not compile: " + diags.Error())
		}
		_ = sch
	}
	return s
}

// Lookup finds a schema by name, case-insensitively.
func (s *SchemaSet) Lookup(name string) (*Schema, bool) {
	sch, ok := s.byKey[upper(name)]
	return sch, ok
}

// Names lists the schemas in definition order.
func (s *SchemaSet) Names() []string { return append([]string(nil), s.order...) }

// ParseSchema decodes a schema definition, keeping the field order.
func ParseSchema(data []byte) (*SchemaDef, Diagnostics) {
	var c collector
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var def SchemaDef
	if err := dec.Decode(&def); err != nil {
		c.add(CodeBadJSON, "", jsonMessage(err))
		return nil, c.list
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err == nil {
		if raw, ok := top["fields"]; ok {
			var err error
			if def.FieldOrder, err = orderedKeys(raw); err != nil {
				c.add(CodeBadJSON, "/fields", err.Error())
			}
		}
	}
	return &def, c.list
}

// Define adds a user schema, or a new version of one. A predefined name
// is refused. The schema is resolved when it is defined: a later change
// to a schema it extends or embeds does not reach it until it is defined
// again (ADR-0031, ADR-0032).
func (s *SchemaSet) Define(def *SchemaDef) (*Schema, Diagnostics) {
	return s.define(def, false)
}

func (s *SchemaSet) define(def *SchemaDef, predefined bool) (*Schema, Diagnostics) {
	var c collector
	if !c.checkName("/name", def.Name, nil) {
		return nil, c.list
	}
	sch := &Schema{Name: def.Name, Version: 1, Predefined: predefined, Extends: def.Extends, Def: def, Depth: 1}
	if old, ok := s.Lookup(def.Name); ok {
		if old.Predefined {
			c.addf(CodeReservedName, "/name", "%s is a predefined schema; its name is reserved", quote(def.Name))
			return nil, c.list
		}
		if old.Name != def.Name {
			c.addf(CodeDuplicate, "/name", "a schema %s exists; names differ in case only", quote(old.Name))
			return nil, c.list
		}
		sch.Version = old.Version + 1
	}
	fields := []formula.Field{}
	seen := map[string]bool{}
	if def.Extends != "" {
		base, ok := s.Lookup(def.Extends)
		switch {
		case !ok:
			c.addf(CodeUnknownSchema, "/extends", "unknown schema %s", quote(def.Extends))
		case upper(base.Name) == upper(def.Name):
			c.add(CodeSchemaCycle, "/extends", "a schema may not extend itself")
		default:
			fields = append(fields, base.Type.Fields()...)
			for _, f := range base.Type.Fields() {
				seen[upper(f.Name)] = true
			}
			sch.Depth = base.Depth
			if base.Presentation != nil && def.Presentation == nil {
				sch.Presentation = base.Presentation
			}
		}
	}
	order := def.FieldOrder
	if order == nil {
		for name := range def.Fields {
			order = append(order, name)
		}
		sort.Strings(order)
	}
	if len(order) == 0 && def.Extends == "" {
		c.add(CodeMissing, "/fields", "a schema declares at least one field")
	}
	for _, name := range order {
		path := "/fields/" + name
		if !c.checkName(path, name, reservedFieldNames) {
			continue
		}
		if seen[upper(name)] {
			c.addf(CodeDuplicate, path, "field %s is declared twice, or is a field of the base schema", quote(name))
			continue
		}
		seen[upper(name)] = true
		ft, depth, ok := s.fieldType(&c, path, def.Fields[name], def.Name)
		if !ok {
			continue
		}
		sch.Depth = max(sch.Depth, depth+1)
		fields = append(fields, formula.Field{Name: name, Type: ft})
	}
	if sch.Depth > MaxObjectDepth {
		c.addf(CodeTooDeep, "/fields", "objects would nest %d deep below a variable; at most %d", sch.Depth, MaxObjectDepth)
	}
	sch.Type = formula.ObjectType(def.Name, fields)
	if def.Presentation != nil {
		sch.Presentation = s.presentation(&c, sch, def.Presentation)
	}
	if c.failed() {
		return nil, c.list
	}
	key := upper(def.Name)
	if _, exists := s.byKey[key]; !exists {
		s.order = append(s.order, def.Name)
	}
	s.byKey[key] = sch
	return sch, nil
}

// fieldType reads a field's type: a scalar other than json, or
// object<Name> of a schema already defined. It reports the depth below
// the field: 0 for a scalar, the schema's depth for an object.
func (s *SchemaSet) fieldType(c *collector, path, typ, owner string) (formula.Type, int, bool) {
	if strings.HasPrefix(typ, "object<") && strings.HasSuffix(typ, ">") {
		name := typ[7 : len(typ)-1]
		if upper(name) == upper(owner) {
			c.addf(CodeSchemaCycle, path, "schema %s may not contain itself", quote(owner))
			return formula.Type{}, 0, false
		}
		sub, ok := s.Lookup(name)
		if !ok {
			c.addf(CodeUnknownSchema, path, "unknown schema %s", quote(name))
			return formula.Type{}, 0, false
		}
		return sub.Type, sub.Depth, true
	}
	t, err := formula.ParseType(typ)
	switch {
	case err != nil:
		c.add(CodeUnknownType, path, err.Error())
		return formula.Type{}, 0, false
	case t.Kind == formula.KindNull:
		c.add(CodeUnknownType, path, "a field is never blank by type")
		return formula.Type{}, 0, false
	case t.Kind == formula.KindJSON:
		c.hint(CodeNotAllowed, path, "a field may not be json", "declare the shape as fields, or keep the json variable on the class")
		return formula.Type{}, 0, false
	case !t.IsScalar():
		c.hint(CodeNotAllowed, path, "a schema holds no collection: a field is a scalar or an object", "an array or rows variable belongs on the class")
		return formula.Type{}, 0, false
	}
	return t, 0, true
}

// presentation compiles a presentation block against the schema's fields.
func (s *SchemaSet) presentation(c *collector, sch *Schema, def *PresentationDef) *Presentation {
	p := &Presentation{Avatar: def.Avatar, Icon: def.Icon, Color: def.Color}
	scope, err := scopeOfFields(sch.Type.Fields())
	if err != nil {
		c.add(CodeBadName, "/fields", err.Error())
		return p
	}
	bind := func(path, src string) *formula.Program {
		if src == "" {
			return nil
		}
		prog, err := c.bind(src, scope, formula.ModePure)
		if err != nil {
			c.formulaErr(path, err)
			return nil
		}
		if prog.Result.Kind != formula.KindText && prog.Result.Kind != formula.KindNull {
			c.hint(CodeTypeMismatch, path, "a presentation formula must be text, got "+prog.Result.String(), "wrap it in TEXT(...)")
		}
		return prog
	}
	p.Title = bind("/presentation/title", def.Title)
	p.Subtitle = bind("/presentation/subtitle", def.Subtitle)
	if def.Avatar != "" {
		f, ok := sch.Type.Field(def.Avatar)
		if !ok {
			c.addf(CodeUnknownVariable, "/presentation/avatar", "no field %s", quote(def.Avatar))
		} else if f.Type.Kind != formula.KindText {
			c.addf(CodeTypeMismatch, "/presentation/avatar", "the avatar field must be text, %s is %s", f.Name, f.Type)
		}
	}
	return p
}

// scopeOfFields makes a scope where each field is a variable.
func scopeOfFields(fields []formula.Field) (*formula.Scope, error) {
	vars := make([]formula.Variable, len(fields))
	for i, f := range fields {
		vars[i] = formula.Variable(f)
	}
	return formula.NewScope(vars...)
}

// Leaf is one scalar column an object flattens to (ADR-0031).
type Leaf struct {
	// Path is the dotted field path from the variable, "shipping.city",
	// which is also the slot name the binder gives it.
	Path string
	// Column is the column name, the path joined with the separator.
	Column string
	Type   formula.Type
}

// leavesOf flattens a type below a prefix: a scalar is one leaf, an
// object one per field, nested.
func leavesOf(prefix string, t formula.Type) []Leaf {
	if t.Kind != formula.KindObject {
		return []Leaf{{Path: prefix, Column: strings.ReplaceAll(prefix, ".", PathSeparator), Type: t}}
	}
	var out []Leaf
	for _, f := range t.Fields() {
		out = append(out, leavesOf(prefix+"."+f.Name, f.Type)...)
	}
	return out
}

// vocabulary is the predefined vocabulary of ADR-0032, in dependency
// order. Its names are reserved; a release may add a schema or a field
// and never removes or retypes one.
var vocabulary = []*SchemaDef{
	{Name: "Person",
		Fields:       map[string]string{"id": "text", "name": "text", "email": "text", "phone": "text", "avatar": "text"},
		FieldOrder:   []string{"id", "name", "email", "phone", "avatar"},
		Presentation: &PresentationDef{Title: "name", Subtitle: "email", Avatar: "avatar", Icon: "user"}},
	{Name: "Address",
		Fields:       map[string]string{"line1": "text", "line2": "text", "city": "text", "postal_code": "text", "region": "text", "country": "text"},
		FieldOrder:   []string{"line1", "line2", "city", "postal_code", "region", "country"},
		Presentation: &PresentationDef{Title: "line1", Subtitle: `IF(ISBLANK(country), city, city & ", " & country)`, Icon: "map-pin"}},
	{Name: "Organization",
		Fields:       map[string]string{"id": "text", "name": "text", "tax_id": "text", "email": "text", "phone": "text", "address": "object<Address>"},
		FieldOrder:   []string{"id", "name", "tax_id", "email", "phone", "address"},
		Presentation: &PresentationDef{Title: "name", Subtitle: "address.city", Icon: "building"}},
	{Name: "GeoPoint",
		Fields:       map[string]string{"lat": "number", "lon": "number"},
		FieldOrder:   []string{"lat", "lon"},
		Presentation: &PresentationDef{Title: `TEXT(lat) & ", " & TEXT(lon)`, Icon: "crosshair"}},
	{Name: "Place",
		Fields:       map[string]string{"name": "text", "address": "object<Address>", "geo": "object<GeoPoint>"},
		FieldOrder:   []string{"name", "address", "geo"},
		Presentation: &PresentationDef{Title: "name", Subtitle: `IF(ISBLANK(address.country), address.city, address.city & ", " & address.country)`, Icon: "map"}},
	{Name: "Period",
		Fields:       map[string]string{"start": "datetime", "end": "datetime"},
		FieldOrder:   []string{"start", "end"},
		Presentation: &PresentationDef{Title: `TEXT(start) & " to " & TEXT(end)`, Icon: "calendar-range"}},
	{Name: "Event",
		Fields:       map[string]string{"title": "text", "start": "datetime", "end": "datetime", "all_day": "bool", "place": "object<Place>", "organizer": "object<Person>"},
		FieldOrder:   []string{"title", "start", "end", "all_day", "place", "organizer"},
		Presentation: &PresentationDef{Title: "title", Subtitle: `IF(all_day, TEXT(DATEOF(start)), TEXT(start))`, Icon: "calendar"}},
	{Name: "Quantity",
		Fields:       map[string]string{"value": "decimal(6)", "unit": "text"},
		FieldOrder:   []string{"value", "unit"},
		Presentation: &PresentationDef{Title: `TEXT(value) & " " & unit`, Icon: "scale"}},
	{Name: "File",
		Fields:       map[string]string{"hash": "text", "name": "text", "media_type": "text", "size": "int"},
		FieldOrder:   []string{"hash", "name", "media_type", "size"},
		Presentation: &PresentationDef{Title: "name", Subtitle: `TEXT(size) & " bytes"`, Icon: "paperclip"}},
}

// VocabularyNames lists the predefined schemas in their order.
func VocabularyNames() []string {
	names := make([]string, len(vocabulary))
	for i, d := range vocabulary {
		names[i] = d.Name
	}
	return names
}
