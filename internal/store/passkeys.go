package store

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/binary"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/MojoHand70/fadodb/internal/webauthn"
)

// Passkeys and invitations (ADR-0088): an administrator invites a
// person by a link, good once and for 7 days, with which the person adds
// their first passkey; more are added on their account page. A person
// signs in with any of them. The database keeps a passkey's public half,
// the name of its device, and its counter; never its private half,
// which the server never sees. Every invitation, every passkey added or
// removed, and every sign-in is recorded.

// WayPasskey is the way of a sign-in with a passkey, as the log of
// sign-ins names it.
const WayPasskey = "passkey"

// The kinds of change to a user that invitations and passkeys add.
const (
	InvitationMade      = "invitation_made"
	InvitationWithdrawn = "invitation_withdrawn"
	PasskeyAdded        = "passkey_added"
	PasskeyRemoved      = "passkey_removed"
	// PasskeyCounterFell is a sign-in refused because the device's
	// counter did not rise, as a copied passkey shows.
	PasskeyCounterFell = "passkey_counter_fell"
)

// How a passkey was added.
const (
	AddedWithInvitation = "invitation"
	AddedOnTheAccount   = "account"
)

// The rules of an invitation and of a challenge (ADR-0088, items 2 and
// 5).
const (
	invitationPrefix = "fadoi_"
	invitationLasts  = 7 * 24 * time.Hour
	challengeLasts   = 5 * time.Minute
	maxDeviceName    = 60
)

// passkeysDDL makes the tables of invitations and passkeys, beside the
// users'. They are made on every open, so a database of the same engine
// schema gains them. A passkey removed is kept, with when and by whom,
// and opens nothing.
var passkeysDDL = []string{
	`CREATE TABLE IF NOT EXISTS fado_invitations (id TEXT PRIMARY KEY, user TEXT NOT NULL, hash BLOB NOT NULL, made_at INTEGER NOT NULL, made_by TEXT NOT NULL, ends INTEGER NOT NULL, ended_at INTEGER NOT NULL, ended_by TEXT NOT NULL, ended_how TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS fado_passkeys (id TEXT PRIMARY KEY, credential BLOB NOT NULL, user TEXT NOT NULL, handle BLOB NOT NULL, name TEXT NOT NULL, public_key BLOB NOT NULL, alg INTEGER NOT NULL, sign_count INTEGER NOT NULL, synced INTEGER NOT NULL, added_at INTEGER NOT NULL, added_with TEXT NOT NULL, used_at INTEGER NOT NULL, removed_at INTEGER NOT NULL, removed_by TEXT NOT NULL)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS fado_passkeys_credential ON fado_passkeys (credential)`,
	`CREATE INDEX IF NOT EXISTS fado_passkeys_user ON fado_passkeys (user, added_at)`,
	// The passkey a session began with, so that removing it ends them.
	`CREATE TABLE IF NOT EXISTS fado_session_passkeys (session TEXT PRIMARY KEY, passkey TEXT NOT NULL)`,
}

// Invitation is an invitation as the database lists it: whom it is for,
// when and by whom it was made, and when it ends. Token is its secret,
// in the answer that makes it and nowhere after.
type Invitation struct {
	User   string `json:"user"`
	MadeAt int64  `json:"made_at"`
	MadeBy string `json:"made_by,omitempty"`
	Ends   int64  `json:"ends"`
	Token  string `json:"token,omitempty"`
}

// Invited is what the page of an invitation says: who it is for, and
// who made it, each by the name a screen shows.
type Invited struct {
	User   string
	Name   string
	By     string
	ByName string
	// Passkeys says the person holds a passkey already, which the
	// device that opened the invitation may hold too.
	Passkeys bool
}

// Passkey is a passkey as the database lists it: its id, which is no
// secret; whose it is; the name of its device; whether it is synced,
// following its owner to a new phone; when and how it was added and
// last used; and, for one removed, when and by whom.
type Passkey struct {
	ID        string `json:"id"`
	User      string `json:"user"`
	Name      string `json:"name"`
	Synced    bool   `json:"synced"`
	AddedAt   int64  `json:"added_at"`
	AddedWith string `json:"added_with"`
	UsedAt    int64  `json:"used_at,omitempty"`
	RemovedAt int64  `json:"removed_at,omitempty"`
	RemovedBy string `json:"removed_by,omitempty"`
}

// The questions a challenge answers, and so what it may be used for.
const (
	askSignIn = 's'
	askInvite = 'i'
	askAdd    = 'a'
	// askPhone uses, on a phone, a passkey the person holds there
	// already, for a code for the phone; askHeld, on the device that
	// opened an invitation, for the invitation.
	askPhone = 'p'
	askHeld  = 'h'
)

// A challenge is good once and for 5 minutes, and only for what it was
// given for. It keeps what it needs in itself, signed with a key the
// server makes when the database is opened, so that a page that asks for
// one writes nothing: what it was given for, when it ends, the 16 bytes
// a device knows the person by, random bytes, and the signature. Only a
// challenge that was used is remembered, until it ends.
const (
	challengeLen = 1 + 8 + 16 + 16 + 16
	macLen       = 16
)

// challenge makes a challenge for a question, bound to what it is for:
// the invitation, or the person adding a passkey.
func (g *signing) challenge(ask byte, bound string, handle []byte, ends int64) ([]byte, error) {
	c := make([]byte, 0, challengeLen)
	c = append(c, ask)
	c = binary.BigEndian.AppendUint64(c, uint64(ends))
	h := make([]byte, 16)
	copy(h, handle)
	c = append(c, h...)
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	c = append(c, nonce...)
	return append(c, g.mac(c, bound)...), nil
}

func (g *signing) mac(c []byte, bound string) []byte {
	g.mu.Lock()
	key := g.key
	g.mu.Unlock()
	m := hmac.New(sha256.New, key[:])
	m.Write(c)
	m.Write([]byte(bound))
	return m.Sum(nil)[:macLen]
}

// challengeOf reads a challenge the server gave, for a question and
// what it is for, and answers the handle it holds; false for one it did
// not give, one that ended, or one that was used.
func (g *signing) challengeOf(c []byte, ask byte, bound string, now int64) ([]byte, bool) {
	if len(c) != challengeLen || c[0] != ask {
		return nil, false
	}
	if !hmac.Equal(c[challengeLen-macLen:], g.mac(c[:challengeLen-macLen], bound)) {
		return nil, false
	}
	if int64(binary.BigEndian.Uint64(c[1:9])) <= now {
		return nil, false
	}
	g.mu.Lock()
	_, used := g.used[string(c)]
	g.mu.Unlock()
	if used {
		return nil, false
	}
	return c[9:25], true
}

// spend marks a challenge used, once its answer held, and answers false
// when it was used meanwhile. The ones that ended are forgotten.
func (g *signing) spend(c []byte, now int64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	for k, ends := range g.used {
		if ends <= now {
			delete(g.used, k)
		}
	}
	if _, used := g.used[string(c)]; used {
		return false
	}
	g.used[string(c)] = int64(binary.BigEndian.Uint64(c[1:9]))
	return true
}

// partyOf is the server as a passkey knows it, from the origin of its
// pages.
func partyOf(origin string) (webauthn.Party, *Error) {
	p, err := webauthn.PartyOf(origin)
	if err != nil {
		return p, &Error{Code: CodeBadValue, Message: err.Error()}
	}
	return p, nil
}

// person is the user a passkey or an invitation may be for: a person,
// enabled.
func (s *Store) person(user string) (*User, *Error) {
	s.people.mu.RLock()
	u := s.people.users[user]
	s.people.mu.RUnlock()
	if u == nil {
		e := unknownUser(user)
		e.Path = "/user"
		return nil, e
	}
	if u.Kind != ActorPerson {
		return nil, &Error{Code: CodeBadValue, Path: "/user", Message: user + " is " + article(u.Kind) + ", which calls with a key and has no passkey",
			Hint: "make it a key with create_key; a passkey is a person's"}
	}
	if u.Disabled {
		return nil, &Error{Code: CodeBadValue, Path: "/user", Message: user + " is disabled", Hint: "enable the user with set_user first"}
	}
	return u, nil
}

// screenName is how a screen names a user: its name, or its id.
func screenName(u *User) string {
	if u.Name != "" {
		return u.Name
	}
	return u.ID
}

// CreateInvitation makes an invitation for a person, good once and for 7
// days, with which they add their first passkey, and answers its secret,
// this once (ADR-0088, item 2). An invitation of theirs that still holds
// ends. A database administrator makes one, or the machine that holds
// the database; the link goes to the person.
func (s *Store) CreateInvitation(ctx context.Context, user string, opts ...Option) (*Invitation, error) {
	o, e := s.keeper(ctx, opts, "make an invitation")
	if e != nil {
		return nil, e
	}
	if e := refuseAt(o, "change to a user"); e != nil {
		return nil, e
	}
	if _, e := s.person(user); e != nil {
		return nil, e
	}
	id, secret, err := secretText()
	if err != nil {
		return nil, &Error{Code: CodeStorage, Message: "make the invitation: " + err.Error()}
	}
	inv := &Invitation{User: user, MadeBy: o.principal.ID}
	sum := hashSecret(secret)
	err = s.writeUsers(ctx, func(w execer, now int64) error {
		inv.MadeAt, inv.Ends = now, now+int64(invitationLasts/time.Second)
		if _, err := w.ExecContext(ctx, "UPDATE fado_invitations SET ended_at = ?, ended_by = ?, ended_how = 'replaced' WHERE user = ? AND ended_at = 0 AND ends > ?", now, o.principal.ID, user, now); err != nil {
			return storageErr("end the invitation before", err)
		}
		if _, err := w.ExecContext(ctx, "INSERT INTO fado_invitations (id, user, hash, made_at, made_by, ends, ended_at, ended_by, ended_how) VALUES (?, ?, ?, ?, ?, ?, 0, '', '')",
			id, user, sum[:], now, inv.MadeBy, inv.Ends); err != nil {
			return storageErr("store the invitation", err)
		}
		return recordUserChange(ctx, w, user, InvitationMade, o.principal, now, inv)
	}, func() {})
	if err != nil {
		return nil, err
	}
	inv.Token = invitationPrefix + id + "_" + secret
	return inv, nil
}

// Invitations lists the invitations that still hold, oldest first,
// never a secret: of one person, or of everybody when user is empty. It
// is a database administrator's.
func (s *Store) Invitations(ctx context.Context, user string, opts ...Option) ([]*Invitation, error) {
	if _, e := s.keeper(ctx, opts, "list the invitations"); e != nil {
		return nil, e
	}
	query, args := "SELECT user, made_at, made_by, ends FROM fado_invitations WHERE ended_at = 0 AND ends > ?", []any{s.now().Unix()}
	if user != "" {
		query += " AND user = ?"
		args = append(args, user)
	}
	rows, err := s.db.QueryContext(ctx, query+" ORDER BY made_at, id", args...)
	if err != nil {
		return nil, storageErr("read the invitations", err)
	}
	defer rows.Close()
	out := []*Invitation{}
	for rows.Next() {
		inv := &Invitation{}
		if err := rows.Scan(&inv.User, &inv.MadeAt, &inv.MadeBy, &inv.Ends); err != nil {
			return nil, storageErr("read an invitation", err)
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

// WithdrawInvitation ends the invitation of a person that still holds:
// its link lets nobody in. It is a database administrator's.
func (s *Store) WithdrawInvitation(ctx context.Context, user string, opts ...Option) error {
	o, e := s.keeper(ctx, opts, "withdraw an invitation")
	if e != nil {
		return e
	}
	if e := refuseAt(o, "change to a user"); e != nil {
		return e
	}
	return s.writeUsers(ctx, func(w execer, now int64) error {
		res, err := w.ExecContext(ctx, "UPDATE fado_invitations SET ended_at = ?, ended_by = ?, ended_how = 'withdrawn' WHERE user = ? AND ended_at = 0 AND ends > ?", now, o.principal.ID, user, now)
		if err != nil {
			return storageErr("withdraw the invitation", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return &Error{Code: CodeUnknownInvitation, Path: "/user", Message: "the database has no invitation for " + strconv.Quote(user) + " that holds",
				Hint: "list_invitations lists the ones that hold; one used, ended or withdrawn lets nobody in"}
		}
		return recordUserChange(ctx, w, user, InvitationWithdrawn, o.principal, now, map[string]any{})
	}, func() {})
}

// invitation finds the invitation of a token that still holds, for a
// person who is enabled.
func (s *Store) invitation(ctx context.Context, token string) (id string, u *User, ok bool) {
	id, sum, ok := splitToken(token, invitationPrefix)
	if !ok || !s.people.on {
		return "", nil, false
	}
	var user string
	var hash []byte
	var ends, ended int64
	err := s.db.QueryRowContext(ctx, "SELECT user, hash, ends, ended_at FROM fado_invitations WHERE id = ?", id).Scan(&user, &hash, &ends, &ended)
	if err != nil || subtle.ConstantTimeCompare(sum[:], hash) != 1 || ended != 0 || s.now().Unix() >= ends {
		return "", nil, false
	}
	u, e := s.person(user)
	if e != nil {
		return "", nil, false
	}
	return id, u, true
}

// Invited answers whom an invitation is for and who made it, while it
// holds, so that its page greets the person.
func (s *Store) Invited(ctx context.Context, token string) (*Invited, error) {
	id, u, ok := s.invitation(ctx, token)
	if !ok {
		return nil, refused()
	}
	var by string
	if err := s.db.QueryRowContext(ctx, "SELECT made_by FROM fado_invitations WHERE id = ?", id).Scan(&by); err != nil {
		return nil, storageErr("read the invitation", err)
	}
	held, err := s.credentials(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	out := &Invited{User: u.ID, Name: screenName(u), By: by, ByName: by, Passkeys: len(held) > 0}
	s.people.mu.RLock()
	if maker := s.people.users[by]; maker != nil {
		out.ByName = screenName(maker)
	}
	s.people.mu.RUnlock()
	return out, nil
}

// handleOf is the 16 bytes a device knows a person by: the ones of
// their passkeys, so that every device of theirs knows them alike, or
// new random ones for their first. A device never knows their id.
func (s *Store) handleOf(ctx context.Context, user string) ([]byte, error) {
	var handle []byte
	err := s.db.QueryRowContext(ctx, "SELECT handle FROM fado_passkeys WHERE user = ? ORDER BY added_at, rowid LIMIT 1", user).Scan(&handle)
	if err == sql.ErrNoRows {
		handle = make([]byte, 16)
		_, err = rand.Read(handle)
	}
	if err != nil {
		return nil, storageErr("read the person's handle", err)
	}
	return handle, nil
}

// credentials are the ids of a person's passkeys, which a device asked
// to make one is told not to make twice.
func (s *Store) credentials(ctx context.Context, user string) ([][]byte, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT credential FROM fado_passkeys WHERE user = ? AND removed_at = 0 ORDER BY added_at, rowid", user)
	if err != nil {
		return nil, storageErr("read the passkeys", err)
	}
	defer rows.Close()
	var out [][]byte
	for rows.Next() {
		var c []byte
		if err := rows.Scan(&c); err != nil {
			return nil, storageErr("read a passkey", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// makeQuestion is the question that asks a browser to make a passkey for
// a person, with a challenge for what it is for.
func (s *Store) makeQuestion(ctx context.Context, u *User, origin string, ask byte, bound string) (string, error) {
	party, e := partyOf(origin)
	if e != nil {
		return "", e
	}
	handle, err := s.handleOf(ctx, u.ID)
	if err != nil {
		return "", err
	}
	exclude, err := s.credentials(ctx, u.ID)
	if err != nil {
		return "", err
	}
	c, err := s.signingIn().challenge(ask, bound, handle, s.now().Add(challengeLasts).Unix())
	if err != nil {
		return "", &Error{Code: CodeStorage, Message: "make the challenge: " + err.Error()}
	}
	return party.CreationOptions(webauthn.User{Handle: handle, Name: u.ID, Display: screenName(u)}, c, exclude, challengeLasts), nil
}

// InvitationQuestion is the question the page of an invitation gives the
// browser, to make the person's passkey: in WebAuthn's JSON, for the
// origin of the server's pages.
func (s *Store) InvitationQuestion(ctx context.Context, token, origin string) (string, error) {
	id, u, ok := s.invitation(ctx, token)
	if !ok {
		return "", refused()
	}
	return s.makeQuestion(ctx, u, origin, askInvite, id)
}

// deviceName checks the name a person gives the device a passkey is on.
func deviceName(name string) (string, *Error) {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" || utf8.RuneCountInString(name) > maxDeviceName || controlIn(name) >= 0 {
		return "", &Error{Code: CodeBadValue, Path: "/name", Message: "a device is named with 1 to " + strconv.Itoa(maxDeviceName) + " characters, such as Ann's iPhone",
			Hint: "the name tells the person's passkeys apart on their account page"}
	}
	return name, nil
}

// made checks a browser's answer to a question to make a passkey, and
// answers the passkey and the handle the challenge held.
func (s *Store) made(answer []byte, origin string, ask byte, bound string) (*webauthn.Made, []byte, []byte, error) {
	party, e := partyOf(origin)
	if e != nil {
		return nil, nil, nil, e
	}
	c, err := webauthn.ParseCreation(answer)
	if err != nil {
		return nil, nil, nil, err
	}
	challenge, err := webauthn.ChallengeOf(c.ClientData)
	if err != nil {
		return nil, nil, nil, err
	}
	g := s.signingIn()
	handle, ok := g.challengeOf(challenge, ask, bound, s.now().Unix())
	if !ok {
		return nil, nil, nil, webauthn.ErrChallenge
	}
	m, err := party.CheckCreation(c, challenge)
	if err != nil {
		return nil, nil, nil, err
	}
	return m, append([]byte(nil), handle...), challenge, nil
}

// insertPasskey writes a new passkey and records it.
func insertPasskey(ctx context.Context, w execer, key *Passkey, m *webauthn.Made, handle []byte, by Principal, now int64) error {
	key.AddedAt = now
	if _, err := w.ExecContext(ctx, "INSERT INTO fado_passkeys (id, credential, user, handle, name, public_key, alg, sign_count, synced, added_at, added_with, used_at, removed_at, removed_by) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 0, '')",
		key.ID, m.Credential, key.User, handle, key.Name, m.Key, m.Alg, int64(m.Count), m.Synced, now, key.AddedWith); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return &Error{Code: CodeDuplicate, Path: "/response", Message: "the database has this passkey already"}
		}
		return storageErr("store the passkey", err)
	}
	return recordUserChange(ctx, w, key.User, PasskeyAdded, by, now, key)
}

// newPasskey is the passkey a person adds, before it is written.
func newPasskey(user, name, with string, m *webauthn.Made) (*Passkey, error) {
	id, _, err := secretText()
	if err != nil {
		return nil, &Error{Code: CodeStorage, Message: "name the passkey: " + err.Error()}
	}
	return &Passkey{ID: id, User: user, Name: name, Synced: m.Synced, AddedWith: with}, nil
}

// UseInvitation takes the passkey a person made with their invitation,
// on the device they named, and gives their session: the invitation
// holds no more. origin is the one of the server's pages, address where
// the request came from. An invitation that does not hold and a passkey
// that does not are refused in the same words.
func (s *Store) UseInvitation(ctx context.Context, token string, answer []byte, device, origin, address string, lengths SessionLengths) (*Session, error) {
	g := s.signingIn()
	if wait := g.waitFor(s.now().Unix(), "a:"+address); wait > 0 {
		return nil, tooMany(wait)
	}
	id, u, ok := s.invitation(ctx, token)
	if !ok {
		return nil, s.refuseSignIn(ctx, "", WayPasskey, address)
	}
	return s.useInvitation(ctx, id, u, answer, device, origin, address, lengths)
}

// useInvitation takes the passkey a person made with the invitation id,
// from its page or from the link of its code for the phone.
func (s *Store) useInvitation(ctx context.Context, id string, u *User, answer []byte, device, origin, address string, lengths SessionLengths) (*Session, error) {
	g := s.signingIn()
	name, e := deviceName(device)
	if e != nil {
		return nil, e
	}
	m, handle, challenge, err := s.made(answer, origin, askInvite, id)
	if err != nil {
		if e := new(Error); errors.As(err, &e) {
			return nil, e
		}
		return nil, s.refuseSignIn(ctx, u.ID, WayPasskey, address)
	}
	key, err := newPasskey(u.ID, name, AddedWithInvitation, m)
	if err != nil {
		return nil, err
	}
	if !g.spend(challenge, s.now().Unix()) {
		return nil, s.refuseSignIn(ctx, u.ID, WayPasskey, address)
	}
	me := Principal{ID: u.ID, Kind: u.Kind}
	return s.beginSession(ctx, u.ID, key.ID, WayPasskey, address, lengths, func(w execer, now int64) error {
		res, err := w.ExecContext(ctx, "UPDATE fado_invitations SET ended_at = ?, ended_by = ?, ended_how = 'used' WHERE id = ? AND ended_at = 0 AND ends > ?", now, u.ID, id, now)
		if err != nil {
			return storageErr("use the invitation", err)
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return refused()
		}
		return insertPasskey(ctx, w, key, m, handle, me, now)
	})
}

// beginSession writes a session begun with a passkey, the way given,
// with what else the sign-in writes, and records the sign-in.
func (s *Store) beginSession(ctx context.Context, user, passkey, way, address string, lengths SessionLengths, also func(w execer, now int64) error) (*Session, error) {
	g := s.signingIn()
	lengths = fitLengths(lengths)
	id, secret, err := secretText()
	if err != nil {
		return nil, &Error{Code: CodeStorage, Message: "make the session: " + err.Error()}
	}
	held := &heldSession{Session: Session{ID: id, User: user, Way: way, Address: address, Passkey: passkey, Idle: int64(lengths.Idle / time.Second)}, hash: hashSecret(secret)}
	err = s.writeUsers(ctx, func(w execer, now int64) error {
		if err := also(w, now); err != nil {
			return err
		}
		held.CreatedAt, held.UsedAt, held.written = now, now, now
		held.Ends = now + int64(lengths.Max/time.Second)
		if _, err := w.ExecContext(ctx, "INSERT INTO fado_sessions (id, user, hash, way, address, created_at, used_at, ends, idle, ended_at, ended_by) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, '')",
			held.ID, held.User, held.hash[:], held.Way, held.Address, now, now, held.Ends, held.Idle); err != nil {
			return storageErr("store the session", err)
		}
		if _, err := w.ExecContext(ctx, "INSERT INTO fado_session_passkeys (session, passkey) VALUES (?, ?)", held.ID, passkey); err != nil {
			return storageErr("store the session's passkey", err)
		}
		return s.logSignIn(ctx, w, now, user, way, address, true)
	}, func() {})
	if err != nil {
		return nil, err
	}
	g.mu.Lock()
	g.sessions[held.ID] = held
	g.mu.Unlock()
	g.succeeded("u:"+user, "a:"+address)
	made := held.Session
	made.Token = sessionPrefix + id + "_" + secret
	return &made, nil
}

// SignInQuestion is the question the sign-in page gives the browser: to
// use any passkey it holds for the server's host. The passkey says who
// the person is.
func (s *Store) SignInQuestion(origin string) (string, error) {
	party, e := partyOf(origin)
	if e != nil {
		return "", e
	}
	c, err := s.signingIn().challenge(askSignIn, "", nil, s.now().Add(challengeLasts).Unix())
	if err != nil {
		return "", &Error{Code: CodeStorage, Message: "make the challenge: " + err.Error()}
	}
	return party.RequestOptions(c, challengeLasts), nil
}

// SignInWithPasskey takes a browser's answer to the sign-in page's
// question and gives the person's session (ADR-0088, items 4 and 5). A
// passkey the database does not have or removed, the passkey of a
// disabled user, a challenge it did not give or that was used, and an
// answer that does not hold are refused in the same words; a counter
// that did not rise is recorded beside the refusal.
func (s *Store) SignInWithPasskey(ctx context.Context, answer []byte, origin, address string, lengths SessionLengths) (*Session, error) {
	if !s.people.on {
		return nil, notKept()
	}
	g := s.signingIn()
	now := s.now().Unix()
	if wait := g.waitFor(now, "a:"+address); wait > 0 {
		return nil, tooMany(wait)
	}
	use, err := s.checkUse(ctx, answer, origin, askSignIn, "", address, now)
	if err != nil {
		return nil, err
	}
	if !g.spend(use.challenge, now) {
		return nil, s.refuseSignIn(ctx, use.user, WayPasskey, address)
	}
	return s.beginSession(ctx, use.user, use.id, WayPasskey, address, lengths, func(w execer, now int64) error {
		return usePasskey(ctx, w, use, now)
	})
}

// InvitationSignInQuestion is the question the page of an invitation
// gives a device that holds a passkey of the person's already, which a
// synced passkey may be: to use it, with the invitation. A device asked
// to make another passkey there is refused by the device itself.
func (s *Store) InvitationSignInQuestion(ctx context.Context, token, origin string) (string, error) {
	id, _, ok := s.invitation(ctx, token)
	if !ok {
		return "", refused()
	}
	return s.useQuestion(origin, askHeld, id)
}

// useQuestion is the question that asks a browser to use a passkey it
// holds, with a challenge for what it is for.
func (s *Store) useQuestion(origin string, ask byte, bound string) (string, error) {
	party, e := partyOf(origin)
	if e != nil {
		return "", e
	}
	c, err := s.signingIn().challenge(ask, bound, nil, s.now().Add(challengeLasts).Unix())
	if err != nil {
		return "", &Error{Code: CodeStorage, Message: "make the challenge: " + err.Error()}
	}
	return party.RequestOptions(c, challengeLasts), nil
}

// SignInWithInvitation takes the passkey of the person's that the device
// holds already, the answer to InvitationSignInQuestion, and gives the
// person's session. The invitation is used, and holds no more. A passkey
// of another person is refused as any that does not hold.
func (s *Store) SignInWithInvitation(ctx context.Context, token string, answer []byte, origin, address string, lengths SessionLengths) (*Session, error) {
	if !s.people.on {
		return nil, notKept()
	}
	if wait := s.signingIn().waitFor(s.now().Unix(), "a:"+address); wait > 0 {
		return nil, tooMany(wait)
	}
	id, u, ok := s.invitation(ctx, token)
	if !ok {
		return nil, s.refuseSignIn(ctx, "", WayPasskey, address)
	}
	return s.signInHeld(ctx, u, id, askHeld, id, answer, origin, address, lengths)
}

// signInHeld checks the use of a passkey of the person's, for the
// question asked and what it is for, and begins their session with it:
// with their invitation, if one is given, which is used.
func (s *Store) signInHeld(ctx context.Context, u *User, invitation string, ask byte, bound string, answer []byte, origin, address string, lengths SessionLengths) (*Session, error) {
	g := s.signingIn()
	now := s.now().Unix()
	use, err := s.checkUse(ctx, answer, origin, ask, bound, address, now)
	if err != nil {
		return nil, err
	}
	if use.user != u.ID || !g.spend(use.challenge, now) {
		return nil, s.refuseSignIn(ctx, u.ID, WayPasskey, address)
	}
	return s.beginSession(ctx, u.ID, use.id, WayPasskey, address, lengths, func(w execer, now int64) error {
		if invitation != "" {
			res, err := w.ExecContext(ctx, "UPDATE fado_invitations SET ended_at = ?, ended_by = ?, ended_how = 'used' WHERE id = ? AND ended_at = 0 AND ends > ?", now, u.ID, invitation, now)
			if err != nil {
				return storageErr("use the invitation", err)
			}
			if n, _ := res.RowsAffected(); n != 1 {
				return refused()
			}
		}
		return usePasskey(ctx, w, use, now)
	})
}

// passkeyUse is a passkey a browser used, once its answer held: the
// passkey, its person, the challenge it answered and what the device
// said.
type passkeyUse struct {
	id, user  string
	challenge []byte
	used      *webauthn.Used
}

// checkUse checks a browser's answer to a question that asks it to use a
// passkey, for that question and what it is for: a challenge the server
// gave and nobody used, a passkey the database holds of a person it
// has, and an answer that holds. Each is refused in the same words. The
// challenge is not spent: the caller spends it once it takes the use.
func (s *Store) checkUse(ctx context.Context, answer []byte, origin string, ask byte, bound, address string, now int64) (*passkeyUse, error) {
	g := s.signingIn()
	party, e := partyOf(origin)
	if e != nil {
		return nil, e
	}
	a, err := webauthn.ParseAssertion(answer)
	if err != nil {
		return nil, s.refuseSignIn(ctx, "", WayPasskey, address)
	}
	challenge, err := webauthn.ChallengeOf(a.ClientData)
	if err != nil {
		return nil, s.refuseSignIn(ctx, "", WayPasskey, address)
	}
	if _, ok := g.challengeOf(challenge, ask, bound, now); !ok {
		return nil, s.refuseSignIn(ctx, "", WayPasskey, address)
	}
	var id, user string
	var handle, key []byte
	var count int64
	err = s.db.QueryRowContext(ctx, "SELECT id, user, handle, public_key, sign_count FROM fado_passkeys WHERE credential = ? AND removed_at = 0", a.Credential).Scan(&id, &user, &handle, &key, &count)
	if err == sql.ErrNoRows {
		return nil, s.refuseSignIn(ctx, "", WayPasskey, address)
	}
	if err != nil {
		return nil, storageErr("read the passkey", err)
	}
	if wait := g.waitFor(now, "u:"+user); wait > 0 {
		return nil, tooMany(wait)
	}
	if _, e := s.person(user); e != nil || !bytes.Equal(a.UserHandle, handle) {
		return nil, s.refuseSignIn(ctx, user, WayPasskey, address)
	}
	used, err := party.CheckAssertion(a, challenge, key, uint32(count))
	if errors.Is(err, webauthn.ErrCounter) {
		_ = s.writeUsers(ctx, func(w execer, now int64) error {
			return recordUserChange(ctx, w, user, PasskeyCounterFell, Principal{ID: user, Kind: ActorPerson}, now, map[string]any{"id": id, "count": count})
		}, func() {})
	}
	if err != nil {
		return nil, s.refuseSignIn(ctx, user, WayPasskey, address)
	}
	return &passkeyUse{id: id, user: user, challenge: challenge, used: used}, nil
}

// usePasskey writes the use of a passkey, in the writer's turn, where
// the counter only rises: of two uses at once checked against the same
// counter, the one that comes second with a lower counter is refused,
// and the counter is never set back.
func usePasskey(ctx context.Context, w execer, use *passkeyUse, now int64) error {
	count := int64(use.used.Count)
	res, err := w.ExecContext(ctx, "UPDATE fado_passkeys SET sign_count = ?, synced = ?, used_at = ? WHERE id = ? AND removed_at = 0 AND (sign_count < ? OR ? = 0)", count, use.used.Synced, now, use.id, count, count)
	if err != nil {
		return storageErr("store the passkey's use", err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return refused()
	}
	return nil
}

// AddPasskeyQuestion is the question the account page gives the browser,
// to make another passkey of the caller's on the device in use.
func (s *Store) AddPasskeyQuestion(ctx context.Context, origin string, opts ...Option) (string, error) {
	o, e := s.known(ctx, opts, "add a passkey")
	if e != nil {
		return "", e
	}
	u, e := s.person(o.principal.ID)
	if e != nil {
		return "", e
	}
	return s.makeQuestion(ctx, u, origin, askAdd, u.ID)
}

// AddPasskey takes the passkey the caller made on the device it named,
// from the account page.
func (s *Store) AddPasskey(ctx context.Context, answer []byte, device, origin string, opts ...Option) (*Passkey, error) {
	o, e := s.known(ctx, opts, "add a passkey")
	if e != nil {
		return nil, e
	}
	if e := refuseAt(o, "change to a user"); e != nil {
		return nil, e
	}
	u, e := s.person(o.principal.ID)
	if e != nil {
		return nil, e
	}
	name, e := deviceName(device)
	if e != nil {
		return nil, e
	}
	m, handle, challenge, err := s.made(answer, origin, askAdd, u.ID)
	if err != nil {
		if e := new(Error); errors.As(err, &e) {
			return nil, e
		}
		return nil, &Error{Code: CodeBadValue, Path: "/response", Message: "the passkey was not added: " + err.Error(), Hint: "try again on this page; a question of the page holds 5 minutes"}
	}
	key, err := newPasskey(u.ID, name, AddedOnTheAccount, m)
	if err != nil {
		return nil, err
	}
	if !s.signingIn().spend(challenge, s.now().Unix()) {
		return nil, &Error{Code: CodeBadValue, Path: "/response", Message: "the passkey was not added: the page's question was answered already", Hint: "load the page again"}
	}
	err = s.writeUsers(ctx, func(w execer, now int64) error {
		return insertPasskey(ctx, w, key, m, handle, o.principal, now)
	}, func() {})
	if err != nil {
		return nil, err
	}
	return key, nil
}

// Passkeys lists the passkeys of a user, oldest first, never a key.
// user left empty is the caller. A database administrator may ask of
// any user, and a user of itself.
func (s *Store) Passkeys(ctx context.Context, user string, opts ...Option) ([]*Passkey, error) {
	o, e := s.known(ctx, opts, "list passkeys")
	if e != nil {
		return nil, e
	}
	if user == "" {
		user = o.principal.ID
	}
	if !o.principal.in(GroupAdministrators) && o.principal.ID != user {
		e := hintf(CodeForbidden, "", who(o.principal)+" may not list the passkeys of "+strconv.Quote(user),
			"a user lists its own; the passkeys of another are for the members of the system group "+GroupAdministrators)
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
	rows, err := s.db.QueryContext(ctx, "SELECT id, user, name, synced, added_at, added_with, used_at FROM fado_passkeys WHERE user = ? AND removed_at = 0 ORDER BY added_at, rowid", user)
	if err != nil {
		return nil, storageErr("read the passkeys", err)
	}
	defer rows.Close()
	out := []*Passkey{}
	for rows.Next() {
		k := &Passkey{}
		if err := rows.Scan(&k.ID, &k.User, &k.Name, &k.Synced, &k.AddedAt, &k.AddedWith, &k.UsedAt); err != nil {
			return nil, storageErr("read a passkey", err)
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// lastPasskey is the refusal to remove a person's own last passkey.
func lastPasskey(name, user string) *Error {
	return hintf(CodeForbidden, "", "the passkey on "+name+" is the last of "+strconv.Quote(user)+", who would be locked out without it",
		"add a passkey on another device first, then remove this one; or ask an administrator, who removes any and invites the person again")
}

// RemovePasskey removes a passkey by its id: it opens nothing more, and
// the sessions it began end (ADR-0088, item 6). A database administrator
// removes anybody's, when a phone is lost; a user its own. Nobody
// removes their own last passkey, unless they sign in with the
// company's sign-on too: they would be locked out.
func (s *Store) RemovePasskey(ctx context.Context, id string, opts ...Option) error {
	o, e := s.known(ctx, opts, "remove a passkey")
	if e != nil {
		return e
	}
	if e := refuseAt(o, "change to a user"); e != nil {
		return e
	}
	var user, name string
	err := s.db.QueryRowContext(ctx, "SELECT user, name FROM fado_passkeys WHERE id = ? AND removed_at = 0", id).Scan(&user, &name)
	if err != nil && err != sql.ErrNoRows {
		return storageErr("read the passkey", err)
	}
	if err == sql.ErrNoRows || (!o.principal.in(GroupAdministrators) && user != o.principal.ID) {
		return &Error{Code: CodeUnknownPasskey, Path: "/passkey", Message: "the database has no passkey " + strconv.Quote(id), Hint: "a passkey is named by its id, as list_passkeys shows it"}
	}
	if user == o.principal.ID {
		var others, signsOn int
		if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM fado_passkeys WHERE user = ? AND removed_at = 0 AND id <> ?", user, id).Scan(&others); err != nil {
			return storageErr("count the passkeys", err)
		}
		if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM fado_signons WHERE user = ?", user).Scan(&signsOn); err != nil {
			return storageErr("read the sign-ons", err)
		}
		if others == 0 && signsOn == 0 {
			return lastPasskey(name, user)
		}
	}
	g := s.signingIn()
	// An administrator removes anybody else's last passkey; the rule of
	// one's own last is held again in the writer's turn, so that two
	// removals at once cannot take both of a person's last two.
	anyLast := user != o.principal.ID
	return s.writeUsers(ctx, func(w execer, now int64) error {
		res, err := w.ExecContext(ctx, "UPDATE fado_passkeys SET removed_at = ?, removed_by = ? WHERE id = ? AND removed_at = 0 AND (? OR EXISTS (SELECT 1 FROM fado_passkeys o WHERE o.user = ? AND o.removed_at = 0 AND o.id <> ?) OR EXISTS (SELECT 1 FROM fado_signons WHERE user = ?))",
			now, o.principal.ID, id, anyLast, user, id, user)
		if err != nil {
			return storageErr("remove the passkey", err)
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return lastPasskey(name, user)
		}
		if _, err := w.ExecContext(ctx, "UPDATE fado_sessions SET ended_at = ?, ended_by = ? WHERE ended_at = 0 AND id IN (SELECT session FROM fado_session_passkeys WHERE passkey = ?)", now, o.principal.ID, id); err != nil {
			return storageErr("end the passkey's sessions", err)
		}
		g.mu.Lock()
		for _, h := range g.sessions {
			if h.Passkey == id && h.EndedAt == 0 {
				h.EndedAt, h.EndedBy = now, o.principal.ID
			}
		}
		g.mu.Unlock()
		return recordUserChange(ctx, w, user, PasskeyRemoved, o.principal, now, &Passkey{ID: id, User: user, Name: name, RemovedAt: now, RemovedBy: o.principal.ID})
	}, func() {})
}
