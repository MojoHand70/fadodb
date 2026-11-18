package signon

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/internal/signon/signontest"
)

const back = "http://127.0.0.1:8080/auth/callback"

// signOn runs a sign-on with a provider as a browser would, and answers
// what Finish answers.
func signOn(t *testing.T, s *Set, name string) (who string, email string, verified bool, err error) {
	t.Helper()
	ctx := context.Background()
	to, state, err := s.Begin(ctx, name, back, "/next")
	if err != nil {
		t.Fatal(err)
	}
	browser := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := browser.Get(to)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	at, err := url.Parse(resp.Header.Get("Location"))
	if err != nil || at.Query().Get("state") != state {
		t.Fatalf("the provider sent the person back to %q", resp.Header.Get("Location"))
	}
	got, next, err := s.Finish(ctx, state, at.Query().Get("code"), back)
	if err != nil {
		return "", "", false, err
	}
	if next != "/next" || got.Provider != name || got.Issuer == "" {
		t.Errorf("Finish answers %+v and %q", got, next)
	}
	return got.Subject, got.Email, got.EmailVerified, nil
}

func set(t *testing.T, p *signontest.Provider, company bool) *Set {
	t.Helper()
	s, err := New(Options{
		Providers: []Provider{{Name: "acme", Issuer: p.Issuer, ClientID: p.ClientID, CompanyEmails: company}},
		Secret:    func(context.Context, string) (string, error) { return p.Secret, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func yes() *bool { v := true; return &v }

// A whole sign-on: the person goes to the provider and back, the code is
// exchanged with its proof, and the note is checked.
func TestASignOn(t *testing.T) {
	p := signontest.New()
	defer p.Close()
	p.Person = signontest.Person{Subject: "00u-ewa", Email: "Ewa@Acme.example", Verified: yes()}
	s := set(t, p, false)
	sub, email, verified, err := signOn(t, s, "acme")
	if err != nil || sub != "00u-ewa" || email != "Ewa@Acme.example" || !verified {
		t.Fatalf("the sign-on answers %q %q %v %v", sub, email, verified, err)
	}
	if got := s.Providers(); len(got) != 1 || got[0].Name != "acme" || got[0].Label != "Sign in with acme" {
		t.Errorf("the providers: %+v", got)
	}

	// The provider changes its key: the server reads the keys again.
	p.Rotate("k2")
	s.by["acme"].askedFor = time.Time{}
	if sub, _, _, err := signOn(t, s, "acme"); err != nil || sub != "00u-ewa" {
		t.Errorf("after the key changed: %v", err)
	}

	// No word on the e-mail: not vouched for, unless the e-mails are the
	// company's own, and then preferred_username stands for a missing
	// e-mail, as Microsoft gives it.
	p.Person = signontest.Person{Subject: "oid-ewa", Email: "ewa@acme.example"}
	if _, _, verified, err := signOn(t, s, "acme"); err != nil || verified {
		t.Errorf("an e-mail with no word on it: %v %v", verified, err)
	}
	company := set(t, p, true)
	p.Person = signontest.Person{Subject: "oid-ewa", Preferred: "ewa@acme.example"}
	if _, email, verified, err := signOn(t, company, "acme"); err != nil || !verified || email != "ewa@acme.example" {
		t.Errorf("the company's own e-mails: %q %v %v", email, verified, err)
	}
}

// Every note that does not hold is refused, and says why.
func TestANoteThatDoesNotHold(t *testing.T) {
	p := signontest.New()
	defer p.Close()
	p.Person = signontest.Person{Subject: "00u-ewa", Email: "ewa@acme.example", Verified: yes()}
	for _, c := range []struct {
		name  string
		spoil func(map[string]any)
		want  string
	}{
		{"another issuer", func(c map[string]any) { c["iss"] = "https://login.evil.example" }, "issued by"},
		{"another client", func(c map[string]any) { c["aud"] = "another-client" }, "not for this server"},
		{"two audiences and no azp", func(c map[string]any) { c["aud"] = []string{"fadodb-test", "another"} }, "not for this server"},
		{"ended", func(c map[string]any) { c["exp"] = time.Now().Add(-2 * time.Minute).Unix() }, "has ended"},
		{"from the future", func(c map[string]any) { c["iat"] = time.Now().Add(time.Hour).Unix() }, "future"},
		{"another nonce", func(c map[string]any) { c["nonce"] = "not this one" }, "nonce"},
		{"nobody", func(c map[string]any) { delete(c, "sub") }, "names nobody"},
	} {
		p.Spoil = c.spoil
		_, _, _, err := signOn(t, set(t, p, false), "acme")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	p.Spoil = nil
	p.Unsigned = true
	if _, _, _, err := signOn(t, set(t, p, false), "acme"); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Errorf("a spoiled signature: %v", err)
	}
	p.Unsigned = false

	// A wrong client secret, a state used twice or come back elsewhere.
	wrong, _ := New(Options{
		Providers: []Provider{{Name: "acme", Issuer: p.Issuer, ClientID: p.ClientID}},
		Secret:    func(context.Context, string) (string, error) { return "not the secret", nil },
	})
	if _, _, _, err := signOn(t, wrong, "acme"); err == nil || !strings.Contains(err.Error(), "invalid_client") {
		t.Errorf("a wrong client secret: %v", err)
	}
	s := set(t, p, false)
	ctx := context.Background()
	_, state, _ := s.Begin(ctx, "acme", back, "/")
	if _, _, err := s.Finish(ctx, state, "a code", "http://127.0.0.1:9999/auth/callback"); err == nil {
		t.Error("a sign-on came back to another address")
	}
	if _, _, err := s.Finish(ctx, state, "a code", back); err == nil {
		t.Error("a state was taken twice")
	}
	if _, _, err := s.Begin(ctx, "nobody", back, "/"); err == nil {
		t.Error("a sign-on the server does not have")
	}
}

// An issuer is an address over HTTPS, or over HTTP on the machine.
func TestTheIssuer(t *testing.T) {
	for issuer, ok := range map[string]bool{
		"https://login.microsoftonline.com/7d1e/v2.0": true,
		"https://accounts.google.com":                 true,
		"http://127.0.0.1:5556":                       true,
		"http://localhost:5556/dex":                   true,
		"http://login.acme.example":                   false,
		"login.acme.example":                          false,
		"https://user@login.acme.example":             false,
		"https://login.acme.example?tenant=1":         false,
	} {
		if err := CheckIssuer(issuer); (err == nil) != ok {
			t.Errorf("%s: %v", issuer, err)
		}
	}
}
