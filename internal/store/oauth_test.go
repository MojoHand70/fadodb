package store

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/internal/model"
)

// The clients of the tests: Claude, known by the document it publishes,
// and FadoFace, which Olga adds for the commands.
var (
	claude = Client{ID: "https://claude.ai/oauth/mcp-oauth-client-metadata", Name: "Claude",
		RedirectURIs: []string{"https://claude.ai/api/mcp/auth_callback"}, Address: AddressAgents}
	face = Client{ID: "fadoface", Name: "FadoFace", RedirectURIs: []string{"https://face.acme.example/callback"}, Address: AddressCommands}
)

const (
	acmeServer = "https://fado.acme.example"
	verifier   = "a-verifier-of-the-client-that-is-long-enough-for-pkce"
)

func challengeOf(v string) string {
	sum := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// connect runs what a client and a person do: the client asks, the
// person allows it, and the client exchanges its code for passes.
func connect(t *testing.T, s *Store, c Client, person Option, unused time.Duration) *Passes {
	t.Helper()
	ctx := context.Background()
	asked := Asked{Client: c, RedirectURI: c.RedirectURIs[0], Challenge: challengeOf(verifier), State: "the client's state", Resource: acmeServer + c.Address}
	id, err := s.AskConsent(ctx, asked)
	if err != nil {
		t.Fatal(err)
	}
	shown, err := s.Consent(ctx, id, person)
	if err != nil || shown.Client.Name != c.Name || shown.State != "the client's state" {
		t.Fatalf("the page that asks: %v %+v", err, shown)
	}
	code, back, err := s.Allow(ctx, id, person)
	if err != nil || !strings.HasPrefix(code, codePrefix) || back.RedirectURI != c.RedirectURIs[0] {
		t.Fatalf("allowed: %v %q %+v", err, code, back)
	}
	passes, err := s.Exchange(ctx, CodeExchange{Client: c.ID, Code: code, RedirectURI: c.RedirectURIs[0], Verifier: verifier, Resource: acmeServer + c.Address, Unused: unused})
	if err != nil || !strings.HasPrefix(passes.Pass, PassPrefix) || !strings.HasPrefix(passes.Renew, renewPrefix) || passes.Lasts != 3600 {
		t.Fatalf("the exchange: %v %+v", err, passes)
	}
	return passes
}

func noGrant(t *testing.T, what string, err error) {
	t.Helper()
	if !errors.Is(err, ErrNoGrant) {
		t.Errorf("%s: got %v, want ErrNoGrant", what, err)
	}
}

func unknownPass(t *testing.T, s *Store, what, pass, address string) {
	t.Helper()
	if p, err := s.PassCaller(context.Background(), pass, address); !errors.Is(err, ErrUnknownKey) {
		t.Errorf("%s: the pass is let through as %+v, %v", what, p, err)
	}
}

func oauthPeople(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	for _, u := range []User{
		{ID: "olga", Kind: ActorPerson, System: []string{GroupAdministrators}},
		{ID: "ann", Kind: ActorPerson, Name: "Ann", Groups: []string{"finance", "approvers"}},
		{ID: "dan", Kind: ActorPerson, System: []string{GroupDesigners}},
	} {
		by := olga
		if u.ID == "olga" {
			by = machine
		}
		if _, err := s.CreateUser(ctx, u, by); err != nil {
			t.Fatal(err)
		}
	}
}

// Ann connects Claude (ADR-0071, items 6 and 7): Claude is an agent of
// its own, ann~claude.ai, acting for Ann with her groups, through a
// substitution Ann gave; its pass opens the door for agents and nothing
// else; it is renewed, and ended, and everything ends with it.
func TestAnAgentAPersonConnects(t *testing.T) {
	ctx := context.Background()
	clock := &still{now: time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)}
	path := filepath.Join(t.TempDir(), "acme.fado")
	s := openUsers(t, path, clock)
	oauthPeople(t, s)

	passes := connect(t, s, claude, annUser, 0)
	p, err := s.PassCaller(ctx, passes.Pass, AddressAgents)
	if err != nil || p.ID != "ann~claude.ai" || p.Kind != ActorAgent || p.For != "ann" || strings.Join(p.Groups, ",") != "finance,approvers" {
		t.Fatalf("Ann's Claude: %v %+v", err, p)
	}
	unknownPass(t, s, "a pass for agents at the commands", passes.Pass, AddressCommands)
	unknownPass(t, s, "a renewing pass as a pass", passes.Renew, AddressAgents)
	agent, err := s.GetUser(ctx, "ann~claude.ai", olga)
	if err != nil || agent.Kind != ActorAgent || agent.Name != "Claude for Ann" || len(agent.Groups) != 0 {
		t.Fatalf("the agent: %v %+v", err, agent)
	}
	changes, _ := s.UserChanges(ctx, "ann~claude.ai", olga)
	if len(changes) != 1 || changes[0].What != AgentConnected || changes[0].By != "ann" {
		t.Errorf("the agent's changes: %+v", changes)
	}
	list, err := s.Grants(ctx, "", annUser)
	if err != nil || len(list) != 1 || list[0].Agent != "ann~claude.ai" || list[0].Client != claude.ID || list[0].Substitution == "" ||
		list[0].Ends != clock.Now().Unix()+30*24*3600 {
		t.Fatalf("Ann's grants: %v %+v", err, list)
	}
	sub := list[0].Substitution
	doc, err := s.Get(ctx, model.SubstitutionClass, sub, annUser)
	if err != nil || doc.Values["by"] != "ann~claude.ai" || doc.Values["for"] != "ann" {
		t.Fatalf("the substitution Ann gave: %v %+v", err, doc)
	}

	// The agent acts for Ann: the engine asks the substitution, and the
	// agent lists its own grants.
	if mine, err := s.Grants(ctx, "", As(p)); err != nil || len(mine) != 1 {
		t.Errorf("the agent's own grants: %v %+v", err, mine)
	}
	// Ann's groups as they are at each request.
	groups := []string{"finance"}
	s.SetUser(ctx, "ann", UserEdit{Groups: &groups}, olga)
	if p, _ := s.PassCaller(ctx, passes.Pass, AddressAgents); strings.Join(p.Groups, ",") != "finance" {
		t.Errorf("the agent's groups after Ann's changed: %v", p.Groups)
	}

	// Connecting again on another surface of Claude: the same agent and
	// the same substitution.
	again := connect(t, s, claude, annUser, 0)
	list, _ = s.Grants(ctx, "ann", olga)
	if len(list) != 2 || list[1].Substitution != sub {
		t.Fatalf("a second grant: %+v", list)
	}

	// Renewed: the renewing pass holds once. Shown again, it was stolen,
	// and the grant ends with every pass of it.
	clock.add(2 * time.Hour)
	unknownPass(t, s, "a pass past its hour", passes.Pass, AddressAgents)
	renewed, err := s.Renew(ctx, claude.ID, passes.Renew, acmeServer+"/mcp")
	if err != nil || renewed.Grant != passes.Grant {
		t.Fatalf("renewed: %v %+v", err, renewed)
	}
	if _, err := s.PassCaller(ctx, renewed.Pass, AddressAgents); err != nil {
		t.Errorf("the renewed pass: %v", err)
	}
	_, err = s.Renew(ctx, "https://evil.example/client", renewed.Renew, "")
	noGrant(t, "another client's renewal", err)
	_, err = s.Renew(ctx, claude.ID, passes.Renew, "")
	noGrant(t, "a renewing pass shown twice", err)
	unknownPass(t, s, "a pass of a grant that ended", renewed.Pass, AddressAgents)
	_, err = s.Renew(ctx, claude.ID, renewed.Renew, "")
	noGrant(t, "the newer renewing pass of a grant that ended", err)
	if _, err := s.PassCaller(ctx, again.Pass, AddressAgents); err == nil {
		t.Error("the other grant's pass outlived its hour")
	}
	// The other grant stands, so the substitution stands too.
	if stands := s.substitutionStands(ctx, &Grant{Agent: "ann~claude.ai", User: "ann", Substitution: sub}, clock.Now().Unix()); !stands {
		t.Error("the substitution was withdrawn while a grant of the agent stands")
	}

	// Ann ends the other grant on her page: its passes end, and the
	// substitution is withdrawn.
	list, _ = s.Grants(ctx, "", annUser)
	if len(list) != 1 {
		t.Fatalf("Ann's grants after the theft: %+v", list)
	}
	refusedWith(t, "Dan ends Ann's grant", s.EndGrant(ctx, list[0].ID, danUser), CodeUnknownGrant)
	if err := s.EndGrant(ctx, list[0].ID, annUser); err != nil {
		t.Fatal(err)
	}
	if err := s.EndGrant(ctx, list[0].ID, annUser); err != nil {
		t.Errorf("a grant ended twice: %v", err)
	}
	_, err = s.Renew(ctx, claude.ID, again.Renew, "")
	noGrant(t, "the renewing pass of a grant Ann ended", err)
	doc, _ = s.Get(ctx, model.SubstitutionClass, sub, annUser)
	if doc.State != model.SubstitutionWithdrawn {
		t.Errorf("the substitution of the last grant is %s", doc.State)
	}

	// Connected again: a new substitution, the same agent.
	third := connect(t, s, claude, annUser, 0)
	list, _ = s.Grants(ctx, "", annUser)
	if len(list) != 1 || list[0].Substitution == sub {
		t.Fatalf("connected again: %+v", list)
	}
	// Ann withdraws the substitution herself: the pass is refused, and
	// the next renewal ends the grant.
	if _, err := s.Transition(ctx, model.SubstitutionClass, list[0].Substitution, "withdraw", nil, annUser); err != nil {
		t.Fatal(err)
	}
	unknownPass(t, s, "a pass whose substitution was withdrawn", third.Pass, AddressAgents)
	_, err = s.Renew(ctx, claude.ID, third.Renew, "")
	noGrant(t, "a renewal after the substitution was withdrawn", err)
	if list, _ := s.Grants(ctx, "", annUser); len(list) != 0 {
		t.Errorf("a grant stands after its substitution was withdrawn: %+v", list)
	}

	// A disabled person, or a disabled agent, gets nothing; enabled
	// again, the grant holds again, as a session does.
	fourth := connect(t, s, claude, annUser, 0)
	off, on := true, false
	s.SetUser(ctx, "ann", UserEdit{Disabled: &off}, olga)
	unknownPass(t, s, "the pass of a disabled person", fourth.Pass, AddressAgents)
	s.SetUser(ctx, "ann", UserEdit{Disabled: &on}, olga)
	s.SetUser(ctx, "ann~claude.ai", UserEdit{Disabled: &off}, olga)
	unknownPass(t, s, "the pass of a disabled agent", fourth.Pass, AddressAgents)
	s.SetUser(ctx, "ann~claude.ai", UserEdit{Disabled: &on}, olga)
	if _, err := s.PassCaller(ctx, fourth.Pass, AddressAgents); err != nil {
		t.Errorf("enabled again: %v", err)
	}

	// The passes hold when the database is opened again.
	s.Close()
	s = openUsers(t, path, clock)
	if p, err := s.PassCaller(ctx, fourth.Pass, AddressAgents); err != nil || p.For != "ann" {
		t.Errorf("after the database was opened again: %v %+v", err, p)
	}

	// Unused for 30 days, it ends, and its substitution is withdrawn.
	clock.days(31)
	if list, _ := s.Grants(ctx, "", annUser); len(list) != 0 {
		t.Errorf("a grant unused for 31 days: %+v", list)
	}
	_, err = s.Renew(ctx, claude.ID, fourth.Renew, "")
	noGrant(t, "the renewal of a grant unused for 31 days", err)

	// The client gives its pass back.
	fifth := connect(t, s, claude, annUser, 0)
	if err := s.Revoke(ctx, "https://evil.example/client", fifth.Pass); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PassCaller(ctx, fifth.Pass, AddressAgents); err != nil {
		t.Errorf("another client revoked Ann's grant: %v", err)
	}
	if err := s.Revoke(ctx, claude.ID, fifth.Renew); err != nil {
		t.Fatal(err)
	}
	unknownPass(t, s, "a pass given back", fifth.Pass, AddressAgents)

	// Nobody else makes an id with a ~.
	_, err = s.CreateUser(ctx, User{ID: "bob~claude.ai", Kind: ActorAgent}, olga)
	refusedWith(t, "a user with a ~", err, CodeBadValue)
}

// A code holds once, from its client, at its address, with its proof,
// for five minutes; a code shown twice ends what it gave.
func TestACodeHoldsOnce(t *testing.T) {
	ctx := context.Background()
	clock := &still{now: time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)}
	s := openUsers(t, filepath.Join(t.TempDir(), "acme.fado"), clock)
	oauthPeople(t, s)
	asked := Asked{Client: claude, RedirectURI: claude.RedirectURIs[0], Challenge: challengeOf(verifier), Resource: acmeServer + "/mcp"}
	code := func() string {
		id, err := s.AskConsent(ctx, asked)
		if err != nil {
			t.Fatal(err)
		}
		c, _, err := s.Allow(ctx, id, annUser)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	right := CodeExchange{Client: claude.ID, RedirectURI: claude.RedirectURIs[0], Verifier: verifier, Resource: acmeServer + "/mcp"}
	for name, change := range map[string]func(*CodeExchange){
		"another client":         func(x *CodeExchange) { x.Client = "https://evil.example/client" },
		"another address":        func(x *CodeExchange) { x.RedirectURI = "https://evil.example/callback" },
		"another verifier":       func(x *CodeExchange) { x.Verifier = strings.Repeat("v", 50) },
		"no verifier":            func(x *CodeExchange) { x.Verifier = "" },
		"another resource":       func(x *CodeExchange) { x.Resource = acmeServer + "/v1" },
		"a code that is no code": func(x *CodeExchange) { x.Code = "fadoac_" + strings.Repeat("A", 63) },
	} {
		x := right
		x.Code = code()
		change(&x)
		_, err := s.Exchange(ctx, x)
		noGrant(t, name, err)
		// A code that failed once is spent.
		x = CodeExchange{Client: right.Client, Code: x.Code, RedirectURI: right.RedirectURI, Verifier: verifier}
		_, err = s.Exchange(ctx, x)
		noGrant(t, name+", then right", err)
	}
	x := right
	x.Code = code()
	clock.add(6 * time.Minute)
	_, err := s.Exchange(ctx, x)
	noGrant(t, "a code past five minutes", err)

	x.Code = code()
	passes, err := s.Exchange(ctx, x)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Exchange(ctx, x)
	noGrant(t, "a code shown twice", err)
	unknownPass(t, s, "the pass of a code shown twice", passes.Pass, AddressAgents)

	// A request is answered once, by a person; an agent and an unknown
	// request are refused.
	id, _ := s.AskConsent(ctx, asked)
	agent := As(Principal{ID: "bot", Kind: ActorAgent})
	_, err = s.Consent(ctx, id, agent)
	refusedWith(t, "an agent asked", err, CodeForbidden)
	if back, err := s.Deny(ctx, id, annUser); err != nil || back.Client.ID != claude.ID {
		t.Errorf("denied: %v %+v", err, back)
	}
	_, _, err = s.Allow(ctx, id, annUser)
	refusedWith(t, "a request answered twice", err, CodeUnknownGrant)

	// What a request must hold.
	for name, change := range map[string]func(*Asked){
		"a redirect the client has not":    func(a *Asked) { a.RedirectURI = "https://evil.example/callback" },
		"no challenge":                     func(a *Asked) { a.Challenge = "" },
		"a document's client for commands": func(a *Asked) { a.Client.Address = AddressCommands },
		"no resource":                      func(a *Asked) { a.Resource = "" },
	} {
		a := asked
		a.Client.RedirectURIs = append([]string(nil), claude.RedirectURIs...)
		change(&a)
		_, err := s.AskConsent(ctx, a)
		refusedWith(t, name, err, CodeBadValue)
	}
}

// Olga adds FadoFace for the commands: its pass is Ann herself, a
// person, at the commands and nowhere else. Removed, it holds no more.
func TestAClientAnAdministratorAdds(t *testing.T) {
	ctx := context.Background()
	clock := &still{now: time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)}
	s := openUsers(t, filepath.Join(t.TempDir(), "acme.fado"), clock)
	oauthPeople(t, s)
	_, err := s.AddClient(ctx, face, annUser)
	refusedWith(t, "Ann adds a client", err, CodeForbidden)
	added, err := s.AddClient(ctx, face, olga)
	if err != nil || added.AddedBy != "olga" || added.AddedAt == 0 {
		t.Fatalf("FadoFace added: %v %+v", err, added)
	}
	_, err = s.AddClient(ctx, face, olga)
	refusedWith(t, "the same client twice", err, CodeDuplicate)
	for name, c := range map[string]Client{
		"an address of a document": {ID: "https://face.acme.example/client", Name: "x", RedirectURIs: face.RedirectURIs, Address: AddressCommands},
		"no name":                  {ID: "x", RedirectURIs: face.RedirectURIs, Address: AddressCommands},
		"another address":          {ID: "x", Name: "x", RedirectURIs: face.RedirectURIs, Address: "/v2"},
		"a redirect over http":     {ID: "x", Name: "x", RedirectURIs: []string{"http://face.acme.example/callback"}, Address: AddressCommands},
		"a redirect with fragment": {ID: "x", Name: "x", RedirectURIs: []string{"https://face.acme.example/callback#x"}, Address: AddressCommands},
		"no redirect":              {ID: "x", Name: "x", Address: AddressCommands},
	} {
		_, err := s.AddClient(ctx, c, olga)
		var e *Error
		if !errors.As(err, &e) || (e.Code != CodeBadValue && e.Code != CodeRequired) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if known, err := s.KnownClient(ctx, "fadoface"); err != nil || known.Name != "FadoFace" {
		t.Errorf("the client the server finds: %v %+v", err, known)
	}

	passes := connect(t, s, face, annUser, 0)
	p, err := s.PassCaller(ctx, passes.Pass, AddressCommands)
	if err != nil || p.ID != "ann" || p.Kind != ActorPerson || p.For != "" {
		t.Fatalf("FadoFace's pass: %v %+v", err, p)
	}
	unknownPass(t, s, "a face's pass at the door for agents", passes.Pass, AddressAgents)
	if list, _ := s.Grants(ctx, "", annUser); len(list) != 1 || list[0].Agent != "" || list[0].Substitution != "" {
		t.Errorf("Ann's grant to FadoFace: %+v", list)
	}

	if err := s.RemoveClient(ctx, "fadoface", olga); err != nil {
		t.Fatal(err)
	}
	unknownPass(t, s, "the pass of a removed client", passes.Pass, AddressCommands)
	if _, err := s.KnownClient(ctx, "fadoface"); err == nil {
		t.Error("a removed client is found")
	}
	clients, _ := s.Clients(ctx, olga)
	if len(clients) != 1 || clients[0].RemovedBy != "olga" {
		t.Errorf("the clients: %+v", clients)
	}
	refusedWith(t, "a client the database has not", s.RemoveClient(ctx, "nobody", olga), CodeUnknownClient)
}

// A client on the machine is sent its code at any port (RFC 8252); every
// other address is as it is written.
func TestTheAddressACodeIsSentTo(t *testing.T) {
	c := &Client{RedirectURIs: []string{"http://127.0.0.1/callback", "http://localhost:8080/cb", "https://app.acme.example/cb"}}
	for uri, ok := range map[string]bool{
		"http://127.0.0.1/callback":         true,
		"http://127.0.0.1:53124/callback":   true,
		"http://127.0.0.1:53124/other":      false,
		"http://localhost:9999/cb":          true,
		"https://app.acme.example/cb":       true,
		"https://app.acme.example:8443/cb":  false,
		"https://app.acme.example/cb?x=1":   false,
		"http://app.acme.example/cb":        false,
		"http://127.0.0.1:53124/callback?x": false,
	} {
		if RedirectAllowed(c, uri) != ok {
			t.Errorf("%s: allowed is %v", uri, !ok)
		}
	}
}
