package server

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// statuses is the table of ADR-0055, item 8, with the two codes that
// ADR-0059, item 2, added to it, written out here a second time so that
// a change to Status that the records do not carry fails.
var statuses = map[int][]string{
	400: {"bad_json"},
	401: {"unauthenticated"},
	403: {"forbidden", "reason_required"},
	404: {"unknown_command", "unknown_class", "unknown_document", "unknown_table", "unknown_schema", "unknown_action", "unknown_blob", "unknown_row"},
	409: {"key_reused", "duplicate", "wrong_state", "terminal", "guard_false"},
	413: {"too_large", "blob_too_large"},
	500: {"internal", "storage"},
	503: {"closed", "busy"},
	504: {"timeout"},
}

// codesIn reads the codes a file of the engine declares: the constants
// named Code… with a text.
func codesIn(t *testing.T, file string) []string {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var codes []string
	for _, decl := range parsed.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			v := spec.(*ast.ValueSpec)
			for i, name := range v.Names {
				if !strings.HasPrefix(name.Name, "Code") || i >= len(v.Values) {
					continue
				}
				if lit, ok := v.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					code, err := strconv.Unquote(lit.Value)
					if err != nil {
						t.Fatal(err)
					}
					codes = append(codes, code)
				}
			}
		}
	}
	if len(codes) == 0 {
		t.Fatalf("%s declares no code", file)
	}
	return codes
}

// Every code the engine, the command set and the door can answer has
// the status the records give it: the ones the table names, and 422 for
// every other refusal. A code that the table names and nothing declares
// fails too, so the table cannot keep a code that was renamed.
func TestTheStatusOfEveryCode(t *testing.T) {
	want := map[string]int{}
	for status, codes := range statuses {
		for _, code := range codes {
			want[code] = status
		}
	}
	declared := map[string]bool{}
	for _, file := range []string{
		filepath.Join("..", "store", "errors.go"),
		filepath.Join("..", "command", "object.go"),
		"codes.go",
	} {
		for _, code := range codesIn(t, file) {
			declared[code] = true
			status, named := want[code]
			if !named {
				status = 422
			}
			if got := Status(code); got != status {
				t.Errorf("%s is answered with %d, want %d", code, got, status)
			}
		}
	}
	for code := range want {
		if !declared[code] {
			t.Errorf("the table of statuses names %s, which nothing declares", code)
		}
	}
	if len(declared) < 45 {
		t.Errorf("only %d codes were found", len(declared))
	}
	if got := Status(""); got != 200 {
		t.Errorf("a command that ran is answered with %d", got)
	}
	// bad_definition is a refusal like any other that the table does not
	// name: the definition was read and the engine does not take it.
	if got := Status("bad_definition"); got != 422 {
		t.Errorf("bad_definition is answered with %d", got)
	}
}
