// Package signontest is a company's provider of OpenID Connect for
// tests: it publishes where it signs people in and its key, signs in at
// once whoever the test says, checks the code, its proof and the client
// secret as a provider does, and hands back a signed note the test may
// spoil on purpose. Only tests import it.
package signontest

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"time"
)

// Person is who the provider says signs in.
type Person struct {
	Subject string
	Email   string
	// Verified is the provider's word on the e-mail: nil says nothing.
	Verified *bool
	// Preferred is the name Microsoft gives as preferred_username.
	Preferred string
}

// Provider is the provider and what the test may change of it.
type Provider struct {
	Server   *httptest.Server
	Issuer   string
	ClientID string
	Secret   string
	// Person is who signs in next.
	Person Person
	// Spoil, when set, changes the claims of the next note before it is
	// signed; Unsigned, when set, spoils its signature.
	Spoil    func(claims map[string]any)
	Unsigned bool
	// Kid is the id the note names for its key.
	Kid string

	mu    sync.Mutex
	key   *rsa.PrivateKey
	codes map[string]asked
}

type asked struct {
	redirect, nonce, challenge string
	person                     Person
}

// New starts a provider. Close it with the test.
func New() *Provider {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	p := &Provider{ClientID: "fadodb-test", Secret: "the client secret", Kid: "k1", key: key, codes: map[string]asked{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", p.discovery)
	mux.HandleFunc("/authorize", p.authorize)
	mux.HandleFunc("/token", p.token)
	mux.HandleFunc("/keys", p.keys)
	p.Server = httptest.NewServer(mux)
	p.Issuer = p.Server.URL
	return p
}

// Close stops the provider.
func (p *Provider) Close() { p.Server.Close() }

func answer(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (p *Provider) discovery(w http.ResponseWriter, _ *http.Request) {
	answer(w, 200, map[string]any{
		"issuer":                                p.Issuer,
		"authorization_endpoint":                p.Issuer + "/authorize",
		"token_endpoint":                        p.Issuer + "/token",
		"jwks_uri":                              p.Issuer + "/keys",
		"token_endpoint_auth_methods_supported": []string{"client_secret_post", "client_secret_basic"},
		"code_challenge_methods_supported":      []string{"S256"},
	})
}

// authorize signs in the test's person at once and sends them back with
// a code.
func (p *Provider) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("client_id") != p.ClientID || q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || q.Get("state") == "" || q.Get("nonce") == "" {
		http.Error(w, "the request is not one this provider takes", http.StatusBadRequest)
		return
	}
	code := make([]byte, 16)
	_, _ = rand.Read(code)
	c := base64.RawURLEncoding.EncodeToString(code)
	p.mu.Lock()
	p.codes[c] = asked{redirect: q.Get("redirect_uri"), nonce: q.Get("nonce"), challenge: q.Get("code_challenge"), person: p.Person}
	p.mu.Unlock()
	back, _ := url.Parse(q.Get("redirect_uri"))
	v := back.Query()
	v.Set("code", c)
	v.Set("state", q.Get("state"))
	back.RawQuery = v.Encode()
	http.Redirect(w, r, back.String(), http.StatusFound)
}

// token exchanges a code for a note, as a provider does: the client and
// its secret, the code once, the address it was sent to, and the proof.
func (p *Provider) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		answer(w, 400, map[string]string{"error": "invalid_request"})
		return
	}
	id, secret, basic := r.BasicAuth()
	if basic {
		id, _ = url.QueryUnescape(id)
		secret, _ = url.QueryUnescape(secret)
	} else {
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	if id != p.ClientID || secret != p.Secret {
		answer(w, 401, map[string]string{"error": "invalid_client", "error_description": "the client or its secret is wrong"})
		return
	}
	p.mu.Lock()
	a, ok := p.codes[r.PostForm.Get("code")]
	delete(p.codes, r.PostForm.Get("code"))
	p.mu.Unlock()
	proof := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	if !ok || a.redirect != r.PostForm.Get("redirect_uri") || base64.RawURLEncoding.EncodeToString(proof[:]) != a.challenge {
		answer(w, 400, map[string]string{"error": "invalid_grant"})
		return
	}
	now := time.Now()
	claims := map[string]any{
		"iss": p.Issuer, "aud": p.ClientID, "sub": a.person.Subject, "nonce": a.nonce,
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	}
	if a.person.Email != "" {
		claims["email"] = a.person.Email
	}
	if a.person.Verified != nil {
		claims["email_verified"] = *a.person.Verified
	}
	if a.person.Preferred != "" {
		claims["preferred_username"] = a.person.Preferred
	}
	if p.Spoil != nil {
		p.Spoil(claims)
	}
	answer(w, 200, map[string]string{"id_token": p.Sign(claims), "token_type": "Bearer"})
}

// Sign signs claims as the provider's note.
func (p *Provider) Sign(claims map[string]any) string {
	head, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": p.Kid, "typ": "JWT"})
	body, _ := json.Marshal(claims)
	signed := base64.RawURLEncoding.EncodeToString(head) + "." + base64.RawURLEncoding.EncodeToString(body)
	sum := sha256.Sum256([]byte(signed))
	sig, err := rsa.SignPKCS1v15(rand.Reader, p.key, crypto.SHA256, sum[:])
	if err != nil {
		panic(err)
	}
	if p.Unsigned {
		sig[0] ^= 0xff
	}
	return signed + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// Rotate gives the provider a new key under a new id, as providers do.
func (p *Provider) Rotate(kid string) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	p.mu.Lock()
	p.key, p.Kid = key, kid
	p.mu.Unlock()
}

func (p *Provider) keys(w http.ResponseWriter, _ *http.Request) {
	p.mu.Lock()
	pub := p.key.PublicKey
	kid := p.Kid
	p.mu.Unlock()
	answer(w, 200, map[string]any{"keys": []map[string]string{{
		"kty": "RSA", "kid": kid, "use": "sig", "alg": "RS256",
		"n": base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}}})
}
