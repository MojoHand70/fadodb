package store

import (
	"bytes"
	"context"
	"strconv"
	"strings"

	"github.com/MojoHand70/fadodb/internal/model"
)

// An app is a name for the classes that belong together (ADR-0077):
// Expenses, a help desk. The database keeps its definition, the words it
// says of itself to people, and each class names the app it belongs to.
// An app holds nothing else: no rule and no right hangs on it.

// App is an app as the database holds it: its definition and the version
// the database gave it.
type App struct {
	Name    string
	Version int
	Def     *model.AppDef
}

// app is a stored app: what was parsed and the text as it was given.
type app struct {
	App
	json []byte
}

// DefineApp checks the definition of an app and stores it (ADR-0077). It
// is for a designer. Defining the same definition again is a no-op;
// another definition under a name the database has is the next version
// of that app, and the database keeps the current one.
func (s *Store) DefineApp(ctx context.Context, data []byte, opts ...Option) (*App, error) {
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
	compact, err := compactJSON(data)
	if err != nil {
		return nil, model.Diagnostics{{Code: model.CodeBadJSON, Pos: -1, Message: err.Error()}}
	}
	def, diags := model.ParseApp(data)
	if len(diags) > 0 {
		return nil, diags
	}
	key := strings.ToUpper(def.Name)
	version := 1
	if old, ok := s.apps[key]; ok {
		if bytes.Equal(old.json, compact) {
			held := old.App
			return &held, nil
		}
		if old.Name != def.Name {
			return nil, model.Diagnostics{{Code: model.CodeDuplicate, Path: "/name", Pos: -1,
				Message: "the app " + strconv.Quote(old.Name) + " is defined; names differ in case only",
				Hint:    "write the name as " + old.Name}}
		}
		version = old.Version + 1
	}
	now := s.now().Unix()
	if _, err := s.writer.ExecContext(ctx, "INSERT INTO apps (name, version, json, created_at, updated_at) VALUES (?, ?, ?, ?, ?)"+
		" ON CONFLICT (name) DO UPDATE SET version = excluded.version, json = excluded.json, updated_at = excluded.updated_at",
		def.Name, version, string(compact), now, now); err != nil {
		return nil, storageErr("store the app", err)
	}
	if _, err := recordDefinition(ctx, s.writer, DefinitionApp, def.Name, version, o.principal, now); err != nil {
		return nil, err
	}
	if _, ok := s.apps[key]; !ok {
		s.appOrder = append(s.appOrder, def.Name)
	}
	made := &app{App: App{Name: def.Name, Version: version, Def: def}, json: compact}
	s.apps[key] = made
	held := made.App
	return &held, nil
}

// Apps lists the names of the apps in the order they were defined.
func (s *Store) Apps() []string {
	s.cmu.RLock()
	defer s.cmu.RUnlock()
	return append([]string(nil), s.appOrder...)
}

// App returns an app by name.
func (s *Store) App(name string) (*App, bool) {
	s.cmu.RLock()
	defer s.cmu.RUnlock()
	a, ok := s.apps[strings.ToUpper(name)]
	if !ok {
		return nil, false
	}
	held := a.App
	return &held, true
}

// ClassesOf lists the classes that belong to an app, in the order they
// were defined. An app nothing belongs to yet answers an empty list; a
// name that is no app is refused.
func (s *Store) ClassesOf(name string) ([]string, error) {
	s.cmu.RLock()
	defer s.cmu.RUnlock()
	if _, ok := s.apps[strings.ToUpper(name)]; !ok {
		return nil, s.noSuchApp(name)
	}
	out := []string{}
	for _, class := range s.order {
		if c, ok := s.classes[strings.ToUpper(class)]; ok && strings.EqualFold(c.cls.App, name) {
			out = append(out, class)
		}
	}
	return out, nil
}

// noSuchApp is the refusal of a name that is no app. The caller holds
// cmu.
func (s *Store) noSuchApp(name string) *Error {
	if len(s.appOrder) == 0 {
		return hintf(CodeUnknownApp, "", "unknown app "+strconv.Quote(name), "the database has no app yet; define_app makes one")
	}
	return hintf(CodeUnknownApp, "", "unknown app "+strconv.Quote(name), "the apps are "+strings.Join(s.appOrder, ", "))
}

// storedApp answers an app as it was given. The caller holds cmu.
func (s *Store) storedApp(name string, version int) (*StoredDefinition, *Error) {
	a, ok := s.apps[strings.ToUpper(name)]
	if !ok {
		return nil, s.noSuchApp(name)
	}
	if version != 0 && version != a.Version {
		return nil, onlyCurrent("app", a.Name, a.Version)
	}
	return &StoredDefinition{Name: a.Name, Version: a.Version, Definition: copyJSON(a.json)}, nil
}

// loadApps reads the stored apps in the order they were defined. The
// caller is Open, before the classes are compiled.
func (s *Store) loadApps(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, "SELECT name, version, json FROM apps ORDER BY rowid")
	if err != nil {
		return storageErr("read the apps", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var version int
		var text []byte
		if err := rows.Scan(&name, &version, &text); err != nil {
			return storageErr("read the apps", err)
		}
		def, diags := model.ParseApp(text)
		if len(diags) > 0 {
			return &Error{Code: CodeStorage, Message: "the stored app " + name + " cannot be read: " + diags.Error()}
		}
		s.apps[strings.ToUpper(def.Name)] = &app{App: App{Name: def.Name, Version: version, Def: def}, json: text}
		s.appOrder = append(s.appOrder, def.Name)
	}
	if err := rows.Err(); err != nil {
		return storageErr("read the apps", err)
	}
	return nil
}
