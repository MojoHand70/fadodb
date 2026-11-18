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
}

// Pages answers the server's own pages, under /auth/.
type Pages struct {
	signOn   SignOn
	accounts Accounts
	origin   string
	secure   bool
	lengths  store.SessionLengths
	report   func(Request)
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
	p := &Pages{accounts: a, lengths: o.Lengths, report: o.Report, signOn: o.SignOn}
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
// Sec-Fetch-Site when it sends no Origin.
func (p *Pages) sameOrigin(r *http.Request) bool {
	origin, ok := p.originOf(r)
	if !ok {
		return false
	}
	if sent := r.Header.Get("Origin"); sent != "" {
		return sent == origin
	}
	return r.Header.Get("Sec-Fetch-Site") == "same-origin"
}

// Who is the door's Who of a server whose pages sign people in: a key or
// a pass in the header as keys says, or else the session a browser
// keeps, for a request from a page of the server itself (ADR-0071,
// item 8). A command that changes something with a session must say it
// comes from the server's own page.
func (p *Pages) Who(keys Who) Who {
	return func(r *http.Request) (store.Principal, error) {
		if r.Header.Get("Authorization") != "" {
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
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	if _, ok := p.originOf(r); !ok {
		p.draw(out, &told, http.StatusNotFound, "notHere", &page{Title: "Not here"})
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
	redirect(out, PageSignIn+"?next="+url.QueryEscape(r.URL.Path))
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
		shown.List, err = p.accounts.Sessions(r.Context(), "", as)
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

// style is the look of every page, light and dark.
const style = `body{font:16px/1.5 system-ui,sans-serif;max-width:32rem;margin:2rem auto;padding:0 1rem;color:#1b1b1f;background:#fff}
h1{font-size:1.4rem}label{display:block;margin:.8rem 0 .2rem}input{font:inherit;width:100%;box-sizing:border-box;padding:.45rem;border:1px solid #888;border-radius:4px}
button{font:inherit;margin-top:1rem;padding:.45rem 1rem;border:0;border-radius:4px;background:#1f5fbf;color:#fff;cursor:pointer}
.error{color:#a40000}.note{color:#1d6b2c}code,.codes{font-family:ui-monospace,monospace}.codes li{margin:.2rem 0}
table{border-collapse:collapse;width:100%}td{padding:.3rem 0;border-bottom:1px solid #ddd;vertical-align:top}
form.inline{display:inline}form.inline button{margin:0;padding:.2rem .6rem}
@media (prefers-color-scheme:dark){body{color:#e8e8ec;background:#141418}input{background:#1f1f25;color:inherit}td{border-color:#333}.error{color:#ff8a80}.note{color:#8fd19e}}`

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

// drawn is a page of the server: the frame, and what the page holds.
func drawn(name string, d *page) string {
	var b strings.Builder
	b.WriteString(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>` + esc(d.Title) + ` · FadoDB</title>
` + refresh(d.Refresh) + `<style>` + style + `</style></head><body>
<h1>` + esc(d.Title) + "</h1>\n")
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
		b.WriteString("</table>")
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
	case "linkGone":
		b.WriteString("<p>This link does not hold: it was used, it ended, or it is not one. Ask an administrator of the database for a new one.</p>")
	default:
		b.WriteString("<p>There is no page here. A server that is reached from other machines signs people in only when its operator gives it its address, url, in its settings.</p>")
	}
	b.WriteString("\n</body></html>\n")
	return b.String()
}
