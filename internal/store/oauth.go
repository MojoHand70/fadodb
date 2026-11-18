package store

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MojoHand70/fadodb/internal/model"
)

// Passes for clients (ADR-0071, items 6 and 7). The server is the
// authority that gives passes for its own database, as OAuth 2.1 has it:
// a client sends a person to the server, the person signs in and allows
// it, and the client is given a code, which it exchanges, with the proof
// of PKCE, for a pass of one hour and a pass that renews it. A pass is
// for one address: the commands at /v1, which a face or an app calls as
// the person, or the door for agents at /mcp, which an agent calls as an
// agent of its own acting for the person, with the person's groups.
//
// The store keeps the clients an administrator added, the grants a
// person allowed, and the passes, each as the hash of its secret, as a
// key is. It fetches nothing: a client known by the document it
// publishes is read by the server, which hands the store what it read.

// The addresses a pass is for.
const (
	// AddressCommands is the commands, which a face or an app calls as
	// the person who allowed it.
	AddressCommands = "/v1"
	// AddressAgents is the door for agents, which an agent calls as an
	// agent of its own acting for the person who allowed it.
	AddressAgents = "/mcp"
)

// Why a grant ended, as the database keeps it.
const (
	// GrantEnded: the person, or an administrator, ended it.
	GrantEnded = "ended"
	// GrantRevoked: the client gave its pass back.
	GrantRevoked = "revoked"
	// GrantUsedTwice: a renewing pass, or a code, was used a second
	// time, which only a thief does: every pass of the grant ends.
	GrantUsedTwice = "used_twice"
	// GrantUnused: no pass was renewed for as long as the
	// grant holds unused, 30 days unless the server was told less.
	GrantUnused = "unused"
	// GrantClientRemoved: an administrator removed the client.
	GrantClientRemoved = "client_removed"
	// GrantWithdrawn: the substitution that lets the agent act for
	// the person was withdrawn.
	GrantWithdrawn = "substitution_withdrawn"
)

// The kinds of change to a user that connecting an agent adds: the user
// of the agent was made for the person.
const AgentConnected = "agent_connected"

// PassPrefix begins a pass for an address, as fado_ begins a key: the
// door knows a pass from a key by it.
const PassPrefix = "fadoat_"

// The secrets of the passes: a pass for an address, a renewing pass and
// a code, each an id and a secret of which the database keeps a hash.
const (
	renewPrefix = "fadort_"
	codePrefix  = "fadoac_"
	// passLasts is how long a pass for an address holds.
	passLasts = time.Hour
	// codeLasts is how long a code waits for its exchange, and askLasts
	// how long a client's request waits for the person to allow it, who
	// may have to sign in first.
	codeLasts = 5 * time.Minute
	askLasts  = 15 * time.Minute
	// maxWaiting bounds the requests and the codes held in memory.
	maxWaiting = 10_000
)

// DefaultGrantUnused is how long a grant holds with no pass
// renewed (ADR-0071, item 6).
const DefaultGrantUnused = 30 * 24 * time.Hour

// Client is a client the server gives passes to: one an administrator
// added, known by its id, or one known by the document it publishes,
// whose id is the address of the document. A client is public: it holds
// no secret, and proves each code with PKCE.
type Client struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// RedirectURIs are where the client is sent its code: an address
	// over HTTPS, or over HTTP on the machine the client runs on, whose
	// port may then be any.
	RedirectURIs []string `json:"redirect_uris"`
	// Address is the one address the client is given passes for:
	// AddressCommands or AddressAgents.
	Address   string `json:"address"`
	AddedAt   int64  `json:"added_at,omitempty"`
	AddedBy   string `json:"added_by,omitempty"`
	RemovedAt int64  `json:"removed_at,omitempty"`
	RemovedBy string `json:"removed_by,omitempty"`
}

// Document reports whether the client is known by the document it
// publishes: its id is an address over HTTPS.
func (c *Client) Document() bool { return strings.HasPrefix(c.ID, "https://") }

// Host is what names the client to a person: the host of its document,
// or the id an administrator gave it.
func (c *Client) Host() string {
	if c.Document() {
		if u, err := url.Parse(c.ID); err == nil {
			return strings.ToLower(u.Hostname())
		}
	}
	return c.ID
}

// AgentOf is the id of the agent a person connects through a client: the
// person's id, a ~ and the client's host, as ann~claude.ai (ADR-0071,
// item 7). Nobody else is given an id with a ~.
func AgentOf(person string, c *Client) string { return person + "~" + c.Host() }

// Asked is what a client asks the server for: a code for a person, sent
// to one of its addresses, proven by PKCE, for one address of the
// server. The server checks the client and the address it is sent to
// before it asks the person, and the store checks them again.
type Asked struct {
	Client      Client
	RedirectURI string
	// Challenge is the PKCE challenge, S256 of the client's verifier.
	Challenge string
	// State is the client's own, sent back with the code.
	State string
	// Resource is the address the pass is for, as the client named it:
	// the server's address and the client's Address.
	Resource string
}

// CodeExchange is what a client sends to exchange its code for passes.
type CodeExchange struct {
	Client      string
	Code        string
	RedirectURI string
	Verifier    string
	// Resource, when given, is the address the pass is for, the one the
	// code was asked for.
	Resource string
	// Unused is how long the grant holds with no pass renewed; zero
	// or longer than the usual is the usual.
	Unused time.Duration
}

// Passes are what a client is given: a pass for an address and a pass
// that renews it, with how many seconds the first holds. Grant is
// the grant they belong to.
type Passes struct {
	Pass  string
	Renew string
	Lasts int64
	Grant string
}

// Grant is a client a person allowed, as the database lists it:
// whose, which client, for which address, and for an agent, the user it
// acts as and the substitution that lets it act for the person.
type Grant struct {
	ID           string `json:"id"`
	User         string `json:"user"`
	Agent        string `json:"agent,omitempty"`
	Client       string `json:"client"`
	ClientName   string `json:"client_name"`
	Address      string `json:"address"`
	Substitution string `json:"substitution,omitempty"`
	CreatedAt    int64  `json:"created_at"`
	UsedAt       int64  `json:"used_at"`
	// Ends is the moment the grant ends if no pass is renewed by
	// then.
	Ends    int64  `json:"ends"`
	EndedAt int64  `json:"ended_at,omitempty"`
	EndedBy string `json:"ended_by,omitempty"`
	Why     string `json:"why,omitempty"`

	resource string
	idle     int64
}

// ErrNoGrant is what the exchange and the renewal of a pass answer for
// every request they do not let through, whatever was wrong with it, so
// that a guess learns nothing. The server answers it with OAuth's
// invalid_grant.
var ErrNoGrant = errors.New("the code or the pass is not one the database lets through")

// oauthDDL makes the tables of the passes, beside the users'.
var oauthDDL = []string{
	`CREATE TABLE IF NOT EXISTS fado_clients (id TEXT PRIMARY KEY, name TEXT NOT NULL, redirect_uris TEXT NOT NULL, address TEXT NOT NULL, added_at INTEGER NOT NULL, added_by TEXT NOT NULL, removed_at INTEGER NOT NULL, removed_by TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS fado_grants (id TEXT PRIMARY KEY, user TEXT NOT NULL, agent TEXT NOT NULL, client TEXT NOT NULL, client_name TEXT NOT NULL, address TEXT NOT NULL, resource TEXT NOT NULL, substitution TEXT NOT NULL, created_at INTEGER NOT NULL, used_at INTEGER NOT NULL, idle INTEGER NOT NULL, ended_at INTEGER NOT NULL, ended_by TEXT NOT NULL, why TEXT NOT NULL)`,
	`CREATE INDEX IF NOT EXISTS fado_grants_user ON fado_grants (user)`,
	`CREATE TABLE IF NOT EXISTS fado_passes (id TEXT PRIMARY KEY, grant_id TEXT NOT NULL, kind TEXT NOT NULL, hash BLOB NOT NULL, made_at INTEGER NOT NULL, ends INTEGER NOT NULL, used_at INTEGER NOT NULL)`,
	`CREATE INDEX IF NOT EXISTS fado_passes_grant ON fado_passes (grant_id)`,
}

// The kinds of pass the table keeps.
const (
	passAccess = "pass"
	passRenew  = "renew"
)

// passing is what the store holds of the passes, under its own lock:
// the clients an administrator added, the grants that stand, the
// passes for an address that may still hold, and the requests and codes
// under way.
type passing struct {
	mu      sync.Mutex
	clients map[string]*Client
	granted map[string]*Grant
	passes  map[string]*heldPass
	asked   map[string]*askedFor
	codes   map[string]*heldCode
}

type heldPass struct {
	gr   string
	hash [sha256.Size]byte
	ends int64
}

type askedFor struct {
	Asked
	ends int64
}

type heldCode struct {
	Asked
	hash [sha256.Size]byte
	user string
	ends int64
	// gr is the grant the code was exchanged for; a code shown a
	// second time ends it.
	gr string
}

func (s *Store) passing() *passing {
	g := &s.auth
	g.mu.Lock()
	if g.asked == nil {
		g.asked = map[string]*askedFor{}
		g.codes = map[string]*heldCode{}
		if g.clients == nil {
			g.clients = map[string]*Client{}
			g.granted = map[string]*Grant{}
			g.passes = map[string]*heldPass{}
		}
	}
	g.mu.Unlock()
	return g
}

// loadPasses reads the clients, the grants that stand and the
// passes for an address that may still hold. Open calls it for a
// database that keeps its users.
func (s *Store) loadPasses(ctx context.Context) error {
	now := s.now().Unix()
	clients := map[string]*Client{}
	rows, err := s.writer.QueryContext(ctx, "SELECT id, name, redirect_uris, address, added_at, added_by, removed_at, removed_by FROM fado_clients")
	if err != nil {
		return storageErr("read the clients", err)
	}
	for rows.Next() {
		c := &Client{}
		var uris string
		if err := rows.Scan(&c.ID, &c.Name, &uris, &c.Address, &c.AddedAt, &c.AddedBy, &c.RemovedAt, &c.RemovedBy); err != nil {
			rows.Close()
			return storageErr("read a client", err)
		}
		if err := json.Unmarshal([]byte(uris), &c.RedirectURIs); err != nil {
			rows.Close()
			return &Error{Code: CodeStorage, Message: "the addresses of the client " + c.ID + " are not a list: " + err.Error()}
		}
		clients[c.ID] = c
	}
	if err := rows.Close(); err != nil {
		return storageErr("read the clients", err)
	}
	granted := map[string]*Grant{}
	rows, err = s.writer.QueryContext(ctx, "SELECT id, user, agent, client, client_name, address, resource, substitution, created_at, used_at, idle FROM fado_grants WHERE ended_at = 0")
	if err != nil {
		return storageErr("read the grants", err)
	}
	for rows.Next() {
		c := &Grant{}
		if err := rows.Scan(&c.ID, &c.User, &c.Agent, &c.Client, &c.ClientName, &c.Address, &c.resource, &c.Substitution, &c.CreatedAt, &c.UsedAt, &c.idle); err != nil {
			rows.Close()
			return storageErr("read a grant", err)
		}
		c.Ends = c.UsedAt + c.idle
		granted[c.ID] = c
	}
	if err := rows.Close(); err != nil {
		return storageErr("read the grants", err)
	}
	passes := map[string]*heldPass{}
	rows, err = s.writer.QueryContext(ctx, "SELECT id, grant_id, hash, ends FROM fado_passes WHERE kind = ? AND ends > ?", passAccess, now)
	if err != nil {
		return storageErr("read the passes", err)
	}
	for rows.Next() {
		var id string
		var hash []byte
		p := &heldPass{}
		if err := rows.Scan(&id, &p.gr, &hash, &p.ends); err != nil {
			rows.Close()
			return storageErr("read a pass", err)
		}
		if granted[p.gr] != nil {
			copy(p.hash[:], hash)
			passes[id] = p
		}
	}
	if err := rows.Close(); err != nil {
		return storageErr("read the passes", err)
	}
	g := &s.auth
	g.mu.Lock()
	g.clients, g.granted, g.passes = clients, granted, passes
	g.mu.Unlock()
	return nil
}

// RedirectAllowed reports whether a client may be sent its code at an
// address: one of its addresses, as it is written, or, for an address
// on the machine the client runs on, the same with any port (RFC 8252,
// section 7.3).
func RedirectAllowed(c *Client, uri string) bool {
	for _, kept := range c.RedirectURIs {
		if kept == uri {
			return true
		}
	}
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "http" || !loopback(u.Hostname()) {
		return false
	}
	for _, kept := range c.RedirectURIs {
		k, err := url.Parse(kept)
		if err == nil && k.Scheme == "http" && k.Hostname() == u.Hostname() && k.Path == u.Path && k.RawQuery == u.RawQuery {
			return true
		}
	}
	return false
}

// loopback reports whether a host is the machine itself.
func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// checkRedirect refuses an address a code cannot be sent to: one over
// HTTPS, or over HTTP on the machine the client runs on, absolute, with
// no fragment and no user.
func checkRedirect(uri string) *Error {
	u, err := url.Parse(uri)
	ok := err == nil && u.Host != "" && u.User == nil && u.Fragment == "" && !strings.Contains(uri, "#") &&
		(u.Scheme == "https" || u.Scheme == "http" && loopback(u.Hostname()))
	if !ok || controlIn(uri) >= 0 || len(uri) > 2000 {
		return &Error{Code: CodeBadValue, Path: "/redirect_uris", Message: strconv.Quote(uri) + " is not an address a code is sent to",
			Hint: "write an address over https, or over http on the machine the client runs on, such as http://127.0.0.1/callback, with no fragment"}
	}
	return nil
}

// checkClient checks a client as an administrator adds it.
func checkClient(c *Client) *Error {
	switch {
	case c.ID == "":
		return &Error{Code: CodeRequired, Path: "/id", Message: "a client needs an id"}
	case len(c.ID) > 64 || strings.IndexFunc(c.ID, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.')
	}) >= 0:
		return &Error{Code: CodeBadValue, Path: "/id", Message: "the id " + strconv.Quote(c.ID) + " is not one a client is given",
			Hint: "an id is 1 to 64 letters, digits, dots, dashes and underscores, such as fadoface; a client known by its document is never added"}
	case strings.TrimSpace(c.Name) == "" || len(c.Name) > 100 || controlIn(c.Name) >= 0:
		return &Error{Code: CodeRequired, Path: "/name", Message: "a client needs a name of at most 100 characters, which the page that asks the person shows",
			Hint: "the name people know it by, such as FadoFace"}
	case c.Address != AddressCommands && c.Address != AddressAgents:
		return &Error{Code: CodeBadValue, Path: "/address", Message: "the address of a client is " + AddressCommands + ", the commands it calls as the person, or " + AddressAgents + ", the door for agents, got " + strconv.Quote(c.Address),
			Hint: "a face or an app is given passes for " + AddressCommands + "; an agent for " + AddressAgents}
	case len(c.RedirectURIs) == 0 || len(c.RedirectURIs) > 10:
		return &Error{Code: CodeRequired, Path: "/redirect_uris", Message: "a client needs 1 to 10 addresses its codes are sent to"}
	}
	for _, uri := range c.RedirectURIs {
		if e := checkRedirect(uri); e != nil {
			return e
		}
	}
	return nil
}

// AddClient adds a client the server gives passes to (ADR-0071, item
// 6): a face or an app of the company, given passes for the commands, or
// an agent, given passes for the door for agents. A client known by the
// document it publishes, as Claude is, is never added. It is a database
// administrator's.
func (s *Store) AddClient(ctx context.Context, c Client, opts ...Option) (*Client, error) {
	o, e := s.keeper(ctx, opts, "add a client")
	if e != nil {
		return nil, e
	}
	if e := refuseAt(o, "change to a client"); e != nil {
		return nil, e
	}
	c.Name = strings.TrimSpace(c.Name)
	if e := checkClient(&c); e != nil {
		return nil, e
	}
	g := s.passing()
	g.mu.Lock()
	_, exists := g.clients[c.ID]
	g.mu.Unlock()
	if exists {
		return nil, &Error{Code: CodeDuplicate, Path: "/id", Message: "a client " + strconv.Quote(c.ID) + " exists",
			Hint: "an id is never given to another client, even once the first was removed; give the client another id"}
	}
	added := &Client{ID: c.ID, Name: c.Name, RedirectURIs: once(c.RedirectURIs), Address: c.Address, AddedBy: o.principal.ID}
	err := s.writeUsers(ctx, func(w execer, now int64) error {
		added.AddedAt = now
		if _, err := w.ExecContext(ctx, "INSERT INTO fado_clients (id, name, redirect_uris, address, added_at, added_by, removed_at, removed_by) VALUES (?, ?, ?, ?, ?, ?, 0, '')",
			added.ID, added.Name, listJSON(added.RedirectURIs), added.Address, now, added.AddedBy); err != nil {
			return storageErr("store the client", err)
		}
		return nil
	}, func() {})
	if err != nil {
		return nil, err
	}
	g.mu.Lock()
	g.clients[added.ID] = added
	g.mu.Unlock()
	shown := *added
	return &shown, nil
}

// Clients lists the clients an administrator added, the removed ones
// among them, in the order of their ids. It is a database
// administrator's.
func (s *Store) Clients(ctx context.Context, opts ...Option) ([]*Client, error) {
	if _, e := s.keeper(ctx, opts, "list the clients"); e != nil {
		return nil, e
	}
	g := s.passing()
	g.mu.Lock()
	list := make([]*Client, 0, len(g.clients))
	for _, c := range g.clients {
		shown := *c
		list = append(list, &shown)
	}
	g.mu.Unlock()
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	return list, nil
}

// RemoveClient removes a client an administrator added: it is given no
// pass any more, and every grant of it ends. It is kept, so that a
// grant of it still names it. It is a database administrator's.
func (s *Store) RemoveClient(ctx context.Context, id string, opts ...Option) error {
	o, e := s.keeper(ctx, opts, "remove a client")
	if e != nil {
		return e
	}
	if e := refuseAt(o, "change to a client"); e != nil {
		return e
	}
	g := s.passing()
	g.mu.Lock()
	c := g.clients[id]
	var ending []*Grant
	for _, gr := range g.granted {
		if gr.Client == id {
			ending = append(ending, gr)
		}
	}
	g.mu.Unlock()
	if c == nil {
		return &Error{Code: CodeUnknownClient, Path: "/client", Message: "the database has no client " + strconv.Quote(id),
			Hint: "list_clients lists the clients an administrator added; a client known by its document is not one of them, and its grants are ended one by one"}
	}
	if c.RemovedAt != 0 {
		return nil
	}
	err := s.writeUsers(ctx, func(w execer, now int64) error {
		if _, err := w.ExecContext(ctx, "UPDATE fado_clients SET removed_at = ?, removed_by = ? WHERE id = ?", now, o.principal.ID, id); err != nil {
			return storageErr("remove the client", err)
		}
		removed := *c
		removed.RemovedAt, removed.RemovedBy = now, o.principal.ID
		g.mu.Lock()
		g.clients[id] = &removed
		g.mu.Unlock()
		return s.endGrants(ctx, w, now, ending, o.principal.ID, GrantClientRemoved)
	}, func() {})
	if err != nil {
		return err
	}
	s.withdrawEnded(ctx, ending, o.principal)
	return nil
}

// KnownClient answers a client an administrator added and did not
// remove, for the server, which asks it of a request that names the
// client by its id.
func (s *Store) KnownClient(ctx context.Context, id string) (*Client, error) {
	if !s.people.on {
		return nil, notKept()
	}
	g := s.passing()
	g.mu.Lock()
	c := g.clients[id]
	g.mu.Unlock()
	if c == nil || c.RemovedAt != 0 {
		return nil, &Error{Code: CodeUnknownClient, Path: "/client", Message: "the database has no client " + strconv.Quote(id)}
	}
	shown := *c
	return &shown, nil
}

// checkAsked checks a client's request as the store takes it.
func checkAsked(a *Asked) *Error {
	switch {
	case a.Client.ID == "" || a.Client.Name == "":
		return &Error{Code: CodeBadValue, Path: "/client", Message: "the request names no client"}
	case a.Client.Address != AddressCommands && a.Client.Address != AddressAgents:
		return &Error{Code: CodeBadValue, Path: "/client", Message: "the client is for no address of the server"}
	case a.Client.Document() && a.Client.Address != AddressAgents:
		return &Error{Code: CodeBadValue, Path: "/client", Message: "a client known by its document is given passes for the door for agents alone"}
	case !RedirectAllowed(&a.Client, a.RedirectURI):
		return &Error{Code: CodeBadValue, Path: "/redirect_uri", Message: "the client is not sent codes at " + strconv.Quote(a.RedirectURI)}
	case len(a.Challenge) != 43 || strings.IndexFunc(a.Challenge, notURLBase64) >= 0:
		return &Error{Code: CodeBadValue, Path: "/code_challenge", Message: "the request has no PKCE challenge of S256"}
	case a.Resource == "":
		return &Error{Code: CodeBadValue, Path: "/resource", Message: "the request names no address of the server"}
	}
	return nil
}

func notURLBase64(r rune) bool {
	return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_')
}

// AskConsent keeps a client's request while the person it is for signs
// in and is asked, for a quarter of an hour, and answers the id that
// finds it again. The server checked the request; the store checks it
// again.
func (s *Store) AskConsent(ctx context.Context, a Asked) (string, error) {
	if !s.people.on {
		return "", notKept()
	}
	if e := checkAsked(&a); e != nil {
		return "", e
	}
	_, id, err := secretText()
	if err != nil {
		return "", &Error{Code: CodeStorage, Message: "make the request: " + err.Error()}
	}
	now := s.now().Unix()
	g := s.passing()
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.asked) >= maxWaiting {
		for k, v := range g.asked {
			if now >= v.ends {
				delete(g.asked, k)
			}
		}
		if len(g.asked) >= maxWaiting {
			return "", hintf(CodeTooManyAttempts, "", "the server holds as many requests of clients as it keeps", "ask again in a few minutes")
		}
	}
	g.asked[id] = &askedFor{Asked: a, ends: now + int64(askLasts/time.Second)}
	return id, nil
}

// askedPerson checks the caller of the page that asks: a person, and
// somebody the database knows.
func (s *Store) askedPerson(ctx context.Context, opts []Option) (Principal, *Error) {
	o, e := s.known(ctx, opts, "allow a client")
	if e != nil {
		return o.principal, e
	}
	if o.principal.Kind != ActorPerson || o.principal.For != "" {
		return o.principal, hintf(CodeForbidden, "", who(o.principal)+" may not allow a client", "a person allows a client, signed in on the server's own page")
	}
	return o.principal, nil
}

// Consent answers a client's request that waits for the person, for the
// page that asks them.
func (s *Store) Consent(ctx context.Context, id string, opts ...Option) (*Asked, error) {
	if _, e := s.askedPerson(ctx, opts); e != nil {
		return nil, e
	}
	g := s.passing()
	g.mu.Lock()
	a := g.asked[id]
	g.mu.Unlock()
	if a == nil || s.now().Unix() >= a.ends {
		return nil, &Error{Code: CodeUnknownGrant, Path: "/request", Message: "the request of the client ended, or is not one",
			Hint: "go back to the client and connect again"}
	}
	shown := a.Asked
	return &shown, nil
}

// takeAsked takes a request away, for the person's answer.
func (s *Store) takeAsked(id string) *askedFor {
	g := s.passing()
	g.mu.Lock()
	defer g.mu.Unlock()
	a := g.asked[id]
	delete(g.asked, id)
	if a == nil || s.now().Unix() >= a.ends {
		return nil
	}
	return a
}

// Allow gives the client of a request its code: the person allowed it.
// The code holds once and for five minutes, and is exchanged with the
// proof the request was made with. The request is answered once.
func (s *Store) Allow(ctx context.Context, id string, opts ...Option) (string, *Asked, error) {
	p, e := s.askedPerson(ctx, opts)
	if e != nil {
		return "", nil, e
	}
	a := s.takeAsked(id)
	if a == nil {
		return "", nil, &Error{Code: CodeUnknownGrant, Path: "/request", Message: "the request of the client ended, or is not one",
			Hint: "go back to the client and connect again"}
	}
	codeID, secret, err := secretText()
	if err != nil {
		return "", nil, &Error{Code: CodeStorage, Message: "make the code: " + err.Error()}
	}
	now := s.now().Unix()
	g := s.passing()
	g.mu.Lock()
	if len(g.codes) >= maxWaiting {
		for k, v := range g.codes {
			if now >= v.ends {
				delete(g.codes, k)
			}
		}
	}
	full := len(g.codes) >= maxWaiting
	if !full {
		g.codes[codeID] = &heldCode{Asked: a.Asked, hash: hashSecret(secret), user: p.ID, ends: now + int64(codeLasts/time.Second)}
	}
	g.mu.Unlock()
	if full {
		return "", nil, hintf(CodeTooManyAttempts, "", "the server holds as many codes as it keeps", "ask again in a few minutes")
	}
	shown := a.Asked
	return codePrefix + codeID + "_" + secret, &shown, nil
}

// Deny answers a request the person did not allow, which the client is
// told.
func (s *Store) Deny(ctx context.Context, id string, opts ...Option) (*Asked, error) {
	if _, e := s.askedPerson(ctx, opts); e != nil {
		return nil, e
	}
	a := s.takeAsked(id)
	if a == nil {
		return nil, &Error{Code: CodeUnknownGrant, Path: "/request", Message: "the request of the client ended, or is not one"}
	}
	shown := a.Asked
	return &shown, nil
}

// pkceProven reports whether a PKCE verifier is the one of a challenge: 43
// to 128 characters of the unreserved ones, whose SHA-256 is the
// challenge (RFC 7636).
func pkceProven(verifier, challenge string) bool {
	if len(verifier) < 43 || len(verifier) > 128 || strings.IndexFunc(verifier, func(r rune) bool { return notURLBase64(r) && r != '.' && r != '~' }) >= 0 {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	return subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(challenge)) == 1
}

// fitUnused keeps how long a grant holds unused within the usual.
func fitUnused(d time.Duration) int64 {
	if d <= 0 || d > DefaultGrantUnused {
		d = DefaultGrantUnused
	}
	return int64(d / time.Second)
}

// Exchange gives a client its passes for a code (ADR-0071, item 6): the
// code once, from the client it was given to, with the address it was
// sent to and the proof of PKCE. For the door for agents, the agent the
// person connects is made the first time, with a substitution that lets
// it act for the person, given by the person (item 7). Every refusal is
// ErrNoGrant; a code shown a second time ends the grant it was
// exchanged for.
func (s *Store) Exchange(ctx context.Context, x CodeExchange) (*Passes, error) {
	if !s.people.on {
		return nil, notKept()
	}
	id, sum, ok := splitToken(x.Code, codePrefix)
	if !ok {
		return nil, ErrNoGrant
	}
	now := s.now().Unix()
	g := s.passing()
	g.mu.Lock()
	code := g.codes[id]
	var held [sha256.Size]byte
	if code != nil {
		held = code.hash
	}
	match := subtle.ConstantTimeCompare(sum[:], held[:]) == 1 && code != nil
	var twice string
	used := false
	if match {
		used = code.gr != ""
		twice = code.gr
		if !used {
			// Taken at once, so that two exchanges of one code cannot
			// both get past here.
			code.gr = "-"
		}
	}
	g.mu.Unlock()
	if !match || now >= code.ends {
		return nil, ErrNoGrant
	}
	if used {
		if twice != "-" {
			s.endGrantOf(ctx, twice, engineActor, GrantUsedTwice)
		}
		return nil, ErrNoGrant
	}
	if code.Client.ID != x.Client || code.RedirectURI != x.RedirectURI || !pkceProven(x.Verifier, code.Challenge) ||
		x.Resource != "" && x.Resource != code.Resource {
		return nil, ErrNoGrant
	}
	if !code.Client.Document() {
		// An administrator may have removed the client since.
		g.mu.Lock()
		kept := g.clients[code.Client.ID]
		g.mu.Unlock()
		if kept == nil || kept.RemovedAt != 0 {
			return nil, ErrNoGrant
		}
	}
	s.people.mu.RLock()
	person := s.people.users[code.user]
	s.people.mu.RUnlock()
	if person == nil || person.Disabled || person.Kind != ActorPerson {
		return nil, ErrNoGrant
	}
	gr := &Grant{User: person.ID, Client: code.Client.ID, ClientName: code.Client.Name, Address: code.Client.Address,
		resource: code.Resource, idle: fitUnused(x.Unused)}
	if gr.Address == AddressAgents {
		agent, sub, err := s.connectAgent(ctx, person, &code.Client)
		if err != nil {
			return nil, err
		}
		gr.Agent, gr.Substitution = agent, sub
	}
	passes, err := s.giveGrant(ctx, gr)
	if err != nil {
		return nil, err
	}
	g.mu.Lock()
	code.gr = gr.ID
	g.mu.Unlock()
	return passes, nil
}

// connectAgent makes, the first time, the agent a person connects
// through a client, and finds or gives the substitution that lets it act
// for the person (ADR-0071, item 7). It answers the agent's id and the
// substitution's.
func (s *Store) connectAgent(ctx context.Context, person *User, c *Client) (string, string, error) {
	agent := AgentOf(person.ID, c)
	by := Principal{ID: person.ID, Kind: person.Kind, Groups: person.Groups, System: person.System}
	s.people.mu.RLock()
	u := s.people.users[agent]
	s.people.mu.RUnlock()
	switch {
	case u != nil && (u.Kind != ActorAgent || u.Disabled):
		// An administrator disabled the agent: the person does not bring
		// it back by connecting again.
		return "", "", ErrNoGrant
	case u == nil:
		name := person.Name
		if name == "" {
			name = person.ID
		}
		made := &User{ID: agent, Kind: ActorAgent, Name: c.Name + " for " + name, Groups: []string{}, System: []string{}}
		err := s.writeUsers(ctx, func(w execer, now int64) error {
			if _, err := w.ExecContext(ctx, "INSERT INTO fado_users (id, kind, name, groups, system_groups, disabled, created_at, updated_at) VALUES (?, ?, ?, '[]', '[]', 0, ?, ?)",
				made.ID, made.Kind, made.Name, now, now); err != nil {
				return storageErr("store the agent", err)
			}
			return recordUserChange(ctx, w, made.ID, AgentConnected, by, now, map[string]any{"user": made, "client": c.ID})
		}, func() { s.people.users[made.ID] = made })
		if err != nil {
			return "", "", err
		}
	}
	subs, e := s.grantsOf(ctx, agent, person.ID)
	if e != nil {
		return "", "", e
	}
	now := s.now().Unix()
	for i := range subs {
		if subs[i].covers("", now, now) {
			return agent, subs[i].id, nil
		}
	}
	doc, err := s.Create(ctx, model.SubstitutionClass, Values{"by": agent, "for": person.ID}, As(by))
	if err != nil {
		return "", "", err
	}
	return agent, doc.ID, nil
}

// giveGrant writes a grant and its first passes.
func (s *Store) giveGrant(ctx context.Context, gr *Grant) (*Passes, error) {
	grantID, _, err := secretText()
	if err != nil {
		return nil, &Error{Code: CodeStorage, Message: "make the grant: " + err.Error()}
	}
	gr.ID = grantID
	passID, passSecret, err1 := secretText()
	renewID, renewSecret, err2 := secretText()
	if err1 != nil || err2 != nil {
		return nil, &Error{Code: CodeStorage, Message: "make the passes"}
	}
	held := &heldPass{gr: grantID, hash: hashSecret(passSecret)}
	err = s.writeUsers(ctx, func(w execer, now int64) error {
		gr.CreatedAt, gr.UsedAt, gr.Ends = now, now, now+gr.idle
		held.ends = now + int64(passLasts/time.Second)
		if _, err := w.ExecContext(ctx, "INSERT INTO fado_grants (id, user, agent, client, client_name, address, resource, substitution, created_at, used_at, idle, ended_at, ended_by, why) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, '', '')",
			gr.ID, gr.User, gr.Agent, gr.Client, gr.ClientName, gr.Address, gr.resource, gr.Substitution, now, now, gr.idle); err != nil {
			return storageErr("store the grant", err)
		}
		return writePasses(ctx, w, now, grantID, passID, held, renewID, renewSecret)
	}, func() {})
	if err != nil {
		return nil, err
	}
	g := s.passing()
	g.mu.Lock()
	g.granted[grantID] = gr
	g.passes[passID] = held
	g.mu.Unlock()
	return &Passes{Pass: PassPrefix + passID + "_" + passSecret, Renew: renewPrefix + renewID + "_" + renewSecret,
		Lasts: int64(passLasts / time.Second), Grant: grantID}, nil
}

// writePasses writes a pass for an address and a renewing pass.
func writePasses(ctx context.Context, w execer, now int64, gr, passID string, held *heldPass, renewID, renewSecret string) error {
	renewHash := hashSecret(renewSecret)
	if _, err := w.ExecContext(ctx, "INSERT INTO fado_passes (id, grant_id, kind, hash, made_at, ends, used_at) VALUES (?, ?, ?, ?, ?, ?, 0), (?, ?, ?, ?, ?, 0, 0)",
		passID, gr, passAccess, held.hash[:], now, held.ends, renewID, gr, passRenew, renewHash[:], now); err != nil {
		return storageErr("store the passes", err)
	}
	return nil
}

// Renew gives a client new passes for its renewing pass, which holds no
// more after it (ADR-0071, item 6). A renewing pass shown a second time
// was stolen: the grant ends, with every pass of it. A grant
// unused for as long as it holds unused, one whose person or agent is
// disabled, and one whose substitution was withdrawn end too. Every
// refusal is ErrNoGrant.
func (s *Store) Renew(ctx context.Context, client, token, resource string) (*Passes, error) {
	if !s.people.on {
		return nil, notKept()
	}
	id, sum, ok := splitToken(token, renewPrefix)
	if !ok {
		return nil, ErrNoGrant
	}
	var grantID, kind string
	var hash []byte
	var usedAt int64
	err := s.db.QueryRowContext(ctx, "SELECT grant_id, kind, hash, used_at FROM fado_passes WHERE id = ?", id).Scan(&grantID, &kind, &hash, &usedAt)
	if err != nil && err != sql.ErrNoRows {
		return nil, storageErr("read the pass", err)
	}
	var held [sha256.Size]byte
	copy(held[:], hash)
	if subtle.ConstantTimeCompare(sum[:], held[:]) != 1 || err != nil || kind != passRenew {
		return nil, ErrNoGrant
	}
	g := s.passing()
	g.mu.Lock()
	gr := g.granted[grantID]
	g.mu.Unlock()
	if gr == nil || gr.Client != client || resource != "" && resource != gr.resource {
		return nil, ErrNoGrant
	}
	if usedAt != 0 {
		s.endGrantOf(ctx, grantID, engineActor, GrantUsedTwice)
		return nil, ErrNoGrant
	}
	now := s.now().Unix()
	if why := s.grantGone(ctx, gr, now); why != "" {
		if why != "-" {
			s.endGrantOf(ctx, grantID, engineActor, why)
		}
		return nil, ErrNoGrant
	}
	s.endUnused(ctx, now)
	passID, passSecret, err1 := secretText()
	renewID, renewSecret, err2 := secretText()
	if err1 != nil || err2 != nil {
		return nil, &Error{Code: CodeStorage, Message: "make the passes"}
	}
	fresh := &heldPass{gr: grantID, hash: hashSecret(passSecret)}
	taken := false
	err = s.writeUsers(ctx, func(w execer, now int64) error {
		res, err := w.ExecContext(ctx, "UPDATE fado_passes SET used_at = ? WHERE id = ? AND used_at = 0", now, id)
		if err != nil {
			return storageErr("use the renewing pass", err)
		}
		if n, _ := res.RowsAffected(); n != 1 {
			// Another renewal took it first.
			taken = true
			return nil
		}
		if _, err := w.ExecContext(ctx, "UPDATE fado_grants SET used_at = ? WHERE id = ?", now, grantID); err != nil {
			return storageErr("note the use of the grant", err)
		}
		fresh.ends = now + int64(passLasts/time.Second)
		return writePasses(ctx, w, now, grantID, passID, fresh, renewID, renewSecret)
	}, func() {})
	if err != nil {
		return nil, err
	}
	if taken {
		s.endGrantOf(ctx, grantID, engineActor, GrantUsedTwice)
		return nil, ErrNoGrant
	}
	g.mu.Lock()
	// A grant is never changed in place: a check that read it keeps
	// what it read.
	if c := g.granted[grantID]; c != nil {
		renewed := *c
		renewed.UsedAt, renewed.Ends = now, now+c.idle
		g.granted[grantID] = &renewed
	}
	for k, p := range g.passes {
		if p.gr == grantID && now >= p.ends {
			delete(g.passes, k)
		}
	}
	g.passes[passID] = fresh
	g.mu.Unlock()
	return &Passes{Pass: PassPrefix + passID + "_" + passSecret, Renew: renewPrefix + renewID + "_" + renewSecret,
		Lasts: int64(passLasts / time.Second), Grant: grantID}, nil
}

// grantGone says why a grant that stands holds no more: unused
// too long, or its substitution withdrawn, each of which ends it; "-"
// for a person or an agent that is disabled, which ends nothing, as a
// session is kept for a user who is enabled again; "" when it holds.
func (s *Store) grantGone(ctx context.Context, gr *Grant, now int64) string {
	if now >= gr.UsedAt+gr.idle {
		return GrantUnused
	}
	s.people.mu.RLock()
	person := s.people.users[gr.User]
	var agent *User
	if gr.Agent != "" {
		agent = s.people.users[gr.Agent]
	}
	s.people.mu.RUnlock()
	if person == nil || person.Disabled || gr.Agent != "" && (agent == nil || agent.Disabled) {
		return "-"
	}
	if gr.Agent != "" && !s.substitutionStands(ctx, gr, now) {
		return GrantWithdrawn
	}
	return ""
}

// substitutionStands reports whether the substitution an agent's
// grant was made with still lets the agent act for the person.
func (s *Store) substitutionStands(ctx context.Context, gr *Grant, now int64) bool {
	subs, e := s.grantsOf(ctx, gr.Agent, gr.User)
	if e != nil {
		return false
	}
	for i := range subs {
		if subs[i].id == gr.Substitution && subs[i].covers("", now, now) {
			return true
		}
	}
	return false
}

// PassCaller answers who a pass belongs to at an address (ADR-0071,
// items 7 and 8): at the commands, the person who allowed the client; at
// the door for agents, the agent of the person, acting for them with
// their groups and system groups as they are now. A pass the database
// does not know, one for another address, one that ended, and one of a
// grant that ended or whose person or agent is disabled answer
// ErrUnknownKey, as a key does. It writes nothing.
func (s *Store) PassCaller(ctx context.Context, token, address string) (Principal, error) {
	id, sum, ok := splitToken(token, PassPrefix)
	if !s.people.on || !ok {
		return Principal{}, ErrUnknownKey
	}
	now := s.now().Unix()
	g := s.passing()
	g.mu.Lock()
	p := g.passes[id]
	var held [sha256.Size]byte
	var gr *Grant
	if p != nil {
		held = p.hash
		gr = g.granted[p.gr]
	}
	g.mu.Unlock()
	if subtle.ConstantTimeCompare(sum[:], held[:]) != 1 || p == nil || gr == nil || now >= p.ends || gr.Address != address {
		return Principal{}, ErrUnknownKey
	}
	if s.grantGone(ctx, gr, now) != "" {
		return Principal{}, ErrUnknownKey
	}
	s.people.mu.RLock()
	u := s.people.users[gr.User]
	s.people.mu.RUnlock()
	if gr.Agent == "" {
		return Principal{ID: u.ID, Kind: u.Kind, Groups: u.Groups, System: u.System}, nil
	}
	return Principal{ID: gr.Agent, Kind: ActorAgent, Groups: u.Groups, System: u.System, For: u.ID}, nil
}

// Revoke ends the grant of a pass its client gives back (RFC
// 7009): either pass ends it, with every pass of it. A pass that is not
// one, or is another client's than the one named, ends nothing, and is
// answered the same.
func (s *Store) Revoke(ctx context.Context, client, token string) error {
	if !s.people.on {
		return notKept()
	}
	var grantID string
	var sum [sha256.Size]byte
	var hash []byte
	if id, h, ok := splitToken(token, PassPrefix); ok {
		sum = h
		err := s.db.QueryRowContext(ctx, "SELECT grant_id, hash FROM fado_passes WHERE id = ? AND kind = ?", id, passAccess).Scan(&grantID, &hash)
		if err != nil && err != sql.ErrNoRows {
			return storageErr("read the pass", err)
		}
	} else if id, h, ok := splitToken(token, renewPrefix); ok {
		sum = h
		err := s.db.QueryRowContext(ctx, "SELECT grant_id, hash FROM fado_passes WHERE id = ? AND kind = ?", id, passRenew).Scan(&grantID, &hash)
		if err != nil && err != sql.ErrNoRows {
			return storageErr("read the pass", err)
		}
	}
	var held [sha256.Size]byte
	copy(held[:], hash)
	if grantID == "" || subtle.ConstantTimeCompare(sum[:], held[:]) != 1 {
		return nil
	}
	g := s.passing()
	g.mu.Lock()
	gr := g.granted[grantID]
	g.mu.Unlock()
	// A client that names itself must be the grant's; one that does
	// not proves itself with the pass alone, which opened the same.
	if gr == nil || client != "" && gr.Client != client {
		return nil
	}
	s.endGrantOf(ctx, grantID, Principal{}, GrantRevoked)
	return nil
}

// Connections lists the grants of a person that stand, oldest
// first; user left empty is the caller. A database administrator may
// ask of any user, and a user of itself: a person of the clients they
// allowed, an agent of the grants it acts through.
func (s *Store) Grants(ctx context.Context, user string, opts ...Option) ([]*Grant, error) {
	o, e := s.known(ctx, opts, "list grants")
	if e != nil {
		return nil, e
	}
	if user == "" {
		user = o.principal.ID
	}
	if !o.principal.in(GroupAdministrators) && o.principal.ID != user {
		e := hintf(CodeForbidden, "", who(o.principal)+" may not list the grants of "+strconv.Quote(user),
			"a user lists its own; the grants of another are for the members of the system group "+GroupAdministrators)
		e.Path = "/system/" + GroupAdministrators
		return nil, e
	}
	s.people.mu.RLock()
	_, exists := s.people.users[user]
	s.people.mu.RUnlock()
	if !exists {
		e := unknownUser(user)
		e.Path = "/user"
		return nil, e
	}
	now := s.now().Unix()
	s.endUnused(ctx, now)
	g := s.passing()
	g.mu.Lock()
	var mine []*Grant
	for _, c := range g.granted {
		if c.User == user || c.Agent == user {
			mine = append(mine, c)
		}
	}
	g.mu.Unlock()
	list := []*Grant{}
	for _, c := range mine {
		if why := s.grantGone(ctx, c, now); why == "" || why == "-" {
			shown := *c
			list = append(list, &shown)
		}
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].CreatedAt != list[j].CreatedAt {
			return list[i].CreatedAt < list[j].CreatedAt
		}
		return list[i].ID < list[j].ID
	})
	return list, nil
}

// EndGrant ends a grant by its id: its passes are refused
// from the next request, and the substitution of its agent is withdrawn
// when no other grant of the agent stands. A database
// administrator ends any, a person their own, and an agent the one it
// acts through. Ending one that has ended changes nothing.
func (s *Store) EndGrant(ctx context.Context, id string, opts ...Option) error {
	o, e := s.known(ctx, opts, "end a grant")
	if e != nil {
		return e
	}
	if e := refuseAt(o, "change to a grant"); e != nil {
		return e
	}
	g := s.passing()
	g.mu.Lock()
	gr := g.granted[id]
	g.mu.Unlock()
	if gr == nil {
		var user string
		err := s.db.QueryRowContext(ctx, "SELECT user FROM fado_grants WHERE id = ?", id).Scan(&user)
		if err == nil && (o.principal.in(GroupAdministrators) || user == o.principal.ID) {
			return nil
		}
		return unknownGrant(id)
	}
	if !o.principal.in(GroupAdministrators) && gr.User != o.principal.ID && gr.Agent != o.principal.ID {
		return unknownGrant(id)
	}
	s.endGrantOf(ctx, id, o.principal, GrantEnded)
	return nil
}

func unknownGrant(id string) *Error {
	return &Error{Code: CodeUnknownGrant, Path: "/grant", Message: "the database has no grant " + strconv.Quote(id),
		Hint: "a grant is named by its id, as list_grants shows it"}
}

// endGrantOf ends one grant that stands, and withdraws the
// substitution of its agent when it was the agent's last. by is who
// ended it: a person, the engine, or nobody for a client that gave its
// pass back.
func (s *Store) endGrantOf(ctx context.Context, id string, by Principal, why string) {
	g := s.passing()
	g.mu.Lock()
	gr := g.granted[id]
	g.mu.Unlock()
	if gr == nil {
		return
	}
	ending := []*Grant{gr}
	err := s.writeUsers(ctx, func(w execer, now int64) error {
		return s.endGrants(ctx, w, now, ending, by.ID, why)
	}, func() {})
	if err == nil {
		withdrawer := by
		if withdrawer.ID == "" {
			withdrawer = engineActor
		}
		s.withdrawEnded(ctx, ending, withdrawer)
	}
}

// endGrants writes the end of grants, inside the writer's
// transaction, and forgets them and their passes.
func (s *Store) endGrants(ctx context.Context, w execer, now int64, ending []*Grant, by, why string) error {
	for _, c := range ending {
		if _, err := w.ExecContext(ctx, "UPDATE fado_grants SET ended_at = ?, ended_by = ?, why = ? WHERE id = ? AND ended_at = 0", now, by, why, c.ID); err != nil {
			return storageErr("end the grant", err)
		}
	}
	g := s.passing()
	g.mu.Lock()
	for _, c := range ending {
		delete(g.granted, c.ID)
		for k, p := range g.passes {
			if p.gr == c.ID {
				delete(g.passes, k)
			}
		}
	}
	g.mu.Unlock()
	return nil
}

// withdrawEnded withdraws the substitution of each agent's grant
// that ended, when no grant that stands was made with it. The
// withdrawal is the engine's own command, recorded as by whoever ended
// the grant.
func (s *Store) withdrawEnded(ctx context.Context, ended []*Grant, by Principal) {
	g := s.passing()
	for _, c := range ended {
		if c.Substitution == "" {
			continue
		}
		g.mu.Lock()
		shared := false
		for _, other := range g.granted {
			shared = shared || other.Substitution == c.Substitution
		}
		g.mu.Unlock()
		if shared {
			continue
		}
		cls, e := s.class(model.SubstitutionClass)
		if e != nil {
			continue
		}
		// A substitution withdrawn or ended already is left as it is.
		_, _ = s.commit(ctx, command{op: opTransition, c: cls, id: c.Substitution, transition: "withdraw", engine: true, o: apply([]Option{As(by)})})
	}
}

// endUnused ends the grants no pass was renewed for in as long as
// they hold unused. It runs when a pass is renewed and when grants
// are listed, so that a grant a client forgot ends without a clock
// of its own.
func (s *Store) endUnused(ctx context.Context, now int64) {
	g := s.passing()
	g.mu.Lock()
	var ending []*Grant
	for _, c := range g.granted {
		if now >= c.UsedAt+c.idle {
			ending = append(ending, c)
		}
	}
	g.mu.Unlock()
	if len(ending) == 0 {
		return
	}
	err := s.writeUsers(ctx, func(w execer, now int64) error {
		return s.endGrants(ctx, w, now, ending, engineActor.ID, GrantUnused)
	}, func() {})
	if err == nil {
		s.withdrawEnded(ctx, ending, engineActor)
	}
}
