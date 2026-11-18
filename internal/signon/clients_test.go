package signon

import (
	"context"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/internal/store"
)

// A client's document, published over HTTPS: the server reads what it
// says, and refuses a document that does not hold (draft-ietf-oauth-
// client-id-metadata-document).
func TestAClientsDocument(t *testing.T) {
	docs := map[string]string{}
	status := map[string]int{}
	site := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if code := status[r.URL.Path]; code != 0 {
			w.Header().Set("Location", "/elsewhere")
			w.WriteHeader(code)
			return
		}
		if r.Header.Get("Accept") != "application/json" {
			http.Error(w, "the reader asks for no JSON", http.StatusNotAcceptable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, strings.ReplaceAll(docs[r.URL.Path], "SITE", "https://"+r.Host))
	}))
	defer site.Close()
	roots := x509.NewCertPool()
	roots.AddCert(site.Certificate())
	d := NewDocuments(DocumentOptions{Loopback: true, Roots: roots})
	ctx := context.Background()

	docs["/claude"] = `{"client_id": "SITE/claude", "client_name": "Claude", "token_endpoint_auth_method": "none",
		"redirect_uris": ["https://claude.ai/api/mcp/auth_callback", "http://127.0.0.1/callback", "com.example.app:/cb", "http://claude.ai/cb"]}`
	c, err := d.Client(ctx, site.URL+"/claude")
	if err != nil || c.Name != "Claude" || c.Address != store.AddressAgents || strings.Join(c.RedirectURIs, " ") != "https://claude.ai/api/mcp/auth_callback http://127.0.0.1/callback" {
		t.Fatalf("Claude's document: %v %+v", err, c)
	}
	docs["/nameless"] = `{"client_id": "SITE/nameless", "redirect_uris": ["https://app.example/cb"]}`
	if c, err := d.Client(ctx, site.URL+"/nameless"); err != nil || c.Name != "127.0.0.1" {
		t.Errorf("a document with no name is named by its host: %v %+v", err, c)
	}

	for name, c := range map[string]struct {
		doc  string
		want string
	}{
		"another client_id":                {`{"client_id": "SITE/other", "redirect_uris": ["https://a.example/cb"]}`, "names the client"},
		"a client secret":                  {`{"client_id": "SITE/x", "client_secret": "s", "redirect_uris": ["https://a.example/cb"]}`, "secret"},
		"a key at the token address":       {`{"client_id": "SITE/x", "token_endpoint_auth_method": "private_key_jwt", "redirect_uris": ["https://a.example/cb"]}`, "PKCE alone"},
		"no none among the methods":        {`{"client_id": "SITE/x", "token_endpoint_auth_methods_supported": ["private_key_jwt"], "redirect_uris": ["https://a.example/cb"]}`, "PKCE alone"},
		"no code among the grants":         {`{"client_id": "SITE/x", "grant_types": ["client_credentials"], "redirect_uris": ["https://a.example/cb"]}`, "asks for no code"},
		"no code among the answers":        {`{"client_id": "SITE/x", "response_types": ["token"], "redirect_uris": ["https://a.example/cb"]}`, "takes no code"},
		"no address a code can be sent to": {`{"client_id": "SITE/x", "redirect_uris": ["http://a.example/cb", "app:/cb"]}`, "names no address"},
		"no JSON":                          {`<html>`, "not JSON"},
		"more than 5 KB":                   {`{"client_id": "SITE/x", "client_name": "` + strings.Repeat("x", 6000) + `"}`, "5 KB"},
	} {
		docs["/x"] = c.doc
		if _, err := d.Client(ctx, site.URL+"/x"); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	status["/moved"] = http.StatusFound
	if _, err := d.Client(ctx, site.URL+"/moved"); err == nil || !strings.Contains(err.Error(), "302") {
		t.Errorf("a redirect is followed: %v", err)
	}

	// Without leave, nothing is read from the machine itself.
	strict := NewDocuments(DocumentOptions{Roots: roots})
	if _, err := strict.Client(ctx, site.URL+"/claude"); err == nil || !strings.Contains(err.Error(), "of the network itself") {
		t.Errorf("a document of the machine itself: %v", err)
	}
}

// The id of a client known by its document is the document's address.
func TestTheIDOfAClientsDocument(t *testing.T) {
	for id, ok := range map[string]bool{
		"https://claude.ai/oauth/mcp-oauth-client-metadata": true,
		"https://app.example:8443/client.json":              true,
		"http://claude.ai/oauth/client":                     false,
		"https://claude.ai":                                 false,
		"https://claude.ai/":                                false,
		"https://claude.ai/client?v=1":                      false,
		"https://claude.ai/client#x":                        false,
		"https://user@claude.ai/client":                     false,
		"https://claude.ai/a/../client":                     false,
		"https://claude.ai/a/./client":                      false,
		"https://claude.ai/a/%2e%2e/client":                 false,
		"fadoface":                                          false,
	} {
		if err := CheckClientID(id); (err == nil) != ok {
			t.Errorf("%s: %v", id, err)
		}
	}
}

// No document is read from an address of the network the server stands
// in: the machine, the private networks, the link (where clouds keep
// what a machine is), the shared and the documentation ones.
func TestTheAddressesNoDocumentIsReadFrom(t *testing.T) {
	for text, refused := range map[string]bool{
		"127.0.0.1":       true,
		"10.1.2.3":        true,
		"172.16.0.1":      true,
		"192.168.1.1":     true,
		"169.254.169.254": true,
		"100.64.0.1":      true,
		"0.0.0.0":         true,
		"224.0.0.1":       true,
		"198.18.0.1":      true,
		"::1":             true,
		"fd00::1":         true,
		"fe80::1":         true,
		"64:ff9b::a00:1":  true,
		"2001:db8::1":     true,
		"8.8.8.8":         false,
		"160.79.104.10":   false,
		"2606:4700::1111": false,
	} {
		if special(netip.MustParseAddr(text)) != refused {
			t.Errorf("%s: refused is %v", text, !refused)
		}
	}
	if !special(netip.MustParseAddr("::ffff:10.0.0.1").Unmap()) {
		t.Error("a private address written as IPv6 is read from")
	}
}
