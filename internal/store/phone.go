package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"errors"
	"strings"
	"time"
)

// The code for the phone (ADR-0088, item 3). The page of an invitation,
// and a person's account page, show a QR code that holds a link of
// their own: the phone's camera reads it, the phone opens the link, and
// the person adds a passkey there, which signs the phone in. The browser
// that showed the code keeps a secret of its own in a cookie, and once
// the phone has added its passkey that browser is signed in too.
//
// A code holds once and for 10 minutes. A new one, of the same
// invitation or the same person, ends the one before. Codes are kept in
// the server's memory alone, as the challenges are: a server that starts
// again holds none, and a page shows a new one when it is loaded.
const (
	// WayPhone is the way of a sign-in of the browser that showed the
	// code for the phone, once the phone added its passkey.
	WayPhone = "phone"

	phonePrefix    = "fadop_"
	phoneCodeLasts = 10 * time.Minute
)

// PhoneCode is a code for the phone, made for the page that shows it:
// Link, the secret of the link the code holds, which the phone opens;
// Computer, the secret the browser that showed the code keeps; and the
// moment both end.
type PhoneCode struct {
	Link     string
	Computer string
	Ends     int64
}

// PhoneFor is whom the link of a code is for, as the phone's page greets
// them: of their invitation, or of their account; and whether they hold
// a passkey already, which the phone may hold too.
type PhoneFor struct {
	User     string
	Name     string
	Invited  bool
	Passkeys bool
}

// The states of a code, as the page that shows it asks.
const (
	PhoneWaiting = "waiting"
	PhoneDone    = "done"
	PhoneEnded   = "ended"
)

// phoneCode is a code as the server keeps it.
type phoneCode struct {
	of         string // "i:" and the invitation's id, or "u:" and the user's
	user       string
	invitation string
	link       [sha256.Size]byte
	computer   [sha256.Size]byte
	ends       int64
	// using is set while the phone's answer is checked; passkey is the
	// passkey the phone added, once it did.
	using   bool
	passkey string
}

// InvitationPhoneCode makes the code the page of an invitation shows:
// its link adds the person's passkey on a phone, with the same
// invitation.
func (s *Store) InvitationPhoneCode(ctx context.Context, token string) (*PhoneCode, error) {
	if !s.people.on {
		return nil, notKept()
	}
	id, u, ok := s.invitation(ctx, token)
	if !ok {
		return nil, refused()
	}
	return s.newPhoneCode("i:"+id, u.ID, id)
}

// AccountPhoneCode makes the code the account page shows: its link adds
// a passkey of the caller's on a phone.
func (s *Store) AccountPhoneCode(ctx context.Context, opts ...Option) (*PhoneCode, error) {
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
	return s.newPhoneCode("u:"+u.ID, u.ID, "")
}

// newPhoneCode makes a code and ends the one it replaces.
func (s *Store) newPhoneCode(of, user, invitation string) (*PhoneCode, error) {
	link, err := randomWord(20)
	if err != nil {
		return nil, &Error{Code: CodeStorage, Message: "make the code: " + err.Error()}
	}
	computer, err := randomWord(32)
	if err != nil {
		return nil, &Error{Code: CodeStorage, Message: "make the code: " + err.Error()}
	}
	now := s.now()
	c := &phoneCode{of: of, user: user, invitation: invitation, link: sha256.Sum256([]byte(link)), computer: sha256.Sum256([]byte(computer)),
		ends: now.Add(phoneCodeLasts).Unix()}
	g := s.signingIn()
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, old := range g.codes {
		if old.ends <= now.Unix() {
			g.dropCode(old)
		}
	}
	if old := g.codes[of]; old != nil {
		g.dropCode(old)
	}
	g.codes[of], g.links[c.link], g.computers[c.computer] = c, c, c
	return &PhoneCode{Link: phonePrefix + link, Computer: computer, Ends: c.ends}, nil
}

// dropCode forgets a code. The caller holds the lock.
func (g *signing) dropCode(c *phoneCode) {
	if g.codes[c.of] == c {
		delete(g.codes, c.of)
	}
	delete(g.links, c.link)
	delete(g.computers, c.computer)
}

// randomWord is n random bytes written in lower-case base32, which a
// link carries as it is.
func randomWord(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)), nil
}

// linkCode answers the code of a link that holds and that the phone has
// not used, a copy, or false.
func (s *Store) linkCode(link string) (phoneCode, bool) {
	word, ok := strings.CutPrefix(link, phonePrefix)
	if !ok || !s.people.on {
		return phoneCode{}, false
	}
	g := s.signingIn()
	g.mu.Lock()
	defer g.mu.Unlock()
	c := g.links[sha256.Sum256([]byte(word))]
	if c == nil || c.ends <= s.now().Unix() || c.passkey != "" {
		return phoneCode{}, false
	}
	return *c, true
}

// phonePerson is the person a code is for, while the code holds and, for
// an invitation's, while the invitation does.
func (s *Store) phonePerson(ctx context.Context, c phoneCode) (*User, bool) {
	u, e := s.person(c.user)
	if e != nil {
		return nil, false
	}
	if c.invitation == "" {
		return u, true
	}
	var ends, ended int64
	err := s.db.QueryRowContext(ctx, "SELECT ends, ended_at FROM fado_invitations WHERE id = ?", c.invitation).Scan(&ends, &ended)
	if err != nil || ended != 0 || s.now().Unix() >= ends {
		return nil, false
	}
	return u, true
}

// PhoneLink answers whom the link of a code is for, while it holds, so
// that the phone's page greets the person.
func (s *Store) PhoneLink(ctx context.Context, link string) (*PhoneFor, error) {
	c, ok := s.linkCode(link)
	if !ok {
		return nil, refused()
	}
	u, ok := s.phonePerson(ctx, c)
	if !ok {
		return nil, refused()
	}
	held, err := s.credentials(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	return &PhoneFor{User: u.ID, Name: screenName(u), Invited: c.invitation != "", Passkeys: len(held) > 0}, nil
}

// PhoneQuestion is the question the phone's page gives the browser, to
// make the person's passkey on the phone: of their invitation, or as
// their account page asks.
func (s *Store) PhoneQuestion(ctx context.Context, link, origin string) (string, error) {
	c, ok := s.linkCode(link)
	if !ok {
		return "", refused()
	}
	u, ok := s.phonePerson(ctx, c)
	if !ok {
		return "", refused()
	}
	if c.invitation != "" {
		return s.makeQuestion(ctx, u, origin, askInvite, c.invitation)
	}
	return s.makeQuestion(ctx, u, origin, askAdd, u.ID)
}

// PhoneSignInQuestion is the question the phone's page gives a phone
// that holds a passkey of the person's already, which a synced passkey
// may be: to use it, for this code. A phone asked to make another
// passkey there is refused by the phone itself.
func (s *Store) PhoneSignInQuestion(ctx context.Context, link, origin string) (string, error) {
	c, ok := s.linkCode(link)
	if !ok {
		return "", refused()
	}
	if _, ok := s.phonePerson(ctx, c); !ok {
		return "", refused()
	}
	return s.useQuestion(origin, askPhone, string(c.link[:]))
}

// UsePhoneLink takes the passkey the phone made, on the device it named,
// and gives the phone's session. The link holds no more, and the browser
// that showed the code may enter (EnterByPhone). A link that does not
// hold and a passkey that does not are refused in the same words.
func (s *Store) UsePhoneLink(ctx context.Context, link string, answer []byte, device, origin, address string, lengths SessionLengths) (*Session, error) {
	return s.takePhoneLink(ctx, link, address, func(c phoneCode) (*Session, error) {
		return s.usePhoneCode(ctx, c, answer, device, origin, address, lengths)
	})
}

// SignInByPhoneLink takes the passkey of the person's that the phone
// holds already, the answer to PhoneSignInQuestion, and gives the
// phone's session, as UsePhoneLink does with a new one: the browser
// that showed the code may enter with the same passkey. A code of an
// invitation uses the invitation, which holds no more. A passkey of
// another person is refused as any that does not hold.
func (s *Store) SignInByPhoneLink(ctx context.Context, link string, answer []byte, origin, address string, lengths SessionLengths) (*Session, error) {
	return s.takePhoneLink(ctx, link, address, func(c phoneCode) (*Session, error) {
		return s.signInOnPhone(ctx, c, answer, origin, address, lengths)
	})
}

// takePhoneLink holds the code of a link while the phone's answer is
// checked, so that two phones that read one code use it once between
// them, and marks it done with the passkey of the session the answer
// gives.
func (s *Store) takePhoneLink(ctx context.Context, link, address string, take func(c phoneCode) (*Session, error)) (*Session, error) {
	if !s.people.on {
		return nil, notKept()
	}
	g := s.signingIn()
	if wait := g.waitFor(s.now().Unix(), "a:"+address); wait > 0 {
		return nil, tooMany(wait)
	}
	word, _ := strings.CutPrefix(link, phonePrefix)
	g.mu.Lock()
	c := g.links[sha256.Sum256([]byte(word))]
	free := c != nil && !c.using && c.passkey == "" && c.ends > s.now().Unix()
	var held phoneCode
	if free {
		c.using = true
		held = *c
	}
	g.mu.Unlock()
	if !free {
		return nil, s.refuseSignIn(ctx, "", WayPasskey, address)
	}
	sess, err := take(held)
	g.mu.Lock()
	c.using = false
	if err == nil {
		c.passkey = sess.Passkey
		delete(g.links, c.link)
	}
	g.mu.Unlock()
	return sess, err
}

func (s *Store) usePhoneCode(ctx context.Context, c phoneCode, answer []byte, device, origin, address string, lengths SessionLengths) (*Session, error) {
	u, ok := s.phonePerson(ctx, c)
	if !ok {
		return nil, s.refuseSignIn(ctx, c.user, WayPasskey, address)
	}
	if c.invitation != "" {
		return s.useInvitation(ctx, c.invitation, u, answer, device, origin, address, lengths)
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
		return nil, s.refuseSignIn(ctx, u.ID, WayPasskey, address)
	}
	key, err := newPasskey(u.ID, name, AddedOnTheAccount, m)
	if err != nil {
		return nil, err
	}
	if !s.signingIn().spend(challenge, s.now().Unix()) {
		return nil, s.refuseSignIn(ctx, u.ID, WayPasskey, address)
	}
	me := Principal{ID: u.ID, Kind: u.Kind}
	return s.beginSession(ctx, u.ID, key.ID, WayPasskey, address, lengths, func(w execer, now int64) error {
		return insertPasskey(ctx, w, key, m, handle, me, now)
	})
}

// signInOnPhone checks the use of a passkey the phone holds for the code,
// and begins the phone's session with it.
func (s *Store) signInOnPhone(ctx context.Context, c phoneCode, answer []byte, origin, address string, lengths SessionLengths) (*Session, error) {
	u, ok := s.phonePerson(ctx, c)
	if !ok {
		return nil, s.refuseSignIn(ctx, c.user, WayPasskey, address)
	}
	return s.signInHeld(ctx, u, c.invitation, askPhone, string(c.link[:]), answer, origin, address, lengths)
}

// PhoneCodeState answers how the code a browser showed stands, by the
// secret the browser keeps: PhoneWaiting for the phone, PhoneDone once
// the phone has added its passkey, or PhoneEnded.
func (s *Store) PhoneCodeState(computer string) string {
	if !s.people.on {
		return PhoneEnded
	}
	g := s.signingIn()
	g.mu.Lock()
	defer g.mu.Unlock()
	c := g.computers[sha256.Sum256([]byte(computer))]
	switch {
	case c == nil || c.ends <= s.now().Unix():
		return PhoneEnded
	case c.passkey != "":
		return PhoneDone
	}
	return PhoneWaiting
}

// EnterByPhone gives the session of the browser that showed the code,
// by the secret it keeps, once the phone has added its passkey: once,
// and within the code's 10 minutes (ADR-0088, item 3). The session is
// the passkey's, and ends when the passkey is removed.
func (s *Store) EnterByPhone(ctx context.Context, computer, address string, lengths SessionLengths) (*Session, error) {
	if !s.people.on {
		return nil, notKept()
	}
	g := s.signingIn()
	now := s.now().Unix()
	if wait := g.waitFor(now, "a:"+address); wait > 0 {
		return nil, tooMany(wait)
	}
	g.mu.Lock()
	found := g.computers[sha256.Sum256([]byte(computer))]
	ok := found != nil && found.passkey != "" && found.ends > now
	var c phoneCode
	if ok {
		c = *found
		g.dropCode(found)
	}
	g.mu.Unlock()
	if !ok {
		return nil, s.refuseSignIn(ctx, "", WayPhone, address)
	}
	if _, e := s.person(c.user); e != nil {
		return nil, s.refuseSignIn(ctx, c.user, WayPhone, address)
	}
	return s.beginSession(ctx, c.user, c.passkey, WayPhone, address, lengths, func(w execer, now int64) error {
		// The passkey was used for this sign-in; if it was removed
		// meanwhile, its sessions are ended and this one is not begun.
		res, err := w.ExecContext(ctx, "UPDATE fado_passkeys SET used_at = ? WHERE id = ? AND removed_at = 0", now, c.passkey)
		if err != nil {
			return storageErr("store the passkey's use", err)
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return refused()
		}
		return nil
	})
}
