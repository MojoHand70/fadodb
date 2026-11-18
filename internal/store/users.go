package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The users, groups and keys a database keeps (ADR-0057).
//
// A database that a server opens for callers of its own keeps who they
// are: a user is a person, an agent or a service known by its id, a
// group is a name on a user, and a key is how a user proves who it is.
// They are kept in the database they belong to (ADR-0056), in three
// tables of the engine's own that no formula and no SQL statement reads,
// and in memory, so that a key is checked without a read.
//
// A host program that says itself who is calling opens a database
// without Options.Users. Such a database keeps none of this: the calls
// below answer not_supported, and the commands they are are not offered.

// User is a person, an agent or a service the database knows by its id.
// The id is what every event records: it never changes and is never
// given to somebody else. A user is disabled and never deleted, so that
// the id in an old event always has its user.
type User struct {
	ID string `json:"id"`
	// Kind is ActorPerson, ActorAgent or ActorService.
	Kind string `json:"kind"`
	// Name is how a screen shows the user.
	Name string `json:"name,omitempty"`
	// Email is a person's company e-mail, in small letters, by which a
	// first sign-in with the company's sign-on finds them (ADR-0071,
	// item 4).
	Email string `json:"email,omitempty"`
	// Groups are the user's groups, which INGROUP answers; System are
	// the system groups it is in (ADR-0052), in a list of their own.
	Groups   []string `json:"groups"`
	System   []string `json:"system_groups"`
	Disabled bool     `json:"disabled"`
}

// UserEdit is what SetUser changes: the members that are set, and no
// other. A list that is set replaces the list the user has.
type UserEdit struct {
	Name     *string
	Email    *string
	Groups   *[]string
	System   *[]string
	Disabled *bool
}

// UserKey is a key of a user, as the database lists it. The database keeps
// the id of a key and a hash of its secret, never the key: Secret holds
// the whole key once, in the answer of CreateKey, and nowhere after.
type UserKey struct {
	// ID names the key in lists and is no secret.
	ID   string `json:"id"`
	User string `json:"user"`
	// Label says where the key is used: "laptop", "nightly import".
	Label     string `json:"label,omitempty"`
	CreatedAt int64  `json:"created_at"`
	CreatedBy string `json:"created_by,omitempty"`
	// Expires is the last day the key holds, as YYYY-MM-DD, in UTC;
	// empty for a key that holds until it is revoked.
	Expires   string `json:"expires,omitempty"`
	RevokedAt int64  `json:"revoked_at,omitempty"`
	RevokedBy string `json:"revoked_by,omitempty"`
	Secret    string `json:"secret,omitempty"`
}

// UserChange is one change to a user or to a key of the user, as the
// database keeps it (ADR-0057, item 8): what was done, who did it, what
// they were, for whom they acted and when. By is empty for a change made
// on the machine that holds the database, by no user.
type UserChange struct {
	// What is created, changed, key_created or key_revoked.
	What string `json:"what"`
	At   int64  `json:"at"`
	By   string `json:"by,omitempty"`
	Kind string `json:"kind,omitempty"`
	For  string `json:"for,omitempty"`
	// User is the user as the change left it; Key is the key that was
	// made or revoked, without its secret.
	User *User    `json:"user,omitempty"`
	Key  *UserKey `json:"key,omitempty"`
}

// The kinds of change to a user.
const (
	UserCreated = "created"
	UserChanged = "changed"
	KeyCreated  = "key_created"
	KeyRevoked  = "key_revoked"
)

// UserFilter narrows a list of users: to the members of a group, of
// either kind, and to the users of one kind. An empty member narrows
// nothing.
type UserFilter struct {
	Group string
	Kind  string
}

// ErrUnknownKey is what Authenticate answers for every key it does not
// let through, whatever is wrong with it, so that a guess learns
// nothing.
var ErrUnknownKey = errors.New("the key is not one the database lets through")

// people is what a database that keeps its users holds of them in
// memory: every user and every key, read when the database is opened
// and written with every commit that changes one. A user and a key are
// never changed in place; a change puts a new one in the map, so that a
// reader keeps what it read.
type people struct {
	on    bool
	mu    sync.RWMutex
	users map[string]*User
	keys  map[string]*heldKey
}

// heldKey is a key as the check of a key needs it.
type heldKey struct {
	UserKey
	hash [sha256.Size]byte
	// ends is the moment the key stops holding, 0 for none.
	ends int64
}

// usersDDL makes the tables of the users (ADR-0057, item 11).
var usersDDL = []string{
	`CREATE TABLE IF NOT EXISTS fado_users (id TEXT PRIMARY KEY, kind TEXT NOT NULL, name TEXT NOT NULL, groups TEXT NOT NULL, system_groups TEXT NOT NULL, disabled INTEGER NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS fado_keys (id TEXT PRIMARY KEY, user TEXT NOT NULL, label TEXT NOT NULL, hash BLOB NOT NULL, created_at INTEGER NOT NULL, created_by TEXT NOT NULL, ends INTEGER NOT NULL, revoked_at INTEGER NOT NULL, revoked_by TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS fado_user_changes (seq INTEGER PRIMARY KEY AUTOINCREMENT, user TEXT NOT NULL, what TEXT NOT NULL, at INTEGER NOT NULL, by_id TEXT NOT NULL, by_kind TEXT NOT NULL, by_for TEXT NOT NULL, detail TEXT NOT NULL)`,
	`CREATE INDEX IF NOT EXISTS fado_user_changes_user ON fado_user_changes (user, seq)`,
}

// KeepsUsers reports whether the database keeps its own users, groups
// and keys: it was opened with Options.Users.
func (s *Store) KeepsUsers() bool { return s.people.on }

// loadPeople reads every user and every key into memory. Open calls it
// for a database that keeps its users.
func (s *Store) loadPeople(ctx context.Context) error {
	users := map[string]*User{}
	rows, err := s.writer.QueryContext(ctx, "SELECT id, kind, name, groups, system_groups, disabled FROM fado_users")
	if err != nil {
		return storageErr("read the users", err)
	}
	for rows.Next() {
		u := &User{}
		var groups, system string
		if err := rows.Scan(&u.ID, &u.Kind, &u.Name, &groups, &system, &u.Disabled); err != nil {
			rows.Close()
			return storageErr("read a user", err)
		}
		if json.Unmarshal([]byte(groups), &u.Groups) != nil || json.Unmarshal([]byte(system), &u.System) != nil {
			rows.Close()
			return &Error{Code: CodeStorage, Message: "the groups of the user " + strconv.Quote(u.ID) + " cannot be read"}
		}
		users[u.ID] = u
	}
	if err := rows.Close(); err != nil {
		return storageErr("read the users", err)
	}
	rows, err = s.writer.QueryContext(ctx, "SELECT user, email FROM fado_user_emails")
	if err != nil {
		return storageErr("read the e-mails of the users", err)
	}
	for rows.Next() {
		var id, email string
		if err := rows.Scan(&id, &email); err != nil {
			rows.Close()
			return storageErr("read an e-mail", err)
		}
		if u := users[id]; u != nil {
			u.Email = email
		}
	}
	if err := rows.Close(); err != nil {
		return storageErr("read the e-mails of the users", err)
	}
	keys := map[string]*heldKey{}
	rows, err = s.writer.QueryContext(ctx, "SELECT id, user, label, hash, created_at, created_by, ends, revoked_at, revoked_by FROM fado_keys")
	if err != nil {
		return storageErr("read the keys", err)
	}
	for rows.Next() {
		k := &heldKey{}
		var hash []byte
		if err := rows.Scan(&k.ID, &k.User, &k.Label, &hash, &k.CreatedAt, &k.CreatedBy, &k.ends, &k.RevokedAt, &k.RevokedBy); err != nil {
			rows.Close()
			return storageErr("read a key", err)
		}
		if len(hash) != sha256.Size {
			rows.Close()
			return &Error{Code: CodeStorage, Message: "the hash of the key " + k.ID + " is not a hash"}
		}
		copy(k.hash[:], hash)
		k.Expires = lastDay(k.ends)
		keys[k.ID] = k
	}
	if err := rows.Close(); err != nil {
		return storageErr("read the keys", err)
	}
	s.people.mu.Lock()
	s.people.users, s.people.keys = users, keys
	s.people.mu.Unlock()
	return nil
}

// The text of a key: fado_, the id of the key, an underscore and the
// secret. The prefix lets a tool that looks for secrets in code find a
// key; the id names the key in lists; the secret is 32 random bytes as
// text.
const (
	keyPrefix = "fado_"
	keyIDLen  = 10
	secretLen = 52
	keyLen    = len(keyPrefix) + keyIDLen + 1 + secretLen
)

// keyText is the alphabet of a key: digits and capital letters without
// the four that are read as another, so that a key read aloud or typed
// is not mistaken.
var keyText = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// Authenticate answers who a key belongs to (ADR-0057, item 9): the
// principal a command sent with the key runs as, built from the user as
// it is now. A key the database does not know, one that was revoked or
// has ended, and the key of a disabled user all answer ErrUnknownKey.
//
// It reads nothing from the database and allocates nothing. The groups
// of the principal are the database's own lists: a caller does not
// change them.
func (s *Store) Authenticate(key string) (Principal, error) {
	if !s.people.on || len(key) != keyLen || key[:len(keyPrefix)] != keyPrefix || key[len(keyPrefix)+keyIDLen] != '_' {
		return Principal{}, ErrUnknownKey
	}
	id := key[len(keyPrefix) : len(keyPrefix)+keyIDLen]
	sum := hashSecret(key[len(keyPrefix)+keyIDLen+1:])
	s.people.mu.RLock()
	k := s.people.keys[id]
	var u *User
	if k != nil {
		u = s.people.users[k.User]
	}
	s.people.mu.RUnlock()
	// The hash is compared whether the id was found or not, and in a
	// time that does not depend on where it differs.
	var held [sha256.Size]byte
	if k != nil {
		held = k.hash
	}
	if subtle.ConstantTimeCompare(sum[:], held[:]) != 1 || k == nil || u == nil {
		return Principal{}, ErrUnknownKey
	}
	if k.RevokedAt != 0 || u.Disabled || (k.ends != 0 && s.now().Unix() >= k.ends) {
		return Principal{}, ErrUnknownKey
	}
	return Principal{ID: u.ID, Kind: u.Kind, Groups: u.Groups, System: u.System}, nil
}

// hashSecret is the hash the database keeps of the secret of a key.
func hashSecret(secret string) [sha256.Size]byte {
	var text [secretLen]byte
	n := copy(text[:], secret)
	return sha256.Sum256(text[:n])
}

// notKept is the refusal of a call about users in a database that keeps
// none.
func notKept() *Error {
	return hintf(CodeNotSupported, "", "this database keeps no users of its own",
		"it was opened by a program that says itself who is calling; a database keeps users, groups and keys when it is opened with Options.Users, as fadodb serve opens it")
}

// keeper is the check of a call that keeps users or keys: the database
// keeps them, and the caller is a database administrator.
func (s *Store) keeper(ctx context.Context, opts []Option, what string) (options, *Error) {
	o, e := s.asked(ctx, opts, "")
	if e != nil {
		return o, e
	}
	if !s.people.on {
		return o, notKept()
	}
	if !o.principal.in(GroupAdministrators) {
		e := hintf(CodeForbidden, "", who(o.principal)+" may not "+what,
			"this is for the members of the system group "+GroupAdministrators+"; an administrator gives it with set_user")
		e.Path = "/system/" + GroupAdministrators
		return o, e
	}
	return o, nil
}

// known is the check of a call that every user may make: the database
// keeps users, and the caller is somebody.
func (s *Store) known(ctx context.Context, opts []Option, what string) (options, *Error) {
	o, e := s.asked(ctx, opts, "")
	if e != nil {
		return o, e
	}
	if !s.people.on {
		return o, notKept()
	}
	if o.principal.ID == "" && !o.principal.in(GroupAdministrators) {
		return o, hintf(CodeForbidden, "", "nobody may not "+what, "this is for every user of the database; the request names no caller")
	}
	return o, nil
}

// checkUserID refuses an id a user cannot have.
func checkUserID(id string) *Error {
	switch {
	case id == "":
		return &Error{Code: CodeRequired, Message: "a user needs an id", Path: "/id"}
	case controlIn(id) >= 0:
		e := hintf(CodeBadValue, "", "the id "+strconv.Quote(id)+" holds a control character",
			"an id is any text without a line break, a tab or another control character")
		e.Path = "/id"
		return e
	case id == engineActor.ID:
		e := hintf(CodeBadValue, "", "the id "+engineActor.ID+" is the engine's own", "it is who the engine acts as; give the user another id")
		e.Path = "/id"
		return e
	}
	return nil
}

// checkGroups checks the two lists of a user and returns them without
// what is given twice. A list is never nil: a user in no group has an
// empty one.
func checkGroups(groups, system []string) ([]string, []string, *Error) {
	p := Principal{Groups: groups, System: system}
	if e := checkPrincipal(p); e != nil {
		return nil, nil, e
	}
	for _, g := range groups {
		if g == "" || controlIn(g) >= 0 {
			e := &Error{Code: CodeBadValue, Message: "the name of a group is a text without a control character, and not empty: " + strconv.Quote(g), Path: "/groups"}
			return nil, nil, e
		}
	}
	return once(groups), once(system), nil
}

// once is a list without what it holds twice, in the order given.
func once(list []string) []string {
	out := make([]string, 0, len(list))
	for _, item := range list {
		seen := false
		for _, kept := range out {
			seen = seen || kept == item
		}
		if !seen {
			out = append(out, item)
		}
	}
	return out
}

// writeUsers runs the statements of one change to the users inside the
// writer's transaction, then gives the new state to the readers.
func (s *Store) writeUsers(ctx context.Context, write func(w execer, now int64) error, publish func()) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errf(CodeClosed, "", "the store is closed")
	}
	w := s.writer
	if _, err := w.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return storageErr("begin", err)
	}
	if err := write(w, s.now().Unix()); err != nil {
		_, _ = w.ExecContext(context.WithoutCancel(ctx), "ROLLBACK")
		return err
	}
	if err := s.endTransaction(ctx); err != nil {
		_, _ = w.ExecContext(context.WithoutCancel(ctx), "ROLLBACK")
		return storageErr("commit", err)
	}
	s.people.mu.Lock()
	publish()
	s.people.mu.Unlock()
	return nil
}

// recordUserChange writes the row of a change with the statements that
// make it.
func recordUserChange(ctx context.Context, w execer, user, what string, by Principal, now int64, detail any) error {
	text, err := json.Marshal(detail)
	if err != nil {
		return &Error{Code: CodeStorage, Message: "write the change of a user: " + err.Error()}
	}
	if _, err := w.ExecContext(ctx, "INSERT INTO fado_user_changes (user, what, at, by_id, by_kind, by_for, detail) VALUES (?, ?, ?, ?, ?, ?, ?)",
		user, what, now, by.ID, by.Kind, by.For, string(text)); err != nil {
		return storageErr("record the change of a user", err)
	}
	return nil
}

func listJSON(list []string) string {
	text, _ := json.Marshal(list)
	return string(text)
}

// CreateUser makes a user. It is a database administrator's.
func (s *Store) CreateUser(ctx context.Context, u User, opts ...Option) (*User, error) {
	o, e := s.keeper(ctx, opts, "make a user")
	if e != nil {
		return nil, e
	}
	if e := refuseAt(o, "change to a user"); e != nil {
		return nil, e
	}
	if e := checkUserID(u.ID); e != nil {
		return nil, e
	}
	if strings.Contains(u.ID, "~") {
		// The ~ marks the agent a person connects (ADR-0071, item 7),
		// which the server makes, so that no other user takes its id.
		e := hintf(CodeBadValue, "", "the id "+strconv.Quote(u.ID)+" holds a ~, which marks an agent a person connected",
			"the server makes those, as ann~claude.ai, when the person allows the agent; give the user an id without a ~")
		e.Path = "/id"
		return nil, e
	}
	switch u.Kind {
	case ActorPerson, ActorAgent, ActorService:
	case "":
		return nil, &Error{Code: CodeRequired, Message: "a user needs a kind: " + ActorPerson + ", " + ActorAgent + " or " + ActorService, Path: "/kind"}
	default:
		return nil, &Error{Code: CodeBadValue, Message: "the kind of a user is " + ActorPerson + ", " + ActorAgent + " or " + ActorService + ", got " + strconv.Quote(u.Kind), Path: "/kind"}
	}
	groups, system, e := checkGroups(u.Groups, u.System)
	if e != nil {
		return nil, e
	}
	made := &User{ID: u.ID, Kind: u.Kind, Name: u.Name, Groups: groups, System: system}
	if u.Email != "" {
		if made.Email, e = s.checkEmail(u.Email, u.Kind, ""); e != nil {
			return nil, e
		}
	}
	s.people.mu.RLock()
	_, exists := s.people.users[u.ID]
	s.people.mu.RUnlock()
	if exists {
		return nil, &Error{Code: CodeDuplicate, Message: "a user " + strconv.Quote(u.ID) + " exists", Hint: "an id is never given to somebody else; change the user with set_user", Path: "/id"}
	}
	err := s.writeUsers(ctx, func(w execer, now int64) error {
		if _, err := w.ExecContext(ctx, "INSERT INTO fado_users (id, kind, name, groups, system_groups, disabled, created_at, updated_at) VALUES (?, ?, ?, ?, ?, 0, ?, ?)",
			made.ID, made.Kind, made.Name, listJSON(made.Groups), listJSON(made.System), now, now); err != nil {
			return storageErr("store the user", err)
		}
		if err := writeEmail(ctx, w, made.ID, made.Email); err != nil {
			return err
		}
		return recordUserChange(ctx, w, made.ID, UserCreated, o.principal, now, made)
	}, func() { s.people.users[made.ID] = made })
	if err != nil {
		return nil, err
	}
	return made, nil
}

// SetUser changes a user: its name, its groups, its system groups, and
// whether it is disabled. It is a database administrator's, who may
// change their own groups too (ADR-0057, the owner's ruling 2); the
// record of the change is the guard. A change that would leave the
// database without an administrator who is not disabled is refused.
func (s *Store) SetUser(ctx context.Context, id string, edit UserEdit, opts ...Option) (*User, error) {
	o, e := s.keeper(ctx, opts, "change a user")
	if e != nil {
		return nil, e
	}
	if e := refuseAt(o, "change to a user"); e != nil {
		return nil, e
	}
	if edit.Name == nil && edit.Email == nil && edit.Groups == nil && edit.System == nil && edit.Disabled == nil {
		return nil, hintf(CodeBadValue, "", "set_user was given nothing to change", "give any of name, email, groups, system_groups and disabled")
	}
	s.people.mu.RLock()
	old := s.people.users[id]
	admins := s.activeAdministrators()
	s.people.mu.RUnlock()
	if old == nil {
		return nil, unknownUser(id)
	}
	changed := *old
	if edit.Name != nil {
		changed.Name = *edit.Name
	}
	if edit.Email != nil {
		changed.Email = ""
		if *edit.Email != "" {
			if changed.Email, e = s.checkEmail(*edit.Email, old.Kind, id); e != nil {
				return nil, e
			}
		}
	}
	groups, system := old.Groups, old.System
	if edit.Groups != nil {
		groups = *edit.Groups
	}
	if edit.System != nil {
		system = *edit.System
	}
	if changed.Groups, changed.System, e = checkGroups(groups, system); e != nil {
		return nil, e
	}
	if edit.Disabled != nil {
		changed.Disabled = *edit.Disabled
	}
	if administers(old) && !administers(&changed) && admins == 1 {
		e := hintf(CodeNotAllowed, "", "the change would leave the database without an administrator",
			strconv.Quote(id)+" is the only administrator who is not disabled; make another user an administrator first")
		e.Path = "/system_groups"
		return nil, e
	}
	err := s.writeUsers(ctx, func(w execer, now int64) error {
		if _, err := w.ExecContext(ctx, "UPDATE fado_users SET name = ?, groups = ?, system_groups = ?, disabled = ?, updated_at = ? WHERE id = ?",
			changed.Name, listJSON(changed.Groups), listJSON(changed.System), changed.Disabled, now, id); err != nil {
			return storageErr("store the user", err)
		}
		if changed.Email != old.Email {
			if err := writeEmail(ctx, w, id, changed.Email); err != nil {
				return err
			}
		}
		return recordUserChange(ctx, w, id, UserChanged, o.principal, now, &changed)
	}, func() { s.people.users[id] = &changed })
	if err != nil {
		return nil, err
	}
	return &changed, nil
}

// administers reports whether a user is an administrator who is not
// disabled.
func administers(u *User) bool {
	if u.Disabled {
		return false
	}
	for _, g := range u.System {
		if g == GroupAdministrators {
			return true
		}
	}
	return false
}

// activeAdministrators counts them. The caller holds the lock.
func (s *Store) activeAdministrators() int {
	n := 0
	for _, u := range s.people.users {
		if administers(u) {
			n++
		}
	}
	return n
}

func unknownUser(id string) *Error {
	return &Error{Code: CodeUnknownUser, Message: "the database has no user " + strconv.Quote(id), Hint: "list_users lists them", Path: "/id"}
}

// GetUser answers one user. Every user of the database may ask: a
// screen shows a name for an id, and an agent finds the manager to ask
// (ADR-0057, the owner's ruling 4).
func (s *Store) GetUser(ctx context.Context, id string, opts ...Option) (*User, error) {
	if _, e := s.known(ctx, opts, "read a user"); e != nil {
		return nil, e
	}
	s.people.mu.RLock()
	u := s.people.users[id]
	s.people.mu.RUnlock()
	if u == nil {
		return nil, unknownUser(id)
	}
	return u, nil
}

// Users lists the users of the database in the order of their ids, the
// disabled ones among them. Every user may ask.
func (s *Store) Users(ctx context.Context, f UserFilter, opts ...Option) ([]*User, error) {
	if _, e := s.known(ctx, opts, "list the users"); e != nil {
		return nil, e
	}
	s.people.mu.RLock()
	list := make([]*User, 0, len(s.people.users))
	for _, u := range s.people.users {
		if f.Kind != "" && u.Kind != f.Kind {
			continue
		}
		if f.Group != "" && !member(u, f.Group) {
			continue
		}
		list = append(list, u)
	}
	s.people.mu.RUnlock()
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	return list, nil
}

// member reports whether a user is in a group, of either kind.
func member(u *User, group string) bool {
	for _, g := range u.Groups {
		if g == group {
			return true
		}
	}
	for _, g := range u.System {
		if g == group {
			return true
		}
	}
	return false
}

// UserChanges lists every change to a user and to its keys, oldest
// first. A database administrator may ask of any user, and a user of
// itself.
func (s *Store) UserChanges(ctx context.Context, id string, opts ...Option) ([]UserChange, error) {
	o, e := s.known(ctx, opts, "read the changes of a user")
	if e != nil {
		return nil, e
	}
	if !o.principal.in(GroupAdministrators) && o.principal.ID != id {
		e := hintf(CodeForbidden, "", who(o.principal)+" may not read the changes of "+strconv.Quote(id),
			"a user reads its own; the changes of another are for the members of the system group "+GroupAdministrators)
		e.Path = "/system/" + GroupAdministrators
		return nil, e
	}
	s.people.mu.RLock()
	_, exists := s.people.users[id]
	s.people.mu.RUnlock()
	if !exists {
		return nil, unknownUser(id)
	}
	rows, err := s.db.QueryContext(ctx, "SELECT what, at, by_id, by_kind, by_for, detail FROM fado_user_changes WHERE user = ? ORDER BY seq", id)
	if err != nil {
		return nil, storageErr("read the changes of a user", err)
	}
	defer rows.Close()
	var changes []UserChange
	for rows.Next() {
		var c UserChange
		var detail string
		if err := rows.Scan(&c.What, &c.At, &c.By, &c.Kind, &c.For, &detail); err != nil {
			return nil, storageErr("read a change of a user", err)
		}
		switch c.What {
		case KeyCreated, KeyRevoked:
			c.Key = &UserKey{}
			err = json.Unmarshal([]byte(detail), c.Key)
		default:
			c.User = &User{}
			err = json.Unmarshal([]byte(detail), c.User)
		}
		if err != nil {
			return nil, &Error{Code: CodeStorage, Message: "a change of the user " + strconv.Quote(id) + " cannot be read"}
		}
		changes = append(changes, c)
	}
	if err := rows.Err(); err != nil {
		return nil, storageErr("read the changes of a user", err)
	}
	return changes, nil
}

// CreateKey makes a key for a user and answers it with its secret, this
// once: the database keeps the id of the key and a hash of the secret,
// and a key that is lost is replaced, not found again. label says where
// the key is used. expires, when given, is the last day the key holds,
// as YYYY-MM-DD in UTC. It is a database administrator's (ADR-0057, the
// owner's ruling 3).
func (s *Store) CreateKey(ctx context.Context, user, label, expires string, opts ...Option) (*UserKey, error) {
	o, e := s.keeper(ctx, opts, "make a key")
	if e != nil {
		return nil, e
	}
	if e := refuseAt(o, "change to a key"); e != nil {
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
	var ends int64
	if expires != "" {
		day, err := time.Parse("2006-01-02", expires)
		if err != nil {
			return nil, &Error{Code: CodeBadValue, Message: "expires is " + strconv.Quote(expires) + ", and the last day of a key is written YYYY-MM-DD", Path: "/expires"}
		}
		ends = day.Add(24 * time.Hour).Unix()
		if ends <= s.now().Unix() {
			return nil, &Error{Code: CodeBadValue, Message: "expires is " + expires + ", a day that has passed", Hint: "leave expires out for a key that holds until it is revoked", Path: "/expires"}
		}
	}
	var random [7 + 32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, &Error{Code: CodeStorage, Message: "make the key: " + err.Error()}
	}
	id := keyText.EncodeToString(random[:7])[:keyIDLen]
	secret := keyText.EncodeToString(random[7:])
	held := &heldKey{UserKey: UserKey{ID: id, User: user, Label: label, CreatedBy: o.principal.ID, Expires: expires}, hash: hashSecret(secret), ends: ends}
	err := s.writeUsers(ctx, func(w execer, now int64) error {
		held.CreatedAt = now
		if _, err := w.ExecContext(ctx, "INSERT INTO fado_keys (id, user, label, hash, created_at, created_by, ends, revoked_at, revoked_by) VALUES (?, ?, ?, ?, ?, ?, ?, 0, '')",
			held.ID, held.User, held.Label, held.hash[:], now, held.CreatedBy, held.ends); err != nil {
			return storageErr("store the key", err)
		}
		return recordUserChange(ctx, w, user, KeyCreated, o.principal, now, &held.UserKey)
	}, func() { s.people.keys[held.ID] = held })
	if err != nil {
		return nil, err
	}
	made := held.UserKey
	made.Secret = keyPrefix + id + "_" + secret
	return &made, nil
}

// lastDay is the last day a key holds, for the moment it ends.
func lastDay(ends int64) string {
	if ends == 0 {
		return ""
	}
	return time.Unix(ends, 0).UTC().Add(-24 * time.Hour).Format("2006-01-02")
}

// Keys lists the keys of a user, oldest first, the revoked ones among
// them, and never a secret. user left empty is the caller. A database
// administrator may ask of any user, and a user of itself.
func (s *Store) Keys(ctx context.Context, user string, opts ...Option) ([]*UserKey, error) {
	o, e := s.known(ctx, opts, "list keys")
	if e != nil {
		return nil, e
	}
	if user == "" {
		user = o.principal.ID
	}
	if !o.principal.in(GroupAdministrators) && o.principal.ID != user {
		e := hintf(CodeForbidden, "", who(o.principal)+" may not list the keys of "+strconv.Quote(user),
			"a user lists its own; the keys of another are for the members of the system group "+GroupAdministrators)
		e.Path = "/system/" + GroupAdministrators
		return nil, e
	}
	s.people.mu.RLock()
	_, exists := s.people.users[user]
	list := make([]*UserKey, 0, 4)
	for _, k := range s.people.keys {
		if k.User == user {
			list = append(list, &k.UserKey)
		}
	}
	s.people.mu.RUnlock()
	if !exists {
		e := unknownUser(user)
		e.Path = "/user"
		return nil, e
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].CreatedAt != list[j].CreatedAt {
			return list[i].CreatedAt < list[j].CreatedAt
		}
		return list[i].ID < list[j].ID
	})
	return list, nil
}

// RevokeKey ends a key, by its id: the next command sent with it is
// refused. A database administrator may revoke any key, and a user its
// own, so that a key that leaked is dead at once. A key is revoked and
// never deleted; revoking one that is revoked changes nothing.
func (s *Store) RevokeKey(ctx context.Context, key string, opts ...Option) error {
	o, e := s.known(ctx, opts, "revoke a key")
	if e != nil {
		return e
	}
	if e := refuseAt(o, "change to a key"); e != nil {
		return e
	}
	s.people.mu.RLock()
	held := s.people.keys[key]
	s.people.mu.RUnlock()
	// A key that is another's is, for a user, a key that does not exist.
	if held == nil || (!o.principal.in(GroupAdministrators) && held.User != o.principal.ID) {
		return &Error{Code: CodeUnknownKey, Message: "the database has no key " + strconv.Quote(key), Hint: "a key is named by its id, as list_keys shows it, and not by the key itself", Path: "/key"}
	}
	if held.RevokedAt != 0 {
		return nil
	}
	revoked := *held
	return s.writeUsers(ctx, func(w execer, now int64) error {
		revoked.RevokedAt, revoked.RevokedBy = now, o.principal.ID
		if _, err := w.ExecContext(ctx, "UPDATE fado_keys SET revoked_at = ?, revoked_by = ? WHERE id = ?", now, revoked.RevokedBy, key); err != nil {
			return storageErr("revoke the key", err)
		}
		return recordUserChange(ctx, w, revoked.User, KeyRevoked, o.principal, now, &revoked.UserKey)
	}, func() { s.people.keys[key] = &revoked })
}

// Whoami answers who the database takes the caller for: the user, of
// what kind and in which groups, and whom it acts for when the call
// names somebody. It is for an agent that starts with nothing but a key.
func (s *Store) Whoami(ctx context.Context, opts ...Option) (*User, string, error) {
	o, e := s.asked(ctx, opts, "")
	if e != nil {
		return nil, "", e
	}
	if !s.people.on {
		return nil, "", notKept()
	}
	p := o.principal
	s.people.mu.RLock()
	u := s.people.users[p.ID]
	s.people.mu.RUnlock()
	if u == nil {
		// A caller the program named itself, such as the command-line
		// tool on the machine: what it was given is what it is.
		u = &User{ID: p.ID, Kind: p.Kind, Groups: once(p.Groups), System: once(p.System)}
	}
	return u, p.For, nil
}
