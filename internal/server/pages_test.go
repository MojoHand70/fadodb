package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/MojoHand70/fadodb/internal/server/web"
	"github.com/MojoHand70/fadodb/internal/store"
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

// A page of the server loads the server's own and nothing else
// (ADR-0088, item 7): its one script, Rhea's typefaces and FadoDB's
// mark, which is also its icon, so that a browser asks for no
// /favicon.ico. Its header lets nothing from elsewhere be, and nothing
// written in the page run; each file is served with its kind, for a
// year, at an address that holds its hash.
func TestPagesLoadOnlyTheServersOwn(t *testing.T) {
	p, err := NewPages(nil, PagesOptions{})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/auth/nothing", nil)
	r.Host = "127.0.0.1:8080"
	p.ServeHTTP(w, r)
	body := w.Body.String()
	policy := w.Header().Get("Content-Security-Policy")
	if policy != "default-src 'none'; script-src 'self'; style-src 'self'; font-src 'self'; img-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'" {
		t.Errorf("the header of a page of the server is %q", policy)
	}
	for _, never := range []string{"http:", "https:", "'unsafe-", "connect-src", "data:"} {
		if strings.Contains(policy, never) {
			t.Errorf("the header of a page of the server holds %q: %q", never, policy)
		}
	}
	if !strings.Contains(body, `<link rel="icon" type="image/png" href="`+web.At("mark.png")+`">`) || !strings.Contains(body, `<script src="`+web.At("pages.js")+`" defer></script>`) {
		t.Errorf("the page names no icon or no script of the server's:\n%s", body)
	}
	if strings.Count(body, "<script") != 1 || strings.Contains(body, "<style") || strings.Contains(body, " style=") {
		t.Errorf("the page holds a script or a style of its own:\n%s", body)
	}
	css := web.Served(web.At("pages.css"))
	for name, kind := range map[string]string{"pages.css": "text/css; charset=utf-8", "pages.js": "text/javascript; charset=utf-8", "mark.png": "image/png", "fonts/schibsted-grotesk-latin.woff2": "font/woff2", "fonts/martian-mono-latin-ext.woff2": "font/woff2"} {
		at := web.At(name)
		if !strings.Contains(body, at) && !strings.Contains(string(css.Body), "url("+at+")") {
			t.Errorf("neither the page nor its style loads %s", at)
		}
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, at, nil)
		r.Host = "fado.acme.example"
		p.ServeHTTP(w, r)
		if w.Code != http.StatusOK || w.Header().Get("Content-Type") != kind || w.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" || w.Body.Len() < 100 {
			t.Errorf("%s: %d %q %q %d bytes", at, w.Code, w.Header().Get("Content-Type"), w.Header().Get("Cache-Control"), w.Body.Len())
		}
	}
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodGet, "/auth/pages.js", nil)
	r.Host = "127.0.0.1:8080"
	p.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound || strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("the script at an address without its hash: %d", w.Code)
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

// The account page says a passkey is synced once: after its dates, or
// in its name when the name says it already, as a synced passkey whose
// password manager is not known is named (the owner's ruling,
// 2026-11-26).
func TestTheAccountPageSaysSyncedOnce(t *testing.T) {
	var b strings.Builder
	accountShown(&b, &page{Passkeys: []*store.Passkey{
		{ID: "k1", Name: "Google Password Manager", Synced: true, AddedAt: 1},
		{ID: "k2", Name: "Olga's iPhone, synced", Synced: true, AddedAt: 2},
		{ID: "k3", Name: "Olga's laptop", AddedAt: 3},
	}})
	got := b.String()
	for _, want := range []string{
		"<span>Google Password Manager</span><span class=\"when\">added 1970-01-01 00:00 UTC, synced</span>",
		"<span>Olga&#39;s iPhone, synced</span><span class=\"when\">added 1970-01-01 00:00 UTC</span>",
		"<span>Olga&#39;s laptop</span><span class=\"when\">added 1970-01-01 00:00 UTC</span>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the account page lacks %s in\n%s", want, got)
		}
	}
}
