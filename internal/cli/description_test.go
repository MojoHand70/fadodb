package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The description of the commands, api/openapi.json, lists two addresses
// that the server answers itself: each is answered as it says, the
// health to anybody and the counts to an administrator.
func TestTheDescriptionAndTheServer(t *testing.T) {
	text, err := os.ReadFile(filepath.Join("..", "..", "api", "openapi.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]struct {
			Tags      []string
			Security  *[]any
			Responses map[string]struct{ Content map[string]any }
		}
	}
	if err := json.Unmarshal(text, &doc); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "acme.fado")
	tool("init", path)
	tool("user", "add", "olga", "--kind", "person", "--system", "administrators", "--db", path)
	_, key, _ := tool("key", "new", "olga", "--db", path)
	key = strings.TrimSpace(key)
	addr, _, _ := serving(t, path, "--listen", "127.0.0.1:0")
	client := &http.Client{Timeout: 10 * time.Second}
	own := 0
	for address, methods := range doc.Paths {
		for method, op := range methods {
			if len(op.Tags) != 1 || op.Tags[0] != "The server" {
				continue
			}
			own++
			// An address that asks for no key says so in the description.
			with := key
			if op.Security != nil && len(*op.Security) == 0 {
				with = ""
			}
			req, err := http.NewRequest(strings.ToUpper(method), "http://"+addr+address, nil)
			if err != nil {
				t.Fatal(err)
			}
			if with != "" {
				req.Header.Set("Authorization", "Bearer "+with)
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			kind, _, _ := strings.Cut(resp.Header.Get("Content-Type"), ";")
			if _, described := op.Responses["200"].Content[kind]; resp.StatusCode != 200 || !described {
				t.Errorf("%s %s is answered with %d and %q, which the description does not say", method, address, resp.StatusCode, kind)
			}
		}
	}
	if own != 2 || doc.Paths["/v1/health"]["get"].Security == nil || doc.Paths[metricsPath]["get"].Security != nil {
		t.Errorf("the description lists %d addresses of the server's own, want the health, for anybody, and the counts, with a key", own)
	}
}
