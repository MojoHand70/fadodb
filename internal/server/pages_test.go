package server

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
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

// A page of the server names an empty icon, written in the page, and its
// header lets such a picture be: a browser then asks the server for no
// /favicon.ico. Firefox asks for one on a page that names none, the
// header refuses the request, and the refusal is written in the
// browser's console on every page where a person signs in.
func TestPagesNameAnEmptyIcon(t *testing.T) {
	p, err := NewPages(nil, PagesOptions{})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/auth/nothing", nil)
	r.Host = "127.0.0.1:8080"
	p.ServeHTTP(w, r)
	if body := w.Body.String(); !strings.Contains(body, `<link rel="icon" href="data:,">`) {
		t.Errorf("the page names no empty icon:\n%s", body)
	}
	policy := w.Header().Get("Content-Security-Policy")
	if !strings.Contains(policy, "; img-src data:;") {
		t.Errorf("the header lets no picture written in the page be: %q", policy)
	}
	// Nothing is loaded and nothing runs, as before.
	for _, never := range []string{"script-src", "connect-src", "http:", "https:", "'self' data:"} {
		if strings.Contains(policy, never) {
			t.Errorf("the header of a page of the server holds %q: %q", never, policy)
		}
	}
	if !strings.HasPrefix(policy, "default-src 'none';") || !strings.Contains(policy, "form-action 'self';") {
		t.Errorf("the header of a page of the server is %q", policy)
	}
}

// Every page of the server carries FadoDB's mark and name above what it
// holds, once, in a card. The picture is written in the page: the
// header lets a page load nothing. The constant the pages write is the
// picture mark.png, byte for byte.
func TestPagesCarryTheMark(t *testing.T) {
	png, err := os.ReadFile("mark.png")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(png, []byte("\x89PNG\r\n")) {
		t.Fatal("mark.png is no PNG picture")
	}
	if want := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png); markData != want {
		t.Fatalf("markData is not mark.png in base64: it has %d signs, the file gives %d", len(markData), len(want))
	}
	head := `<p class="brand"><img src="` + markData + `" width="40" height="40" alt=""><span>FadoDB</span></p>`
	for _, name := range []string{"signIn", "code", "codes", "account", "setPassword", "signedOn", "passwordSet", "onward", "requestGone", "notAllowed", "linkGone", "no such page"} {
		body := drawn(name, &page{Title: "A page"})
		if strings.Count(body, head) != 1 {
			t.Errorf("%s: the page carries the mark and the name %d times, want once", name, strings.Count(body, head))
			continue
		}
		if strings.Count(body, "<main>") != 1 || strings.Count(body, "</main>") != 1 {
			t.Errorf("%s: the page is not one card", name)
		}
		if strings.Index(body, head) > strings.Index(body, "<h1>") {
			t.Errorf("%s: the mark stands below the title of the page", name)
		}
		// A picture, a style or a script that the browser would have to fetch is refused by the header.
		for _, never := range []string{`src="http`, `src="/`, `href="http`, "<script", "url("} {
			if strings.Contains(body, never) {
				t.Errorf("%s: the page holds %q, which it would have to load", name, never)
			}
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
		// A browser sends the Origin of a form as null for a page that
		// sends no referrer; Sec-Fetch-Site then says where it is from.
		// Chromium did so with every form of the pages until they sent
		// a referrer to the server itself.
		{"127.0.0.1:8080", "null", "same-origin", true},
		{"127.0.0.1:8080", "null", "cross-site", false},
		{"127.0.0.1:8080", "null", "", false},
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

// The pages send a referrer to the server itself alone: never to another
// site, where a link's secret in the address would be read, and to the
// server, so that a browser says the true Origin of a form.
func TestPagesSendAReferrerToThemselvesAlone(t *testing.T) {
	p, err := NewPages(nil, PagesOptions{})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/auth/nothing", nil)
	r.Host = "127.0.0.1:8080"
	p.ServeHTTP(w, r)
	if got := w.Header().Get("Referrer-Policy"); got != "same-origin" {
		t.Errorf("the pages' referrer policy is %q", got)
	}
}
