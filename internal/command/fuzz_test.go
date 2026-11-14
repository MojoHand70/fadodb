package command

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/MojoHand70/fadodb/internal/store"
)

// The reader of a command's object walks bytes that encoding/json has
// called valid, without checking again where a value ends. These two
// hold it to that: whatever is sent, a command is answered and nothing
// panics, and the members it finds are the ones encoding/json finds.

func FuzzExec(f *testing.F) {
	for _, seed := range []string{
		`{}`, ``, `[]`, `null`, `"x"`, `{"class": "Invoice"}`, `{"class": "Invoice", "id": "7", "values": {"note": "x"}}`,
		`{"class": "Invoice", "values": {"number": "1", "net": "1.00 PLN"}, "id": "a/b"}`,
		`{"class":"Invoice","id":"7","valeus":{}}`, `{"class": "Invoice", "id": "7", "id": "8"}`,
		`{"class": null, "id": "\u0000"}`, `{"class": "Invoice", "id": "\"\\"}`, `{"class": "Invoice", "where": "STATE() = \"draft\"", "order": ["-number"], "limit": 1e3}`,
		` { "class" : "Invoice" , "id" : "7" , "at" : 1763026200 } `, `{"class": "Invoice", "ids": ["a", ["b"]], "transition": "pay"}`,
		`{"definition": {"name": "X", "variables": {"a": {"type": "text"}}}}`, `{"statement": "SELECT 1", "args": [1, 1.5, "a", null, true, {"x": []}]}`,
		`{"class": "Invoice", "documents": [{"values": {"number": "1"}}, {"values": null}, {}, 7]}`, `{"a":{"b":[{"c":"}"}],"d":"]"}}`,
	} {
		f.Add("set", []byte(seed))
		f.Add("create", []byte(seed))
		f.Add("query", []byte(seed))
		f.Add("create_many", []byte(seed))
		f.Add("sql", []byte(seed))
		f.Add("validate_class", []byte(seed))
		f.Add("nothing", []byte(seed))
	}
	s := open(f)
	define(f, s)
	ctx := context.Background()
	f.Fuzz(func(t *testing.T, name string, object []byte) {
		answer, err := Exec(ctx, s, olga, name, object)
		var out map[string]json.RawMessage
		if jerr := json.Unmarshal(answer, &out); jerr != nil {
			t.Fatalf("%s %q was answered with what is not a JSON object: %q", name, object, answer)
		}
		if _, refused := out["error"]; refused != (err != nil) {
			t.Fatalf("%s %q: the answer %q and the error %v do not agree", name, object, answer, err)
		}
		if Code(err) == CodeInternal {
			t.Fatalf("%s %q failed in the engine: %v", name, object, err)
		}
	})
}

func FuzzMembers(f *testing.F) {
	for _, seed := range []string{`{}`, `{"a": 1}`, ` {"a" : [1, {"b": "]}"}], "c": "\"", "d": null, "e": -1.5e-3, "f": {"g": {}}} `,
		`{"a": "\\", "zażółć": "发票", "~/": true, "": false}`, `[1]`, `7`, `"{"`} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, object []byte) {
		if !json.Valid(object) {
			t.Skip()
		}
		var want map[string]json.RawMessage
		isObject := json.Unmarshal(object, &want) == nil && want != nil
		got := map[string][]byte{}
		found, e := members(object, func(name string, value []byte) *store.Error {
			got[name] = value
			return nil
		})
		if e != nil || found != isObject {
			t.Fatalf("%q: an object %v, read as one %v, %v", object, isObject, found, e)
		}
		if !isObject {
			return
		}
		if len(got) != len(want) {
			t.Fatalf("%q: %d members, encoding/json reads %d", object, len(got), len(want))
		}
		for name, value := range want {
			var a, b bytes.Buffer
			if json.Compact(&a, value) != nil || json.Compact(&b, got[name]) != nil || !bytes.Equal(a.Bytes(), b.Bytes()) {
				t.Fatalf("%q: the member %q is %q, encoding/json reads %q", object, name, got[name], value)
			}
			// A list is walked to its end, element by element.
			if len(got[name]) > 0 && got[name][0] == '[' {
				var elems []json.RawMessage
				_ = json.Unmarshal(value, &elems)
				n := 0
				if e := elements(got[name], func(i int, element []byte) *store.Error {
					var x, y bytes.Buffer
					if i >= len(elems) || json.Compact(&x, elems[i]) != nil || json.Compact(&y, element) != nil || !bytes.Equal(x.Bytes(), y.Bytes()) {
						t.Fatalf("%q: element %d of %q is %q", object, i, name, element)
					}
					n++
					return nil
				}); e != nil || n != len(elems) {
					t.Fatalf("%q: %q has %d elements, read %d", object, name, len(elems), n)
				}
			}
		}
	})
}
