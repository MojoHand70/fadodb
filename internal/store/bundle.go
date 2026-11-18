package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/MojoHand70/fadodb/internal/formula"
	"github.com/MojoHand70/fadodb/internal/model"
)

// BundleOf names the bundle a page asks for (ADR-0070): a class, the
// person's language, and an earlier version of the class when not the
// current one.
type BundleOf struct {
	Class    string
	Language string
	Version  int
}

// Bundle is a class, its words in a language and its compiled formulas,
// as one JSON object (ADR-0070), with the tag a page sends back to learn
// whether it changed.
type Bundle struct {
	Class   string
	Version int
	Tag     string
	JSON    []byte
}

// bundleJSON is the bundle on the wire, in the order a page reads it.
type bundleJSON struct {
	Class      string                     `json:"class"`
	Version    int                        `json:"version"`
	Bytecode   int                        `json:"bytecode"`
	Language   string                     `json:"language,omitempty"`
	Languages  []string                   `json:"languages"`
	Words      map[string]int             `json:"words"`
	Tag        string                     `json:"tag,omitempty"`
	Definition json.RawMessage            `json:"definition"`
	Schemas    map[string]json.RawMessage `json:"schemas"`
	Compiled   *model.BundleCompiled      `json:"compiled"`
}

// keptBundles is how many bundles the store keeps built. A page asks
// for a few classes in a few languages; past the limit the store starts
// again, so that callers asking in every tag there is cost time and not
// memory.
const keptBundles = 256

// keptBundle is a bundle built, and the count of definitions it was
// built after.
type keptBundle struct {
	defined uint64
	bundle  *Bundle
}

// Bundle answers the bundle of a class in a language (ADR-0070): the
// class as it was defined with the words of the language put in place,
// the schemas it uses with theirs, and every formula a page runs to show
// a document and check it before it is sent, compiled. Reading it is
// open to every caller, as reading a definition is (ADR-0051, item 4).
// It is built the first time it is asked for after a definition changes
// and kept in memory.
func (s *Store) Bundle(ctx context.Context, of BundleOf) (*Bundle, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	language := ""
	if of.Language != "" {
		canonical, why := model.LanguageTag(of.Language)
		if why != "" {
			return nil, &Error{Code: CodeBadValue, Path: "/language", Message: strconv.Quote(of.Language) + " is not a language tag of BCP 47: " + why,
				Hint: "name the language as a browser does, such as pl, de or pt-BR"}
		}
		language = canonical
	}
	if of.Version < 0 {
		return nil, hintf(CodeBadValue, "", "a version is 1 or more, got "+strconv.Itoa(of.Version), "leave the version out for the current one")
	}
	s.cmu.RLock()
	defer s.cmu.RUnlock()
	key := strings.ToUpper(of.Class)
	c, ok := s.classes[key]
	if !ok {
		return nil, errf(CodeUnknownClass, "", "unknown class %q", of.Class)
	}
	if of.Version != 0 && of.Version != c.cls.Version {
		current := c.cls.Version
		if c, ok = s.versions[key][of.Version]; !ok {
			return nil, hintf(CodeUnknownClass, "", "the class "+s.classes[key].cls.Name+" has no version "+strconv.Itoa(of.Version)+" that computed a document",
				"its current version is "+strconv.Itoa(current)+"; a page needs the bundle of a version that computed a document it shows")
		}
	}
	at := key + "/" + strconv.Itoa(c.cls.Version) + "/" + language
	s.bmu.Lock()
	kept := s.bundles[at]
	s.bmu.Unlock()
	if kept != nil && kept.defined == s.defined {
		return kept.bundle, nil
	}
	b, err := s.buildBundle(c, language)
	if err != nil {
		return nil, err
	}
	s.bmu.Lock()
	if s.bundles == nil || len(s.bundles) >= keptBundles {
		s.bundles = map[string]*keptBundle{}
	}
	s.bundles[at] = &keptBundle{defined: s.defined, bundle: b}
	s.bmu.Unlock()
	return b, nil
}

// buildBundle builds the bundle of a version of a class in a language,
// the canonical tag or none. The caller holds cmu.
func (s *Store) buildBundle(c *class, language string) (*Bundle, error) {
	out := bundleJSON{
		Class: c.cls.Name, Version: c.cls.Version, Bytecode: formula.FormatVersion, Language: language,
		Languages: s.wordsLanguages(model.WordsOfClass, c.cls.Name),
		Words:     map[string]int{},
		Schemas:   map[string]json.RawMessage{},
		Compiled:  model.Compiled(c.cls),
	}
	w := s.lookupWords(model.WordsOfClass, c.cls.Name, language)
	var def *model.WordsDef
	if w != nil {
		def = w.def
		out.Words[w.name()] = w.version
	}
	text, err := model.PutClassWords(c.json, c.cls, def)
	if err != nil {
		return nil, storageErr("put the words in the class", err)
	}
	out.Definition = text
	for _, sch := range s.bundleSchemas(c.cls) {
		var raw []byte
		if sch.Predefined {
			raw = schemaJSON(sch.Def)
		} else {
			raw = s.schemaJSON[strings.ToUpper(sch.Name)]
		}
		var def *model.WordsDef
		if w := s.lookupWords(model.WordsOfSchema, sch.Name, language); w != nil {
			def = w.def
			out.Words[w.name()] = w.version
		}
		if raw, err = model.PutSchemaWords(raw, def); err != nil {
			return nil, storageErr("put the words in the schema", err)
		}
		out.Schemas[sch.Name] = raw
	}
	// The tag is worked out from the bundle without it, and the bundle
	// then carries it.
	plain, err := marshalBundle(&out)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(plain)
	out.Tag = hex.EncodeToString(sum[:8])
	text, err = marshalBundle(&out)
	if err != nil {
		return nil, err
	}
	return &Bundle{Class: c.cls.Name, Version: c.cls.Version, Tag: out.Tag, JSON: text}, nil
}

// lookupWords finds the words of a class or a schema in a language as
// the lookup of RFC 4647 does: pt-BR, then pt; nil when none of them has
// any. The caller holds cmu.
func (s *Store) lookupWords(kind, target, language string) *wordsDef {
	if language == "" {
		return nil
	}
	for _, tag := range model.LanguageLookup(language) {
		if w := s.findWords(kind, target, tag); w != nil && !w.removed {
			return w
		}
	}
	return nil
}

// wordsLanguages lists the languages a class or a schema has words in,
// in order. The caller holds cmu.
func (s *Store) wordsLanguages(kind, target string) []string {
	languages := []string{}
	for _, w := range s.words {
		if !w.removed && w.kind == kind && strings.EqualFold(w.target, target) {
			languages = append(languages, w.def.Language)
		}
	}
	sort.Strings(languages)
	return languages
}

// bundleSchemas lists the named schemas a class uses, the schemas they
// extend and the schemas of their object fields among them, in the
// order of the database's schemas. The caller holds cmu.
func (s *Store) bundleSchemas(cls *model.Class) []*model.Schema {
	seen := map[string]bool{}
	var walk func(sch *model.Schema)
	walk = func(sch *model.Schema) {
		if sch == nil || seen[strings.ToUpper(sch.Name)] {
			return
		}
		seen[strings.ToUpper(sch.Name)] = true
		if sch.Extends != "" {
			if base, ok := s.schemas.Lookup(sch.Extends); ok {
				walk(base)
			}
		}
		for _, f := range sch.Type.Fields() {
			if name := f.Type.Schema(); name != "" {
				if inner, ok := s.schemas.Lookup(name); ok {
					walk(inner)
				}
			}
		}
	}
	for _, sch := range cls.Schemas {
		walk(sch)
	}
	var out []*model.Schema
	for _, name := range s.schemas.Names() {
		if seen[strings.ToUpper(name)] {
			if sch, ok := s.schemas.Lookup(name); ok {
				out = append(out, sch)
			}
		}
	}
	return out
}

// marshalBundle writes the bundle as compact JSON, leaving <, > and &
// as they are written.
func marshalBundle(b *bundleJSON) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(b); err != nil {
		return nil, storageErr("write the bundle", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}
