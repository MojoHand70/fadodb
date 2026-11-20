package server

import (
	"context"
	"errors"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/MojoHand70/fadodb/internal/store"
)

// The pages where a person signs in (ADR-0071, items 1 to 5 and 9): the
// server's own, plain HTML with no script, in English. A password, a
// code and a session's token are typed and kept here and never travel
// in a command.

// Accounts is what the pages ask of a database that keeps its users.
type Accounts interface {
	BeginSignIn(ctx context.Context, user, password, address string) (*store.SignIn, error)
	SetUpSecondStep(ctx context.Context, signIn string) (*store.SecondStep, error)
	FinishSignIn(ctx context.Context, signIn, code, address string, lengths store.SessionLengths) (*store.Session, error)
	SessionCaller(ctx context.Context, token string) (store.Principal, *store.Session, error)
	SignOut(ctx context.Context, token string) error
	ChangePassword(ctx context.Context, old, password, address string, opts ...store.Option) error
	PasswordLinkUser(ctx context.Context, token string) (string, error)
	UsePasswordLink(ctx context.Context, token, password, address string) error
	SetUpMySecondStep(ctx context.Context, opts ...store.Option) (*store.SecondStep, error)
	ConfirmMySecondStep(ctx context.Context, code, address string, opts ...store.Option) ([]string, error)
	HasSecondStep(ctx context.Context, user string, opts ...store.Option) (has, required bool, err error)
	ClearSecondStep(ctx context.Context, user string, opts ...store.Option) error
	Sessions(ctx context.Context, user string, opts ...store.Option) ([]*store.Session, error)
	EndSession(ctx context.Context, id string, opts ...store.Option) error
	SignOn(ctx context.Context, who store.SignedOn, address string, lengths store.SessionLengths) (*store.Session, error)
	// The passes the server gives clients (ADR-0071, items 6 and 7).
	KnownClient(ctx context.Context, id string) (*store.Client, error)
	AskConsent(ctx context.Context, a store.Asked) (string, error)
	Consent(ctx context.Context, id string, opts ...store.Option) (*store.Asked, error)
	Allow(ctx context.Context, id string, opts ...store.Option) (string, *store.Asked, error)
	Deny(ctx context.Context, id string, opts ...store.Option) (*store.Asked, error)
	Exchange(ctx context.Context, x store.CodeExchange) (*store.Passes, error)
	Renew(ctx context.Context, client, token, resource string) (*store.Passes, error)
	Revoke(ctx context.Context, client, token string) error
	PassCaller(ctx context.Context, token, address string) (store.Principal, error)
	Grants(ctx context.Context, user string, opts ...store.Option) ([]*store.Grant, error)
	EndGrant(ctx context.Context, id string, opts ...store.Option) error
}

// SignOn is a company's sign-on as the pages ask it (ADR-0071, item 4):
// the providers to offer, sending a person to one, and taking them back
// with who the provider says they are. Package signon is one.
type SignOn interface {
	Providers() []store.SignOnProvider
	Begin(ctx context.Context, name, redirect, next string) (to, state string, err error)
	Finish(ctx context.Context, state, code, redirect string) (*store.SignedOn, string, error)
}

// PagesOptions are what the pages are told: the address people reach
// the server at, empty for this machine only; how long a session holds;
// and what every request is told to once it was answered.
type PagesOptions struct {
	URL     string
	Lengths store.SessionLengths
	Report  func(Request)
	// SignOn is the company sign-ons the pages offer; nil for none.
	SignOn SignOn
	// Documents reads the documents clients publish to name themselves,
	// as Claude does; nil gives passes to the clients an administrator
	// added alone. Unused is how long a grant holds with no pass renewed,
	// zero for the usual 30 days.
	Documents Documents
	Unused    time.Duration
}

// Pages answers the server's own pages, under /auth/.
type Pages struct {
	signOn    SignOn
	documents Documents
	accounts  Accounts
	origin    string
	secure    bool
	lengths   store.SessionLengths
	unused    time.Duration
	report    func(Request)
}

// SessionCookie is the name of the cookie a browser keeps its session
// in (ADR-0071, item 5).
const SessionCookie = "fado_session"

// The addresses of the pages.
const (
	PageSignIn   = "/auth/signin"
	PageCode     = "/auth/code"
	PageSignOut  = "/auth/signout"
	PageAccount  = "/auth/account"
	PagePassword = "/auth/password"
	PageSignOn   = "/auth/signon"
	PageCallback = "/auth/callback"
)

// signOnCookie keeps the state of a sign-on in the browser that began
// it, so that the provider's answer is taken only from that browser. It
// is sent back on the provider's redirect, which comes from another
// site, so it is Lax where the session is Strict.
const signOnCookie = "fado_signon"

// NewPages makes the pages of a database that keeps its users. A URL,
// when given, is the address people reach the server at: its scheme and
// host are the one origin the pages and a session's commands take, and
// over HTTPS the cookie travels only encrypted. Without one the pages
// answer requests to this machine alone.
func NewPages(a Accounts, o PagesOptions) (*Pages, error) {
	p := &Pages{accounts: a, lengths: o.Lengths, report: o.Report, signOn: o.SignOn, documents: o.Documents, unused: o.Unused}
	if o.URL != "" {
		u, err := url.Parse(o.URL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
			return nil, errors.New("the address of the server, url, is written as https://host or https://host:port, with no path")
		}
		p.origin = u.Scheme + "://" + u.Host
		p.secure = u.Scheme == "https"
	}
	return p, nil
}

// originOf answers the origin a request may come from, and whether the
// pages answer it at all: the one of the address the server was told,
// or, without one, the machine itself.
func (p *Pages) originOf(r *http.Request) (string, bool) {
	if p.origin != "" {
		return p.origin, true
	}
	host, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		host = r.Host
	}
	if host != "localhost" && host != "127.0.0.1" && host != "[::1]" && host != "::1" {
		return "", false
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host, true
}

// sameOrigin reports whether a request that changes something comes from
// a page of the server itself, as the browser says in Origin, or in
// Sec-Fetch-Site when it sends no Origin, or sends it as null, which a
// browser does for a page that sends no referrer. A page cannot set
// either header: the browser does.
func (p *Pages) sameOrigin(r *http.Request) bool {
	origin, ok := p.originOf(r)
	if !ok {
		return false
	}
	if sent := r.Header.Get("Origin"); sent != "" && sent != "null" {
		return sent == origin
	}
	return r.Header.Get("Sec-Fetch-Site") == "same-origin"
}

// Who is the door's Who of a server whose pages sign people in: a pass
// the server gave, for the address the request is sent to; a key in the
// header as keys says; or else the session a browser keeps, for a
// request from a page of the server itself (ADR-0071, item 8). A command
// that changes something with a session must say it comes from the
// server's own page.
func (p *Pages) Who(keys Who) Who {
	return func(r *http.Request) (store.Principal, error) {
		if sent := r.Header.Get("Authorization"); sent != "" {
			if token, ok := bearer(sent); ok && strings.HasPrefix(token, store.PassPrefix) {
				return p.accounts.PassCaller(r.Context(), token, addressOf(r.URL.Path))
			}
			return keys(r)
		}
		c, err := r.Cookie(SessionCookie)
		if err != nil || c.Value == "" {
			return keys(r)
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !p.sameOrigin(r) {
			return store.Principal{}, store.ErrUnknownKey
		}
		principal, _, err := p.accounts.SessionCaller(r.Context(), c.Value)
		return principal, err
	}
}

// address is where a request came from, for the log of sign-ins and the
// count of failures.
func address(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// next is where a page sends the person when they are done: a path of
// the server, never another site.
func next(r *http.Request) string {
	to := r.FormValue("next")
	if to == "" || to[0] != '/' || strings.HasPrefix(to, "//") || strings.ContainsAny(to, "\\\r\n") {
		return PageAccount
	}
	return to
}

// page is what a page is drawn from.
type page struct {
	Title   string
	Error   string
	Note    string
	Next    string
	User    string
	SignIn  string
	Link    string
	Step    *store.SecondStep
	Codes   []string
	Has     bool
	Needs   bool
	Current string
	List    []*store.Session
	// Providers are the company sign-ons the sign-in page offers.
	Providers []store.SignOnProvider
	// Asked is the request of a client the page asks the person about,
	// and Request its id; Agent is the agent the client acts as, for the
	// door for agents; Groups and System are the person's.
	Asked   *store.Asked
	Request string
	Agent   string
	Groups  []string
	System  []string
	// Grants are the clients the person allowed.
	Grants []*store.Grant
	// Refresh is where the page sends the browser at once, by itself.
	Refresh string
}

// ServeHTTP answers a page.
func (p *Pages) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	told := Request{At: time.Now(), Command: strings.TrimPrefix(r.URL.Path, "/")}
	out := &reply{w: w}
	defer func() {
		if p.report != nil {
			told.Status = out.status
			told.Took = time.Since(told.At)
			p.report(told)
		}
	}()
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Security-Policy", pagesPolicy("'self'"))
	h.Set("X-Frame-Options", "DENY")
	// A referrer goes to the server itself alone, never to another site.
	// With none at all, a browser sends the Origin of a form as null.
	h.Set("Referrer-Policy", "same-origin")
	h.Set("X-Content-Type-Options", "nosniff")
	if _, ok := p.originOf(r); !ok {
		p.draw(out, &told, http.StatusNotFound, "notHere", &page{Title: "Not here"})
		return
	}
	// What a client calls, not a page: no cookie, from anywhere.
	switch {
	case strings.HasPrefix(r.URL.Path, "/.well-known/"):
		p.metadata(out, r, &told)
		return
	case r.URL.Path == PageToken:
		p.token(out, r, &told)
		return
	case r.URL.Path == PageRevoke:
		p.revoke(out, r, &told)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		if r.Method != http.MethodPost {
			h.Set("Allow", "GET, POST")
			p.draw(out, &told, http.StatusMethodNotAllowed, "notHere", &page{Title: "Not here"})
			return
		}
		// A form is taken only from a page of the server itself, so that
		// another site cannot sign a person in or out, or change what
		// they have, by sending their browser here.
		if !p.sameOrigin(r) {
			told.Code = store.CodeForbidden
			p.draw(out, &told, http.StatusForbidden, "notHere", &page{Title: "Not from this server"})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	}
	switch r.URL.Path {
	case PageSignIn:
		p.signIn(out, r, &told)
	case PageCode:
		p.code(out, r, &told)
	case PageSignOut:
		p.signOut(out, r, &told)
	case PageAccount:
		p.account(out, r, &told)
	case PagePassword:
		p.password(out, r, &told)
	case PageSignOn:
		p.beginSignOn(out, r, &told)
	case PageCallback:
		p.callback(out, r, &told)
	case PageAuthorize:
		p.authorize(out, r, &told)
	case PageAllow:
		p.allow(out, r, &told)
	default:
		p.draw(out, &told, http.StatusNotFound, "notHere", &page{Title: "Not here"})
	}
}

// codeOf is the code of a refusal, for the log of requests.
func codeOf(err error) string {
	var e *store.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return "internal"
}

// said is what a page says of a refusal: the engine's words, which for
// signing in are the same whatever was wrong.
func said(err error) string {
	var e *store.Error
	if errors.As(err, &e) {
		return e.Message
	}
	return "the server could not do it; try again"
}

func (p *Pages) signIn(out *reply, r *http.Request, told *Request) {
	if r.Method != http.MethodPost {
		if c, err := r.Cookie(SessionCookie); err == nil {
			if _, _, err := p.accounts.SessionCaller(r.Context(), c.Value); err == nil {
				redirect(out, next(r))
				return
			}
		}
		p.draw(out, told, http.StatusOK, "signIn", &page{Title: "Sign in", Next: next(r), Providers: p.providers()})
		return
	}
	user := strings.TrimSpace(r.FormValue("user"))
	told.User = user
	in, err := p.accounts.BeginSignIn(r.Context(), user, r.FormValue("password"), address(r))
	if err != nil {
		told.Code = codeOf(err)
		p.draw(out, told, http.StatusUnauthorized, "signIn", &page{Title: "Sign in", Error: said(err), Next: next(r), User: user, Providers: p.providers()})
		return
	}
	switch in.Next {
	case store.NextSession:
		p.finish(out, r, told, in.ID, "")
	case store.NextCode:
		p.draw(out, told, http.StatusOK, "code", &page{Title: "Second step", SignIn: in.ID, Next: next(r), User: in.User})
	case store.NextSetUp:
		step, err := p.accounts.SetUpSecondStep(r.Context(), in.ID)
		if err != nil {
			told.Code = codeOf(err)
			p.draw(out, told, http.StatusUnauthorized, "signIn", &page{Title: "Sign in", Error: said(err), Next: next(r)})
			return
		}
		p.draw(out, told, http.StatusOK, "setUp", &page{Title: "Set up a second step", SignIn: in.ID, Next: next(r), User: in.User, Step: step})
	}
}

func (p *Pages) code(out *reply, r *http.Request, told *Request) {
	if r.Method != http.MethodPost {
		redirect(out, PageSignIn)
		return
	}
	p.finish(out, r, told, r.FormValue("signin"), r.FormValue("code"))
}

// finish gives the session of a sign-in and sends the person on: to the
// codes for a lost phone when the second step was just set up.
func (p *Pages) finish(out *reply, r *http.Request, told *Request, signIn, code string) {
	sess, err := p.accounts.FinishSignIn(r.Context(), signIn, code, address(r), p.lengths)
	if err != nil {
		told.Code = codeOf(err)
		p.draw(out, told, http.StatusUnauthorized, "signIn", &page{Title: "Sign in", Error: said(err), Next: next(r)})
		return
	}
	told.User = sess.User
	http.SetCookie(out.w, &http.Cookie{Name: SessionCookie, Value: sess.Token, Path: "/", HttpOnly: true, Secure: p.secure,
		SameSite: http.SameSiteStrictMode, MaxAge: int(sess.Ends - time.Now().Unix())})
	if len(sess.Codes) > 0 {
		p.draw(out, told, http.StatusOK, "codes", &page{Title: "Codes for a lost phone", Codes: sess.Codes, Next: next(r)})
		return
	}
	redirect(out, next(r))
}

func (p *Pages) signOut(out *reply, r *http.Request, told *Request) {
	if r.Method == http.MethodPost {
		if c, err := r.Cookie(SessionCookie); err == nil {
			if principal, _, err := p.accounts.SessionCaller(r.Context(), c.Value); err == nil {
				told.User = principal.ID
			}
			_ = p.accounts.SignOut(r.Context(), c.Value)
		}
		http.SetCookie(out.w, &http.Cookie{Name: SessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: p.secure, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	}
	redirect(out, PageSignIn)
}

// signedIn answers the caller of the session a request carries, or sends
// the person to sign in and answers false.
func (p *Pages) signedIn(out *reply, r *http.Request, told *Request) (store.Principal, *store.Session, bool) {
	c, err := r.Cookie(SessionCookie)
	if err == nil {
		if principal, sess, err := p.accounts.SessionCaller(r.Context(), c.Value); err == nil {
			told.User = principal.ID
			return principal, sess, true
		}
	}
	back := r.URL.Path
	if r.Method == http.MethodGet && r.URL.RawQuery != "" {
		back = r.URL.RequestURI()
	}
	redirect(out, PageSignIn+"?next="+url.QueryEscape(back))
	return store.Principal{}, nil, false
}

func (p *Pages) account(out *reply, r *http.Request, told *Request) {
	who, sess, ok := p.signedIn(out, r, told)
	if !ok {
		return
	}
	as := store.As(who)
	shown := &page{Title: "Your account", User: who.ID, Current: sess.ID}
	status := http.StatusOK
	if r.Method == http.MethodPost {
		var err error
		switch r.FormValue("do") {
		case "password":
			if r.FormValue("password") != r.FormValue("again") {
				err = &store.Error{Code: store.CodeBadValue, Message: "the two new passwords differ"}
			} else if err = p.accounts.ChangePassword(r.Context(), r.FormValue("old"), r.FormValue("password"), address(r), as); err == nil {
				// Whoever was signed in elsewhere with the old password
				// signs in again.
				others, _ := p.accounts.Sessions(r.Context(), "", as)
				for _, o := range others {
					if o.ID != sess.ID {
						_ = p.accounts.EndSession(r.Context(), o.ID, as)
					}
				}
				shown.Note = "Your password is changed, and your other sessions are ended."
			}
		case "set-up":
			var step *store.SecondStep
			if step, err = p.accounts.SetUpMySecondStep(r.Context(), as); err == nil {
				shown.Step = step
				shown.Title = "Set up a second step"
				p.draw(out, told, http.StatusOK, "setUpMine", shown)
				return
			}
		case "confirm":
			var codes []string
			if codes, err = p.accounts.ConfirmMySecondStep(r.Context(), r.FormValue("code"), address(r), as); err == nil {
				p.draw(out, told, http.StatusOK, "codes", &page{Title: "Codes for a lost phone", Codes: codes, Next: PageAccount})
				return
			}
		case "clear":
			if err = p.accounts.ClearSecondStep(r.Context(), "", as); err == nil {
				shown.Note = "Your second step is taken away."
			}
		case "end-grant":
			if err = p.accounts.EndGrant(r.Context(), r.FormValue("grant"), as); err == nil {
				shown.Note = "The client holds no pass any more."
			}
		case "end":
			if err = p.accounts.EndSession(r.Context(), r.FormValue("session"), as); err == nil {
				if r.FormValue("session") == sess.ID {
					http.SetCookie(out.w, &http.Cookie{Name: SessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: p.secure, SameSite: http.SameSiteStrictMode, MaxAge: -1})
					redirect(out, PageSignIn)
					return
				}
				shown.Note = "The session is ended."
			}
		default:
			err = &store.Error{Code: store.CodeBadValue, Message: "the page was sent nothing it does"}
		}
		if err != nil {
			told.Code = codeOf(err)
			shown.Error = said(err)
			status = http.StatusUnprocessableEntity
		}
	}
	var err error
	if shown.Has, shown.Needs, err = p.accounts.HasSecondStep(r.Context(), "", as); err == nil {
		if shown.List, err = p.accounts.Sessions(r.Context(), "", as); err == nil {
			shown.Grants, err = p.accounts.Grants(r.Context(), "", as)
		}
	}
	if err != nil {
		told.Code = codeOf(err)
		shown.Error = said(err)
		status = http.StatusInternalServerError
	}
	p.draw(out, told, status, "account", shown)
}

func (p *Pages) password(out *reply, r *http.Request, told *Request) {
	link := r.FormValue("link")
	user, err := p.accounts.PasswordLinkUser(r.Context(), link)
	if err != nil {
		told.Code = codeOf(err)
		p.draw(out, told, http.StatusNotFound, "linkGone", &page{Title: "Set your password"})
		return
	}
	told.User = user
	shown := &page{Title: "Set your password", User: user, Link: link}
	if r.Method != http.MethodPost {
		p.draw(out, told, http.StatusOK, "setPassword", shown)
		return
	}
	if r.FormValue("password") != r.FormValue("again") {
		err = &store.Error{Code: store.CodeBadValue, Message: "the two passwords differ"}
	} else {
		err = p.accounts.UsePasswordLink(r.Context(), link, r.FormValue("password"), address(r))
	}
	if err != nil {
		told.Code = codeOf(err)
		shown.Error = said(err)
		p.draw(out, told, http.StatusUnprocessableEntity, "setPassword", shown)
		return
	}
	p.draw(out, told, http.StatusOK, "passwordSet", shown)
}

// providers are the company sign-ons to offer.
func (p *Pages) providers() []store.SignOnProvider {
	if p.signOn == nil {
		return nil
	}
	return p.signOn.Providers()
}

// callbackOf is the address a provider sends a person back to.
func (p *Pages) callbackOf(r *http.Request) string {
	origin, _ := p.originOf(r)
	return origin + PageCallback
}

// beginSignOn sends a person to the company's provider they chose, and
// keeps the state of the sign-on in their browser.
func (p *Pages) beginSignOn(out *reply, r *http.Request, told *Request) {
	if p.signOn == nil || r.Method != http.MethodPost {
		p.draw(out, told, http.StatusNotFound, "notHere", &page{Title: "Not here"})
		return
	}
	to, state, err := p.signOn.Begin(r.Context(), r.FormValue("provider"), p.callbackOf(r), next(r))
	if err != nil {
		told.Code, told.Err = store.CodeSignInRefused, err
		p.draw(out, told, http.StatusBadGateway, "signIn", &page{Title: "Sign in", Error: "the sign-on did not start; try again, or sign in with your password", Next: next(r), Providers: p.providers()})
		return
	}
	http.SetCookie(out.w, &http.Cookie{Name: signOnCookie, Value: state, Path: PageCallback, HttpOnly: true, Secure: p.secure, SameSite: http.SameSiteLaxMode, MaxAge: 600})
	out.w.Header().Set("Location", to)
	out.status, out.wrote = http.StatusSeeOther, true
	out.w.WriteHeader(http.StatusSeeOther)
}

// callback takes a person back from the provider: the state must be the
// one their browser keeps, the provider's note must hold, and the store
// must know them. The session is set, and the page sends them on with a
// link of its own, so that the browser sends the session's cookie, which
// it holds back on the end of a chain of redirects begun on another
// site.
func (p *Pages) callback(out *reply, r *http.Request, told *Request) {
	if p.signOn == nil || r.Method != http.MethodGet {
		p.draw(out, told, http.StatusNotFound, "notHere", &page{Title: "Not here"})
		return
	}
	http.SetCookie(out.w, &http.Cookie{Name: signOnCookie, Value: "", Path: PageCallback, HttpOnly: true, Secure: p.secure, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	q := r.URL.Query()
	failed := func(err error) {
		told.Code, told.Err = store.CodeSignInRefused, err
		p.draw(out, told, http.StatusUnauthorized, "signIn", &page{Title: "Sign in", Error: "the sign-on was not completed; try again, or ask an administrator of the database", Providers: p.providers()})
	}
	if e := q.Get("error"); e != "" {
		failed(errors.New("the provider answered " + e + ": " + q.Get("error_description")))
		return
	}
	c, err := r.Cookie(signOnCookie)
	state := q.Get("state")
	if err != nil || state == "" || c.Value != state {
		failed(errors.New("the answer of the provider came to a browser that did not begin the sign-on"))
		return
	}
	who, to, err := p.signOn.Finish(r.Context(), state, q.Get("code"), p.callbackOf(r))
	if err != nil {
		failed(err)
		return
	}
	sess, err := p.accounts.SignOn(r.Context(), *who, address(r), p.lengths)
	if err != nil {
		told.Code = codeOf(err)
		p.draw(out, told, http.StatusUnauthorized, "signIn", &page{Title: "Sign in", Error: said(err), Providers: p.providers()})
		return
	}
	told.User = sess.User
	http.SetCookie(out.w, &http.Cookie{Name: SessionCookie, Value: sess.Token, Path: "/", HttpOnly: true, Secure: p.secure,
		SameSite: http.SameSiteStrictMode, MaxAge: int(sess.Ends - time.Now().Unix())})
	if to == "" || to[0] != '/' || strings.HasPrefix(to, "//") {
		to = PageAccount
	}
	p.draw(out, told, http.StatusOK, "signedOn", &page{Title: "Signed in", User: sess.User, Next: to, Refresh: to})
}

// redirect sends the browser on, after a form, with a GET.
func redirect(out *reply, to string) {
	out.w.Header().Set("Location", to)
	out.status, out.wrote = http.StatusSeeOther, true
	out.w.WriteHeader(http.StatusSeeOther)
}

func (p *Pages) draw(out *reply, told *Request, status int, name string, data *page) {
	out.w.Header().Set("Content-Type", "text/html; charset=utf-8")
	out.status, out.wrote = status, true
	out.w.WriteHeader(status)
	if _, err := io.WriteString(out.w, drawn(name, data)); err != nil && told.Err == nil {
		told.Err = err
	}
}

// The pages are written by hand, each value escaped, and not with
// html/template: a template calls methods by their names, which keeps
// every method of the program in it, two megabytes more of the server.

// style is the look of every page, light and dark: the colours of
// FadoDB, warm white and steel blue, a card on the page, and the mark
// above it. The letters are the machine's own: the header lets a page
// load no font.
const style = `:root{--bg:#F7F5F0;--surface:#FFF;--ink:#24262B;--ink-2:#4B4F57;--line:#DAD6CC;--line-2:#E8E4DA;--blue:#3F74A3;--blue-strong:#2E5C86;--on-blue:#FFF;--err:#8C2A1C;--ok:#2F6A4C}
@media (prefers-color-scheme:dark){:root{--bg:#17191D;--surface:#22252B;--ink:#ECE8DF;--ink-2:#C7C3B9;--line:#33373E;--line-2:#2B2F36;--blue:#6FA3D4;--blue-strong:#8DB8E2;--on-blue:#0F1114;--err:#FF9B8A;--ok:#7FC39E}}
*{box-sizing:border-box}
body{margin:0;padding:2.5rem 1rem;font:16px/1.55 system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;color:var(--ink);background:var(--bg)}
main{max-width:30rem;margin:0 auto;padding:1.75rem 1.75rem 2rem;background:var(--surface);border:1px solid var(--line);border-radius:12px;box-shadow:0 1px 2px rgba(36,38,43,.05),0 8px 24px rgba(36,38,43,.06)}
.brand{display:flex;align-items:center;gap:.6rem;margin:0 0 1.25rem;font-weight:700;font-size:1.15rem;letter-spacing:-.02em}
.brand img{display:block;width:40px;height:40px}
h1{margin:0 0 .75rem;font-size:1.45rem;letter-spacing:-.01em}
h2{margin:1.75rem 0 .5rem;padding-top:1.25rem;border-top:1px solid var(--line-2);font-size:1.05rem}
p{margin:.6rem 0}a{color:var(--blue-strong)}
label{display:block;margin:.9rem 0 .25rem;font-weight:600;font-size:.92rem;color:var(--ink-2)}
input{font:inherit;width:100%;padding:.55rem .7rem;border:1px solid var(--line);border-radius:8px;background:var(--surface);color:inherit}
input:focus-visible,button:focus-visible,a:focus-visible{outline:2px solid var(--blue);outline-offset:2px}
button{font:inherit;font-weight:600;margin-top:1.1rem;padding:.55rem 1.1rem;border:0;border-radius:8px;background:var(--blue);color:var(--on-blue);cursor:pointer}
button:hover{background:var(--blue-strong)}
.error{color:var(--err);font-weight:600}.note{color:var(--ok);font-weight:600}
code,.codes{font-family:ui-monospace,"Cascadia Mono",Consolas,monospace;overflow-wrap:anywhere}.codes li{margin:.2rem 0}
table{border-collapse:collapse;width:100%}td{padding:.5rem 0;border-bottom:1px solid var(--line-2);vertical-align:top}
form.inline{display:inline}form.inline button{margin:0;padding:.3rem .7rem;font-weight:500}`

// brand is the head of every page: the mark and the name. The picture
// is written in the page, as the header asks: a page loads nothing.
// The words beside it say what it is, so it has none of its own.
const brand = `<p class="brand"><img src="` + markData + `" width="40" height="40" alt=""><span>FadoDB</span></p>` + "\n"

// refresh is the line of a page that sends the browser on by itself.
func refresh(to string) string {
	if to == "" {
		return ""
	}
	return `<meta http-equiv="refresh" content="0; url=` + esc(to) + `">` + "\n"
}

// esc is a value as HTML writes it, in text and in an attribute.
func esc(s string) string { return html.EscapeString(s) }

func moment(secs int64) string { return time.Unix(secs, 0).UTC().Format("2006-01-02 15:04 UTC") }

// hidden is a field the form sends back as it was given.
func hidden(b *strings.Builder, name, value string) {
	b.WriteString(`<input type="hidden" name="` + name + `" value="` + esc(value) + `">`)
}

// codeField is the field of a code of six digits.
func codeField(b *strings.Builder) {
	b.WriteString(`<label for="code">Code</label><input id="code" name="code" autocomplete="one-time-code" inputmode="numeric" required autofocus>`)
}

// stepShown is the key of a second step as a page shows it.
func stepShown(b *strings.Builder, step *store.SecondStep) {
	b.WriteString("<p><code>" + esc(step.Secret) + "</code></p>\n<p>Or open this link on the phone: <code>" + esc(step.Link) + "</code></p>\n")
}

// pagesPolicy is the Content-Security-Policy a page of the server is
// sent with: it loads nothing and runs nothing, its style is written in
// it, and its form goes where forms says, the server itself and, for
// the one page that sends a person on to a client, that client.
//
// A picture may be written in the page, and none is loaded: the page
// names an empty icon that way (pageIcon). A browser asks for
// /favicon.ico on a page that names no icon, the header refuses the
// request, and the browser writes the refusal for whoever looks.
func pagesPolicy(forms string) string {
	return "default-src 'none'; style-src 'unsafe-inline'; img-src data:; form-action " + forms + "; frame-ancestors 'none'; base-uri 'none'"
}

// pageIcon names an empty icon in a page of the server, so that a
// browser asks for none.
const pageIcon = `<link rel="icon" href="data:,">`

// drawn is a page of the server: the frame, and what the page holds.
func drawn(name string, d *page) string {
	var b strings.Builder
	b.WriteString(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>` + esc(d.Title) + ` · FadoDB</title>
` + pageIcon + `
` + refresh(d.Refresh) + `<style>` + style + `</style></head><body>
<main>
` + brand + `<h1>` + esc(d.Title) + "</h1>\n")
	if d.Error != "" {
		b.WriteString(`<p class="error" role="alert">` + esc(d.Error) + "</p>\n")
	}
	if d.Note != "" {
		b.WriteString(`<p class="note">` + esc(d.Note) + "</p>\n")
	}
	switch name {
	case "signIn":
		b.WriteString(`<form method="post" action="` + PageSignIn + `">`)
		hidden(&b, "next", d.Next)
		b.WriteString(`<label for="user">User</label><input id="user" name="user" autocomplete="username" value="` + esc(d.User) + `" required autofocus>
<label for="password">Password</label><input id="password" name="password" type="password" autocomplete="current-password" required>
<button type="submit">Sign in</button></form>`)
		for _, pr := range d.Providers {
			b.WriteString(`<form method="post" action="` + PageSignOn + `">`)
			hidden(&b, "provider", pr.Name)
			hidden(&b, "next", d.Next)
			b.WriteString(`<button type="submit">` + esc(pr.Label) + `</button></form>`)
		}
	case "code":
		b.WriteString("<p>Enter the six digits your authenticator app shows for " + esc(d.User) + ", or one of your codes for a lost phone.</p>\n")
		b.WriteString(`<form method="post" action="` + PageCode + `">`)
		hidden(&b, "signin", d.SignIn)
		hidden(&b, "next", d.Next)
		codeField(&b)
		b.WriteString(`<button type="submit">Go on</button></form>`)
	case "setUp":
		b.WriteString("<p>" + esc(d.User) + ", your groups ask for a second step. Add this key to an authenticator app on your phone, then enter the six digits it shows.</p>\n")
		stepShown(&b, d.Step)
		b.WriteString(`<form method="post" action="` + PageCode + `">`)
		hidden(&b, "signin", d.SignIn)
		hidden(&b, "next", d.Next)
		codeField(&b)
		b.WriteString(`<button type="submit">Go on</button></form>`)
	case "setUpMine":
		b.WriteString("<p>Add this key to an authenticator app on your phone, then enter the six digits it shows.</p>\n")
		stepShown(&b, d.Step)
		b.WriteString(`<form method="post" action="` + PageAccount + `">`)
		hidden(&b, "do", "confirm")
		codeField(&b)
		b.WriteString(`<button type="submit">Keep it</button></form>`)
	case "codes":
		b.WriteString("<p>Write these down and keep them away from your phone. Each lets you in once if the phone is lost. They are shown this once.</p>\n<ol class=\"codes\">")
		for _, c := range d.Codes {
			b.WriteString("<li>" + esc(c) + "</li>")
		}
		b.WriteString("</ol>\n<p><a href=\"" + esc(d.Next) + "\">Go on</a></p>")
	case "account":
		b.WriteString("<p>You are signed in as <strong>" + esc(d.User) + "</strong>.</p>\n")
		b.WriteString(`<form method="post" action="` + PageSignOut + `"><button type="submit">Sign out</button></form>
<h2>Password</h2>
<form method="post" action="` + PageAccount + `">`)
		hidden(&b, "do", "password")
		b.WriteString(`<input type="text" name="user" value="` + esc(d.User) + `" autocomplete="username" hidden>
<label for="old">Your password</label><input id="old" name="old" type="password" autocomplete="current-password" required>
<label for="password">A new password, at least 12 characters</label><input id="password" name="password" type="password" autocomplete="new-password" minlength="12" required>
<label for="again">The new password again</label><input id="again" name="again" type="password" autocomplete="new-password" minlength="12" required>
<button type="submit">Change it</button></form>
<h2>Second step</h2>
`)
		switch {
		case d.Has:
			b.WriteString("<p>A code from your authenticator app is asked after your password.</p>\n")
			if !d.Needs {
				b.WriteString(`<form method="post" action="` + PageAccount + `">`)
				hidden(&b, "do", "clear")
				b.WriteString(`<button type="submit">Take it away</button></form>`)
			}
		case d.Needs:
			b.WriteString("<p>Your groups ask for one, and you set it up at your next sign-in.</p>\n")
		default:
			b.WriteString("<p>You have none. A code from an app on your phone keeps your account yours even if your password leaks.</p>\n")
		}
		b.WriteString(`<form method="post" action="` + PageAccount + `">`)
		hidden(&b, "do", "set-up")
		if d.Has {
			b.WriteString(`<button type="submit">Set up a new one</button></form>`)
		} else {
			b.WriteString(`<button type="submit">Set one up</button></form>`)
		}
		b.WriteString("\n<h2>Sessions</h2>\n<table>")
		for _, s := range d.List {
			b.WriteString("<tr><td>" + esc(moment(s.CreatedAt)))
			if s.Address != "" {
				b.WriteString(", from " + esc(s.Address))
			}
			if s.ID == d.Current {
				b.WriteString(" <strong>(this one)</strong>")
			}
			b.WriteString("<br>last used " + esc(moment(s.UsedAt)) + `</td><td><form class="inline" method="post" action="` + PageAccount + `">`)
			hidden(&b, "do", "end")
			hidden(&b, "session", s.ID)
			b.WriteString(`<button type="submit">End</button></form></td></tr>`)
		}
		b.WriteString("</table>\n<h2>Clients you allowed</h2>\n")
		if len(d.Grants) == 0 {
			b.WriteString("<p>None. A client you allow, such as Claude, is listed here, and you end it here.</p>")
		} else {
			b.WriteString("<table>")
			for _, g := range d.Grants {
				b.WriteString("<tr><td><strong>" + esc(g.ClientName) + "</strong>")
				if g.Agent != "" {
					b.WriteString(", acting for you as " + esc(g.Agent))
				}
				b.WriteString("<br>allowed " + esc(moment(g.CreatedAt)) + ", last renewed " + esc(moment(g.UsedAt)) + `</td><td><form class="inline" method="post" action="` + PageAccount + `">`)
				hidden(&b, "do", "end-grant")
				hidden(&b, "grant", g.ID)
				b.WriteString(`<button type="submit">End</button></form></td></tr>`)
			}
			b.WriteString("</table>")
		}
	case "setPassword":
		b.WriteString("<p>Choose the password of <strong>" + esc(d.User) + "</strong>: at least 12 characters. A sentence of four or five words is long enough and easy to remember.</p>\n")
		b.WriteString(`<form method="post" action="` + PagePassword + `">`)
		hidden(&b, "link", d.Link)
		b.WriteString(`<input type="text" name="user" value="` + esc(d.User) + `" autocomplete="username" hidden>
<label for="password">Password</label><input id="password" name="password" type="password" autocomplete="new-password" minlength="12" required autofocus>
<label for="again">The password again</label><input id="again" name="again" type="password" autocomplete="new-password" minlength="12" required>
<button type="submit">Set it</button></form>`)
	case "signedOn":
		b.WriteString("<p>You are signed in as <strong>" + esc(d.User) + "</strong>.</p>\n<p><a href=\"" + esc(d.Next) + "\">Go on</a></p>")
	case "passwordSet":
		b.WriteString(`<p class="note">The password of ` + esc(d.User) + ` is set.</p>` + "\n<p><a href=\"" + PageSignIn + "\">Sign in</a></p>")
	case "onward":
		b.WriteString("<p><a href=\"" + esc(d.Next) + "\">Go on</a></p>")
	case "consent":
		a := d.Asked
		host := a.Client.Host()
		if a.Client.Address == store.AddressAgents {
			b.WriteString("<p><strong>" + esc(a.Client.Name) + "</strong> (" + esc(host) + ") wants to act for you, <strong>" + esc(d.User) +
				"</strong>, in this database, as the agent " + esc(d.Agent) + ".</p>\n<p>It may do what your groups let you do")
		} else {
			b.WriteString("<p><strong>" + esc(a.Client.Name) + "</strong> (" + esc(host) + ") wants to use this database as you, <strong>" + esc(d.User) +
				"</strong>.</p>\n<p>It may do what your groups let you do")
		}
		if len(d.Groups) > 0 {
			b.WriteString(", in " + esc(strings.Join(d.Groups, ", ")))
		}
		if len(d.System) > 0 {
			b.WriteString(", and the work of " + esc(strings.Join(d.System, ", ")))
		}
		b.WriteString(", until you end it on your account page.")
		if a.Client.Address == store.AddressAgents {
			b.WriteString(" Where a class asks for a person, it is refused.")
		}
		b.WriteString("</p>\n")
		to := originOfURI(a.RedirectURI)
		if u, err := url.Parse(a.RedirectURI); err == nil && (u.Hostname() == "localhost" || strings.HasPrefix(u.Hostname(), "127.") || u.Hostname() == "::1") {
			b.WriteString("<p>Its pass goes to a program on your own computer, at " + esc(to) + ". Allow it only if you started that program.</p>\n")
		} else {
			b.WriteString("<p>Its pass goes to " + esc(to) + ".</p>\n")
		}
		b.WriteString(`<form class="inline" method="post" action="` + PageAllow + `">`)
		hidden(&b, "request", d.Request)
		hidden(&b, "do", "allow")
		b.WriteString(`<button type="submit">Allow</button></form> <form class="inline" method="post" action="` + PageAllow + `">`)
		hidden(&b, "request", d.Request)
		hidden(&b, "do", "deny")
		b.WriteString(`<button type="submit">Deny</button></form>`)
	case "requestGone":
		b.WriteString("<p>The request of the client ended, was answered, or is not one. Go back to the client and connect again.</p>")
	case "notAllowed":
		b.WriteString("<p>Nothing was sent to the client. If you followed a link to get here, the link may not be what it said.</p>")
	case "linkGone":
		b.WriteString("<p>This link does not hold: it was used, it ended, or it is not one. Ask an administrator of the database for a new one.</p>")
	default:
		b.WriteString("<p>There is no page here. A server that is reached from other machines signs people in only when its operator gives it its address, url, in its settings.</p>")
	}
	b.WriteString("\n</main>\n</body></html>\n")
	return b.String()
}
