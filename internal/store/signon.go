package store

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// A company's sign-on (ADR-0071, item 4). The server asks the company's
// provider, Microsoft, Google or another that speaks OpenID Connect, who
// a person is, and hands the store the answer it checked. The store ties
// the provider's id for the person to a user the first time, by the
// e-mail an administrator gave the user, and from then on by that id
// alone; it gives the session. Nobody gets in for being in the
// company's directory: an account without a user is refused as a wrong
// password is.

// WaySignOn is the way of a sign-in with a company's sign-on, as the log
// of sign-ins names it.
const WaySignOn = "signon"

// SignOnLinked is the change to a user when a company account is tied to
// it.
const SignOnLinked = "signon_linked"

// SignedOn is who a company's provider says a person is, as the server
// checked it: the provider of the settings, the issuer of its note, the
// provider's id for the person, the e-mail, and whether the provider
// vouches for the e-mail.
type SignedOn struct {
	Provider      string
	Issuer        string
	Subject       string
	Email         string
	EmailVerified bool
}

// SignOnProvider is a company's sign-on as the page of signing in
// offers it: its name in the settings and the words of its button.
type SignOnProvider struct {
	Name  string
	Label string
}

// MaxEmail is the longest e-mail a user is given.
const MaxEmail = 254

// signonDDL makes the tables of a company's sign-on, beside the users'.
var signonDDL = []string{
	`CREATE TABLE IF NOT EXISTS fado_user_emails (user TEXT PRIMARY KEY, email TEXT NOT NULL)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS fado_user_emails_email ON fado_user_emails (email)`,
	`CREATE TABLE IF NOT EXISTS fado_signons (issuer TEXT NOT NULL, subject TEXT NOT NULL, user TEXT NOT NULL, provider TEXT NOT NULL, linked_at INTEGER NOT NULL, PRIMARY KEY (issuer, subject))`,
	`CREATE INDEX IF NOT EXISTS fado_signons_user ON fado_signons (user, issuer)`,
}

// checkEmail reads an e-mail as a user is given it: in small letters,
// one @ with something on each side and a dot after it, no space, at
// most 254 characters, a person's, and no other user's. self is the
// user it is given to, empty for a new one.
func (s *Store) checkEmail(email, kind, self string) (string, *Error) {
	email = strings.ToLower(strings.TrimSpace(email))
	at := strings.IndexByte(email, '@')
	bad := len(email) > MaxEmail || at < 1 || at != strings.LastIndexByte(email, '@') || !strings.Contains(email[at+1:], ".") ||
		strings.HasSuffix(email, ".") || strings.IndexFunc(email, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0
	if bad {
		return "", &Error{Code: CodeBadValue, Path: "/email", Message: strconv.Quote(email) + " is not an e-mail", Hint: "write it as the company's directory has it, such as ewa@acme.example"}
	}
	if kind != ActorPerson {
		return "", &Error{Code: CodeBadValue, Path: "/email", Message: "an e-mail ties a person's company sign-on, and this user is " + article(kind),
			Hint: "an agent and a service call with a key; leave the e-mail out"}
	}
	s.people.mu.RLock()
	defer s.people.mu.RUnlock()
	for _, u := range s.people.users {
		if u.Email == email && u.ID != self {
			return "", &Error{Code: CodeDuplicate, Path: "/email", Message: "the user " + strconv.Quote(u.ID) + " has the e-mail " + email,
				Hint: "an e-mail finds one person; take it from the other user first with set_user"}
		}
	}
	return email, nil
}

// writeEmail keeps the e-mail of a user, or takes it away when empty.
func writeEmail(ctx context.Context, w execer, user, email string) error {
	if email == "" {
		if _, err := w.ExecContext(ctx, "DELETE FROM fado_user_emails WHERE user = ?", user); err != nil {
			return storageErr("take the e-mail away", err)
		}
		return nil
	}
	if _, err := w.ExecContext(ctx, "INSERT OR REPLACE INTO fado_user_emails (user, email) VALUES (?, ?)", user, email); err != nil {
		return storageErr("keep the e-mail", err)
	}
	return nil
}

// SignOn gives the session of a person a company's provider vouched
// for (ADR-0071, item 4). The provider's id for them finds the user it
// was tied to; the first time, the e-mail the provider vouches for finds
// the user an administrator gave it, and the two are tied. Nobody else
// gets in: an account without a user, an e-mail the provider does not
// vouch for, a user that is no person or is disabled, and a user tied to
// another account of the same issuer are refused in the same words as a
// wrong password. No second step is asked: the company's sign-on brings
// its own.
func (s *Store) SignOn(ctx context.Context, who SignedOn, address string, lengths SessionLengths) (*Session, error) {
	if !s.people.on {
		return nil, notKept()
	}
	g := s.signingIn()
	if wait := g.waitFor(s.now().Unix(), "a:"+address); wait > 0 {
		return nil, tooMany(wait)
	}
	if who.Issuer == "" || who.Subject == "" {
		return nil, s.refuseSignIn(ctx, "", WaySignOn, address)
	}
	var user string
	err := s.db.QueryRowContext(ctx, "SELECT user FROM fado_signons WHERE issuer = ? AND subject = ?", who.Issuer, who.Subject).Scan(&user)
	if err != nil && err != sql.ErrNoRows {
		return nil, storageErr("read the company accounts", err)
	}
	link := false
	if user == "" {
		email := strings.ToLower(strings.TrimSpace(who.Email))
		if !who.EmailVerified || email == "" {
			return nil, s.refuseSignIn(ctx, "", WaySignOn, address)
		}
		s.people.mu.RLock()
		for _, u := range s.people.users {
			if u.Email == email {
				user = u.ID
			}
		}
		s.people.mu.RUnlock()
		if user == "" {
			return nil, s.refuseSignIn(ctx, "", WaySignOn, address)
		}
		// A user is tied to one account of an issuer: another account
		// with the same e-mail is somebody the e-mail passed to.
		var other string
		err := s.db.QueryRowContext(ctx, "SELECT subject FROM fado_signons WHERE user = ? AND issuer = ?", user, who.Issuer).Scan(&other)
		if err != nil && err != sql.ErrNoRows {
			return nil, storageErr("read the company accounts", err)
		}
		if other != "" {
			return nil, s.refuseSignIn(ctx, user, WaySignOn, address)
		}
		link = true
	}
	s.people.mu.RLock()
	u := s.people.users[user]
	s.people.mu.RUnlock()
	if u == nil || u.Disabled || u.Kind != ActorPerson {
		return nil, s.refuseSignIn(ctx, user, WaySignOn, address)
	}
	lengths = fitLengths(lengths)
	id, secret, gerr := secretText()
	if gerr != nil {
		return nil, &Error{Code: CodeStorage, Message: "make the session: " + gerr.Error()}
	}
	held := &heldSession{Session: Session{ID: id, User: user, Way: WaySignOn, Address: address, Idle: int64(lengths.Idle / time.Second)}, hash: hashSecret(secret)}
	err = s.writeUsers(ctx, func(w execer, now int64) error {
		held.CreatedAt, held.UsedAt, held.written = now, now, now
		held.Ends = now + int64(lengths.Max/time.Second)
		if link {
			if _, err := w.ExecContext(ctx, "INSERT INTO fado_signons (issuer, subject, user, provider, linked_at) VALUES (?, ?, ?, ?, ?)", who.Issuer, who.Subject, user, who.Provider, now); err != nil {
				return storageErr("tie the company account", err)
			}
			if err := recordUserChange(ctx, w, user, SignOnLinked, Principal{ID: user, Kind: u.Kind}, now, map[string]any{"provider": who.Provider, "issuer": who.Issuer}); err != nil {
				return err
			}
		}
		if _, err := w.ExecContext(ctx, "INSERT INTO fado_sessions (id, user, hash, way, address, created_at, used_at, ends, idle, ended_at, ended_by) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, '')",
			held.ID, held.User, held.hash[:], held.Way, held.Address, now, now, held.Ends, held.Idle); err != nil {
			return storageErr("store the session", err)
		}
		return s.logSignIn(ctx, w, now, user, WaySignOn, address, true)
	}, func() {})
	if err != nil {
		return nil, err
	}
	g.mu.Lock()
	g.sessions[held.ID] = held
	g.mu.Unlock()
	g.succeeded("a:" + address)
	made := held.Session
	made.Token = sessionPrefix + id + "_" + secret
	return &made, nil
}

// fitLengths keeps the lengths of a session within the usual: a length
// not given, or longer, is the usual one.
func fitLengths(lengths SessionLengths) SessionLengths {
	if lengths.Idle <= 0 || lengths.Idle > DefaultSessionLengths.Idle {
		lengths.Idle = DefaultSessionLengths.Idle
	}
	if lengths.Max <= 0 || lengths.Max > DefaultSessionLengths.Max {
		lengths.Max = DefaultSessionLengths.Max
	}
	return lengths
}
