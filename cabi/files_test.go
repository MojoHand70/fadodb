package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
)

// A claim with the scan of its receipt: Ann of sales makes it and puts
// the scan, Bob of accounting reads it, Carol of marketing may not.
const claim = `{
  "name": "Claim",
  "variables": {
    "number": {"type": "text", "required": true},
    "scan":   {"type": "object", "schema": "File"}
  },
  "states": {"initial": "open", "list": ["open", "paid"], "final": ["paid"]},
  "transitions": [{"name": "pay", "from": "open", "to": "paid"}],
  "access": {
    "read":   "CREATEDBY() = USER() OR INGROUP(\"accounting\")",
    "create": "INGROUP(\"sales\")",
    "update": "CREATEDBY() = USER()",
    "delete": "FALSE",
    "transition": {"pay": "INGROUP(\"accounting\")"}
  }
}`

const (
	carol = `{"id": "carol", "kind": "person", "groups": ["marketing"]}`
	cleo  = `{"id": "cleo", "kind": "person", "system_groups": ["content-administrators"]}`
)

func TestAFileThroughTheLibrary(t *testing.T) {
	n := opened(t, "")
	done(t, execCommand(n, designer, "define_class", `{"definition": `+claim+`}`))
	done(t, execCommand(n, ann, "create", `{"class": "Claim", "id": "C-7", "values": {"number": "C-7"}}`))

	scan := []byte("%PDF-1.7 the taxi from the airport, 84.00 PLN")
	sum := sha256.Sum256(scan)
	hash := hex.EncodeToString(sum[:])

	put := done(t, putFile(n, ann, `{"name": "taxi.pdf", "media_type": "application/pdf"}`, scan))
	file := put["file"].(map[string]any)
	if file["hash"] != hash || file["name"] != "taxi.pdf" || file["media_type"] != "application/pdf" || file["size"] != float64(len(scan)) {
		t.Fatalf("put answered %v", put)
	}
	// The facts are committed on the document as any value is.
	done(t, execCommand(n, ann, "set", `{"class": "Claim", "id": "C-7", "values": {"scan": `+jsonOf(file)+`}}`))

	answer, data := getFile(n, bob, `{"class": "Claim", "id": "C-7", "hash": "`+hash+`"}`)
	if got := done(t, answer)["file"].(map[string]any); got["hash"] != hash || got["name"] != "taxi.pdf" {
		t.Fatalf("get answered %s", answer)
	}
	if string(data) != string(scan) {
		t.Fatalf("Bob read %q", data)
	}

	// Carol may not read the claim, and so not its scan.
	answer, data = getFile(n, carol, `{"class": "Claim", "id": "C-7", "hash": "`+hash+`"}`)
	if e := refused(t, answer); data != nil || (e["code"] != "forbidden" && e["code"] != "unknown_document") {
		t.Fatalf("Carol was answered %s with %d bytes", answer, len(data))
	}
	// The document at version 1 held no scan.
	answer, data = getFile(n, bob, `{"class": "Claim", "id": "C-7", "hash": "`+hash+`", "version": 1}`)
	if refused(t, answer); data != nil {
		t.Fatalf("version 1 gave %d bytes", len(data))
	}
	// The bytes by their hash alone are a content administrator's, with
	// a reason.
	answer, data = getBlob(n, bob, `{"hash": "`+hash+`"}`)
	if refused(t, answer); data != nil {
		t.Fatalf("Bob read the blob: %s", answer)
	}
	answer, data = getBlob(n, cleo, `{"hash": "`+hash+`", "reason": "the auditor asked for every scan of November"}`)
	if done(t, answer); string(data) != string(scan) {
		t.Fatalf("Cleo read %q: %s", data, answer)
	}

	// An empty file is a file.
	answer = putFile(n, ann, `{"name": "empty.txt"}`, nil)
	if got := done(t, answer)["file"].(map[string]any); got["size"] != float64(0) {
		t.Fatalf("an empty put answered %s", answer)
	}
}

func TestWhatTheFunctionsOfFilesMaySay(t *testing.T) {
	n := opened(t, "")
	done(t, execCommand(n, designer, "define_class", `{"definition": `+claim+`}`))
	for _, c := range []struct {
		object, code, path string
		get                bool
	}{
		{`{"name": "a.pdf", "type": "application/pdf"}`, "bad_json", "/type", false},
		{`{"name": 7}`, "bad_json", "/name", false},
		{`{"class": "Claim", "id": "C-7"}`, "bad_json", "/hash", true},
		{`{"class": "Claim", "id": "C-7", "hash": "ab", "version": 0}`, "bad_json", "/version", true},
		{`{"class": "Claim", "id": "C-7", "hash": "ab", "version": "2"}`, "bad_json", "/version", true},
		{`{"class": "Claim", "id": "C-7", "hash": "ab", "colour": "red"}`, "bad_json", "/colour", true},
	} {
		var answer, data []byte
		if c.get {
			answer, data = getFile(n, bob, c.object)
		} else {
			answer = putFile(n, ann, c.object, []byte("x"))
		}
		e := refused(t, answer)
		if e["code"] != c.code || e["path"] != c.path || data != nil {
			t.Errorf("%s: refused with %v, want %s at %s", c.object, e, c.code, c.path)
		}
	}
	if e := refused(t, putFile(999, ann, `{}`, []byte("x"))); e["code"] != "closed" {
		t.Errorf("a put to no database: %v", e)
	}
	if answer, data := getBlob(n, cleo, `{}`); data != nil || refused(t, answer)["path"] != "/hash" {
		t.Errorf("a blob of no hash: %s", answer)
	}
}

func jsonOf(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
