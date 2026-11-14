package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The people of the tests of users (ADR-0057). The machine is whoever
// holds the database on their own machine: the command-line tool, which
// names no user and says it administers.
var (
	machine = As(Principal{System: []string{GroupAdministrators}})
	olga    = As(Principal{ID: "olga", Kind: ActorPerson, System: []string{GroupAdministrators}})
	annUser = As(Principal{ID: "ann", Kind: ActorPerson, Groups: []string{"finance"}})
	danUser = As(Principal{ID: "dan", Kind: ActorPerson, System: []string{GroupDesigners}})
)

// set puts the clock at a moment.
func (c *still) set(to time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = to
}

// openUsers opens a database that keeps its own users, with a clock the
// test moves.
func openUsers(t testing.TB, path string, clock *still) *Store {
	t.Helper()
	s, err := Open(path, Options{Users: true, Now: clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// refusedWith fails unless the error is a refusal of the code, and
// answers it.
func refusedWith(t testing.TB, what string, err error, code string) *Error {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Code != code {
		t.Errorf("%s: got %v, want a refusal with %s", what, err, code)
		return &Error{}
	}
	return e
}

// The life of the users of one database: the first administrator is
// made on the machine, she makes the others, each proves who it is with
// a key, and every change is kept.
func TestUsersGroupsAndKeys(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	clock := &still{now: now}
	path := filepath.Join(t.TempDir(), "acme.fado")
	s := openUsers(t, path, clock)

	// A new database has no user. The first is made on the machine, by
	// no user, and is given a key (item 7).
	if users, err := s.Users(ctx, UserFilter{}, machine); err != nil || len(users) != 0 {
		t.Fatalf("a new database has the users %v, %v", users, err)
	}
	first, err := s.CreateUser(ctx, User{ID: "olga", Kind: ActorPerson, Name: "Olga Nowak", System: []string{GroupAdministrators}}, machine)
	if err != nil {
		t.Fatal(err)
	}
	if first.Groups == nil || len(first.Groups) != 0 || first.Disabled {
		t.Errorf("the first user is %+v", first)
	}
	laptop, err := s.CreateKey(ctx, "olga", "laptop", "", machine)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(laptop.Secret, "fado_"+laptop.ID+"_") || len(laptop.Secret) != keyLen || laptop.CreatedBy != "" || laptop.User != "olga" {
		t.Errorf("the key is %+v", laptop)
	}

	// The key says who she is, of what kind and in which groups.
	p, err := s.Authenticate(laptop.Secret)
	if err != nil || p.ID != "olga" || p.Kind != ActorPerson || len(p.System) != 1 || p.System[0] != GroupAdministrators || p.For != "" {
		t.Fatalf("Olga's key answers %+v, %v", p, err)
	}

	// Olga makes the others: a designer, a person of finance, an agent.
	for _, u := range []User{
		{ID: "dan", Kind: ActorPerson, System: []string{GroupDesigners}},
		{ID: "ann", Kind: ActorPerson, Name: "Ann Kowalska", Groups: []string{"finance", "finance"}},
		{ID: "ap-agent", Kind: ActorAgent, Name: "Accounts payable agent", Groups: []string{"finance"}},
	} {
		if _, err := s.CreateUser(ctx, u, olga); err != nil {
			t.Fatal(err)
		}
	}
	agentKey, err := s.CreateKey(ctx, "ap-agent", "invoice matching", "2027-11-14", olga)
	if err != nil {
		t.Fatal(err)
	}
	if agentKey.Expires != "2027-11-14" || agentKey.CreatedBy != "olga" || agentKey.CreatedAt != now.Unix() {
		t.Errorf("the agent's key is %+v", agentKey)
	}
	annKey, err := s.CreateKey(ctx, "ann", "laptop", "", olga)
	if err != nil {
		t.Fatal(err)
	}

	// What a user cannot be.
	for what, c := range map[string]struct {
		u    User
		code string
		path string
	}{
		"an id that is taken":          {User{ID: "ann", Kind: ActorPerson}, CodeDuplicate, "/id"},
		"no id":                        {User{Kind: ActorPerson}, CodeRequired, "/id"},
		"a line break in the id":       {User{ID: "a\nb", Kind: ActorPerson}, CodeBadValue, "/id"},
		"the engine's own id":          {User{ID: "fadodb", Kind: ActorService}, CodeBadValue, "/id"},
		"no kind":                      {User{ID: "eve"}, CodeRequired, "/kind"},
		"a kind the engine knows not":  {User{ID: "eve", Kind: "robot"}, CodeBadValue, "/kind"},
		"a system group among groups":  {User{ID: "eve", Kind: ActorPerson, Groups: []string{GroupDesigners}}, CodeBadValue, ""},
		"everyone among groups":        {User{ID: "eve", Kind: ActorPerson, Groups: []string{GroupEveryone}}, CodeBadValue, ""},
		"a system group that is none":  {User{ID: "eve", Kind: ActorPerson, System: []string{"finance"}}, CodeBadValue, ""},
		"a group with an empty name":   {User{ID: "eve", Kind: ActorPerson, Groups: []string{""}}, CodeBadValue, "/groups"},
		"a group with a control sign":  {User{ID: "eve", Kind: ActorPerson, Groups: []string{"a\tb"}}, CodeBadValue, "/groups"},
		"a second user called olga":    {User{ID: "olga", Kind: ActorPerson}, CodeDuplicate, "/id"},
		"an agent with a wrong system": {User{ID: "eve", Kind: ActorAgent, System: []string{GroupEveryone}}, CodeBadValue, ""},
	} {
		_, err := s.CreateUser(ctx, c.u, olga)
		if e := refusedWith(t, what, err, c.code); e.Path != c.path {
			t.Errorf("%s: the place is %q, want %q", what, e.Path, c.path)
		}
	}

	// Only an administrator keeps users and keys (ruling 3).
	if _, err := s.CreateUser(ctx, User{ID: "eve", Kind: ActorPerson}, annUser); refusedWith(t, "Ann makes a user", err, CodeForbidden).Path != "/system/"+GroupAdministrators {
		t.Errorf("Ann making a user is refused at another place: %v", err)
	}
	disabled := true
	_, err = s.SetUser(ctx, "dan", UserEdit{Disabled: &disabled}, annUser)
	refusedWith(t, "Ann disables Dan", err, CodeForbidden)
	_, err = s.CreateKey(ctx, "ann", "another", "", annUser)
	refusedWith(t, "Ann makes herself a key", err, CodeForbidden)
	_, err = s.CreateKey(ctx, "ann", "another", "", danUser)
	refusedWith(t, "Dan, a designer, makes a key", err, CodeForbidden)

	// Every user sees every user (ruling 4), in the order of the ids,
	// and nobody sees none.
	users, err := s.Users(ctx, UserFilter{}, annUser)
	if err != nil || len(users) != 4 || users[0].ID != "ann" || users[1].ID != "ap-agent" || users[2].ID != "dan" || users[3].ID != "olga" {
		t.Fatalf("Ann lists %v, %v", users, err)
	}
	if got := users[0]; got.Name != "Ann Kowalska" || len(got.Groups) != 1 || got.Groups[0] != "finance" || got.System == nil {
		t.Errorf("Ann is listed as %+v", got)
	}
	if finance, err := s.Users(ctx, UserFilter{Group: "finance"}, danUser); err != nil || len(finance) != 2 {
		t.Errorf("the members of finance are %v, %v", finance, err)
	}
	if admins, err := s.Users(ctx, UserFilter{Group: GroupAdministrators}, danUser); err != nil || len(admins) != 1 || admins[0].ID != "olga" {
		t.Errorf("the administrators are %v, %v", admins, err)
	}
	if agents, err := s.Users(ctx, UserFilter{Kind: ActorAgent}, danUser); err != nil || len(agents) != 1 || agents[0].ID != "ap-agent" {
		t.Errorf("the agents are %v, %v", agents, err)
	}
	if u, err := s.GetUser(ctx, "olga", annUser); err != nil || u.Name != "Olga Nowak" {
		t.Errorf("Ann reads Olga as %+v, %v", u, err)
	}
	_, err = s.GetUser(ctx, "eve", annUser)
	refusedWith(t, "a user that is none", err, CodeUnknownUser)
	_, err = s.Users(ctx, UserFilter{})
	refusedWith(t, "nobody lists the users", err, CodeForbidden)
	_, err = s.GetUser(ctx, "olga", As(Principal{}))
	refusedWith(t, "nobody reads a user", err, CodeForbidden)

	// An administrator may change their own groups (ruling 2), and the
	// change is kept with her id.
	now = now.Add(time.Hour)
	clock.set(now)
	finance := []string{"finance"}
	changed, err := s.SetUser(ctx, "olga", UserEdit{Groups: &finance}, olga)
	if err != nil || len(changed.Groups) != 1 || changed.Name != "Olga Nowak" || len(changed.System) != 1 {
		t.Fatalf("Olga puts herself in finance: %+v, %v", changed, err)
	}
	if p, _ := s.Authenticate(laptop.Secret); len(p.Groups) != 1 || p.Groups[0] != "finance" {
		t.Errorf("after the change Olga's key answers %+v", p)
	}

	// The last administrator stays (item 6).
	none := []string{}
	_, err = s.SetUser(ctx, "olga", UserEdit{System: &none}, olga)
	if e := refusedWith(t, "Olga leaves the administrators", err, CodeNotAllowed); !strings.Contains(e.Hint, "make another user an administrator first") {
		t.Errorf("the refusal does not say what to do: %+v", e)
	}
	_, err = s.SetUser(ctx, "olga", UserEdit{Disabled: &disabled}, olga)
	refusedWith(t, "Olga disables herself", err, CodeNotAllowed)
	both := []string{GroupDesigners, GroupAdministrators}
	if _, err := s.SetUser(ctx, "dan", UserEdit{System: &both}, olga); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetUser(ctx, "olga", UserEdit{System: &none}, olga); err != nil {
		t.Errorf("with Dan an administrator, Olga may leave: %v", err)
	}
	admins := []string{GroupAdministrators}
	if _, err := s.SetUser(ctx, "olga", UserEdit{System: &admins}, As(Principal{ID: "dan", Kind: ActorPerson, System: both})); err != nil {
		t.Fatal(err)
	}

	// A change that changes nothing, and a user that is none.
	_, err = s.SetUser(ctx, "ann", UserEdit{}, olga)
	refusedWith(t, "a change of nothing", err, CodeBadValue)
	name := "Eve"
	_, err = s.SetUser(ctx, "eve", UserEdit{Name: &name}, olga)
	refusedWith(t, "a change of a user that is none", err, CodeUnknownUser)
	_, err = s.CreateKey(ctx, "eve", "laptop", "", olga)
	refusedWith(t, "a key for a user that is none", err, CodeUnknownUser)
	_, err = s.CreateKey(ctx, "ann", "laptop", "14.11.2027", olga)
	refusedWith(t, "a last day written another way", err, CodeBadValue)
	_, err = s.CreateKey(ctx, "ann", "laptop", "2026-11-13", olga)
	refusedWith(t, "a last day that has passed", err, CodeBadValue)

	// A user lists and revokes its own keys; an administrator any.
	if keys, err := s.Keys(ctx, "", annUser); err != nil || len(keys) != 1 || keys[0].ID != annKey.ID || keys[0].Secret != "" {
		t.Errorf("Ann's keys are %+v, %v", keys, err)
	}
	if keys, err := s.Keys(ctx, "ap-agent", olga); err != nil || len(keys) != 1 || keys[0].Label != "invoice matching" || keys[0].Secret != "" {
		t.Errorf("the agent's keys, for Olga, are %+v, %v", keys, err)
	}
	_, err = s.Keys(ctx, "ap-agent", annUser)
	refusedWith(t, "Ann lists the agent's keys", err, CodeForbidden)
	_, err = s.Keys(ctx, "eve", olga)
	refusedWith(t, "the keys of a user that is none", err, CodeUnknownUser)
	// The key of another is, for Ann, a key that does not exist.
	refusedWith(t, "Ann revokes the agent's key", s.RevokeKey(ctx, agentKey.ID, annUser), CodeUnknownKey)
	refusedWith(t, "a key that is none", s.RevokeKey(ctx, "0000000000", olga), CodeUnknownKey)
	if _, err := s.Authenticate(annKey.Secret); err != nil {
		t.Fatalf("Ann's key, before she revokes it: %v", err)
	}
	if err := s.RevokeKey(ctx, annKey.ID, annUser); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(annKey.Secret); err != ErrUnknownKey {
		t.Errorf("Ann's key, revoked, answers %v", err)
	}
	if err := s.RevokeKey(ctx, annKey.ID, annUser); err != nil {
		t.Errorf("revoking a revoked key: %v", err)
	}
	if keys, _ := s.Keys(ctx, "ann", olga); len(keys) != 1 || keys[0].RevokedAt != now.Unix() || keys[0].RevokedBy != "ann" {
		t.Errorf("Ann's revoked key is listed as %+v", keys)
	}

	// A disabled user is stopped at the next command, and let through
	// again when enabled (item 10).
	if _, err := s.Authenticate(agentKey.Secret); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetUser(ctx, "ap-agent", UserEdit{Disabled: &disabled}, olga); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(agentKey.Secret); err != ErrUnknownKey {
		t.Errorf("the key of a disabled user answers %v", err)
	}
	enabled := false
	if _, err := s.SetUser(ctx, "ap-agent", UserEdit{Disabled: &enabled}, olga); err != nil {
		t.Fatal(err)
	}

	// A key holds through its last day, in UTC, and not after.
	clock.set(time.Date(2027, 11, 14, 23, 59, 59, 0, time.UTC))
	if _, err := s.Authenticate(agentKey.Secret); err != nil {
		t.Errorf("the agent's key in the last second of its last day: %v", err)
	}
	clock.set(time.Date(2027, 11, 15, 0, 0, 0, 0, time.UTC))
	if _, err := s.Authenticate(agentKey.Secret); err != ErrUnknownKey {
		t.Errorf("the agent's key after its last day answers %v", err)
	}
	clock.set(now)

	// What is no key at all.
	for _, key := range []string{"", "fado_", laptop.Secret[:keyLen-1], laptop.Secret + "0", "FADO" + laptop.Secret[4:],
		laptop.Secret[:keyLen-1] + "!", "fado_" + agentKey.ID + laptop.Secret[len("fado_")+keyIDLen:], strings.Repeat("x", keyLen)} {
		if p, err := s.Authenticate(key); err != ErrUnknownKey || p.ID != "" {
			t.Errorf("the key %q answers %+v, %v", key, p, err)
		}
	}

	// Every change is kept (item 8): who made it, what they were and
	// when. Olga reads anybody's, Ann her own, and no secret is in any.
	changes, err := s.UserChanges(ctx, "olga", olga)
	if err != nil {
		t.Fatal(err)
	}
	var whats []string
	for _, c := range changes {
		whats = append(whats, c.What+" by "+c.By)
	}
	if got, want := strings.Join(whats, ", "), "created by , key_created by , changed by olga, changed by olga, changed by dan"; got != want {
		t.Errorf("the changes of Olga are %s, want %s", got, want)
	}
	if c := changes[0]; c.User == nil || c.User.ID != "olga" || c.Kind != "" || c.At == 0 || c.Key != nil {
		t.Errorf("the making of Olga is kept as %+v", c)
	}
	if c := changes[1]; c.Key == nil || c.Key.ID != laptop.ID || c.Key.Secret != "" || c.User != nil {
		t.Errorf("the making of Olga's key is kept as %+v", c)
	}
	if c := changes[2]; c.Kind != ActorPerson || c.User == nil || len(c.User.Groups) != 1 {
		t.Errorf("Olga's change of her own groups is kept as %+v", c)
	}
	if own, err := s.UserChanges(ctx, "ann", annUser); err != nil || len(own) != 3 || own[2].What != KeyRevoked || own[2].Key.RevokedBy != "ann" {
		t.Errorf("Ann reads her own changes as %+v, %v", own, err)
	}
	_, err = s.UserChanges(ctx, "olga", annUser)
	refusedWith(t, "Ann reads Olga's changes", err, CodeForbidden)
	_, err = s.UserChanges(ctx, "eve", olga)
	refusedWith(t, "the changes of a user that is none", err, CodeUnknownUser)

	// Whoami answers the user, and a caller the program named itself as
	// it was named.
	if u, acts, err := s.Whoami(ctx, olga); err != nil || u.Name != "Olga Nowak" || acts != "" || len(u.Groups) != 1 {
		t.Errorf("Olga is %+v, for %q, %v", u, acts, err)
	}
	if u, _, err := s.Whoami(ctx, machine); err != nil || u.ID != "" || len(u.System) != 1 || u.Groups == nil {
		t.Errorf("the machine is %+v, %v", u, err)
	}

	// The database carries its people with it: closed and opened again,
	// it answers the same keys, and not the revoked one.
	s.Close()
	s = openUsers(t, path, clock)
	if p, err := s.Authenticate(laptop.Secret); err != nil || p.ID != "olga" || len(p.Groups) != 1 {
		t.Errorf("after the database was opened again Olga's key answers %+v, %v", p, err)
	}
	if _, err := s.Authenticate(annKey.Secret); err != ErrUnknownKey {
		t.Errorf("after the database was opened again Ann's revoked key answers %v", err)
	}
	if keys, err := s.Keys(ctx, "ap-agent", olga); err != nil || len(keys) != 1 || keys[0].Expires != "2027-11-14" {
		t.Errorf("after the database was opened again the agent's keys are %+v, %v", keys, err)
	}

	// The database keeps no key: the hash of a secret, and nowhere the
	// secret itself.
	for _, table := range []string{"fado_users", "fado_keys", "fado_user_changes"} {
		rows, err := s.writer.QueryContext(ctx, "SELECT * FROM "+table)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			cells := make([]any, len(cols))
			for i := range cells {
				cells[i] = new(any)
			}
			if err := rows.Scan(cells...); err != nil {
				t.Fatal(err)
			}
			text, _ := json.Marshal(cells)
			for _, key := range []*UserKey{laptop, agentKey, annKey} {
				secret := key.Secret[len("fado_")+keyIDLen+1:]
				if strings.Contains(string(text), secret) {
					t.Errorf("the table %s holds the secret of the key %s", table, key.ID)
				}
			}
		}
		rows.Close()
	}
}

// The tables of the users are the engine's own: no SQL statement reads
// them, and no class or table takes their names.
func TestTheTablesOfTheUsersAreTheEngines(t *testing.T) {
	ctx := context.Background()
	s := openUsers(t, filepath.Join(t.TempDir(), "acme.fado"), &still{now: time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)})
	if _, err := s.CreateUser(ctx, User{ID: "olga", Kind: ActorPerson, System: []string{GroupAdministrators, GroupAnalysts, GroupDesigners}}, machine); err != nil {
		t.Fatal(err)
	}
	all := As(Principal{ID: "olga", Kind: ActorPerson, System: []string{GroupAdministrators, GroupAnalysts, GroupDesigners}})
	for _, table := range []string{"fado_users", "fado_keys", "fado_user_changes"} {
		if _, err := s.SQL(ctx, "SELECT * FROM "+table, nil, all); err == nil {
			t.Errorf("a SQL statement reads %s", table)
		}
		def := `{"name": "` + table + `", "variables": {"n": {"type": "number"}}, "access": {"read": "TRUE", "create": "TRUE", "update": "TRUE", "delete": "TRUE"}}`
		if _, err := s.DefineClass(ctx, []byte(def), all); err == nil {
			t.Errorf("a class takes the name %s", table)
		}
	}
}

// A database whose program says itself who is calling keeps no users:
// the calls say so, and no key is let through.
func TestADatabaseThatKeepsNoUsers(t *testing.T) {
	ctx := context.Background()
	s, _ := openStore(t, Options{})
	if s.KeepsUsers() {
		t.Fatal("a database opened without Users keeps users")
	}
	_, err := s.CreateUser(ctx, User{ID: "olga", Kind: ActorPerson}, machine)
	refusedWith(t, "CreateUser", err, CodeNotSupported)
	_, err = s.SetUser(ctx, "olga", UserEdit{}, machine)
	refusedWith(t, "SetUser", err, CodeNotSupported)
	_, err = s.GetUser(ctx, "olga", olga)
	refusedWith(t, "GetUser", err, CodeNotSupported)
	_, err = s.Users(ctx, UserFilter{}, olga)
	refusedWith(t, "Users", err, CodeNotSupported)
	_, err = s.UserChanges(ctx, "olga", olga)
	refusedWith(t, "UserChanges", err, CodeNotSupported)
	_, err = s.CreateKey(ctx, "olga", "", "", machine)
	if e := refusedWith(t, "CreateKey", err, CodeNotSupported); !strings.Contains(e.Hint, "Options.Users") {
		t.Errorf("the refusal does not say how a database keeps users: %+v", e)
	}
	_, err = s.Keys(ctx, "olga", olga)
	refusedWith(t, "Keys", err, CodeNotSupported)
	refusedWith(t, "RevokeKey", s.RevokeKey(ctx, "0000000000", olga), CodeNotSupported)
	_, _, err = s.Whoami(ctx, olga)
	refusedWith(t, "Whoami", err, CodeNotSupported)
	if _, err := s.Authenticate("fado_0000000000_" + strings.Repeat("0", secretLen)); err != ErrUnknownKey {
		t.Errorf("Authenticate answers %v", err)
	}
}

// BenchmarkWhoIsCalling is the check of a key, which a server makes for
// every request (ADR-0057): one hash, one look into what the database
// holds in memory, and no allocation. Its budget is 2 µs.
func BenchmarkWhoIsCalling(b *testing.B) {
	ctx := context.Background()
	s := openUsers(b, filepath.Join(b.TempDir(), "bench.fado"), &still{now: time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)})
	// A database with a thousand users, each with a key.
	var key string
	for i := range 1000 {
		id := "user-" + itoa(i)
		if _, err := s.CreateUser(ctx, User{ID: id, Kind: ActorAgent, Groups: []string{"finance", "sales"}}, machine); err != nil {
			b.Fatal(err)
		}
		k, err := s.CreateKey(ctx, id, "bench", "2027-11-14", machine)
		if err != nil {
			b.Fatal(err)
		}
		if i == 500 {
			key = k.Secret
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if p, err := s.Authenticate(key); err != nil || p.ID != "user-500" {
			b.Fatal(p, err)
		}
	}
}
