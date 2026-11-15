package docgen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/internal/command"
)

// TestGenerated keeps the three generated chapters of the manual, and
// the description of the commands for other languages, equal to what
// the engine has. It fails when a function, a code or a command was
// added, changed or removed and the files were not written again; run
//
//	make docs
//
// which is this test with FADODB_UPDATE_DOCS set, and commit the result.
func TestGenerated(t *testing.T) {
	refusals, err := Refusals("..")
	if err != nil {
		t.Fatal(err)
	}
	commands, err := Commands("..")
	if err != nil {
		t.Fatal(err)
	}
	description, err := OpenAPI("..")
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"docs/manual/functions.md": Functions(),
		"docs/manual/refusals.md":  refusals,
		"docs/manual/commands.md":  commands,
		"api/openapi.json":         description,
	} {
		path := filepath.Join("..", "..", filepath.FromSlash(name))
		if os.Getenv("FADODB_UPDATE_DOCS") != "" {
			if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s is missing: run `make docs`: %v", name, err)
		}
		// A checkout on Windows may carry CRLF line endings.
		if strings.ReplaceAll(string(got), "\r\n", "\n") != want {
			t.Errorf("%s is not what the engine has now: run `make docs` and commit the result", name)
		}
	}
}

// The description of the commands has every command of the table, with
// the members of its object, and no address that is none: it is an
// OpenAPI 3.1 document whose references all lead somewhere.
func TestTheDescriptionOfTheCommands(t *testing.T) {
	text, err := OpenAPI("..")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		OpenAPI string `json:"openapi"`
		Info    struct{ Title, Version string }
		Paths   map[string]map[string]struct {
			OperationID string `json:"operationId"`
			Summary     string
			Tags        []string
			Stream      bool `json:"x-fado-stream"`
			RequestBody struct {
				Content map[string]struct {
					Schema struct {
						Properties           map[string]map[string]any
						Required             []string
						AdditionalProperties *bool `json:"additionalProperties"`
					}
				}
			} `json:"requestBody"`
			Responses map[string]struct {
				Ref     string `json:"$ref"`
				Content map[string]any
			}
		}
		Tags       []struct{ Name string }
		Components struct {
			Schemas, Responses, SecuritySchemes map[string]any
		}
	}
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatalf("the description is not JSON: %v", err)
	}
	if doc.OpenAPI != "3.1.0" || doc.Info.Title != "FadoDB" || doc.Info.Version != APIVersion {
		t.Errorf("the description says it is %q of %q, version %q", doc.OpenAPI, doc.Info.Title, doc.Info.Version)
	}
	tags := map[string]bool{}
	for _, tag := range doc.Tags {
		tags[tag.Name] = true
	}
	// Every command, as the table has it.
	ids := map[string]string{}
	for _, c := range command.Commands() {
		op, found := doc.Paths["/v1/"+c.Name]["post"]
		if !found || len(doc.Paths["/v1/"+c.Name]) != 1 {
			t.Errorf("the description has no POST /v1/%s, or more than it", c.Name)
			continue
		}
		if op.OperationID != c.Name || op.Summary != c.Does || len(op.Tags) != 1 || op.Tags[0] != c.Group || op.Stream != c.Stream {
			t.Errorf("%s is described as %q, %q, of %v", c.Name, op.OperationID, op.Summary, op.Tags)
		}
		body := op.RequestBody.Content["application/json"].Schema
		if body.AdditionalProperties == nil || *body.AdditionalProperties {
			t.Errorf("%s: the description lets a member through that the command does not have", c.Name)
		}
		var required []string
		members := 0
		for _, f := range c.Fields {
			if f.Refused {
				if _, shown := body.Properties[f.Name]; shown {
					t.Errorf("%s: the description shows %s, which the command refuses", c.Name, f.Name)
				}
				continue
			}
			members++
			if got := body.Properties[f.Name]["description"]; got != f.Does {
				t.Errorf("%s: the member %s is described as %v", c.Name, f.Name, got)
			}
			if f.Required {
				required = append(required, f.Name)
			}
		}
		if len(body.Properties) != members || strings.Join(body.Required, ",") != strings.Join(required, ",") {
			t.Errorf("%s: the description has %d members and needs %v; the command has %d and needs %v", c.Name, len(body.Properties), body.Required, members, required)
		}
		kind := "application/json"
		if c.Stream {
			kind = "text/event-stream"
		}
		if _, answers := op.Responses["200"].Content[kind]; !answers || op.Responses["default"].Ref != "#/components/responses/Refused" {
			t.Errorf("%s: the description does not say what it answers as %s, or how it is refused", c.Name, kind)
		}
	}
	// And nothing else but the files and what the server answers itself.
	besides := map[string]string{"/v1/blobs": "post", "/v1/file": "get", "/v1/blob": "get", "/v1/health": "get", "/v1/metrics": "get"}
	if want := len(command.Commands()) + len(besides); len(doc.Paths) != want {
		t.Errorf("the description has %d addresses, want %d", len(doc.Paths), want)
	}
	for path, method := range besides {
		if _, found := doc.Paths[path][method]; !found || len(doc.Paths[path]) != 1 {
			t.Errorf("the description has no %s %s, or more than it", method, path)
		}
	}
	for path, methods := range doc.Paths {
		for method, op := range methods {
			if was, twice := ids[op.OperationID]; twice || op.OperationID == "" {
				t.Errorf("%s %s has the name %q, as %s has", method, path, op.OperationID, was)
			}
			ids[op.OperationID] = path
			for _, tag := range op.Tags {
				if !tags[tag] {
					t.Errorf("%s %s is of %q, which the description does not list", method, path, tag)
				}
			}
		}
	}
	// Every reference leads somewhere.
	for _, ref := range regexp.MustCompile(`"\$ref": "([^"]+)"`).FindAllStringSubmatch(text, -1) {
		parts := strings.Split(ref[1], "/")
		var held map[string]any
		if len(parts) == 4 && parts[0] == "#" && parts[1] == "components" {
			held = map[string]map[string]any{"schemas": doc.Components.Schemas, "responses": doc.Components.Responses}[parts[2]]
		}
		if _, found := held[parts[len(parts)-1]]; !found {
			t.Errorf("the description refers to %s, which it does not have", ref[1])
		}
	}
	if _, has := doc.Components.SecuritySchemes["key"]; !has {
		t.Error("the description does not say how a caller proves who it is")
	}
	// The document ends each line once, and is the same on every run.
	again, _ := OpenAPI("..")
	if again != text || !strings.HasSuffix(text, "}\n") || strings.Contains(text, "\r") {
		t.Error("the description is not written the same twice")
	}
}

// Every code has a meaning of its own words, and no list is empty.
func TestCodesHaveMeanings(t *testing.T) {
	for _, src := range sources {
		codes, err := codesOf(filepath.Join("..", filepath.FromSlash(src.file)), src.prefix)
		if err != nil {
			t.Fatal(err)
		}
		if len(codes) < 4 {
			t.Errorf("%s: %d codes found", src.file, len(codes))
		}
		for _, c := range codes {
			if len(c.meaning) < 10 || strings.Contains(c.meaning, c.value+":") {
				t.Errorf("%s: the meaning of %s is %q", src.file, c.value, c.meaning)
			}
		}
	}
}
