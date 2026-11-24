package server

import (
	"context"
	"errors"
	"html"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/MojoHand70/fadodb/internal/qrcode"
	"github.com/MojoHand70/fadodb/internal/server/web"
	"github.com/MojoHand70/fadodb/internal/store"
)

// The pages where a person signs in (ADR-0071, items 1 to 5 and 9, and
// ADR-0088): the server's own, in English, built to the sign-in of
// Rhea, with one script of the server's own that asks the browser for a
// passkey. A passkey's answer, a password, a code and a session's token
// are taken and kept here and never travel in a command.

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
	// Passkeys and invitations (ADR-0088).
	Invited(ctx context.Context, token string) (*store.Invited, error)
	InvitationQuestion(ctx context.Context, token, origin string) (string, error)
	UseInvitation(ctx context.Context, token string, answer []byte, device, origin, address string, lengths store.SessionLengths) (*store.Session, error)
	SignInQuestion(origin string) (string, error)
	SignInWithPasskey(ctx context.Context, answer []byte, origin, address string, lengths store.SessionLengths) (*store.Session, error)
	AddPasskeyQuestion(ctx context.Context, origin string, opts ...store.Option) (string, error)
	AddPasskey(ctx context.Context, answer []byte, device, origin string, opts ...store.Option) (*store.Passkey, error)
	Passkeys(ctx context.Context, user string, opts ...store.Option) ([]*store.Passkey, error)
	RemovePasskey(ctx context.Context, id string, opts ...store.Option) error
	GetUser(ctx context.Context, id string, opts ...store.Option) (*store.User, error)
	// The code for the phone (ADR-0088, item 3).
	InvitationPhoneCode(ctx context.Context, token string) (*store.PhoneCode, error)
	AccountPhoneCode(ctx context.Context, opts ...store.Option) (*store.PhoneCode, error)
	PhoneLink(ctx context.Context, link string) (*store.PhoneFor, error)
	PhoneQuestion(ctx context.Context, link, origin string) (string, error)
	UsePhoneLink(ctx context.Context, link string, answer []byte, device, origin, address string, lengths store.SessionLengths) (*store.Session, error)
	PhoneCodeState(computer string) string
	EnterByPhone(ctx context.Context, computer, address string, lengths store.SessionLengths) (*store.Session, error)
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
	// Proxies are the addresses of the proxies in front of the server
	// (ADR-0088, item 8): from a request that comes from one of them,
	// the caller's address is read in X-Forwarded-For. None, the
	// address of the connection is the caller's.
	Proxies []netip.Prefix
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
	proxies   []netip.Prefix
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
	PageInvite   = "/auth/invite"
	// The code for the phone (ADR-0088, item 3): the page its link
	// opens on the phone, what the page that shows the code asks of it,
	// and where the browser that showed it enters.
	PagePhone      = "/auth/phone"
	PagePhoneState = "/auth/phone/state"
	PagePhoneEnter = "/auth/phone/enter"
)

// phoneCookie keeps the secret of the code for the phone in the browser
// that showed it, for its 10 minutes: with it, and only with it, that
// browser enters once the phone has added its passkey.
const phoneCookie = "fado_phone"

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
	p := &Pages{accounts: a, lengths: o.Lengths, report: o.Report, signOn: o.SignOn, documents: o.Documents, unused: o.Unused, proxies: o.Proxies}
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

// Origin is the address a request reached the server at, as people
// reach it: the server's url when it was given one, and otherwise the
// address of a request made on the machine itself, or false. The door
// for agents is given it (Options.Origin).
func (p *Pages) Origin(r *http.Request) (string, bool) { return p.originOf(r) }

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
func (p *Pages) address(r *http.Request) string { return callerAddress(r, p.proxies) }

// callerAddress is the address a request came from: the address of the
// connection, or, when that is one of the proxies, the last address of
// X-Forwarded-For that is not one of them (ADR-0088, item 8). Each
// proxy adds the address it was called from at the end, so whatever
// stands left of that was written by the caller and is not believed;
// from an address that is no proxy, the header is not read at all. An
// entry that is no address stops the reading at the proxy that passed
// it on.
func callerAddress(r *http.Request, proxies []netip.Prefix) string {
	from := r.RemoteAddr
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		from = host
	}
	at, err := netip.ParseAddr(from)
	if len(proxies) == 0 || err != nil || !isProxy(at, proxies) {
		return from
	}
	var hops []string
	for _, line := range r.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(line, ",")...)
	}
	for i := len(hops) - 1; i >= 0; i-- {
		hop, ok := hopAddress(strings.TrimSpace(hops[i]))
		if !ok {
			break
		}
		at = hop
		if !isProxy(hop, proxies) {
			break
		}
	}
	return at.Unmap().WithZone("").String()
}

// isProxy reports whether an address is one of the proxies.
func isProxy(a netip.Addr, proxies []netip.Prefix) bool {
	a = a.Unmap().WithZone("")
	for _, p := range proxies {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// hopAddress reads one entry of X-Forwarded-For: an address, which some
// proxies write with its port or, for IPv6, in brackets.
func hopAddress(s string) (netip.Addr, bool) {
	if a, err := netip.ParseAddr(s); err == nil {
		return a, true
	}
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Addr(), true
	}
	if len(s) > 2 && s[0] == '[' && s[len(s)-1] == ']' {
		if a, err := netip.ParseAddr(s[1 : len(s)-1]); err == nil {
			return a, true
		}
	}
	return netip.Addr{}, false
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
	// Question is the question a form gives the browser to make or use
	// a passkey, in WebAuthn's JSON; empty, the form is not drawn.
	Question string
	// Invited is whom the invitation of the page, Token, is for; Device
	// the name of the device given; Name how the page names the person.
	Invited *store.Invited
	Token   string
	Device  string
	Name    string
	// Passkeys are the person's, Passkey the one the session began with,
	// and Admin says the person is an administrator, who is asked to
	// keep two.
	Passkeys []*store.Passkey
	Passkey  string
	Admin    bool
	// ByPassword says the session began with a password: only then does
	// the account page show the password and the second step, which go
	// with passwords (ADR-0088, step 4).
	ByPassword bool
	// Code is the picture of the code for the phone, and Wait what the
	// page does once the phone has added its passkey: "enter" signs this
	// browser in, "reload" shows the account again. Phone is whom the
	// phone's page greets.
	Code  string
	Wait  string
	Phone *store.PhoneFor
}

// ServeHTTP answers a page, or a file the pages load.
func (p *Pages) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if serveAsset(w, r) {
		return
	}
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
	origin, ok := p.originOf(r)
	if !ok {
		p.draw(out, &told, http.StatusNotFound, "notHere", &page{Title: "Not here"})
		return
	}
	out.host = strings.TrimPrefix(strings.TrimPrefix(origin, "https://"), "http://")
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
	case PageInvite:
		p.invite(out, r, &told)
	case PagePhone:
		p.phone(out, r, &told)
	case PagePhoneState:
		p.phoneState(out, r, &told)
	case PagePhoneEnter:
		p.phoneEnter(out, r, &told)
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

// passkeySaid is what a page says of a passkey that did not let the
// person in: the same words whatever was wrong, as for a password.
func passkeySaid(err error) string {
	if codeOf(err) == store.CodeSignInRefused {
		return "the passkey did not let you in: it is not one of this server's, it was removed, or the page waited too long; try again"
	}
	return said(err)
}

// setSession gives the browser the cookie of a session.
func (p *Pages) setSession(out *reply, sess *store.Session) {
	http.SetCookie(out.w, &http.Cookie{Name: SessionCookie, Value: sess.Token, Path: "/", HttpOnly: true, Secure: p.secure,
		SameSite: http.SameSiteStrictMode, MaxAge: int(sess.Ends - time.Now().Unix())})
}

// endSession takes the cookie of a session away.
func (p *Pages) endSession(out *reply) {
	http.SetCookie(out.w, &http.Cookie{Name: SessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: p.secure, SameSite: http.SameSiteStrictMode, MaxAge: -1})
}

// signInPage is the sign-in page with a passkey: its question for the
// browser, the company's sign-ons, and what went wrong.
func (p *Pages) signInPage(r *http.Request, wrong string) *page {
	d := &page{Title: "Sign in", Error: wrong, Next: next(r), Providers: p.providers()}
	if origin, ok := p.originOf(r); ok {
		if q, err := p.accounts.SignInQuestion(origin); err == nil {
			d.Question = q
		}
	}
	return d
}

func (p *Pages) signIn(out *reply, r *http.Request, told *Request) {
	if r.Method != http.MethodPost {
		if c, err := r.Cookie(SessionCookie); err == nil {
			if _, _, err := p.accounts.SessionCaller(r.Context(), c.Value); err == nil {
				redirect(out, next(r))
				return
			}
		}
		if r.FormValue("with") == "password" {
			p.draw(out, told, http.StatusOK, "signInPassword", &page{Title: "Sign in", Next: next(r), Providers: p.providers()})
			return
		}
		p.draw(out, told, http.StatusOK, "signIn", p.signInPage(r, ""))
		return
	}
	if r.FormValue("do") == "passkey" {
		origin, _ := p.originOf(r)
		sess, err := p.accounts.SignInWithPasskey(r.Context(), []byte(r.FormValue("response")), origin, p.address(r), p.lengths)
		if err != nil {
			told.Code = codeOf(err)
			p.draw(out, told, http.StatusUnauthorized, "signIn", p.signInPage(r, passkeySaid(err)))
			return
		}
		told.User = sess.User
		p.setSession(out, sess)
		redirect(out, next(r))
		return
	}
	user := strings.TrimSpace(r.FormValue("user"))
	told.User = user
	in, err := p.accounts.BeginSignIn(r.Context(), user, r.FormValue("password"), p.address(r))
	if err != nil {
		told.Code = codeOf(err)
		p.draw(out, told, http.StatusUnauthorized, "signInPassword", &page{Title: "Sign in", Error: said(err), Next: next(r), User: user, Providers: p.providers()})
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
			p.draw(out, told, http.StatusUnauthorized, "signInPassword", &page{Title: "Sign in", Error: said(err), Next: next(r)})
			return
		}
		p.draw(out, told, http.StatusOK, "setUp", &page{Title: "Set up a second step", SignIn: in.ID, Next: next(r), User: in.User, Step: step})
	}
}

// invite is the page of an invitation (ADR-0088, item 2): the person is
// greeted, names the device, and adds a passkey on it, which signs them
// in.
func (p *Pages) invite(out *reply, r *http.Request, told *Request) {
	token := r.FormValue("t")
	origin, _ := p.originOf(r)
	gone := func(err error) {
		told.Code = codeOf(err)
		p.draw(out, told, http.StatusNotFound, "inviteGone", &page{Title: "This invitation does not hold"})
	}
	who, err := p.accounts.Invited(r.Context(), token)
	if err != nil {
		gone(err)
		return
	}
	told.User = who.User
	// The page greets the person by the first word of their name.
	greet := who.Name
	if words := strings.Fields(greet); len(words) > 0 {
		greet = words[0]
	}
	shown := &page{Title: "Welcome, " + greet, Invited: who, Token: token, Next: next(r)}
	status := http.StatusOK
	if r.Method == http.MethodPost {
		sess, err := p.accounts.UseInvitation(r.Context(), token, []byte(r.FormValue("response")), r.FormValue("device"), origin, p.address(r), p.lengths)
		if err == nil {
			p.setSession(out, sess)
			redirect(out, next(r))
			return
		}
		told.Code = codeOf(err)
		shown.Error, shown.Device, status = passkeySaid(err), r.FormValue("device"), http.StatusUnprocessableEntity
		if told.Code == store.CodeSignInRefused || told.Code == store.CodeTooManyAttempts {
			status = http.StatusUnauthorized
		}
	}
	q, err := p.accounts.InvitationQuestion(r.Context(), token, origin)
	if err != nil {
		gone(err)
		return
	}
	shown.Question = q
	// On a computer the code for the phone comes first; a phone keeps
	// the passkey itself.
	if !onPhone(r) {
		if code, err := p.accounts.InvitationPhoneCode(r.Context(), token); err == nil {
			p.showCode(out, shown, origin, code, "enter")
		}
	}
	p.draw(out, told, status, "invite", shown)
}

// onPhone reports whether the browser is a phone's, by what it says of
// itself: a phone is shown no code for a phone.
func onPhone(r *http.Request) bool {
	ua := r.UserAgent()
	return strings.Contains(ua, "iPhone") || strings.Contains(ua, "iPod") || (strings.Contains(ua, "Android") && strings.Contains(ua, "Mobile"))
}

// showCode puts the code for the phone on a page, and its secret in the
// browser's cookie, for the code's 10 minutes.
func (p *Pages) showCode(out *reply, shown *page, origin string, code *store.PhoneCode, wait string) {
	c, err := qrcode.Encode(origin + PagePhone + "?t=" + code.Link)
	if err != nil {
		return
	}
	http.SetCookie(out.w, &http.Cookie{Name: phoneCookie, Value: code.Computer, Path: "/auth/", HttpOnly: true, Secure: p.secure,
		SameSite: http.SameSiteStrictMode, MaxAge: int(code.Ends - time.Now().Unix())})
	shown.Code, shown.Wait = c.SVG("A code for your phone's camera"), wait
	// This page's script asks the server how the code stands: the server
	// alone, and only a page that shows a code.
	out.w.Header().Set("Content-Security-Policy", strings.Replace(pagesPolicy("'self'"), "; form-action", "; connect-src 'self'; form-action", 1))
}

// phone is the page the code's link opens on the phone: the person adds
// a passkey there and the phone is signed in.
func (p *Pages) phone(out *reply, r *http.Request, told *Request) {
	link := r.FormValue("t")
	origin, _ := p.originOf(r)
	gone := func(err error) {
		told.Code = codeOf(err)
		p.draw(out, told, http.StatusNotFound, "phoneGone", &page{Title: "This code has ended"})
	}
	who, err := p.accounts.PhoneLink(r.Context(), link)
	if err != nil {
		gone(err)
		return
	}
	told.User = who.User
	title := "Add this phone"
	if who.Invited {
		title = "Welcome, " + who.Name
		if words := strings.Fields(who.Name); len(words) > 0 {
			title = "Welcome, " + words[0]
		}
	}
	shown := &page{Title: title, Phone: who, Token: link}
	status := http.StatusOK
	if r.Method == http.MethodPost {
		sess, err := p.accounts.UsePhoneLink(r.Context(), link, []byte(r.FormValue("response")), r.FormValue("device"), origin, p.address(r), p.lengths)
		if err == nil {
			p.setSession(out, sess)
			redirect(out, PageAccount)
			return
		}
		told.Code = codeOf(err)
		shown.Error, shown.Device, status = passkeySaid(err), r.FormValue("device"), http.StatusUnprocessableEntity
		if told.Code == store.CodeSignInRefused || told.Code == store.CodeTooManyAttempts {
			status = http.StatusUnauthorized
		}
	}
	q, err := p.accounts.PhoneQuestion(r.Context(), link, origin)
	if err != nil {
		gone(err)
		return
	}
	shown.Question = q
	p.draw(out, told, status, "phone", shown)
}

// phoneState answers the page that shows the code how the code stands,
// in a word of JSON: waiting, done or ended.
func (p *Pages) phoneState(out *reply, r *http.Request, told *Request) {
	state := store.PhoneEnded
	if c, err := r.Cookie(phoneCookie); err == nil {
		state = p.accounts.PhoneCodeState(c.Value)
	}
	out.w.Header().Set("Content-Type", "application/json")
	out.status, out.wrote = http.StatusOK, true
	out.w.WriteHeader(http.StatusOK)
	if _, err := io.WriteString(out.w, `{"state":"`+state+`"}`); err != nil && told.Err == nil {
		told.Err = err
	}
}

// phoneEnter signs in the browser that showed the code, once the phone
// has added its passkey.
func (p *Pages) phoneEnter(out *reply, r *http.Request, told *Request) {
	if r.Method != http.MethodPost {
		redirect(out, PageSignIn)
		return
	}
	var computer string
	if c, err := r.Cookie(phoneCookie); err == nil {
		computer = c.Value
	}
	http.SetCookie(out.w, &http.Cookie{Name: phoneCookie, Value: "", Path: "/auth/", HttpOnly: true, Secure: p.secure, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	sess, err := p.accounts.EnterByPhone(r.Context(), computer, p.address(r), p.lengths)
	if err != nil {
		told.Code = codeOf(err)
		d := p.signInPage(r, "this browser was not signed in by the phone: the code ended, or was used; sign in with your passkey")
		p.draw(out, told, http.StatusUnauthorized, "signIn", d)
		return
	}
	told.User = sess.User
	p.setSession(out, sess)
	redirect(out, PageAccount)
}

func (p *Pages) code(out *reply, r *http.Request, told *Request) {
	if r.Method != http.MethodPost {
		redirect(out, PageSignIn)
		return
	}
	p.finish(out, r, told, r.FormValue("signin"), r.FormValue("code"))
}

// finish gives the session of a sign-in with a password and sends the
// person on: to the codes for a lost phone when the second step was just
// set up.
func (p *Pages) finish(out *reply, r *http.Request, told *Request, signIn, code string) {
	sess, err := p.accounts.FinishSignIn(r.Context(), signIn, code, p.address(r), p.lengths)
	if err != nil {
		told.Code = codeOf(err)
		p.draw(out, told, http.StatusUnauthorized, "signInPassword", &page{Title: "Sign in", Error: said(err), Next: next(r)})
		return
	}
	told.User = sess.User
	p.setSession(out, sess)
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
		p.endSession(out)
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
	origin, _ := p.originOf(r)
	shown := &page{Title: "Your account", User: who.ID, Name: who.ID, Current: sess.ID, Passkey: sess.Passkey,
		ByPassword: sess.Way == store.WayPassword || sess.Way == store.WaySecondStep || sess.Way == store.WayLink}
	for _, g := range who.System {
		shown.Admin = shown.Admin || g == store.GroupAdministrators
	}
	if u, err := p.accounts.GetUser(r.Context(), who.ID, as); err == nil && u.Name != "" {
		shown.Name = u.Name
	}
	status := http.StatusOK
	if r.Method == http.MethodPost {
		var err error
		switch r.FormValue("do") {
		case "add-passkey":
			var k *store.Passkey
			if k, err = p.accounts.AddPasskey(r.Context(), []byte(r.FormValue("response")), r.FormValue("device"), origin, as); err == nil {
				shown.Note = "The passkey on " + k.Name + " is added."
			} else {
				shown.Device = r.FormValue("device")
			}
		case "remove-passkey":
			id := r.FormValue("passkey")
			if err = p.accounts.RemovePasskey(r.Context(), id, as); err == nil {
				if id == sess.Passkey {
					p.endSession(out)
					redirect(out, PageSignIn)
					return
				}
				shown.Note = "The passkey is removed, and the sessions it began are ended."
			}
		case "password":
			if r.FormValue("password") != r.FormValue("again") {
				err = &store.Error{Code: store.CodeBadValue, Message: "the two new passwords differ"}
			} else if err = p.accounts.ChangePassword(r.Context(), r.FormValue("old"), r.FormValue("password"), p.address(r), as); err == nil {
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
			if codes, err = p.accounts.ConfirmMySecondStep(r.Context(), r.FormValue("code"), p.address(r), as); err == nil {
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
					p.endSession(out)
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
	if shown.Passkeys, err = p.accounts.Passkeys(r.Context(), "", as); err == nil {
		if shown.Has, shown.Needs, err = p.accounts.HasSecondStep(r.Context(), "", as); err == nil {
			if shown.List, err = p.accounts.Sessions(r.Context(), "", as); err == nil {
				shown.Grants, err = p.accounts.Grants(r.Context(), "", as)
			}
		}
	}
	if err != nil {
		told.Code = codeOf(err)
		shown.Error = said(err)
		status = http.StatusInternalServerError
	}
	if q, err := p.accounts.AddPasskeyQuestion(r.Context(), origin, as); err == nil {
		shown.Question = q
		if !onPhone(r) {
			if code, err := p.accounts.AccountPhoneCode(r.Context(), as); err == nil {
				p.showCode(out, shown, origin, code, "reload")
			}
		}
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
		err = p.accounts.UsePasswordLink(r.Context(), link, r.FormValue("password"), p.address(r))
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
		p.draw(out, told, http.StatusBadGateway, "signIn", p.signInPage(r, "the sign-on did not start; try again, or sign in with your passkey"))
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
		p.draw(out, told, http.StatusUnauthorized, "signIn", p.signInPage(r, "the sign-on was not completed; try again, or ask an administrator of the database"))
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
	sess, err := p.accounts.SignOn(r.Context(), *who, p.address(r), p.lengths)
	if err != nil {
		told.Code = codeOf(err)
		p.draw(out, told, http.StatusUnauthorized, "signIn", p.signInPage(r, said(err)))
		return
	}
	told.User = sess.User
	p.setSession(out, sess)
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
	if _, err := io.WriteString(out.w, drawn(name, out.host, data)); err != nil && told.Err == nil {
		told.Err = err
	}
}

// pagesPolicy is the Content-Security-Policy a page of the server is
// sent with: it loads the server's own style, script, typefaces and
// pictures (package web), nothing from elsewhere, and runs and styles
// nothing written in the page; and its form goes where forms says, the
// server itself and, for the one page that sends a person on to a
// client, that client.
func pagesPolicy(forms string) string {
	return "default-src 'none'; script-src 'self'; style-src 'self'; font-src 'self'; img-src 'self'; form-action " + forms + "; frame-ancestors 'none'; base-uri 'none'"
}

// The pages are written by hand, each value escaped, and not with
// html/template: a template calls methods by their names, which keeps
// every method of the program in it, two megabytes more of the server.

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
	b.WriteString(`<label for="code">Code<input id="code" name="code" autocomplete="one-time-code" inputmode="numeric" required autofocus></label>`)
}

// stepShown is the key of a second step as a page shows it.
func stepShown(b *strings.Builder, step *store.SecondStep) {
	b.WriteString("<p><code>" + esc(step.Secret) + "</code></p>\n<p class=\"small\">Or open this link on the phone: <code>" + esc(step.Link) + "</code></p>\n")
}

// passkeyForm is a form that makes a passkey (make) or uses one, with the
// server's question for the browser; the script hands it on and sends
// the answer back in response.
func passkeyForm(b *strings.Builder, action, kind, question string) {
	b.WriteString(`<form method="post" action="` + action + `" data-passkey="` + kind + `" data-question="` + esc(question) + `">`)
	hidden(b, "response", "")
}

// codeShown is the code for the phone with what to do, where the script
// says how the code stands, and the form that signs this browser in once
// the phone has added its passkey.
func codeShown(b *strings.Builder, d *page, say string) {
	b.WriteString(`<figure class="code" data-phone="` + esc(d.Wait) + `">` + d.Code + `<figcaption>` + esc(say) + `</figcaption>`)
	b.WriteString(`<p class="state" data-phone-said role="status" aria-live="polite"></p></figure>` + "\n")
	if d.Wait == "enter" {
		b.WriteString(`<form method="post" action="` + PagePhoneEnter + `" data-phone-enter hidden></form>` + "\n")
	}
}

// saidHere writes where the script says what went wrong, under a button.
func saidHere(b *strings.Builder) {
	b.WriteString(`<p class="said" data-said role="status" aria-live="polite"></p>`)
}

// deviceField is the field that names the device a passkey is made on,
// which the script proposes from the browser.
func deviceField(b *strings.Builder, person, given string) {
	b.WriteString(`<label for="device">Name of this device<input id="device" name="device" maxlength="60" required autocomplete="off" data-device="` + esc(person) + `" value="` + esc(given) + `"></label>`)
}

// drawn is a page of the server: the frame, which names the server by
// its address under FadoDB's mark, and what the page holds.
func drawn(name, host string, d *page) string {
	var b strings.Builder
	class := ""
	if name == "account" {
		class = ` class="wide"`
	}
	b.WriteString(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="light dark">
<title>` + esc(d.Title) + ` · FadoDB</title>
<link rel="icon" type="image/png" href="` + web.At("mark.png") + `">
` + refresh(d.Refresh) + `<link rel="stylesheet" href="` + web.At("pages.css") + `">
<script src="` + web.At("pages.js") + `" defer></script></head><body><main` + class + `>
<p class="brand"><img src="` + web.At("mark.png") + `" alt="FadoDB" width="44" height="44"></p>
`)
	if host != "" {
		b.WriteString(`<p class="kicker">` + esc(host) + "</p>\n")
	}
	b.WriteString("<h1>" + esc(d.Title) + "</h1>\n")
	// What the page says of itself comes first; what went wrong, after.
	switch name {
	case "signIn":
		b.WriteString("<p class=\"lead\">With a passkey on this device: your fingerprint, your face or its PIN. No password.</p>\n")
	case "signInPassword":
		b.WriteString("<p class=\"lead\">With your user and your password.</p>\n")
	case "invite":
		by := "You are invited."
		if d.Invited.By != "" {
			by = esc(d.Invited.ByName) + " invited you."
		}
		b.WriteString("<p class=\"lead\">" + by + " Add a passkey to sign in: your fingerprint, your face or this device's PIN. No password.</p>\n")
	case "phone":
		if d.Phone.Invited {
			b.WriteString("<p class=\"lead\">Add a passkey on this phone to sign in: your face, your fingerprint or its PIN. No password.</p>\n")
		} else {
			b.WriteString("<p class=\"lead\">Add a passkey of yours on this phone: your face, your fingerprint or its PIN.</p>\n")
		}
	case "account":
		b.WriteString("<p class=\"lead\">You are signed in as <strong>" + esc(d.User) + "</strong>.</p>\n")
		b.WriteString(`<div class="pair"><form method="post" action="` + PageSignOut + `"><button class="btn ghost small" type="submit">Sign out</button></form></div>` + "\n")
	}
	if d.Error != "" {
		b.WriteString(`<p class="error" role="alert">` + esc(d.Error) + "</p>\n")
	}
	if d.Note != "" {
		b.WriteString(`<p class="note">` + esc(d.Note) + "</p>\n")
	}
	switch name {
	case "signIn":
		if d.Question != "" {
			passkeyForm(&b, PageSignIn, "use", d.Question)
			hidden(&b, "do", "passkey")
			hidden(&b, "next", d.Next)
			b.WriteString(`<button class="btn main" type="submit">Sign in with a passkey</button>`)
			saidHere(&b)
			b.WriteString("</form>\n")
		}
		providersOf(&b, d)
		b.WriteString("<p class=\"small\">No passkey yet? Ask an administrator of this server for an invitation, and open it on this device.</p>\n")
		b.WriteString(`<p class="small"><a class="lnk" href="` + PageSignIn + "?with=password&amp;next=" + esc(url.QueryEscape(d.Next)) + `">Sign in with a password</a></p>`)
	case "signInPassword":
		b.WriteString(`<form method="post" action="` + PageSignIn + `">`)
		hidden(&b, "next", d.Next)
		b.WriteString(`<label for="user">User<input id="user" name="user" autocomplete="username" value="` + esc(d.User) + `" required autofocus></label>
<label for="password">Password<input id="password" name="password" type="password" autocomplete="current-password" required></label>
<button class="btn main" type="submit">Sign in</button></form>
`)
		providersOf(&b, d)
		b.WriteString(`<p class="small"><a class="lnk" href="` + PageSignIn + "?next=" + esc(url.QueryEscape(d.Next)) + `">Sign in with a passkey</a></p>`)
	case "invite":
		if d.Code != "" {
			codeShown(&b, d, "Point your phone's camera at the code. Your phone keeps the key.")
			b.WriteString("<p class=\"or\">or</p>\n")
		}
		passkeyForm(&b, PageInvite, "make", d.Question)
		hidden(&b, "t", d.Token)
		hidden(&b, "next", d.Next)
		deviceField(&b, d.Invited.Name, d.Device)
		if d.Code != "" {
			b.WriteString(`<button class="btn ghost" type="submit">Use this computer instead</button>`)
		} else {
			b.WriteString(`<button class="btn main" type="submit">Add a passkey and enter</button>`)
		}
		saidHere(&b)
		b.WriteString("</form>\n<p class=\"small\">The passkey stays on the device that makes it, and opens this server alone.</p>")
	case "phone":
		passkeyForm(&b, PagePhone, "make", d.Question)
		hidden(&b, "t", d.Token)
		deviceField(&b, d.Phone.Name, d.Device)
		b.WriteString(`<button class="btn main" type="submit">Add a passkey and enter</button>`)
		saidHere(&b)
		b.WriteString("</form>\n<p class=\"small\">The passkey stays on this phone, and opens this server alone.</p>")
	case "phoneGone":
		b.WriteString("<p class=\"lead\">It was used, or its 10 minutes ended. Load the page on your computer again for a new code.</p>")
	case "inviteGone":
		b.WriteString("<p class=\"lead\">It was used, it ended, or it is not one. Ask the person who invited you for a new one.</p>")
	case "code":
		b.WriteString("<p class=\"lead\">Enter the six digits your authenticator app shows for " + esc(d.User) + ", or one of your codes for a lost phone.</p>\n")
		b.WriteString(`<form method="post" action="` + PageCode + `">`)
		hidden(&b, "signin", d.SignIn)
		hidden(&b, "next", d.Next)
		codeField(&b)
		b.WriteString(`<button class="btn main" type="submit">Go on</button></form>`)
	case "setUp":
		b.WriteString("<p class=\"lead\">" + esc(d.User) + ", your groups ask for a second step. Add this key to an authenticator app on your phone, then enter the six digits it shows.</p>\n")
		stepShown(&b, d.Step)
		b.WriteString(`<form method="post" action="` + PageCode + `">`)
		hidden(&b, "signin", d.SignIn)
		hidden(&b, "next", d.Next)
		codeField(&b)
		b.WriteString(`<button class="btn main" type="submit">Go on</button></form>`)
	case "setUpMine":
		b.WriteString("<p class=\"lead\">Add this key to an authenticator app on your phone, then enter the six digits it shows.</p>\n")
		stepShown(&b, d.Step)
		b.WriteString(`<form method="post" action="` + PageAccount + `">`)
		hidden(&b, "do", "confirm")
		codeField(&b)
		b.WriteString(`<button class="btn main" type="submit">Keep it</button></form>`)
	case "codes":
		b.WriteString("<p class=\"lead\">Write these down and keep them away from your phone. Each lets you in once if the phone is lost. They are shown this once.</p>\n<ol class=\"codes\">")
		for _, c := range d.Codes {
			b.WriteString("<li>" + esc(c) + "</li>")
		}
		b.WriteString("</ol>\n<p><a class=\"btn main\" href=\"" + esc(d.Next) + "\">Go on</a></p>")
	case "account":
		accountShown(&b, d)
	case "setPassword":
		b.WriteString("<p class=\"lead\">Choose the password of <strong>" + esc(d.User) + "</strong>: at least 12 characters. A sentence of four or five words is long enough and easy to remember.</p>\n")
		b.WriteString(`<form method="post" action="` + PagePassword + `">`)
		hidden(&b, "link", d.Link)
		b.WriteString(`<input type="text" name="user" value="` + esc(d.User) + `" autocomplete="username" hidden>
<label for="password">Password<input id="password" name="password" type="password" autocomplete="new-password" minlength="12" required autofocus></label>
<label for="again">The password again<input id="again" name="again" type="password" autocomplete="new-password" minlength="12" required></label>
<button class="btn main" type="submit">Set it</button></form>`)
	case "signedOn":
		b.WriteString("<p class=\"lead\">You are signed in as <strong>" + esc(d.User) + "</strong>.</p>\n<p><a class=\"btn main\" href=\"" + esc(d.Next) + "\">Go on</a></p>")
	case "passwordSet":
		b.WriteString(`<p class="note">The password of ` + esc(d.User) + ` is set.</p>` + "\n<p><a class=\"btn main\" href=\"" + PageSignIn + "?with=password\">Sign in</a></p>")
	case "onward":
		b.WriteString("<p><a class=\"btn main\" href=\"" + esc(d.Next) + "\">Go on</a></p>")
	case "consent":
		consentShown(&b, d)
	case "requestGone":
		b.WriteString("<p class=\"lead\">The request of the client ended, was answered, or is not one. Go back to the client and connect again.</p>")
	case "notAllowed":
		b.WriteString("<p class=\"lead\">Nothing was sent to the client. If you followed a link to get here, the link may not be what it said.</p>")
	case "linkGone":
		b.WriteString("<p class=\"lead\">This link does not hold: it was used, it ended, or it is not one. Ask an administrator of the database for a new one.</p>")
	default:
		b.WriteString("<p class=\"lead\">There is no page here. A server that is reached from other machines signs people in only when its operator gives it its address, url, in its settings.</p>")
	}
	b.WriteString("\n</main></body></html>\n")
	return b.String()
}

// providersOf writes the buttons of the company's sign-ons, below the
// page's own way in.
func providersOf(b *strings.Builder, d *page) {
	if len(d.Providers) == 0 {
		return
	}
	b.WriteString("<p class=\"or\">or</p>\n")
	for _, pr := range d.Providers {
		b.WriteString(`<form method="post" action="` + PageSignOn + `">`)
		hidden(b, "provider", pr.Name)
		hidden(b, "next", d.Next)
		b.WriteString(`<button class="btn ghost" type="submit">` + esc(pr.Label) + "</button></form>\n")
	}
}

// accountShown is what the account page holds: the person's passkeys,
// and the way to add one on this device; the password and the second
// step, to a person who signed in with a password, until passwords go;
// the sessions; and the clients allowed. A person who signed in with a
// passkey or the company's sign-on is asked for no password.
func accountShown(b *strings.Builder, d *page) {
	b.WriteString("<h2>Passkeys</h2>\n")
	if d.Admin && len(d.Passkeys) < 2 {
		b.WriteString("<p class=\"small warn left\">An administrator is asked to keep two passkeys, on two devices, so that losing one locks nobody out. Add one on another device.</p>\n")
	}
	if len(d.Passkeys) == 0 {
		b.WriteString("<p class=\"small left\">None yet. A passkey is your fingerprint, your face or this device's PIN: nothing to type, and nothing a fake page can use.</p>\n")
	} else {
		b.WriteString(`<div class="rows">`)
		for _, k := range d.Passkeys {
			b.WriteString(`<div class="row"><div class="what"><span>` + esc(k.Name))
			if k.ID == d.Passkey {
				b.WriteString(" <strong>(this session)</strong>")
			}
			b.WriteString(`</span><span class="when">added ` + esc(moment(k.AddedAt)))
			if k.UsedAt != 0 {
				b.WriteString(", last used " + esc(moment(k.UsedAt)))
			}
			if k.Synced {
				b.WriteString(", synced")
			}
			b.WriteString(`</span></div><form method="post" action="` + PageAccount + `">`)
			hidden(b, "do", "remove-passkey")
			hidden(b, "passkey", k.ID)
			b.WriteString(`<button class="btn ghost small" type="submit">Remove</button></form></div>`)
		}
		b.WriteString("</div>\n")
	}
	if d.Question != "" {
		if d.Code != "" {
			codeShown(b, d, "To add a passkey on your phone, point its camera at the code.")
			b.WriteString("<p class=\"or\">or</p>\n")
		}
		passkeyForm(b, PageAccount, "make", d.Question)
		hidden(b, "do", "add-passkey")
		deviceField(b, d.Name, d.Device)
		b.WriteString(`<button class="btn main" type="submit">Add a passkey on this device</button>`)
		saidHere(b)
		b.WriteString("</form>\n")
	}
	if d.ByPassword {
		passwordShown(b, d)
	}
	b.WriteString("\n<h2>Sessions</h2>\n<div class=\"rows\">")
	for _, s := range d.List {
		b.WriteString(`<div class="row"><div class="what"><span>` + esc(moment(s.CreatedAt)))
		if s.Address != "" {
			b.WriteString(", from " + esc(s.Address))
		}
		if s.ID == d.Current {
			b.WriteString(" <strong>(this one)</strong>")
		}
		b.WriteString(`</span><span class="when">last used ` + esc(moment(s.UsedAt)) + `</span></div><form method="post" action="` + PageAccount + `">`)
		hidden(b, "do", "end")
		hidden(b, "session", s.ID)
		b.WriteString(`<button class="btn ghost small" type="submit">End</button></form></div>`)
	}
	b.WriteString("</div>\n<h2>Clients you allowed</h2>\n")
	if len(d.Grants) == 0 {
		b.WriteString("<p class=\"small left\">None. A client you allow, such as Claude, is listed here, and you end it here.</p>")
		return
	}
	b.WriteString(`<div class="rows">`)
	for _, g := range d.Grants {
		b.WriteString(`<div class="row"><div class="what"><span><strong>` + esc(g.ClientName) + "</strong>")
		if g.Agent != "" {
			b.WriteString(", acting for you as " + esc(g.Agent))
		}
		b.WriteString(`</span><span class="when">allowed ` + esc(moment(g.CreatedAt)) + ", last renewed " + esc(moment(g.UsedAt)) + `</span></div><form method="post" action="` + PageAccount + `">`)
		hidden(b, "do", "end-grant")
		hidden(b, "grant", g.ID)
		b.WriteString(`<button class="btn ghost small" type="submit">End</button></form></div>`)
	}
	b.WriteString("</div>")
}

// passwordShown is the part of the account page that goes with a
// password: changing it, and the second step.
func passwordShown(b *strings.Builder, d *page) {
	b.WriteString("<h2>Password</h2>\n" + `<form method="post" action="` + PageAccount + `">`)
	hidden(b, "do", "password")
	b.WriteString(`<input type="text" name="user" value="` + esc(d.User) + `" autocomplete="username" hidden>
<label for="old">Your password<input id="old" name="old" type="password" autocomplete="current-password" required></label>
<label for="password">A new password, at least 12 characters<input id="password" name="password" type="password" autocomplete="new-password" minlength="12" required></label>
<label for="again">The new password again<input id="again" name="again" type="password" autocomplete="new-password" minlength="12" required></label>
<button class="btn ghost" type="submit">Change it</button></form>
<h2>Second step</h2>
`)
	switch {
	case d.Has:
		b.WriteString("<p class=\"small left\">A code from your authenticator app is asked after your password.</p>\n")
		if !d.Needs {
			b.WriteString(`<form method="post" action="` + PageAccount + `">`)
			hidden(b, "do", "clear")
			b.WriteString(`<button class="btn ghost" type="submit">Take it away</button></form>`)
		}
	case d.Needs:
		b.WriteString("<p class=\"small left\">Your groups ask for one, and you set it up at your next sign-in with your password.</p>\n")
	default:
		b.WriteString("<p class=\"small left\">You have none. A code from an app on your phone is asked after your password, when you sign in with one.</p>\n")
	}
	b.WriteString(`<form method="post" action="` + PageAccount + `">`)
	hidden(b, "do", "set-up")
	if d.Has {
		b.WriteString(`<button class="btn ghost" type="submit">Set up a new one</button></form>`)
	} else {
		b.WriteString(`<button class="btn ghost" type="submit">Set one up</button></form>`)
	}
}

// consentShown is the page that asks a person to allow a client.
func consentShown(b *strings.Builder, d *page) {
	a := d.Asked
	host := a.Client.Host()
	b.WriteString(`<div class="lead">`)
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
	b.WriteString(`</div><div class="pair"><form method="post" action="` + PageAllow + `">`)
	hidden(b, "request", d.Request)
	hidden(b, "do", "allow")
	b.WriteString(`<button class="btn" type="submit">Allow</button></form><form method="post" action="` + PageAllow + `">`)
	hidden(b, "request", d.Request)
	hidden(b, "do", "deny")
	b.WriteString(`<button class="btn ghost" type="submit">Deny</button></form></div>`)
}
