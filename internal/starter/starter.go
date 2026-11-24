// Package starter holds the starters a database can be made with
// (ADR-0087): each is an app ready to use, kept as the commands of the
// command set that make it, in the order they are sent. The program
// carries them; `fadodb init FILE --starter NAME` sends one to the
// database it makes.
package starter

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

//go:embed *.json
var files embed.FS

// Starter is one starter: its name, what it is, and the commands that
// make it.
type Starter struct {
	// Name is what --starter names it by: the name of its file.
	Name string `json:"name"`
	// Label and Description are what a person reads of it.
	Label       string `json:"label"`
	Description string `json:"description"`
	// Next says what the people of a database made with it do first,
	// once its first administrator serves it.
	Next string `json:"next"`
	// Commands make it, sent in this order.
	Commands []Command `json:"commands"`
}

// Command is one command of the command set, as Exec takes it: its
// name, and its object.
type Command struct {
	Command string          `json:"command"`
	Object  json.RawMessage `json:"object"`
}

// Names lists the starters, in the order of their names.
func Names() []string {
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if name, ok := strings.CutSuffix(e.Name(), ".json"); ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

// Get answers the starter of a name, or says which there are.
func Get(name string) (*Starter, error) {
	var text []byte
	err := fs.ErrNotExist
	if name != "" && !strings.ContainsAny(name, `/\.`) {
		text, err = files.ReadFile(name + ".json")
	}
	if err != nil {
		return nil, fmt.Errorf("there is no starter %q: the starters are %s", name, strings.Join(Names(), ", "))
	}
	var s Starter
	d := json.NewDecoder(bytes.NewReader(text))
	d.DisallowUnknownFields()
	if err := d.Decode(&s); err != nil {
		return nil, fmt.Errorf("the starter %s is not read: %w", name, err)
	}
	if s.Name != name {
		return nil, fmt.Errorf("the starter in %s.json is named %q", name, s.Name)
	}
	return &s, nil
}

// Made says what a command of a starter makes, as a person reads it:
// "the app Expenses", "the group hr".
func (c Command) Made() string {
	var o struct {
		Name       string `json:"name"`
		Definition struct {
			Name string `json:"name"`
		} `json:"definition"`
	}
	_ = json.Unmarshal(c.Object, &o)
	name := o.Definition.Name
	if name == "" {
		name = o.Name
	}
	what, ok := made[c.Command]
	if !ok {
		what = c.Command
	}
	return what + " " + name
}

// made is what each command a starter sends makes.
var made = map[string]string{
	"define_app":    "the app",
	"create_group":  "the group",
	"define_schema": "the schema",
	"define_table":  "the table",
	"define_class":  "the class",
	"define_words":  "the words of",
}
