package store

import (
	"context"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Signing in (ADR-0071): a person proves who they are with a password,
// and, when their system groups ask for it, a second step; the server
// gives them a session. A password is kept as a PBKDF2 hash, a session
// and a link to set a password as the hash of a secret, as a key is.
// Every sign-in, and every one refused, is kept.

// The ways a person proves who they are, as the log of sign-ins names
// them.
const (
	WayPassword   = "password"
	WaySecondStep = "second_step"
	WayLink       = "link"
)

// What comes after the password of a sign-in.
const (
	// NextSession: nothing; FinishSignIn gives the session.
	NextSession = "session"
	// NextCode: the code of the second step.
	NextCode = "code"
	// NextSetUp: the second step is required and not set up yet; the
	// person sets it up, with SetUpSecondStep, and gives its first code.
	NextSetUp = "set_up"
)

// The kinds of change to a user that signing in adds.
const (
	PasswordSet       = "password_set"
	SecondStepSet     = "second_step_set"
	SecondStepCleared = "second_step_cleared"
	PasswordLinkMade  = "password_link_made"
)

// The rules of a password and of its hash (ADR-0071, item 2).
const (
	MinPassword    = 12
	MaxPassword    = 256
	passwordRounds = 600_000
	saltLen        = 16
	hashLen        = 32
)

// SignIn is a sign-in under way after its password: ID is the secret the
// next step is given, good once and for ten minutes.
type SignIn struct {
	ID   string `json:"-"`
	User string `json:"user"`
	Next string `json:"next"`
}

// SecondStep is what a person enters in an authenticator app: the
// secret as the apps write it, in groups of four, and the same as an
// otpauth link.
type SecondStep struct {
	Secret string `json:"secret"`
	Link   string `json:"link"`
}

// SessionLengths are how long a session holds: unused, and at most.
type SessionLengths struct {
	Idle, Max time.Duration
}

// DefaultSessionLengths are the lengths of ADR-0071, item 5.
var DefaultSessionLengths = SessionLengths{Idle: 8 * time.Hour, Max: 30 * 24 * time.Hour}

// Session is a session as the database lists it. Token is the secret
// the browser holds, in the answer that makes the session and nowhere
// after; Codes are the codes for when the phone is lost, in the answer
// of the sign-in that set the second step up.
type Session struct {
	ID        string `json:"id"`
	User      string `json:"user"`
	Way       string `json:"way"`
	Address   string `json:"address,omitempty"`
	CreatedAt int64  `json:"created_at"`
	UsedAt    int64  `json:"used_at"`
	Ends      int64  `json:"ends"`
	Idle      int64  `json:"idle"`
	EndedAt   int64  `json:"ended_at,omitempty"`
	EndedBy   string `json:"ended_by,omitempty"`
	// Passkey is the id of the passkey the session began with, for one
	// begun with a passkey (ADR-0088): removing it ends the session.
	Passkey string   `json:"passkey,omitempty"`
	Token   string   `json:"-"`
	Codes   []string `json:"-"`
}

// PasswordLink is a link that lets a person set their password, good
// once and for a day. Token is its secret, in the answer that makes it
// and nowhere after.
type PasswordLink struct {
	User    string `json:"user"`
	Ends    int64  `json:"ends"`
	MadeBy  string `json:"made_by,omitempty"`
	Token   string `json:"token"`
	Address string `json:"-"`
}

// SignInEntry is one sign-in as the log keeps it, refused or not.
type SignInEntry struct {
	Seq     int64  `json:"seq"`
	At      int64  `json:"at"`
	User    string `json:"user"`
	Way     string `json:"way"`
	Address string `json:"address,omitempty"`
	OK      bool   `json:"ok"`
}

// SignInFilter narrows the log of sign-ins: to one user, from a moment,
// after a seq, at most so many.
type SignInFilter struct {
	User  string
	Since int64
	After int64
	Limit int
}

// The secrets of signing in: a session's token, fados_ and its id, and
// a link's, fadol_; both as long as a key's (ADR-0057, item 3).
const (
	sessionPrefix = "fados_"
	linkPrefix    = "fadol_"
	pendingLen    = 32
	signInLasts   = 10 * time.Minute
	linkLasts     = 24 * time.Hour
	codeStep      = 30
	recoveryCodes = 10
	// touchEvery is how often the moment a session was last used is
	// written: its idle end is worked out from memory, and a session
	// used a minute ago is not a write at every request.
	touchEvery = 5 * 60
)

// signinDDL makes the tables of signing in, beside the users' (ADR-0071,
// item 11). They are made on every open, so a database of the same
// engine schema gains them.
var signinDDL = []string{
	`CREATE TABLE IF NOT EXISTS fado_passwords (user TEXT PRIMARY KEY, hash BLOB NOT NULL, salt BLOB NOT NULL, rounds INTEGER NOT NULL, set_at INTEGER NOT NULL, set_by TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS fado_second_steps (user TEXT PRIMARY KEY, secret BLOB NOT NULL, codes TEXT NOT NULL, last_step INTEGER NOT NULL, set_at INTEGER NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS fado_sessions (id TEXT PRIMARY KEY, user TEXT NOT NULL, hash BLOB NOT NULL, way TEXT NOT NULL, address TEXT NOT NULL, created_at INTEGER NOT NULL, used_at INTEGER NOT NULL, ends INTEGER NOT NULL, idle INTEGER NOT NULL, ended_at INTEGER NOT NULL, ended_by TEXT NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS fado_password_links (id TEXT PRIMARY KEY, user TEXT NOT NULL, hash BLOB NOT NULL, made_at INTEGER NOT NULL, made_by TEXT NOT NULL, ends INTEGER NOT NULL, used_at INTEGER NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS fado_sign_ins (seq INTEGER PRIMARY KEY AUTOINCREMENT, at INTEGER NOT NULL, user TEXT NOT NULL, way TEXT NOT NULL, address TEXT NOT NULL, ok INTEGER NOT NULL)`,
}

// signing is what the store holds of signing in, under its own lock:
// the sessions, read when the database is opened; the sign-ins under
// way; the second steps being set up; the count of recent failures by
// user and by address; and the key the challenges of passkeys are
// signed with, made when the database is opened, with the challenges
// used that have not ended (passkeys.go).
type signing struct {
	mu       sync.Mutex
	sessions map[string]*heldSession
	pending  map[string]*pendingSignIn
	setting  map[string]*settingUp
	fails    map[string]*failures
	key      [32]byte
	used     map[string]int64
	// The codes for a phone that hold, each by what it is of (an
	// invitation or a person), and by the hashes of its two secrets.
	codes     map[string]*phoneCode
	links     map[[sha256.Size]byte]*phoneCode
	computers map[[sha256.Size]byte]*phoneCode
}

type heldSession struct {
	Session
	hash    [sha256.Size]byte
	written int64
}

type pendingSignIn struct {
	user   string
	next   string
	ends   int64
	tries  int
	secret []byte
}

type settingUp struct {
	secret []byte
	ends   int64
}

type failures struct {
	n    int
	last int64
}

// refused is the refusal of a sign-in, in the same words whatever was
// wrong.
func refused() *Error {
	return hintf(CodeSignInRefused, "", "the user, the password or the code is not right, or the link is not one that holds",
		"check what was typed, or ask an administrator of the database for a link to set the password")
}

func (s *Store) signingIn() *signing {
	s.sign.mu.Lock()
	if s.sign.pending == nil {
		s.sign.pending = map[string]*pendingSignIn{}
		s.sign.setting = map[string]*settingUp{}
		s.sign.fails = map[string]*failures{}
		s.sign.used = map[string]int64{}
		s.sign.codes = map[string]*phoneCode{}
		s.sign.links = map[[sha256.Size]byte]*phoneCode{}
		s.sign.computers = map[[sha256.Size]byte]*phoneCode{}
		if _, err := rand.Read(s.sign.key[:]); err != nil {
			// The system's source of random bytes does not fail; if it
			// did, no challenge it signs would be let through.
			panic("store: no random bytes for the key of the challenges: " + err.Error())
		}
		if s.sign.sessions == nil {
			s.sign.sessions = map[string]*heldSession{}
		}
	}
	s.sign.mu.Unlock()
	return &s.sign
}

// loadSessions reads the sessions that may still hold. Open calls it for
// a database that keeps its users.
func (s *Store) loadSessions(ctx context.Context) error {
	now := s.now().Unix()
	rows, err := s.writer.QueryContext(ctx, "SELECT s.id, s.user, s.hash, s.way, s.address, s.created_at, s.used_at, s.ends, s.idle, coalesce(p.passkey, '') FROM fado_sessions s LEFT JOIN fado_session_passkeys p ON p.session = s.id WHERE s.ended_at = 0 AND s.ends > ?", now)
	if err != nil {
		return storageErr("read the sessions", err)
	}
	defer rows.Close()
	held := map[string]*heldSession{}
	for rows.Next() {
		h := &heldSession{}
		var hash []byte
		if err := rows.Scan(&h.ID, &h.User, &hash, &h.Way, &h.Address, &h.CreatedAt, &h.UsedAt, &h.Ends, &h.Idle, &h.Passkey); err != nil {
			return storageErr("read a session", err)
		}
		copy(h.hash[:], hash)
		h.written = h.UsedAt
		held[h.ID] = h
	}
	if err := rows.Err(); err != nil {
		return storageErr("read the sessions", err)
	}
	s.sign.mu.Lock()
	s.sign.sessions = held
	s.sign.mu.Unlock()
	return nil
}

// secretText makes the id and the secret of a token, in the alphabet of
// a key.
func secretText() (id, secret string, err error) {
	var random [7 + 32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", "", err
	}
	return keyText.EncodeToString(random[:7])[:keyIDLen], keyText.EncodeToString(random[7:]), nil
}

// splitToken reads a token of a prefix into its id and the hash of its
// secret.
func splitToken(token, prefix string) (string, [sha256.Size]byte, bool) {
	if len(token) != len(prefix)+keyIDLen+1+secretLen || token[:len(prefix)] != prefix || token[len(prefix)+keyIDLen] != '_' {
		return "", [sha256.Size]byte{}, false
	}
	return token[len(prefix) : len(prefix)+keyIDLen], hashSecret(token[len(prefix)+keyIDLen+1:]), true
}

// waitFor says how long sign-ins for a user or from an address wait:
// after five failures for a user, or twenty from an address, which many
// people of one office share, within a quarter of an hour, one second,
// then twice as long after each failure, up to a quarter of an hour.
func (g *signing) waitFor(now int64, keys ...string) int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	var wait int64
	for _, k := range keys {
		f := g.fails[k]
		if f == nil || now-f.last > 15*60 {
			continue
		}
		free := 5
		if strings.HasPrefix(k, "a:") {
			free = 20
		}
		if f.n >= free {
			d := int64(1) << min(f.n-free, 10)
			if d > 15*60 {
				d = 15 * 60
			}
			// The moments are whole seconds, and a failure at second L
			// happened anywhere within it: the wait holds through the
			// second L+d, so that it is never shorter than d, nor
			// nothing when the clock turns a second between a failure
			// and the next try.
			if now <= f.last+d {
				wait = max(wait, f.last+d-now, 1)
			}
		}
	}
	return wait
}

func (g *signing) failed(now int64, keys ...string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, k := range keys {
		f := g.fails[k]
		if f == nil || now-f.last > 15*60 {
			f = &failures{}
			g.fails[k] = f
		}
		f.n++
		f.last = now
	}
}

func (g *signing) succeeded(keys ...string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, k := range keys {
		delete(g.fails, k)
	}
}

func tooMany(wait int64) *Error {
	minutes := (wait + 59) / 60
	return hintf(CodeTooManyAttempts, "", "too many sign-ins failed of late; the next is taken in "+strconv.FormatInt(minutes, 10)+" minute(s)",
		"wait, then try again; an administrator of the database can make a link to set the password")
}

// hashPassword is the hash the database keeps of a password.
func hashPassword(password string, salt []byte, rounds int) ([]byte, error) {
	return pbkdf2.Key(sha256.New, password, salt, rounds, hashLen)
}

// dummySalt is what a sign-in of a user without a password is hashed
// with, so that it takes as long as one with.
var dummySalt = make([]byte, saltLen)

// checkPassword refuses a password the rules of ADR-0071, item 2, do not
// take.
func checkPassword(password string) *Error {
	n := utf8.RuneCountInString(password)
	if n < MinPassword || n > MaxPassword || !utf8.ValidString(password) {
		return &Error{Code: CodeBadValue, Path: "/password", Message: "a password has " + strconv.Itoa(MinPassword) + " to " + strconv.Itoa(MaxPassword) + " characters, and this one has " + strconv.Itoa(n),
			Hint: "a sentence of four or five words is long enough and easy to remember; no other rule applies"}
	}
	return nil
}

func (s *Store) logSignIn(ctx context.Context, w execer, now int64, user, way, address string, ok bool) error {
	if _, err := w.ExecContext(ctx, "INSERT INTO fado_sign_ins (at, user, way, address, ok) VALUES (?, ?, ?, ?, ?)", now, user, way, address, ok); err != nil {
		return storageErr("record the sign-in", err)
	}
	return nil
}

// refuseSignIn records a refused sign-in, counts it and answers the
// refusal.
func (s *Store) refuseSignIn(ctx context.Context, user, way, address string) error {
	now := s.now().Unix()
	s.signingIn().failed(now, "u:"+user, "a:"+address)
	if err := s.writeUsers(ctx, func(w execer, now int64) error {
		return s.logSignIn(ctx, w, now, user, way, address, false)
	}, func() {}); err != nil {
		return err
	}
	return refused()
}

// secondStepRequired reports whether a user's system groups ask for a
// second step: administrators and designers (the owner's ruling 3).
func secondStepRequired(u *User) bool {
	for _, g := range u.System {
		if g == GroupAdministrators || g == GroupDesigners {
			return true
		}
	}
	return false
}

// hasSecondStep reports whether a user has set a second step up.
func (s *Store) hasSecondStep(ctx context.Context, user string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, "SELECT 1 FROM fado_second_steps WHERE user = ?", user).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, storageErr("read the second step", err)
	}
	return true, nil
}

// BeginSignIn takes the password of a person (ADR-0071, item 2) and
// answers what comes next: the session, the code of the second step, or
// setting the second step up. A user that is no person, is disabled or
// has no password, and a wrong password, are refused in the same words,
// in the same time. address is where the request came from, for the log
// and for the count of failures.
func (s *Store) BeginSignIn(ctx context.Context, user, password, address string) (*SignIn, error) {
	if !s.people.on {
		return nil, notKept()
	}
	g := s.signingIn()
	if wait := g.waitFor(s.now().Unix(), "u:"+user, "a:"+address); wait > 0 {
		return nil, tooMany(wait)
	}
	s.people.mu.RLock()
	u := s.people.users[user]
	s.people.mu.RUnlock()
	var hash, salt []byte
	rounds := passwordRounds
	if u != nil && u.Kind == ActorPerson {
		err := s.db.QueryRowContext(ctx, "SELECT hash, salt, rounds FROM fado_passwords WHERE user = ?", user).Scan(&hash, &salt, &rounds)
		if err != nil && err != sql.ErrNoRows {
			return nil, storageErr("read the password", err)
		}
	}
	// The hash is worked out whether the user and the password exist or
	// not, outside the writer's turn.
	if salt == nil {
		salt = dummySalt
	}
	sum, err := hashPassword(password, salt, rounds)
	if err != nil {
		return nil, &Error{Code: CodeStorage, Message: "hash the password: " + err.Error()}
	}
	if hash == nil || subtle.ConstantTimeCompare(sum, hash) != 1 || u == nil || u.Disabled {
		return nil, s.refuseSignIn(ctx, user, WayPassword, address)
	}
	next := NextSession
	if has, err := s.hasSecondStep(ctx, user); err != nil {
		return nil, err
	} else if has {
		next = NextCode
	} else if secondStepRequired(u) {
		next = NextSetUp
	}
	id, secret, err := secretText()
	if err != nil {
		return nil, &Error{Code: CodeStorage, Message: "begin the sign-in: " + err.Error()}
	}
	token := id + secret
	g.mu.Lock()
	g.pending[token] = &pendingSignIn{user: user, next: next, ends: s.now().Add(signInLasts).Unix()}
	g.mu.Unlock()
	return &SignIn{ID: token, User: user, Next: next}, nil
}

// pendingFor finds a sign-in under way, which ends ten minutes after its
// password.
func (g *signing) pendingFor(token string, now int64) *pendingSignIn {
	g.mu.Lock()
	defer g.mu.Unlock()
	for k, p := range g.pending {
		if p.ends <= now {
			delete(g.pending, k)
		}
	}
	return g.pending[token]
}

// newStepSecret makes the secret of a second step: twenty random bytes,
// as RFC 6238 has them for HMAC-SHA1.
func newStepSecret() ([]byte, error) {
	secret := make([]byte, 20)
	_, err := rand.Read(secret)
	return secret, err
}

var stepText = base32.StdEncoding.WithPadding(base32.NoPadding)

// stepShown is the secret as an authenticator app takes it: in groups of
// four, and as an otpauth link with the database's name as its issuer.
func (s *Store) stepShown(user string, secret []byte) *SecondStep {
	text := stepText.EncodeToString(secret)
	var grouped strings.Builder
	for i := 0; i < len(text); i += 4 {
		if i > 0 {
			grouped.WriteByte(' ')
		}
		grouped.WriteString(text[i:min(i+4, len(text))])
	}
	issuer := "FadoDB"
	label := url.PathEscape(issuer + ":" + user)
	return &SecondStep{Secret: grouped.String(), Link: "otpauth://totp/" + label + "?secret=" + text + "&issuer=" + url.QueryEscape(issuer) + "&algorithm=SHA1&digits=6&period=30"}
}

// SetUpSecondStep gives the secret of the second step of a sign-in that
// must set one up (NextSetUp), for the person to enter in their app. The
// first code FinishSignIn is given confirms it.
func (s *Store) SetUpSecondStep(ctx context.Context, signIn string) (*SecondStep, error) {
	g := s.signingIn()
	p := g.pendingFor(signIn, s.now().Unix())
	if p == nil || p.next != NextSetUp {
		return nil, refused()
	}
	if p.secret == nil {
		secret, err := newStepSecret()
		if err != nil {
			return nil, &Error{Code: CodeStorage, Message: "make the secret: " + err.Error()}
		}
		g.mu.Lock()
		p.secret = secret
		g.mu.Unlock()
	}
	return s.stepShown(p.user, p.secret), nil
}

// stepCode is the code of a secret at a step of thirty seconds (RFC 6238,
// RFC 4226): six digits.
func stepCode(secret []byte, step int64) string {
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(step))
	mac := hmac.New(sha1.New, secret)
	mac.Write(counter[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	text := strconv.FormatUint(uint64(v%1_000_000), 10)
	return strings.Repeat("0", 6-len(text)) + text
}

// StepCode is the code an authenticator app shows for a secret at a
// moment, the secret as SecondStep writes it. It is for tests and for a
// program that signs a person in on their behalf.
func StepCode(secret string, at time.Time) string {
	raw, err := stepText.DecodeString(strings.ReplaceAll(secret, " ", ""))
	if err != nil {
		return ""
	}
	return stepCode(raw, at.Unix()/codeStep)
}

// stepMatches finds the step a code is of, one step either side of now,
// and never one used before.
func stepMatches(secret []byte, code string, now, last int64) (int64, bool) {
	if len(code) != 6 {
		return 0, false
	}
	step := now / codeStep
	for _, at := range []int64{step, step - 1, step + 1} {
		if at > last && subtle.ConstantTimeCompare([]byte(stepCode(secret, at)), []byte(code)) == 1 {
			return at, true
		}
	}
	return 0, false
}

// normalCode reads a code as a person types it: without spaces and
// dashes, in capitals.
func normalCode(code string) string {
	return strings.ToUpper(strings.NewReplacer(" ", "", "-", "").Replace(strings.TrimSpace(code)))
}

// newRecoveryCodes makes the codes for when the phone is lost: ten, each
// of ten characters of the alphabet of a key, written as two groups.
func newRecoveryCodes() (shown []string, hashes []string, err error) {
	for i := 0; i < recoveryCodes; i++ {
		var random [7]byte
		if _, err := rand.Read(random[:]); err != nil {
			return nil, nil, err
		}
		text := keyText.EncodeToString(random[:])[:10]
		shown = append(shown, text[:5]+"-"+text[5:])
		sum := sha256.Sum256([]byte(text))
		hashes = append(hashes, string(keyText.EncodeToString(sum[:])))
	}
	return shown, hashes, nil
}

// FinishSignIn takes the code of the second step, when one comes next,
// and gives the session. A sign-in that set the second step up answers
// the codes for when the phone is lost with the session, this once. A
// code may be one of those codes, which then holds no more.
func (s *Store) FinishSignIn(ctx context.Context, signIn, code, address string, lengths SessionLengths) (*Session, error) {
	g := s.signingIn()
	now := s.now().Unix()
	p := g.pendingFor(signIn, now)
	if p == nil {
		return nil, refused()
	}
	if wait := g.waitFor(now, "u:"+p.user, "a:"+address); wait > 0 {
		return nil, tooMany(wait)
	}
	s.people.mu.RLock()
	u := s.people.users[p.user]
	s.people.mu.RUnlock()
	if u == nil || u.Disabled {
		return nil, refused()
	}
	var codes, hashes []string
	way := WayPassword
	var step int64
	var usedCodes string
	switch p.next {
	case NextSetUp:
		if p.secret == nil {
			return nil, refused()
		}
		if _, ok := stepMatches(p.secret, normalCode(code), now, 0); !ok {
			return nil, s.failStep(ctx, g, p, signIn, address)
		}
		var err error
		if codes, hashes, err = newRecoveryCodes(); err != nil {
			return nil, &Error{Code: CodeStorage, Message: "make the codes: " + err.Error()}
		}
		step = now / codeStep
		way = WaySecondStep
	case NextCode:
		var secret []byte
		var kept string
		var last int64
		if err := s.db.QueryRowContext(ctx, "SELECT secret, codes, last_step FROM fado_second_steps WHERE user = ?", p.user).Scan(&secret, &kept, &last); err != nil {
			return nil, refused()
		}
		typed := normalCode(code)
		if at, ok := stepMatches(secret, typed, now, last); ok {
			step = at
		} else if left, ok := useRecoveryCode(kept, typed); ok {
			step, usedCodes = last, left
		} else {
			return nil, s.failStep(ctx, g, p, signIn, address)
		}
		way = WaySecondStep
	}
	lengths = fitLengths(lengths)
	id, secret, err := secretText()
	if err != nil {
		return nil, &Error{Code: CodeStorage, Message: "make the session: " + err.Error()}
	}
	held := &heldSession{Session: Session{ID: id, User: p.user, Way: way, Address: address, Idle: int64(lengths.Idle / time.Second)}, hash: hashSecret(secret)}
	err = s.writeUsers(ctx, func(w execer, now int64) error {
		held.CreatedAt, held.UsedAt, held.written = now, now, now
		held.Ends = now + int64(lengths.Max/time.Second)
		switch p.next {
		case NextSetUp:
			text, _ := json.Marshal(hashes)
			if _, err := w.ExecContext(ctx, "INSERT OR REPLACE INTO fado_second_steps (user, secret, codes, last_step, set_at) VALUES (?, ?, ?, ?, ?)", p.user, p.secret, string(text), step, now); err != nil {
				return storageErr("store the second step", err)
			}
			if err := recordUserChange(ctx, w, p.user, SecondStepSet, Principal{ID: p.user, Kind: u.Kind}, now, map[string]any{}); err != nil {
				return err
			}
		case NextCode:
			query, args := "UPDATE fado_second_steps SET last_step = ? WHERE user = ?", []any{step, p.user}
			if usedCodes != "" {
				query, args = "UPDATE fado_second_steps SET codes = ? WHERE user = ?", []any{usedCodes, p.user}
			}
			if _, err := w.ExecContext(ctx, query, args...); err != nil {
				return storageErr("store the step used", err)
			}
		}
		if _, err := w.ExecContext(ctx, "INSERT INTO fado_sessions (id, user, hash, way, address, created_at, used_at, ends, idle, ended_at, ended_by) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, '')",
			held.ID, held.User, held.hash[:], held.Way, held.Address, now, now, held.Ends, held.Idle); err != nil {
			return storageErr("store the session", err)
		}
		return s.logSignIn(ctx, w, now, p.user, way, address, true)
	}, func() {})
	if err != nil {
		return nil, err
	}
	g.mu.Lock()
	delete(g.pending, signIn)
	g.sessions[held.ID] = held
	g.mu.Unlock()
	g.succeeded("u:"+p.user, "a:"+address)
	made := held.Session
	made.Token = sessionPrefix + id + "_" + secret
	made.Codes = codes
	return &made, nil
}

// failStep refuses a wrong code; five wrong codes end the sign-in under
// way, and its password is asked again.
func (s *Store) failStep(ctx context.Context, g *signing, p *pendingSignIn, signIn, address string) error {
	g.mu.Lock()
	p.tries++
	if p.tries >= 5 {
		delete(g.pending, signIn)
	}
	g.mu.Unlock()
	return s.refuseSignIn(ctx, p.user, WaySecondStep, address)
}

// useRecoveryCode finds a code for when the phone is lost among the
// hashes kept, and answers the hashes left without it.
func useRecoveryCode(kept, typed string) (string, bool) {
	var hashes []string
	if json.Unmarshal([]byte(kept), &hashes) != nil || len(typed) != 10 {
		return "", false
	}
	sum := sha256.Sum256([]byte(typed))
	mine := keyText.EncodeToString(sum[:])
	for i, h := range hashes {
		if subtle.ConstantTimeCompare([]byte(h), []byte(mine)) == 1 {
			left := append(append([]string{}, hashes[:i]...), hashes[i+1:]...)
			text, _ := json.Marshal(left)
			return string(text), true
		}
	}
	return "", false
}

// SessionCaller answers who a session belongs to: the principal a
// request sent with its token runs as, built from the user as it is now,
// and the session. A token the database does not know, a session that
// ended, unused too long or past its end, and the session of a disabled
// user all answer ErrUnknownKey, as a key does.
func (s *Store) SessionCaller(ctx context.Context, token string) (Principal, *Session, error) {
	id, sum, ok := splitToken(token, sessionPrefix)
	if !s.people.on || !ok {
		return Principal{}, nil, ErrUnknownKey
	}
	g := s.signingIn()
	now := s.now().Unix()
	g.mu.Lock()
	h := g.sessions[id]
	var held [sha256.Size]byte
	if h != nil {
		held = h.hash
	}
	g.mu.Unlock()
	if subtle.ConstantTimeCompare(sum[:], held[:]) != 1 || h == nil {
		return Principal{}, nil, ErrUnknownKey
	}
	s.people.mu.RLock()
	u := s.people.users[h.User]
	s.people.mu.RUnlock()
	g.mu.Lock()
	gone := h.EndedAt != 0 || now >= h.Ends || now-h.UsedAt >= h.Idle || u == nil || u.Disabled
	write := !gone && now-h.written >= touchEvery
	if !gone {
		h.UsedAt = now
		if write {
			h.written = now
		}
	}
	shown := h.Session
	g.mu.Unlock()
	if gone {
		return Principal{}, nil, ErrUnknownKey
	}
	if write {
		// The moment it was used is written now and then, for a server
		// that starts again.
		_ = s.writeUsers(ctx, func(w execer, _ int64) error {
			_, err := w.ExecContext(ctx, "UPDATE fado_sessions SET used_at = ? WHERE id = ?", now, id)
			return err
		}, func() {})
	}
	return Principal{ID: u.ID, Kind: u.Kind, Groups: u.Groups, System: u.System}, &shown, nil
}

// SignOut ends the session of a token. A token that holds nothing ends
// nothing.
func (s *Store) SignOut(ctx context.Context, token string) error {
	p, sess, err := s.SessionCaller(ctx, token)
	if err != nil {
		return nil
	}
	return s.endSession(ctx, sess.ID, p.ID)
}

func (s *Store) endSession(ctx context.Context, id, by string) error {
	g := s.signingIn()
	return s.writeUsers(ctx, func(w execer, now int64) error {
		_, err := w.ExecContext(ctx, "UPDATE fado_sessions SET ended_at = ?, ended_by = ? WHERE id = ? AND ended_at = 0", now, by, id)
		if err != nil {
			return storageErr("end the session", err)
		}
		g.mu.Lock()
		if h := g.sessions[id]; h != nil {
			h.EndedAt, h.EndedBy = now, by
		}
		g.mu.Unlock()
		return nil
	}, func() {})
}

// Sessions lists the sessions of a user that still hold, oldest first.
// user left empty is the caller. A database administrator may ask of any
// user, and a user of itself.
func (s *Store) Sessions(ctx context.Context, user string, opts ...Option) ([]*Session, error) {
	o, e := s.known(ctx, opts, "list sessions")
	if e != nil {
		return nil, e
	}
	if user == "" {
		user = o.principal.ID
	}
	if !o.principal.in(GroupAdministrators) && o.principal.ID != user {
		e := hintf(CodeForbidden, "", who(o.principal)+" may not list the sessions of "+strconv.Quote(user),
			"a user lists its own; the sessions of another are for the members of the system group "+GroupAdministrators)
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
	g := s.signingIn()
	now := s.now().Unix()
	list := []*Session{}
	g.mu.Lock()
	for _, h := range g.sessions {
		if h.User == user && h.EndedAt == 0 && now < h.Ends && now-h.UsedAt < h.Idle {
			one := h.Session
			list = append(list, &one)
		}
	}
	g.mu.Unlock()
	sort.Slice(list, func(i, j int) bool {
		if list[i].CreatedAt != list[j].CreatedAt {
			return list[i].CreatedAt < list[j].CreatedAt
		}
		return list[i].ID < list[j].ID
	})
	return list, nil
}

// EndSession ends a session by its id: the next request with it is
// refused. A database administrator ends any, and a user its own.
// Ending one that has ended changes nothing.
func (s *Store) EndSession(ctx context.Context, id string, opts ...Option) error {
	o, e := s.known(ctx, opts, "end a session")
	if e != nil {
		return e
	}
	if e := refuseAt(o, "change to a session"); e != nil {
		return e
	}
	g := s.signingIn()
	g.mu.Lock()
	h := g.sessions[id]
	g.mu.Unlock()
	if h == nil || (!o.principal.in(GroupAdministrators) && h.User != o.principal.ID) {
		return &Error{Code: CodeUnknownSession, Message: "the database has no session " + strconv.Quote(id), Hint: "a session is named by its id, as list_sessions shows it", Path: "/session"}
	}
	if h.EndedAt != 0 {
		return nil
	}
	return s.endSession(ctx, id, o.principal.ID)
}

// setPassword writes a password's hash, worked out before the writer's
// turn, and records the change.
func (s *Store) setPassword(ctx context.Context, user, password string, by Principal, extra func(w execer, now int64) error) error {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return &Error{Code: CodeStorage, Message: "make the salt: " + err.Error()}
	}
	hash, err := hashPassword(password, salt, passwordRounds)
	if err != nil {
		return &Error{Code: CodeStorage, Message: "hash the password: " + err.Error()}
	}
	return s.writeUsers(ctx, func(w execer, now int64) error {
		if _, err := w.ExecContext(ctx, "INSERT OR REPLACE INTO fado_passwords (user, hash, salt, rounds, set_at, set_by) VALUES (?, ?, ?, ?, ?, ?)", user, hash, salt, passwordRounds, now, by.ID); err != nil {
			return storageErr("store the password", err)
		}
		if extra != nil {
			if err := extra(w, now); err != nil {
				return err
			}
		}
		return recordUserChange(ctx, w, user, PasswordSet, by, now, map[string]any{})
	}, func() {})
}

// ChangePassword changes the caller's own password, given the one it
// has (ADR-0071, item 2). Its sessions hold: the account page ends the
// others, since it knows which one the person is in.
func (s *Store) ChangePassword(ctx context.Context, old, password, address string, opts ...Option) error {
	o, e := s.known(ctx, opts, "change a password")
	if e != nil {
		return e
	}
	if e := checkPassword(password); e != nil {
		return e
	}
	user := o.principal.ID
	g := s.signingIn()
	if wait := g.waitFor(s.now().Unix(), "u:"+user, "a:"+address); wait > 0 {
		return tooMany(wait)
	}
	var hash, salt []byte
	var rounds int
	err := s.db.QueryRowContext(ctx, "SELECT hash, salt, rounds FROM fado_passwords WHERE user = ?", user).Scan(&hash, &salt, &rounds)
	if err != nil && err != sql.ErrNoRows {
		return storageErr("read the password", err)
	}
	if salt == nil {
		salt, rounds = dummySalt, passwordRounds
	}
	sum, herr := hashPassword(old, salt, rounds)
	if herr != nil || hash == nil || subtle.ConstantTimeCompare(sum, hash) != 1 {
		return s.refuseSignIn(ctx, user, WayPassword, address)
	}
	return s.setPassword(ctx, user, password, o.principal, nil)
}

// CreatePasswordLink makes a link that lets a person set their password,
// good once and for a day (ADR-0071, item 2). Nobody sets another's
// password: a database administrator, or the machine that holds the
// database, makes the link and gives it to the person. An agent and a
// service call with a key and have no password.
func (s *Store) CreatePasswordLink(ctx context.Context, user string, opts ...Option) (*PasswordLink, error) {
	o, e := s.keeper(ctx, opts, "make a link to set a password")
	if e != nil {
		return nil, e
	}
	if e := refuseAt(o, "change to a user"); e != nil {
		return nil, e
	}
	s.people.mu.RLock()
	u := s.people.users[user]
	s.people.mu.RUnlock()
	if u == nil {
		e := unknownUser(user)
		e.Path = "/user"
		return nil, e
	}
	if u.Kind != ActorPerson {
		return nil, &Error{Code: CodeBadValue, Path: "/user", Message: user + " is " + article(u.Kind) + ", which calls with a key and has no password",
			Hint: "make it a key with create_key; a password is a person's"}
	}
	if u.Disabled {
		return nil, &Error{Code: CodeBadValue, Path: "/user", Message: user + " is disabled", Hint: "enable the user with set_user first"}
	}
	id, secret, err := secretText()
	if err != nil {
		return nil, &Error{Code: CodeStorage, Message: "make the link: " + err.Error()}
	}
	link := &PasswordLink{User: user, MadeBy: o.principal.ID}
	sum := hashSecret(secret)
	err = s.writeUsers(ctx, func(w execer, now int64) error {
		link.Ends = now + int64(linkLasts/time.Second)
		if _, err := w.ExecContext(ctx, "INSERT INTO fado_password_links (id, user, hash, made_at, made_by, ends, used_at) VALUES (?, ?, ?, ?, ?, ?, 0)", id, user, sum[:], now, link.MadeBy, link.Ends); err != nil {
			return storageErr("store the link", err)
		}
		return recordUserChange(ctx, w, user, PasswordLinkMade, o.principal, now, map[string]any{"ends": link.Ends})
	}, func() {})
	if err != nil {
		return nil, err
	}
	link.Token = linkPrefix + id + "_" + secret
	return link, nil
}

// passwordLink finds the link of a token that still holds.
func (s *Store) passwordLink(ctx context.Context, token string) (id, user string, ok bool) {
	id, sum, ok := splitToken(token, linkPrefix)
	if !ok || !s.people.on {
		return "", "", false
	}
	var hash []byte
	var ends, used int64
	err := s.db.QueryRowContext(ctx, "SELECT user, hash, ends, used_at FROM fado_password_links WHERE id = ?", id).Scan(&user, &hash, &ends, &used)
	if err != nil || subtle.ConstantTimeCompare(sum[:], hash) != 1 || used != 0 || s.now().Unix() >= ends {
		return "", "", false
	}
	s.people.mu.RLock()
	u := s.people.users[user]
	s.people.mu.RUnlock()
	if u == nil || u.Disabled {
		return "", "", false
	}
	return id, user, true
}

// PasswordLinkUser answers whose a link to set a password is, while it
// holds, so that its page names the person.
func (s *Store) PasswordLinkUser(ctx context.Context, token string) (string, error) {
	_, user, ok := s.passwordLink(ctx, token)
	if !ok {
		return "", refused()
	}
	return user, nil
}

// UsePasswordLink sets the password of the person a link is for, and the
// link holds no more. Every session of the person ends: whoever had one
// signs in again with the new password.
func (s *Store) UsePasswordLink(ctx context.Context, token, password, address string) error {
	if e := checkPassword(password); e != nil {
		return e
	}
	id, user, ok := s.passwordLink(ctx, token)
	if !ok {
		return s.refuseSignIn(ctx, "", WayLink, address)
	}
	g := s.signingIn()
	err := s.setPassword(ctx, user, password, Principal{ID: user, Kind: ActorPerson}, func(w execer, now int64) error {
		res, err := w.ExecContext(ctx, "UPDATE fado_password_links SET used_at = ? WHERE id = ? AND used_at = 0", now, id)
		if err != nil {
			return storageErr("use the link", err)
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return refused()
		}
		if _, err := w.ExecContext(ctx, "UPDATE fado_sessions SET ended_at = ?, ended_by = ? WHERE user = ? AND ended_at = 0", now, user, user); err != nil {
			return storageErr("end the sessions", err)
		}
		g.mu.Lock()
		for _, h := range g.sessions {
			if h.User == user && h.EndedAt == 0 {
				h.EndedAt, h.EndedBy = now, user
			}
		}
		g.mu.Unlock()
		return s.logSignIn(ctx, w, now, user, WayLink, address, true)
	})
	return err
}

// SetUpMySecondStep gives the caller a secret for a second step, which
// ConfirmMySecondStep confirms with its first code. It replaces the one
// the caller has, once confirmed.
func (s *Store) SetUpMySecondStep(ctx context.Context, opts ...Option) (*SecondStep, error) {
	o, e := s.known(ctx, opts, "set up a second step")
	if e != nil {
		return nil, e
	}
	secret, err := newStepSecret()
	if err != nil {
		return nil, &Error{Code: CodeStorage, Message: "make the secret: " + err.Error()}
	}
	g := s.signingIn()
	g.mu.Lock()
	g.setting[o.principal.ID] = &settingUp{secret: secret, ends: s.now().Add(signInLasts).Unix()}
	g.mu.Unlock()
	return s.stepShown(o.principal.ID, secret), nil
}

// ConfirmMySecondStep takes the first code of the secret
// SetUpMySecondStep gave, keeps the second step and answers the codes
// for when the phone is lost, this once.
func (s *Store) ConfirmMySecondStep(ctx context.Context, code, address string, opts ...Option) ([]string, error) {
	o, e := s.known(ctx, opts, "set up a second step")
	if e != nil {
		return nil, e
	}
	user := o.principal.ID
	g := s.signingIn()
	now := s.now().Unix()
	g.mu.Lock()
	set := g.setting[user]
	g.mu.Unlock()
	if set == nil || set.ends <= now {
		return nil, refused()
	}
	if _, ok := stepMatches(set.secret, normalCode(code), now, 0); !ok {
		return nil, s.refuseSignIn(ctx, user, WaySecondStep, address)
	}
	codes, hashes, err := newRecoveryCodes()
	if err != nil {
		return nil, &Error{Code: CodeStorage, Message: "make the codes: " + err.Error()}
	}
	text, _ := json.Marshal(hashes)
	err = s.writeUsers(ctx, func(w execer, now int64) error {
		if _, err := w.ExecContext(ctx, "INSERT OR REPLACE INTO fado_second_steps (user, secret, codes, last_step, set_at) VALUES (?, ?, ?, ?, ?)", user, set.secret, string(text), now/codeStep, now); err != nil {
			return storageErr("store the second step", err)
		}
		return recordUserChange(ctx, w, user, SecondStepSet, o.principal, now, map[string]any{})
	}, func() {})
	if err != nil {
		return nil, err
	}
	g.mu.Lock()
	delete(g.setting, user)
	g.mu.Unlock()
	return codes, nil
}

// HasSecondStep reports whether a user has set a second step up, and
// whether their system groups require one. user left empty is the
// caller; a database administrator may ask of any user.
func (s *Store) HasSecondStep(ctx context.Context, user string, opts ...Option) (has, required bool, err error) {
	o, e := s.known(ctx, opts, "read a second step")
	if e != nil {
		return false, false, e
	}
	if user == "" {
		user = o.principal.ID
	}
	if !o.principal.in(GroupAdministrators) && o.principal.ID != user {
		return false, false, hintf(CodeForbidden, "", who(o.principal)+" may not read the second step of "+strconv.Quote(user), "a user reads its own")
	}
	s.people.mu.RLock()
	u := s.people.users[user]
	s.people.mu.RUnlock()
	if u == nil {
		return false, false, unknownUser(user)
	}
	has, herr := s.hasSecondStep(ctx, user)
	if herr != nil {
		return false, false, herr
	}
	return has, secondStepRequired(u), nil
}

// ClearSecondStep takes away the second step of a user, who sets it up
// again at their next sign-in when their system groups require one: for
// a person who lost the phone and the codes (ADR-0071, item 3). A
// database administrator clears anybody's; a user its own, when its
// groups do not require one.
func (s *Store) ClearSecondStep(ctx context.Context, user string, opts ...Option) error {
	o, e := s.known(ctx, opts, "clear a second step")
	if e != nil {
		return e
	}
	if e := refuseAt(o, "change to a user"); e != nil {
		return e
	}
	if user == "" {
		user = o.principal.ID
	}
	s.people.mu.RLock()
	u := s.people.users[user]
	s.people.mu.RUnlock()
	if u == nil {
		e := unknownUser(user)
		e.Path = "/user"
		return e
	}
	admin := o.principal.in(GroupAdministrators)
	if !admin && (o.principal.ID != user || secondStepRequired(u)) {
		e := hintf(CodeForbidden, "", who(o.principal)+" may not clear the second step of "+strconv.Quote(user),
			"a user clears its own when its system groups do not require one; anybody else's is for the members of the system group "+GroupAdministrators)
		e.Path = "/system/" + GroupAdministrators
		return e
	}
	return s.writeUsers(ctx, func(w execer, now int64) error {
		res, err := w.ExecContext(ctx, "DELETE FROM fado_second_steps WHERE user = ?", user)
		if err != nil {
			return storageErr("clear the second step", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil
		}
		return recordUserChange(ctx, w, user, SecondStepCleared, o.principal, now, map[string]any{})
	}, func() {})
}

// SignIns lists the log of sign-ins, oldest first, refused ones among
// them. It is a database administrator's.
func (s *Store) SignIns(ctx context.Context, f SignInFilter, opts ...Option) ([]SignInEntry, error) {
	if _, e := s.keeper(ctx, opts, "read the log of sign-ins"); e != nil {
		return nil, e
	}
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 1000
	}
	query := "SELECT seq, at, user, way, address, ok FROM fado_sign_ins WHERE seq > ? AND at >= ?"
	args := []any{f.After, f.Since}
	if f.User != "" {
		query += " AND user = ?"
		args = append(args, f.User)
	}
	query += " ORDER BY seq LIMIT ?"
	args = append(args, f.Limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, storageErr("read the sign-ins", err)
	}
	defer rows.Close()
	out := []SignInEntry{}
	for rows.Next() {
		var e SignInEntry
		if err := rows.Scan(&e.Seq, &e.At, &e.User, &e.Way, &e.Address, &e.OK); err != nil {
			return nil, storageErr("read a sign-in", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
