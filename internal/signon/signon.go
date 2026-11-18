// Package signon asks a company's provider who a person is, by OpenID
// Connect (ADR-0071, item 4): Microsoft, Google, Okta, or any provider
// that publishes where it signs people in and the keys it signs with.
// The person is sent to the provider with a code challenge (PKCE), a
// state and a nonce; the provider sends them back with a code, which the
// server exchanges for a signed note; the note's signature, issuer,
// audience, time and nonce are checked here before the store is told who
// the person is. It is the standard library alone, and only the server
// links it: the engine and the door send no request of their own.
package signon

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/MojoHand70/fadodb/internal/store"
)

// Provider is a company's sign-on as the operator's settings give it.
// CompanyEmails says the provider's e-mails are the company's own, kept
// by its administrators, so that an e-mail is taken without the
// provider's word that it was checked: Microsoft's directory of one
// company says no such word.
type Provider struct {
	Name          string
	Label         string
	Issuer        string
	ClientID      string
	CompanyEmails bool
}

// Options are what a set of providers is made with: the providers, the
// secret of each, the client its requests go through, and the clock.
type Options struct {
	Providers []Provider
	// Secret answers the client secret of a provider, read again for
	// every sign-in so that it is changed without a restart.
	Secret func(ctx context.Context, provider string) (string, error)
	Client *http.Client
	Now    func() time.Time
}

// Set is the company sign-ons of a server.
type Set struct {
	order   []string
	by      map[string]*provider
	secret  func(ctx context.Context, provider string) (string, error)
	client  *http.Client
	now     func() time.Time
	mu      sync.Mutex
	flows   map[string]*flow
	lasting time.Duration
}

// provider is a provider and what was read of it: where it signs people
// in and the keys it signs with, each read again after a day.
type provider struct {
	Provider
	mu       sync.Mutex
	found    *discovered
	foundAt  time.Time
	keys     map[string]crypto.PublicKey
	keysAt   time.Time
	askedFor time.Time
}

type discovered struct {
	Issuer        string   `json:"issuer"`
	Authorization string   `json:"authorization_endpoint"`
	Token         string   `json:"token_endpoint"`
	JWKS          string   `json:"jwks_uri"`
	AuthMethods   []string `json:"token_endpoint_auth_methods_supported"`
}

// flow is a sign-on under way, from the person sent to the provider to
// the person sent back.
type flow struct {
	provider string
	nonce    string
	verifier string
	redirect string
	next     string
	ends     time.Time
}

// The limits of a sign-on: how long a person has at the provider, how
// many sign-ons are under way at once, how much of an answer is read and
// how long a request to a provider may take.
const (
	flowLasts  = 10 * time.Minute
	maxFlows   = 10_000
	maxAnswer  = 1 << 20
	askWithin  = 10 * time.Second
	readAgain  = 24 * time.Hour
	keysAtMost = time.Minute
	skew       = time.Minute
)

// New makes the sign-ons of the settings. An issuer is an address over
// HTTPS, or over HTTP on the machine itself, for a test.
func New(o Options) (*Set, error) {
	s := &Set{by: map[string]*provider{}, secret: o.Secret, client: o.Client, now: o.Now, flows: map[string]*flow{}, lasting: flowLasts}
	if s.client == nil {
		s.client = &http.Client{Timeout: askWithin}
	}
	if s.now == nil {
		s.now = time.Now
	}
	for _, p := range o.Providers {
		if err := CheckIssuer(p.Issuer); err != nil {
			return nil, fmt.Errorf("the sign-on %s: %v", p.Name, err)
		}
		if p.ClientID == "" {
			return nil, fmt.Errorf("the sign-on %s has no client_id", p.Name)
		}
		if p.Label == "" {
			p.Label = "Sign in with " + p.Name
		}
		key := strings.ToLower(p.Name)
		if _, twice := s.by[key]; twice {
			return nil, fmt.Errorf("the sign-on %s is given twice", p.Name)
		}
		s.by[key] = &provider{Provider: p}
		s.order = append(s.order, key)
	}
	return s, nil
}

// CheckIssuer refuses an issuer that is no address over HTTPS, or over
// HTTP on the machine itself.
func CheckIssuer(issuer string) error {
	u, err := url.Parse(issuer)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("the issuer is an address such as https://login.microsoftonline.com/<tenant>/v2.0")
	}
	if u.Scheme == "https" {
		return nil
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); u.Scheme == "http" && (host == "localhost" || ip != nil && ip.IsLoopback()) {
		return nil
	}
	return errors.New("the issuer is reached over HTTPS")
}

// Providers lists the sign-ons, in the order of the settings, as the
// page of signing in offers them.
func (s *Set) Providers() []store.SignOnProvider {
	out := make([]store.SignOnProvider, 0, len(s.order))
	for _, key := range s.order {
		p := s.by[key]
		out = append(out, store.SignOnProvider{Name: p.Name, Label: p.Label})
	}
	return out
}

func random(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Begin answers the address that sends a person to a provider, and the
// state that names this sign-on, which the page keeps in the person's
// browser so that the answer is taken only from the browser that asked.
// redirect is where the provider sends the person back; next where the
// server sends them after.
func (s *Set) Begin(ctx context.Context, name, redirect, next string) (to, state string, err error) {
	p := s.by[strings.ToLower(name)]
	if p == nil {
		return "", "", fmt.Errorf("the server has no sign-on %q", name)
	}
	d, err := p.discover(ctx, s)
	if err != nil {
		return "", "", err
	}
	f := &flow{provider: strings.ToLower(name), redirect: redirect, next: next, ends: s.now().Add(s.lasting)}
	if state, err = random(32); err == nil {
		if f.nonce, err = random(32); err == nil {
			f.verifier, err = random(32)
		}
	}
	if err != nil {
		return "", "", err
	}
	challenge := sha256.Sum256([]byte(f.verifier))
	s.mu.Lock()
	now := s.now()
	for k, old := range s.flows {
		if !old.ends.After(now) {
			delete(s.flows, k)
		}
	}
	if len(s.flows) >= maxFlows {
		s.mu.Unlock()
		return "", "", errors.New("too many sign-ons are under way; try again in a few minutes")
	}
	s.flows[state] = f
	s.mu.Unlock()
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {p.ClientID},
		"redirect_uri":          {redirect},
		"scope":                 {"openid email profile"},
		"state":                 {state},
		"nonce":                 {f.nonce},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method": {"S256"},
	}
	sep := "?"
	if strings.Contains(d.Authorization, "?") {
		sep = "&"
	}
	return d.Authorization + sep + q.Encode(), state, nil
}

// Finish takes the code a provider sent the person back with, for the
// sign-on its state names, exchanges it for the provider's signed note,
// checks the note and answers who the person is, and where the server
// sends them after. A state is taken once.
func (s *Set) Finish(ctx context.Context, state, code, redirect string) (*store.SignedOn, string, error) {
	s.mu.Lock()
	f := s.flows[state]
	delete(s.flows, state)
	s.mu.Unlock()
	if f == nil || !f.ends.After(s.now()) {
		return nil, "", errors.New("the sign-on is not one under way, or it took longer than ten minutes")
	}
	if f.redirect != redirect {
		return nil, "", errors.New("the sign-on came back to another address than it left from")
	}
	if code == "" {
		return nil, f.next, errors.New("the provider sent no code")
	}
	p := s.by[f.provider]
	d, err := p.discover(ctx, s)
	if err != nil {
		return nil, f.next, err
	}
	secret, err := s.secret(ctx, p.Name)
	if err != nil {
		return nil, f.next, fmt.Errorf("the sign-on %s has no client secret: %v", p.Name, err)
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirect},
		"code_verifier": {f.verifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.Token, nil)
	if err != nil {
		return nil, f.next, err
	}
	// The secret goes in the body unless the provider takes it only in
	// the header.
	basic := len(d.AuthMethods) > 0 && !contains(d.AuthMethods, "client_secret_post") && contains(d.AuthMethods, "client_secret_basic")
	if basic {
		req.SetBasicAuth(url.QueryEscape(p.ClientID), url.QueryEscape(secret))
	} else {
		form.Set("client_id", p.ClientID)
		form.Set("client_secret", secret)
	}
	body := form.Encode()
	req.Body = io.NopCloser(strings.NewReader(body))
	req.ContentLength = int64(len(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	var answer struct {
		IDToken     string `json:"id_token"`
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	status, err := s.ask(req, &answer)
	if err != nil {
		return nil, f.next, fmt.Errorf("the provider %s did not exchange the code: %v", p.Name, err)
	}
	if status != http.StatusOK || answer.IDToken == "" {
		return nil, f.next, fmt.Errorf("the provider %s did not exchange the code: status %d, %s %s", p.Name, status, answer.Error, answer.Description)
	}
	who, err := p.check(ctx, s, answer.IDToken, f.nonce)
	if err != nil {
		return nil, f.next, fmt.Errorf("the note of the provider %s does not hold: %v", p.Name, err)
	}
	return who, f.next, nil
}

func contains(list []string, item string) bool {
	for _, x := range list {
		if x == item {
			return true
		}
	}
	return false
}

// ask sends a request to a provider and reads its JSON answer, at most a
// megabyte of it.
func (s *Set) ask(req *http.Request, into any) (int, error) {
	resp, err := s.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	text, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswer+1))
	if err != nil {
		return resp.StatusCode, err
	}
	if len(text) > maxAnswer {
		return resp.StatusCode, errors.New("the answer is larger than a megabyte")
	}
	if err := json.Unmarshal(text, into); err != nil {
		return resp.StatusCode, fmt.Errorf("the answer is no JSON: %v", err)
	}
	return resp.StatusCode, nil
}

// discover reads where a provider signs people in, from its issuer's
// /.well-known/openid-configuration, once a day.
func (p *provider) discover(ctx context.Context, s *Set) (*discovered, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.found != nil && s.now().Sub(p.foundAt) < readAgain {
		return p.found, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(p.Issuer, "/")+"/.well-known/openid-configuration", nil)
	if err != nil {
		return nil, err
	}
	var d discovered
	status, err := s.ask(req, &d)
	if err == nil && status != http.StatusOK {
		err = fmt.Errorf("status %d", status)
	}
	if err != nil {
		if p.found != nil {
			// A provider that does not answer now is asked again later,
			// and what was read of it holds meanwhile.
			return p.found, nil
		}
		return nil, fmt.Errorf("the provider %s does not say where it signs people in: %v", p.Name, err)
	}
	if d.Issuer != p.Issuer {
		return nil, fmt.Errorf("the provider %s says its issuer is %q, and the settings say %q", p.Name, d.Issuer, p.Issuer)
	}
	for _, at := range []string{d.Authorization, d.Token, d.JWKS} {
		if at == "" || CheckIssuer(at) != nil {
			return nil, fmt.Errorf("the provider %s gives an address that is not over HTTPS, or none: %q", p.Name, at)
		}
	}
	p.found, p.foundAt = &d, s.now()
	return p.found, nil
}

// key answers the key a note names, reading the provider's keys again
// when it names one not read yet, at most once a minute.
func (p *provider) key(ctx context.Context, s *Set, kid string) (crypto.PublicKey, error) {
	d, err := p.discover(ctx, s)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	now := s.now()
	if k, ok := p.keys[kid]; ok && now.Sub(p.keysAt) < readAgain {
		return k, nil
	}
	if now.Sub(p.askedFor) < keysAtMost {
		if k, ok := p.keys[kid]; ok {
			return k, nil
		}
		return nil, fmt.Errorf("the note is signed with a key %q the provider does not publish", kid)
	}
	p.askedFor = now
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.JWKS, nil)
	if err != nil {
		return nil, err
	}
	var set struct {
		Keys []jwk `json:"keys"`
	}
	status, err := s.ask(req, &set)
	if err == nil && status != http.StatusOK {
		err = fmt.Errorf("status %d", status)
	}
	if err != nil {
		return nil, fmt.Errorf("the keys of the provider cannot be read: %v", err)
	}
	keys := map[string]crypto.PublicKey{}
	for _, k := range set.Keys {
		if pub, err := k.public(); err == nil {
			keys[k.Kid] = pub
		}
	}
	p.keys, p.keysAt = keys, now
	if k, ok := keys[kid]; ok {
		return k, nil
	}
	return nil, fmt.Errorf("the note is signed with a key %q the provider does not publish", kid)
}

// jwk is one published key (RFC 7517): an RSA key of 2048 bits or more,
// or a key of the curve P-256.
type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

func (k jwk) public() (crypto.PublicKey, error) {
	if k.Use != "" && k.Use != "sig" {
		return nil, errors.New("not a key that signs")
	}
	switch k.Kty {
	case "RSA":
		n, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			return nil, err
		}
		e, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil || len(e) == 0 || len(e) > 4 {
			return nil, errors.New("the exponent is not one")
		}
		pub := &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
		if pub.N.BitLen() < 2048 {
			return nil, errors.New("the key is shorter than 2048 bits")
		}
		return pub, nil
	case "EC":
		if k.Crv != "P-256" {
			return nil, errors.New("the curve is not P-256")
		}
		x, err := base64.RawURLEncoding.DecodeString(k.X)
		if err != nil || len(x) != 32 {
			return nil, errors.New("the point is not one")
		}
		y, err := base64.RawURLEncoding.DecodeString(k.Y)
		if err != nil || len(y) != 32 {
			return nil, errors.New("the point is not one")
		}
		return ecdsa.ParseUncompressedPublicKey(elliptic.P256(), append(append([]byte{4}, x...), y...))
	}
	return nil, errors.New("a key of a kind not taken")
}

// check verifies a provider's note, a JWT (RFC 7519), and answers who it
// says the person is: its signature by a key the provider publishes, its
// issuer, that it is for this server's client, that it has not ended,
// and that its nonce is the one this sign-on sent.
func (p *provider) check(ctx context.Context, s *Set, token, nonce string) (*store.SignedOn, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("it is no JWT")
	}
	var head struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := decodePart(parts[0], &head); err != nil {
		return nil, err
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, errors.New("its signature does not read")
	}
	key, err := p.key(ctx, s, head.Kid)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	switch pub := key.(type) {
	case *rsa.PublicKey:
		switch head.Alg {
		case "RS256":
			err = rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig)
		case "PS256":
			err = rsa.VerifyPSS(pub, crypto.SHA256, sum[:], sig, nil)
		default:
			err = fmt.Errorf("it is signed with %q, which an RSA key does not sign here", head.Alg)
		}
	case *ecdsa.PublicKey:
		if head.Alg != "ES256" || len(sig) != 64 {
			err = fmt.Errorf("it is signed with %q, which a P-256 key does not sign here", head.Alg)
		} else if !ecdsa.Verify(pub, sum[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
			err = errors.New("the signature is not the key's")
		}
	default:
		err = errors.New("a key of a kind not taken")
	}
	if err != nil {
		return nil, fmt.Errorf("its signature does not hold: %v", err)
	}
	var claims struct {
		Iss           string          `json:"iss"`
		Sub           string          `json:"sub"`
		Aud           json.RawMessage `json:"aud"`
		Azp           string          `json:"azp"`
		Exp           json.Number     `json:"exp"`
		Iat           json.Number     `json:"iat"`
		Nonce         string          `json:"nonce"`
		Email         string          `json:"email"`
		EmailVerified json.RawMessage `json:"email_verified"`
		Preferred     string          `json:"preferred_username"`
	}
	if err := decodePart(parts[1], &claims); err != nil {
		return nil, err
	}
	if claims.Iss != p.Issuer {
		return nil, fmt.Errorf("it is issued by %q, and the settings say %q", claims.Iss, p.Issuer)
	}
	var aud []string
	if json.Unmarshal(claims.Aud, &aud) != nil {
		var one string
		if json.Unmarshal(claims.Aud, &one) != nil {
			return nil, errors.New("its audience does not read")
		}
		aud = []string{one}
	}
	if !contains(aud, p.ClientID) || len(aud) > 1 && claims.Azp != p.ClientID {
		return nil, errors.New("it is not for this server")
	}
	now := s.now()
	exp, err := claims.Exp.Int64()
	if err != nil || !now.Before(time.Unix(exp, 0).Add(skew)) {
		return nil, errors.New("it has ended")
	}
	if iat, err := claims.Iat.Int64(); err == nil && now.Add(skew).Before(time.Unix(iat, 0)) {
		return nil, errors.New("it is issued in the future")
	}
	if claims.Nonce == "" || subtle.ConstantTimeCompare([]byte(claims.Nonce), []byte(nonce)) != 1 {
		return nil, errors.New("its nonce is not this sign-on's")
	}
	if claims.Sub == "" {
		return nil, errors.New("it names nobody")
	}
	who := &store.SignedOn{Provider: p.Name, Issuer: claims.Iss, Subject: claims.Sub, Email: claims.Email}
	switch v := strings.Trim(string(claims.EmailVerified), `"`); {
	case v == "true":
		who.EmailVerified = true
	case p.CompanyEmails:
		if who.Email == "" && strings.Contains(claims.Preferred, "@") {
			who.Email = claims.Preferred
		}
		who.EmailVerified = who.Email != ""
	}
	return who, nil
}

func decodePart(part string, into any) error {
	text, err := base64.RawURLEncoding.DecodeString(part)
	if err != nil {
		return errors.New("a part of it does not read")
	}
	dec := json.NewDecoder(strings.NewReader(string(text)))
	dec.UseNumber()
	if err := dec.Decode(into); err != nil {
		return errors.New("a part of it is no JSON")
	}
	return nil
}
