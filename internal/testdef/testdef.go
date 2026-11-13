// Package testdef opens the definitions the tests of this module use.
//
// A rule a class leaves out lets nobody through (ADR-0052, item 5), and
// the same holds for a table. The tests that are not about who may see
// or change a document define their classes through Open, which writes
// TRUE for every rule a definition leaves out: the class is then open to
// every caller, a call without a principal too, as every class was
// before that ruling. The tests of the rules themselves define their
// classes as they are written. Nothing outside the tests uses this
// package.
package testdef

import (
	"bytes"
	"encoding/json"
)

// Open is the class definition with each access rule it leaves out
// written as TRUE: read, create, update and delete, or read and create
// for a class without states, which has no documents to change and
// whose previews ask those two. A definition that is not a JSON object
// is returned as it is, for the test that wants it refused.
func Open(def []byte) []byte {
	rules := []string{"read", "create", "update", "delete"}
	var top map[string]json.RawMessage
	if json.Unmarshal(def, &top) != nil {
		return def
	}
	if _, ok := top["states"]; !ok {
		rules = []string{"read", "create"}
	}
	return fill(def, top, rules)
}

// OpenTable is the table definition with the read and the write rule it
// leaves out written as TRUE.
func OpenTable(def []byte) []byte {
	var top map[string]json.RawMessage
	if json.Unmarshal(def, &top) != nil {
		return def
	}
	return fill(def, top, []string{"read", "write"})
}

// OpenAll opens each definition of a set.
func OpenAll(defs [][]byte) [][]byte {
	out := make([][]byte, len(defs))
	for i, def := range defs {
		out[i] = Open(def)
	}
	return out
}

// fill writes TRUE for each of the rules the access block of the
// definition lacks, and leaves the rest of the text as it is.
func fill(def []byte, top map[string]json.RawMessage, rules []string) []byte {
	access := map[string]json.RawMessage{}
	raw, has := top["access"]
	if has && json.Unmarshal(raw, &access) != nil {
		return def
	}
	changed := false
	for _, k := range rules {
		if _, ok := access[k]; !ok {
			access[k], changed = json.RawMessage(`"TRUE"`), true
		}
	}
	if !changed {
		return def
	}
	filled, _ := json.Marshal(access)
	if has {
		return bytes.Replace(def, raw, filled, 1)
	}
	end := bytes.LastIndexByte(def, '}')
	out := append([]byte{}, def[:end]...)
	if len(top) > 0 {
		out = append(out, ", "...)
	}
	out = append(append(out, `"access": `...), filled...)
	return append(out, def[end:]...)
}
