package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/MojoHand70/fadodb/internal/command"
	"github.com/MojoHand70/fadodb/internal/store"
)

// The server gives passes for its own database (ADR-0071, items 6 to 9),
// as OAuth 2.1 and the protocol of agents have it: it publishes how it
// gives them, a client sends the person here, the person signs in and
// allows it, and the client exchanges its code, with the proof of PKCE,
// for a pass of one hour and a pass that renews it.

// Documents reads the document a client publishes to name itself, for a
// client whose id is the address of the document. Package signon has
// one.
type Documents interface {
	Client(ctx context.Context, id string) (*store.Client, error)
}

// The addresses of OAuth, beside the pages.
const (
	PageAuthorize = "/auth/authorize"
	PageAllow     = "/auth/allow"
	PageToken     = "/auth/token"
	PageRevoke    = "/auth/revoke"
	// WellKnownServer says how the server gives passes (RFC 8414), and
	// WellKnownResource, followed by an address, where the passes of the
	// address come from (RFC 9728).
	WellKnownServer   = "/.well-known/oauth-authorization-server"
	WellKnownResource = "/.well-known/oauth-protected-resource"
)

// addressOf is the address of the server a request is for, as a pass
// names it.
func addressOf(path string) string {
	if path == store.AddressAgents || strings.HasPrefix(path, store.AddressAgents+"/") {
		return store.AddressAgents
	}
	return store.AddressCommands
}

// bearer is the token of a header Authorization: Bearer <token>.
func bearer(header string) (string, bool) {
	const scheme = "Bearer "
	if len(header) <= len(scheme) || !strings.EqualFold(header[:len(scheme)], scheme) {
		return "", false
	}
	return header[len(scheme):], true
}

// answerJSON writes an answer of OAuth's: JSON that no cache keeps, and
// that a page of any site may read, since it carries no cookie.
func answerJSON(out *reply, status int, v any) {
	text, err := json.Marshal(v)
	if err != nil {
		text = []byte(`{"error":"server_error"}`)
		status = http.StatusInternalServerError
	}
	h := out.w.Header()
	h.Set("Pragma", "no-cache")
	h.Set("Access-Control-Allow-Origin", "*")
	out.send(status, text)
}

// oauthError is a refusal in OAuth's words.
type oauthError struct {
	Error       string `json:"error"`
	Description string `json:"error_description,omitempty"`
}

func (p *Pages) metadata(out *reply, r *http.Request, told *Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		out.w.Header().Set("Allow", "GET")
		answerJSON(out, http.StatusMethodNotAllowed, oauthError{Error: "invalid_request", Description: "metadata is read with GET"})
		return
	}
	origin, _ := p.originOf(r)
	switch r.URL.Path {
	case WellKnownServer:
		m := map[string]any{
			"issuer":                                         origin,
			"authorization_endpoint":                         origin + PageAuthorize,
			"token_endpoint":                                 origin + PageToken,
			"revocation_endpoint":                            origin + PageRevoke,
			"response_types_supported":                       []string{"code"},
			"grant_types_supported":                          []string{"authorization_code", "refresh_token"},
			"code_challenge_methods_supported":               []string{"S256"},
			"token_endpoint_auth_methods_supported":          []string{"none"},
			"revocation_endpoint_auth_methods_supported":     []string{"none"},
			"authorization_response_iss_parameter_supported": true,
			"client_id_metadata_document_supported":          p.documents != nil,
		}
		answerJSON(out, http.StatusOK, m)
	case WellKnownResource + store.AddressCommands:
		answerJSON(out, http.StatusOK, map[string]any{
			"resource":                 origin + store.AddressCommands,
			"authorization_servers":    []string{origin},
			"bearer_methods_supported": []string{"header"},
		})
	default:
		told.Code = command.CodeUnknownCommand
		answerJSON(out, http.StatusNotFound, oauthError{Error: "invalid_request", Description: "there is nothing at " + r.URL.Path})
	}
}

// clientOf finds the client a request names: one an administrator added,
// or one known by its document.
func (p *Pages) clientOf(ctx context.Context, id string) (*store.Client, error) {
	if strings.HasPrefix(id, "https://") {
		if p.documents == nil {
			return nil, errors.New("this server reads no client's document")
		}
		return p.documents.Client(ctx, id)
	}
	if id == "" {
		return nil, errors.New("the request names no client")
	}
	return p.accounts.KnownClient(ctx, id)
}

// authorize takes a client's request for a code (ADR-0071, item 6). A
// client the server does not know, or an address it may not be sent to,
// is told to the person, and the client is not: the request might be
// somebody else's. Every other refusal goes back to the client. A request
// that holds is kept, and the page sends the browser on to the page that
// asks the person, by itself, so that the browser sends the cookie of
// the person's session, which it holds back on a request another site
// began.
func (p *Pages) authorize(out *reply, r *http.Request, told *Request) {
	if r.Method != http.MethodGet {
		out.w.Header().Set("Allow", "GET")
		p.draw(out, told, http.StatusMethodNotAllowed, "notHere", &page{Title: "Not here"})
		return
	}
	q := r.URL.Query()
	for _, name := range []string{"client_id", "redirect_uri", "response_type", "code_challenge", "code_challenge_method", "state", "resource"} {
		if len(q[name]) > 1 {
			told.Code = store.CodeBadValue
			p.draw(out, told, http.StatusBadRequest, "notAllowed", &page{Title: "This request does not hold", Error: "the request says " + name + " twice"})
			return
		}
	}
	origin, _ := p.originOf(r)
	client, err := p.clientOf(r.Context(), q.Get("client_id"))
	if err != nil {
		told.Code, told.Err = store.CodeUnknownClient, err
		p.draw(out, told, http.StatusBadRequest, "notAllowed", &page{Title: "This client is not known", Error: "the server does not know the client " + strconv.Quote(q.Get("client_id")) + ": " + err.Error()})
		return
	}
	redirect := q.Get("redirect_uri")
	if redirect == "" && len(client.RedirectURIs) == 1 {
		redirect = client.RedirectURIs[0]
	}
	if !store.RedirectAllowed(client, redirect) {
		told.Code = store.CodeBadValue
		p.draw(out, told, http.StatusBadRequest, "notAllowed", &page{Title: "This request does not hold", Error: "the client " + client.Name + " is not sent its codes at " + strconv.Quote(redirect)})
		return
	}
	back := func(code, description string) {
		told.Code = code
		sendBack(out, redirect, origin, url.Values{"error": {code}, "error_description": {description}, "state": {q.Get("state")}})
	}
	if q.Get("response_type") != "code" {
		back("unsupported_response_type", "the server answers with a code alone")
		return
	}
	if q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256" {
		back("invalid_request", "the server takes a request with a PKCE challenge of S256 alone")
		return
	}
	want := origin + client.Address
	if resource := strings.TrimSuffix(q.Get("resource"), "/"); resource != "" && resource != want {
		back("invalid_target", "the client "+client.Name+" is given passes for "+want+" alone")
		return
	}
	id, err := p.accounts.AskConsent(r.Context(), store.Asked{Client: *client, RedirectURI: redirect, Challenge: q.Get("code_challenge"), State: q.Get("state"), Resource: want})
	if err != nil {
		back("invalid_request", said(err))
		return
	}
	to := PageAllow + "?request=" + url.QueryEscape(id)
	p.draw(out, told, http.StatusOK, "onward", &page{Title: "One moment", Next: to, Refresh: to})
}

// sendBack sends the browser to the client with what it is told, and the
// server's own name, so that the client knows who answered (RFC 9207).
func sendBack(out *reply, to, origin string, v url.Values) {
	u, _ := url.Parse(to)
	q := u.Query()
	for k, list := range v {
		if len(list) > 0 && list[0] != "" {
			q.Set(k, list[0])
		}
	}
	q.Set("iss", origin)
	u.RawQuery = q.Encode()
	redirect(out, u.String())
}

// originOfURI is the origin of an address, as a page's policy names it.
func originOfURI(uri string) string {
	u, err := url.Parse(uri)
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// allow is the page that asks the person: "Claude (claude.ai) wants to
// act for you in this database. Allow?" A person signed in answers it,
// and the client is sent its code, or told it was not allowed.
func (p *Pages) allow(out *reply, r *http.Request, told *Request) {
	who, _, ok := p.signedIn(out, r, told)
	if !ok {
		return
	}
	as := store.As(who)
	id := r.FormValue("request")
	origin, _ := p.originOf(r)
	if r.Method != http.MethodPost {
		a, err := p.accounts.Consent(r.Context(), id, as)
		if err != nil {
			told.Code = codeOf(err)
			p.draw(out, told, http.StatusNotFound, "requestGone", &page{Title: "This request ended"})
			return
		}
		// The form is sent here, and its answer sends the browser on to
		// the client: the page lets the one form go there.
		out.w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self' "+originOfURI(a.RedirectURI)+"; frame-ancestors 'none'; base-uri 'none'")
		shown := &page{Title: "Allow " + a.Client.Name + "?", User: who.ID, Asked: a, Request: id, Groups: who.Groups, System: who.System}
		if a.Client.Address == store.AddressAgents {
			shown.Agent = store.AgentOf(who.ID, &a.Client)
		}
		p.draw(out, told, http.StatusOK, "consent", shown)
		return
	}
	if r.FormValue("do") != "allow" {
		a, err := p.accounts.Deny(r.Context(), id, as)
		if err != nil {
			told.Code = codeOf(err)
			p.draw(out, told, http.StatusNotFound, "requestGone", &page{Title: "This request ended"})
			return
		}
		told.Code = "access_denied"
		sendBack(out, a.RedirectURI, origin, url.Values{"error": {"access_denied"}, "error_description": {who.ID + " did not allow it"}, "state": {a.State}})
		return
	}
	code, a, err := p.accounts.Allow(r.Context(), id, as)
	if err != nil {
		told.Code = codeOf(err)
		p.draw(out, told, http.StatusNotFound, "requestGone", &page{Title: "This request ended"})
		return
	}
	sendBack(out, a.RedirectURI, origin, url.Values{"code": {code}, "state": {a.State}})
}

// tokenAnswer is the passes as OAuth answers them.
type tokenAnswer struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
}

// token gives a client its passes, for its code or its renewing pass. It
// is called by the client, not by a page: it takes no cookie, and a page
// of any site may read its answer.
func (p *Pages) token(out *reply, r *http.Request, told *Request) {
	if r.Method != http.MethodPost {
		out.w.Header().Set("Allow", "POST")
		answerJSON(out, http.StatusMethodNotAllowed, oauthError{Error: "invalid_request", Description: "passes are asked for with POST"})
		return
	}
	refuse := func(status int, code, description string) {
		told.Code = code
		answerJSON(out, status, oauthError{Error: code, Description: description})
	}
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/x-www-form-urlencoded") {
		refuse(http.StatusBadRequest, "invalid_request", "the body is a form, application/x-www-form-urlencoded")
		return
	}
	r.Body = http.MaxBytesReader(out.w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		refuse(http.StatusBadRequest, "invalid_request", "the form could not be read")
		return
	}
	if _, _, basic := r.BasicAuth(); basic || r.PostForm.Get("client_secret") != "" {
		out.w.Header().Set("WWW-Authenticate", "Basic")
		refuse(http.StatusUnauthorized, "invalid_client", "a client of this server holds no secret: it sends its client_id and proves its code with PKCE")
		return
	}
	for name, list := range r.PostForm {
		if len(list) > 1 {
			refuse(http.StatusBadRequest, "invalid_request", "the form says "+name+" twice")
			return
		}
	}
	f := r.PostForm
	client := f.Get("client_id")
	resource := strings.TrimSuffix(f.Get("resource"), "/")
	var passes *store.Passes
	var err error
	switch f.Get("grant_type") {
	case "authorization_code":
		passes, err = p.accounts.Exchange(r.Context(), store.CodeExchange{Client: client, Code: f.Get("code"), RedirectURI: f.Get("redirect_uri"),
			Verifier: f.Get("code_verifier"), Resource: resource, Unused: p.unused})
	case "refresh_token":
		passes, err = p.accounts.Renew(r.Context(), client, f.Get("refresh_token"), resource)
	default:
		refuse(http.StatusBadRequest, "unsupported_grant_type", "the server gives passes for a code or a renewing pass alone")
		return
	}
	switch {
	case errors.Is(err, store.ErrNoGrant):
		refuse(http.StatusBadRequest, "invalid_grant", "the code or the pass is not one the server lets through")
	case err != nil:
		told.Err = err
		refuse(http.StatusInternalServerError, "server_error", "the server could not give the passes; try again")
	default:
		answerJSON(out, http.StatusOK, tokenAnswer{AccessToken: passes.Pass, TokenType: "Bearer", ExpiresIn: passes.Lasts, RefreshToken: passes.Renew})
	}
}

// revoke ends the grant of a pass its client gives back (RFC 7009). It
// answers the same whatever the pass was.
func (p *Pages) revoke(out *reply, r *http.Request, told *Request) {
	if r.Method != http.MethodPost {
		out.w.Header().Set("Allow", "POST")
		answerJSON(out, http.StatusMethodNotAllowed, oauthError{Error: "invalid_request", Description: "a pass is given back with POST"})
		return
	}
	r.Body = http.MaxBytesReader(out.w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil || r.PostForm.Get("token") == "" {
		told.Code = "invalid_request"
		answerJSON(out, http.StatusBadRequest, oauthError{Error: "invalid_request", Description: "the form names no token"})
		return
	}
	if err := p.accounts.Revoke(r.Context(), r.PostForm.Get("client_id"), r.PostForm.Get("token")); err != nil {
		told.Err = err
		answerJSON(out, http.StatusServiceUnavailable, oauthError{Error: "temporarily_unavailable", Description: "the server could not end the grant; try again"})
		return
	}
	answerJSON(out, http.StatusOK, struct{}{})
}
