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

// The words of a class, a table or a schema in another language
// (ADR-0068, item 6; ADR-0069, item 6). Each is a definition of its own,
// kept under what it translates and its language, with every version it
// had and who defined each. Defining one makes no new version of what it
// translates and touches no document.

// DefinitionWords is what Definitions and Definition call a definition
// of words. Its name is what it translates, the name of that and the
// language: class/Invoice/pl (model.WordsName).
const DefinitionWords = "words"

// WordsOf names the words of one thing in one language, or, for
// ListWords, the ones to list: one of Class, Table and Schema, and the
// language. A field left out lists them all.
type WordsOf struct {
	Class    string `json:"class,omitempty"`
	Table    string `json:"table,omitempty"`
	Schema   string `json:"schema,omitempty"`
	Language string `json:"language,omitempty"`
}

// WordsInfo is one definition of words, as ListWords lists it and
// DefineWords returns it.
type WordsInfo struct {
	// Name is what Definition and Definitions take: class/Invoice/pl.
	Name     string `json:"name"`
	Class    string `json:"class,omitempty"`
	Table    string `json:"table,omitempty"`
	Schema   string `json:"schema,omitempty"`
	Language string `json:"language"`
	Version  int    `json:"version"`
	// Defined says who defined this version and when.
	Defined *Defined `json:"defined,omitempty"`
	// Notes name each path the current version of what it translates no
	// longer has, code unused_words.
	Notes model.Diagnostics `json:"notes,omitempty"`
}

// wordsDef is one definition of words of the database. removed marks one
// that was taken away: its versions stay.
type wordsDef struct {
	def     *model.WordsDef
	kind    string
	target  string
	json    []byte
	version int
	defined Defined
	removed bool
}

// wordsKey is the key a definition is held under: the kind, the name of
// what it translates as it is compared, and the language.
func wordsKey(kind, target, language string) string {
	return kind + "/" + strings.ToUpper(target) + "/" + language
}

// name is what Definition and Definitions take.
func (w *wordsDef) name() string { return model.WordsName(w.kind, w.target, w.def.Language) }

// unknownWords is the refusal of words a database does not have.
func unknownWords(kind, target, language string) *Error {
	return hintf(CodeUnknownWords, "", "the database has no words of the "+kind+" "+strconv.Quote(target)+" in "+strconv.Quote(language),
		"list_words lists them; a designer defines them with define_words")
}

// wordsTarget finds what a definition of words translates, in its
// current version, as a definition names it. The caller holds cmu.
func (s *Store) wordsTarget(kind, target string) (model.WordsTarget, string, *Error) {
	switch kind {
	case model.WordsOfClass:
		c, ok := s.classes[strings.ToUpper(target)]
		if !ok {
			return model.WordsTarget{}, "", hintf(CodeUnknownClass, "/class", "unknown class "+strconv.Quote(target), "list_classes lists them; the class is defined before its words")
		}
		return model.WordsTarget{Class: c.cls}, c.cls.Name, nil
	case model.WordsOfTable:
		t, ok := s.tables[strings.ToUpper(target)]
		if !ok {
			return model.WordsTarget{}, "", hintf(CodeUnknownTable, "/table", "unknown table "+strconv.Quote(target), "list_tables lists them; the table is defined before its words")
		}
		return model.WordsTarget{Table: t.def}, t.def.Name, nil
	case model.WordsOfSchema:
		sch, ok := s.schemas.Lookup(target)
		if !ok {
			return model.WordsTarget{}, "", hintf(CodeUnknownSchema, "/schema", "unknown schema "+strconv.Quote(target), "list_schemas lists them; the schema is defined before its words")
		}
		return model.WordsTarget{Schema: sch}, sch.Name, nil
	}
	return model.WordsTarget{}, "", hintf(CodeBadValue, "", "words are of a class, a table or a schema", `name one: "class": "Invoice"`)
}

// wordsInfo is a definition as a list shows it, with its notes against
// the current version of what it translates. The caller holds cmu.
func (s *Store) wordsInfo(w *wordsDef) WordsInfo {
	info := WordsInfo{Name: w.name(), Language: w.def.Language, Version: w.version}
	switch w.kind {
	case model.WordsOfClass:
		info.Class = w.target
	case model.WordsOfTable:
		info.Table = w.target
	case model.WordsOfSchema:
		info.Schema = w.target
	}
	if w.defined != (Defined{}) {
		d := w.defined
		info.Defined = &d
	}
	if t, _, e := s.wordsTarget(w.kind, w.target); e == nil {
		info.Notes = model.UnusedWords(w.def, t)
	}
	return info
}

// DefineWords checks the words of a class, a table or a schema in one
// language against the current version of what they translate, and
// stores them as the next version of those words: a path that thing
// does not have is refused with bad_value. The same words again are a
// no-op. It is for the system group designers, as defining a class is.
func (s *Store) DefineWords(ctx context.Context, data []byte, opts ...Option) (*WordsInfo, error) {
	o, e := s.worker(ctx, workDesign, opts)
	if e != nil {
		return nil, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errf(CodeClosed, "", "the store is closed")
	}
	s.cmu.Lock()
	defer s.cmu.Unlock()
	// A bundle built before this definition is built again (ADR-0070).
	s.defined++
	compact, err := compactJSON(data)
	if err != nil {
		return nil, model.Diagnostics{{Code: model.CodeBadJSON, Pos: -1, Message: err.Error()}}
	}
	def, diags := model.ParseWords(data)
	if len(diags) > 0 {
		return nil, diags
	}
	kind, given := def.Kind()
	t, target, te := s.wordsTarget(kind, given)
	if te != nil {
		return nil, model.Diagnostics{{Code: te.Code, Path: te.Path, Pos: -1, Message: te.Message, Hint: te.Hint}}
	}
	key := wordsKey(kind, target, def.Language)
	old := s.words[key]
	if old != nil && !old.removed && bytes.Equal(old.json, compact) {
		info := s.wordsInfo(old)
		return &info, nil
	}
	if diags := model.CheckWords(def, t); len(diags) > 0 {
		return nil, diags
	}
	version := 1
	if old != nil {
		version = old.version + 1
	}
	if def.Version != 0 && def.Version != version {
		return nil, model.Diagnostics{{Code: model.CodeBadValue, Path: "/version", Pos: -1,
			Message: "these words are at version " + strconv.Itoa(version-1) + "; a redefinition is version " + strconv.Itoa(version),
			Hint:    "omit version, or write " + strconv.Itoa(version)}}
	}
	now := s.now().Unix()
	w := s.writer
	if _, err := w.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return nil, storageErr("begin", err)
	}
	rollback := func(err error) (*WordsInfo, error) {
		_, _ = w.ExecContext(ctx, "ROLLBACK")
		return nil, err
	}
	if _, err := w.ExecContext(ctx, "INSERT INTO words (kind, target, language, version, json, removed, created_at, updated_at) VALUES (?, ?, ?, ?, ?, 0, ?, ?)"+
		" ON CONFLICT (kind, target, language) DO UPDATE SET target = excluded.target, version = excluded.version, json = excluded.json, removed = 0, updated_at = excluded.updated_at",
		kind, target, def.Language, version, string(compact), now, now); err != nil {
		return rollback(storageErr("store the words", err))
	}
	if _, err := w.ExecContext(ctx, "INSERT INTO words_versions (kind, target, language, version, json, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		kind, target, def.Language, version, string(compact), now); err != nil {
		return rollback(storageErr("store the version of the words", err))
	}
	defined, derr := recordDefinition(ctx, w, DefinitionWords, model.WordsName(kind, target, def.Language), version, o.principal, now)
	if derr != nil {
		return rollback(derr)
	}
	if err := s.endTransaction(ctx); err != nil {
		return nil, storageErr("commit", err)
	}
	stored := &wordsDef{def: def, kind: kind, target: target, json: compact, version: version, defined: defined}
	s.registerWords(stored)
	info := s.wordsInfo(stored)
	return &info, nil
}

// registerWords makes a definition the current one of its key. The
// caller holds cmu.
func (s *Store) registerWords(w *wordsDef) {
	key := wordsKey(w.kind, w.target, w.def.Language)
	if _, exists := s.words[key]; !exists {
		s.wordsOrder = append(s.wordsOrder, key)
	}
	s.words[key] = w
}

// ListWords lists the definitions of words of the database in the order
// they were first defined, each with its version, who defined it and a
// note for each path that what it translates no longer has. A field of
// of narrows the list. It is open to every caller, as reading a
// definition is.
func (s *Store) ListWords(of WordsOf) []WordsInfo {
	s.cmu.RLock()
	defer s.cmu.RUnlock()
	out := []WordsInfo{}
	for _, key := range s.wordsOrder {
		w := s.words[key]
		if w.removed || of.Language != "" && !strings.EqualFold(of.Language, w.def.Language) {
			continue
		}
		if kind, target := of.kind(); kind != "" && (kind != w.kind || !strings.EqualFold(target, w.target)) {
			continue
		}
		out = append(out, s.wordsInfo(w))
	}
	return out
}

func (of WordsOf) kind() (string, string) {
	return (&model.WordsDef{Class: of.Class, Table: of.Table, Schema: of.Schema}).Kind()
}

// RemoveWords takes away the words of one thing in one language: a page
// shows that thing in its own words from then on. Their versions stay,
// and defining them again makes their next version. It is for the system
// group designers.
func (s *Store) RemoveWords(ctx context.Context, of WordsOf, opts ...Option) error {
	if _, e := s.worker(ctx, workDesign, opts); e != nil {
		return e
	}
	kind, target := of.kind()
	if kind == "" || of.Language == "" {
		return hintf(CodeBadValue, "", "remove_words names one class, table or schema and the language",
			`write {"class": "Invoice", "language": "pl"}`)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errf(CodeClosed, "", "the store is closed")
	}
	s.cmu.Lock()
	defer s.cmu.Unlock()
	// A bundle built before this definition is built again (ADR-0070).
	s.defined++
	w := s.findWords(kind, target, of.Language)
	if w == nil || w.removed {
		return unknownWords(kind, target, of.Language)
	}
	if _, err := s.writer.ExecContext(ctx, "UPDATE words SET removed = 1, updated_at = ? WHERE kind = ? AND target = ? AND language = ?",
		s.now().Unix(), w.kind, w.target, w.def.Language); err != nil {
		return storageErr("remove the words", err)
	}
	gone := *w
	gone.removed = true
	s.words[wordsKey(w.kind, w.target, w.def.Language)] = &gone
	return nil
}

// findWords finds a definition by what it translates and its language,
// compared without regard to case. The caller holds cmu.
func (s *Store) findWords(kind, target, language string) *wordsDef {
	if w := s.words[wordsKey(kind, target, language)]; w != nil {
		return w
	}
	for _, w := range s.words {
		if w.kind == kind && strings.EqualFold(w.target, target) && strings.EqualFold(w.def.Language, language) {
			return w
		}
	}
	return nil
}

// loadWords reads the stored words in the order they were first defined.
// They were checked when they were defined; what they translate may have
// changed since, which their notes say. The caller is Open, after the
// classes, the tables and the schemas are loaded.
func (s *Store) loadWords(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, "SELECT kind, target, version, json, removed FROM words ORDER BY rowid")
	if err != nil {
		return storageErr("read the words", err)
	}
	defer rows.Close()
	var loaded []*wordsDef
	for rows.Next() {
		w := &wordsDef{}
		var src []byte
		if err := rows.Scan(&w.kind, &w.target, &w.version, &src, &w.removed); err != nil {
			return storageErr("read the words", err)
		}
		def, diags := model.ParseWords(src)
		if len(diags) > 0 {
			return &Error{Code: CodeStorage, Message: "the stored words of " + w.kind + " " + w.target + " do not read: " + diags.Error()}
		}
		w.def, w.json = def, src
		loaded = append(loaded, w)
	}
	if err := rows.Err(); err != nil {
		return storageErr("read the words", err)
	}
	for _, w := range loaded {
		s.registerWords(w)
	}
	return s.loadWordsDefined(ctx)
}

// loadWordsDefined gives every definition of words the record of its
// current version.
func (s *Store) loadWordsDefined(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, "SELECT name, version, defined_at, defined_by, defined_kind, defined_for FROM definitions WHERE what = ?", DefinitionWords)
	if err != nil {
		return storageErr("read who defined the words", err)
	}
	defer rows.Close()
	byName := map[string]*wordsDef{}
	for _, w := range s.words {
		byName[strings.ToUpper(w.name())] = w
	}
	for rows.Next() {
		var name string
		var version int
		var d Defined
		var by, kind, acts sql.NullString
		if err := rows.Scan(&name, &version, &d.At, &by, &kind, &acts); err != nil {
			return storageErr("read who defined the words", err)
		}
		if w := byName[strings.ToUpper(name)]; w != nil && w.version == version {
			d.By, d.Kind, d.For = by.String, kind.String, acts.String
			w.defined = d
		}
	}
	return storageErr("read who defined the words", rows.Err())
}

// storedWords finds the text and the version of a definition of words
// for Definition: the current one from memory, an earlier one from its
// table. name is class/Invoice/pl. Removed words are read like any
// other: their versions stay. The caller holds cmu.
func (s *Store) storedWords(ctx context.Context, name string, version int) (*StoredDefinition, *Error) {
	kind, rest, _ := strings.Cut(name, "/")
	target, language, ok := cutLast(rest, "/")
	if !ok || kind == "" || target == "" || language == "" {
		return nil, hintf(CodeBadValue, "", "the name of words is what they translate, its name and the language: class/Invoice/pl, got "+strconv.Quote(name),
			"get_words takes the class, the table or the schema and the language apart")
	}
	w := s.findWords(kind, target, language)
	if w == nil {
		return nil, unknownWords(kind, target, language)
	}
	info := s.wordsInfo(w)
	if version == 0 || version == w.version {
		return &StoredDefinition{Name: w.name(), Version: w.version, Notes: info.Notes, Definition: copyJSON(w.json)}, nil
	}
	var text string
	err := s.db.QueryRowContext(ctx, "SELECT json FROM words_versions WHERE kind = ? AND target = ? AND language = ? AND version = ?",
		w.kind, w.target, w.def.Language, version).Scan(&text)
	if err == sql.ErrNoRows {
		return nil, hintf(CodeUnknownWords, "", "the words "+w.name()+" have no version "+strconv.Itoa(version),
			"their current version is "+strconv.Itoa(w.version)+"; definitions lists every version with who defined it")
	}
	if err != nil {
		return nil, asError(storageErr("read the version of the words", err))
	}
	return &StoredDefinition{Name: w.name(), Version: version, Definition: json.RawMessage(text)}, nil
}

// cutLast cuts s around the last sep.
func cutLast(s, sep string) (before, after string, found bool) {
	if i := strings.LastIndex(s, sep); i >= 0 {
		return s[:i], s[i+len(sep):], true
	}
	return s, "", false
}
