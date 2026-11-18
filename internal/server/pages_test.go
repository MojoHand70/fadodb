package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// Where a page sends a person when they are done: a path of the server,
// never another site.
func TestPagesSendOnlyToThemselves(t *testing.T) {
	for given, want := range map[string]string{
		"":                       PageAccount,
		"/v1/whoami":             "/v1/whoami",
		"/kit/invoices?state=x":  "/kit/invoices?state=x",
		"//evil.example/":        PageAccount,
		"https://evil.example/":  PageAccount,
		"/\\evil.example":        PageAccount,
		"/ok\r\nSet-Cookie: x=y": PageAccount,
		"evil":                   PageAccount,
	} {
		r := httptest.NewRequest(http.MethodGet, "/auth/signin?next="+url.QueryEscape(given), nil)
		if got := next(r); got != want {
			t.Errorf("next=%q sends to %q, want %q", given, got, want)
		}
	}
}

// Without the address of the server the pages answer this machine alone,
// and take a form only from a page of the same origin.
func TestPagesAnswerTheirOwnOrigin(t *testing.T) {
	p, err := NewPages(nil, PagesOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for host, answers := range map[string]bool{
		"127.0.0.1:8080": true, "localhost:8080": true, "[::1]:8080": true, "localhost": true,
		"fado.acme.example": false, "10.0.0.7:8080": false, "evil.example:8080": false,
	} {
		r := httptest.NewRequest(http.MethodGet, "/auth/signin", nil)
		r.Host = host
		if _, ok := p.originOf(r); ok != answers {
			t.Errorf("a request to %s is answered: %v", host, ok)
		}
	}
	form := func(host, origin, site string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/auth/signin", strings.NewReader("user=ann"))
		r.Host = host
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if site != "" {
			r.Header.Set("Sec-Fetch-Site", site)
		}
		return r
	}
	for _, c := range []struct {
		host, origin, site string
		same               bool
	}{
		{"127.0.0.1:8080", "http://127.0.0.1:8080", "", true},
		{"127.0.0.1:8080", "http://localhost:8080", "", false},
		{"127.0.0.1:8080", "https://evil.example", "same-origin", false},
		{"127.0.0.1:8080", "", "same-origin", true},
		{"127.0.0.1:8080", "", "cross-site", false},
		{"127.0.0.1:8080", "", "", false},
	} {
		if got := p.sameOrigin(form(c.host, c.origin, c.site)); got != c.same {
			t.Errorf("a form to %s from %q (%q) is the server's own: %v", c.host, c.origin, c.site, got)
		}
	}

	// With the address, that origin alone, whatever the host says.
	p, err = NewPages(nil, PagesOptions{URL: "https://fado.acme.example"})
	if err != nil || !p.secure {
		t.Fatalf("the pages of an address: %v", err)
	}
	if got := p.sameOrigin(form("127.0.0.1:8080", "http://127.0.0.1:8080", "")); got {
		t.Error("a form from the machine is taken by a server with an address")
	}
	if got := p.sameOrigin(form("fado.acme.example", "https://fado.acme.example", "")); !got {
		t.Error("a form from the server's address is refused")
	}
	for _, bad := range []string{"fado.acme.example", "ftp://fado.acme.example", "https://fado.acme.example/app", "https://ann@fado.acme.example", "https://fado.acme.example?x=1", "https://"} {
		if _, err := NewPages(nil, PagesOptions{URL: bad}); err == nil {
			t.Errorf("the address %q is taken", bad)
		}
	}
}
