package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/MojoHand70/fadodb/internal/command"
	"github.com/MojoHand70/fadodb/internal/store"
)

// shelf stands in for a database with the bundle of one class: it
// answers get_bundle as the engine does, and says what it was sent.
func shelf(sent *[]map[string]any) engine {
	return func(_ context.Context, _ store.Principal, name string, object []byte) ([]byte, error) {
		var asked map[string]any
		_ = json.Unmarshal(object, &asked)
		*sent = append(*sent, asked)
		switch {
		case name != "get_bundle":
			return nil, &store.Error{Code: command.CodeUnknownCommand, Message: name}
		case asked["class"] != "Invoice":
			e := &store.Error{Code: store.CodeUnknownClass, Message: "unknown class"}
			text, _ := command.Marshal(struct {
				Error *store.Error `json:"error"`
			}{e})
			return text, e
		case asked["tag"] == "5d0c9a7e31b2f468":
			return []byte(`{"unchanged":true,"tag":"5d0c9a7e31b2f468"}`), nil
		}
		return []byte(`{"class":"Invoice","version":1,"tag":"5d0c9a7e31b2f468"}`), nil
	}
}

// GET /v1/bundle is get_bundle with its parameters, the tag a browser
// holds sent as the command's tag; it answers with the tag as the ETag,
// and a browser that holds it is answered 304 and no body (ADR-0070,
// item 2).
func TestTheBundleIsKeptByTheBrowser(t *testing.T) {
	var sent []map[string]any
	d := door(t, shelf(&sent), Options{})
	w := get(d, "/v1/bundle?class=Invoice&language=pl&version=1")
	if w.Code != http.StatusOK || w.Body.String() != `{"class":"Invoice","version":1,"tag":"5d0c9a7e31b2f468"}`+"\n" {
		t.Fatalf("the bundle: %d %s", w.Code, w.Body)
	}
	for header, want := range map[string]string{
		"ETag":          `"5d0c9a7e31b2f468"`,
		"Cache-Control": "private, no-cache",
		"Content-Type":  "application/json",
	} {
		if got := w.Header().Get(header); got != want {
			t.Errorf("%s is %q, want %q", header, got, want)
		}
	}
	if len(sent) != 1 || sent[0]["class"] != "Invoice" || sent[0]["language"] != "pl" || sent[0]["version"] != 1.0 || sent[0]["tag"] != nil {
		t.Errorf("the command sent: %v", sent)
	}
	for _, held := range []string{`"5d0c9a7e31b2f468"`, `W/"5d0c9a7e31b2f468"`, `"5d0c9a7e31b2f468", "0000"`} {
		w = get(d, "/v1/bundle?class=Invoice&language=pl", "If-None-Match", held)
		if w.Code != http.StatusNotModified || w.Body.Len() != 0 || w.Header().Get("ETag") != `"5d0c9a7e31b2f468"` {
			t.Errorf("a browser that holds %s: %d %v %s", held, w.Code, w.Header(), w.Body)
		}
	}
	if w = get(d, "/v1/bundle?class=Invoice", "If-None-Match", `"0000"`); w.Code != http.StatusOK {
		t.Errorf("a browser that holds an older bundle: %d", w.Code)
	}
}

// What the address refuses: another method, a parameter it does not
// take, no class, a version that is no number, and the engine's own
// refusals in its words.
func TestTheBundleRefuses(t *testing.T) {
	var sent []map[string]any
	d := door(t, shelf(&sent), Options{})
	w := post(d, "/v1/bundle?class=Invoice", "")
	if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != http.MethodGet || refusal(t, w).Code != command.CodeBadJSON {
		t.Errorf("POST /v1/bundle: %d %v %s", w.Code, w.Header(), w.Body)
	}
	for path, want := range map[string]string{
		"/v1/bundle?language=pl":                "/class",
		"/v1/bundle?class=Invoice&lang=pl":      "/lang",
		"/v1/bundle?class=Invoice&version=two":  "/version",
		"/v1/bundle?class=Invoice&version=0":    "/version",
		"/v1/bundle?class=Invoice&class=Ledger": "/class",
	} {
		w := get(d, path)
		if e := refusal(t, w); w.Code != http.StatusBadRequest || e.Code != command.CodeBadJSON || e.Path != want {
			t.Errorf("%s: %d %+v", path, w.Code, e)
		}
	}
	w = get(d, "/v1/bundle?class=Ledger")
	if e := refusal(t, w); w.Code != Status(store.CodeUnknownClass) || e.Code != store.CodeUnknownClass || w.Header().Get("ETag") != "" {
		t.Errorf("a class the database does not have: %d %+v", w.Code, e)
	}
	if len(sent) != 1 {
		t.Errorf("the engine was sent %d commands, want 1: %v", len(sent), sent)
	}
}
