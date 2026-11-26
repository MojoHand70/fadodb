package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/internal/webauthn/webauthntest"
)

const (
	acmeOrigin = "https://fado.acme.example"
	officeAt   = "10.0.0.7"
)

// invited makes a passkey on a device with an invitation, and answers
// the session it gives.
func invited(t *testing.T, s *Store, token string, d *webauthntest.Device, device string) *Session {
	t.Helper()
	ctx := context.Background()
	question, err := s.InvitationQuestion(ctx, token, acmeOrigin)
	if err != nil {
		t.Fatalf("the invitation's question: %v", err)
	}
	answer, err := d.Make(acmeOrigin, question)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := s.UseInvitation(ctx, token, []byte(answer), device, acmeOrigin, officeAt, SessionLengths{})
	if err != nil {
		t.Fatalf("the invitation used: %v", err)
	}
	return sess
}

// signIn signs in with the passkey a device holds, from an address.
func signIn(s *Store, d *webauthntest.Device, from string) (*Session, error) {
	question, err := s.SignInQuestion(acmeOrigin)
	if err != nil {
		return nil, err
	}
	answer, err := d.Use(acmeOrigin, question)
	if err != nil {
		return nil, err
	}
	return s.SignInWithPasskey(context.Background(), []byte(answer), acmeOrigin, from, SessionLengths{})
}

// Passkeys and invitations (ADR-0088), from the first administrator,
// invited on the machine, to a person whose last passkey an
// administrator removes: each invitation holds once, a passkey signs in
// with no name typed, a challenge holds once, a copied passkey is
// refused and recorded, removing a passkey ends what it began, and
// nobody removes their own last one.
func TestPasskeys(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	clock := &still{now: now}
	path := filepath.Join(t.TempDir(), "acme.fado")
	s := openUsers(t, path, clock)
	for _, u := range []User{
		{ID: "olga", Kind: ActorPerson, Name: "Olga", System: []string{GroupAdministrators}},
		{ID: "ann", Kind: ActorPerson, Name: "Ann Nowak", Groups: []string{"finance"}},
		{ID: "ewa", Kind: ActorPerson, Groups: []string{"finance"}},
		{ID: "ap-agent", Kind: ActorAgent, Groups: []string{"finance"}},
	} {
		if _, err := s.CreateUser(ctx, u, machine); err != nil {
			t.Fatal(err)
		}
	}
	annAs := As(Principal{ID: "ann", Kind: ActorPerson, Groups: []string{"finance"}})

	// The machine invites Olga; her laptop makes her first passkey.
	inv, err := s.CreateInvitation(ctx, "olga", machine)
	if err != nil || !strings.HasPrefix(inv.Token, invitationPrefix) || inv.Ends != now.Add(7*24*time.Hour).Unix() || inv.MadeBy != "" {
		t.Fatalf("the invitation: %v %+v", err, inv)
	}
	if who, err := s.Invited(ctx, inv.Token); err != nil || who.User != "olga" || who.Name != "Olga" {
		t.Errorf("whom the invitation is for: %v %+v", err, who)
	}
	laptop := webauthntest.New(webauthntest.ES256)
	first := invited(t, s, inv.Token, laptop, "Olga's laptop")
	if first.Way != WayPasskey || first.User != "olga" || first.Passkey == "" || first.Ends != now.Add(30*24*time.Hour).Unix() || !strings.HasPrefix(first.Token, sessionPrefix) {
		t.Fatalf("Olga's first session: %+v", first)
	}
	if p, _, err := s.SessionCaller(ctx, first.Token); err != nil || p.ID != "olga" {
		t.Errorf("the session's caller: %v %+v", err, p)
	}
	if _, err := s.InvitationQuestion(ctx, inv.Token, acmeOrigin); err == nil {
		t.Error("an invitation held twice")
	}

	// Olga signs in with it, naming nobody.
	sess, err := signIn(s, laptop, officeAt)
	if err != nil || sess.User != "olga" || sess.Passkey != first.Passkey {
		t.Fatalf("Olga signs in: %v %+v", err, sess)
	}

	// A challenge holds once, for 5 minutes, for its own question.
	question, _ := s.SignInQuestion(acmeOrigin)
	answer, _ := laptop.Use(acmeOrigin, question)
	if _, err := s.SignInWithPasskey(ctx, []byte(answer), acmeOrigin, officeAt, SessionLengths{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SignInWithPasskey(ctx, []byte(answer), acmeOrigin, officeAt, SessionLengths{}); err == nil {
		t.Error("an answer held twice")
	}
	question, _ = s.SignInQuestion(acmeOrigin)
	answer, _ = laptop.Use(acmeOrigin, question)
	clock.add(6 * time.Minute)
	refusedWith(t, "a challenge 6 minutes old", func() error {
		_, err := s.SignInWithPasskey(ctx, []byte(answer), acmeOrigin, officeAt, SessionLengths{})
		return err
	}(), CodeSignInRefused)
	addQuestion, err := s.AddPasskeyQuestion(ctx, acmeOrigin, olga)
	if err != nil {
		t.Fatal(err)
	}
	other := webauthntest.New(webauthntest.ES256)
	made, _ := other.Make(acmeOrigin, addQuestion)
	if _, err := s.UseInvitation(ctx, inv.Token, []byte(made), "a phone", acmeOrigin, officeAt, SessionLengths{}); err == nil {
		t.Error("a challenge of the account page made a passkey with an invitation")
	}

	// From another site, for another host, with another person's handle:
	// each is refused.
	laptop.Origin = "https://fado-acme.example"
	if _, err := signIn(s, laptop, officeAt); err == nil {
		t.Error("a page of another site signed Olga in")
	}
	laptop.Origin, laptop.Host = "", "evil.example"
	if _, err := signIn(s, laptop, officeAt); err == nil {
		t.Error("a passkey of another host signed Olga in")
	}
	laptop.Host = ""
	handle := laptop.Handle
	laptop.Handle = []byte("sixteen bytes!!!")
	if _, err := signIn(s, laptop, officeAt); err == nil {
		t.Error("a passkey said another person and signed Olga in")
	}
	laptop.Handle = handle

	// A copy of the passkey, whose counter is behind, is refused, and
	// the refusal is recorded.
	ahead := laptop.Count
	laptop.Count = 2
	if _, err := signIn(s, laptop, officeAt); err == nil {
		t.Error("a passkey whose counter fell signed Olga in")
	}
	laptop.Count = ahead + 1
	if _, err := signIn(s, laptop, officeAt); err != nil {
		t.Errorf("the passkey itself, after its copy was refused: %v", err)
	}
	changes, _ := s.UserChanges(ctx, "olga", olga)
	if !hasChange(changes, PasskeyCounterFell) || !hasChange(changes, InvitationMade) || !hasChange(changes, PasskeyAdded) {
		t.Errorf("Olga's changes: %+v", changes)
	}

	// Olga invites Ann; a second invitation ends the first.
	inv1, err := s.CreateInvitation(ctx, "ann", olga)
	if err != nil || inv1.MadeBy != "olga" {
		t.Fatalf("Ann's invitation: %v %+v", err, inv1)
	}
	inv2, _ := s.CreateInvitation(ctx, "ann", olga)
	if _, err := s.Invited(ctx, inv1.Token); err == nil {
		t.Error("an invitation held after another was made")
	}
	if list, err := s.Invitations(ctx, "ann", olga); err != nil || len(list) != 1 || list[0].Token != "" || list[0].MadeBy != "olga" {
		t.Errorf("Ann's invitations: %v %+v", err, list)
	}
	if err := s.WithdrawInvitation(ctx, "ann", olga); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Invited(ctx, inv2.Token); err == nil {
		t.Error("a withdrawn invitation held")
	}
	refusedWith(t, "withdrawn twice", s.WithdrawInvitation(ctx, "ann", olga), CodeUnknownInvitation)
	refusedWith(t, "an agent invited", func() error { _, err := s.CreateInvitation(ctx, "ap-agent", olga); return err }(), CodeBadValue)
	refusedWith(t, "nobody invited", func() error { _, err := s.CreateInvitation(ctx, "nobody", olga); return err }(), CodeUnknownUser)
	refusedWith(t, "Ann invites", func() error { _, err := s.CreateInvitation(ctx, "ewa", annAs); return err }(), CodeForbidden)
	refusedWith(t, "Ann lists invitations", func() error { _, err := s.Invitations(ctx, "", annAs); return err }(), CodeForbidden)

	// Ann's phone takes her invitation; her computer adds a second
	// passkey on her account page, and both know her by the same 16
	// bytes, never by her id.
	inv3, _ := s.CreateInvitation(ctx, "ann", olga)
	phone := webauthntest.New(webauthntest.EdDSA)
	phone.Counts, phone.Synced = false, true
	refusedWith(t, "no device's name", func() error {
		q, _ := s.InvitationQuestion(ctx, inv3.Token, acmeOrigin)
		a, _ := phone.Make(acmeOrigin, q)
		_, err := s.UseInvitation(ctx, inv3.Token, []byte(a), "  ", acmeOrigin, officeAt, SessionLengths{})
		return err
	}(), CodeBadValue)
	onPhone := invited(t, s, inv3.Token, phone, "Ann's iPhone")
	pc := webauthntest.New(webauthntest.RS256)
	q, err := s.AddPasskeyQuestion(ctx, acmeOrigin, annAs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := phone.Make(acmeOrigin, q); err == nil || !strings.Contains(err.Error(), "InvalidStateError") {
		t.Errorf("the phone was not told it holds a passkey of Ann's already: %v", err)
	}
	a, _ := pc.Make(acmeOrigin, q)
	added, err := s.AddPasskey(ctx, []byte(a), "Ann's PC", acmeOrigin, annAs)
	if err != nil || added.Name != "Ann's PC" || added.AddedWith != AddedOnTheAccount || added.User != "ann" {
		t.Fatalf("Ann adds a passkey: %v %+v", err, added)
	}
	if string(pc.Handle) != string(phone.Handle) || len(pc.Handle) != 16 || strings.Contains(string(pc.Handle), "ann") {
		t.Errorf("Ann's handles: %x %x", pc.Handle, phone.Handle)
	}
	if _, err := s.AddPasskey(ctx, []byte(a), "Ann's PC", acmeOrigin, annAs); err == nil {
		t.Error("an answer of the account page held twice")
	}
	onPC, err := signIn(s, pc, officeAt)
	if err != nil || onPC.User != "ann" {
		t.Fatalf("Ann signs in on her PC: %v %+v", err, onPC)
	}
	keys, err := s.Passkeys(ctx, "", annAs)
	if err != nil || len(keys) != 2 || keys[0].Name != "Ann's iPhone" || !keys[0].Synced || keys[0].AddedWith != AddedWithInvitation || keys[1].UsedAt == 0 {
		t.Fatalf("Ann's passkeys: %v %+v", err, keys)
	}
	if list, err := s.Passkeys(ctx, "ann", olga); err != nil || len(list) != 2 {
		t.Errorf("Olga lists Ann's passkeys: %v %d", err, len(list))
	}
	refusedWith(t, "Ann lists Olga's", func() error { _, err := s.Passkeys(ctx, "olga", annAs); return err }(), CodeForbidden)

	// Ann removes her iPhone's: the session it began ends, the PC's holds.
	if err := s.RemovePasskey(ctx, keys[0].ID, annAs); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SessionCaller(ctx, onPhone.Token); err == nil {
		t.Error("the session of a removed passkey holds")
	}
	if _, _, err := s.SessionCaller(ctx, onPC.Token); err != nil {
		t.Errorf("the PC's session: %v", err)
	}
	if _, err := signIn(s, phone, officeAt); err == nil {
		t.Error("a removed passkey signed Ann in")
	}
	refusedWith(t, "removed twice", s.RemovePasskey(ctx, keys[0].ID, annAs), CodeUnknownPasskey)
	refusedWith(t, "Ann removes Olga's", s.RemovePasskey(ctx, first.Passkey, annAs), CodeUnknownPasskey)

	// Her last one stays; an administrator removes it, for a lost phone.
	refusedWith(t, "Ann's last", s.RemovePasskey(ctx, keys[1].ID, annAs), CodeForbidden)
	refusedWith(t, "Olga's own last", s.RemovePasskey(ctx, first.Passkey, olga), CodeForbidden)
	if err := s.RemovePasskey(ctx, keys[1].ID, olga); err != nil {
		t.Fatalf("Olga removes Ann's last: %v", err)
	}
	if _, _, err := s.SessionCaller(ctx, onPC.Token); err == nil {
		t.Error("the session of Ann's last passkey holds after it was removed")
	}
	changes, _ = s.UserChanges(ctx, "ann", olga)
	if !hasChange(changes, InvitationWithdrawn) || !hasChange(changes, PasskeyRemoved) {
		t.Errorf("Ann's changes: %+v", changes)
	}
	for _, c := range changes {
		if c.What == PasskeyRemoved && c.Passkey.Name == "Ann's PC" && c.By != "olga" {
			t.Errorf("the removal of Ann's PC by: %+v", c)
		}
	}

	// A disabled person is refused with the passkey they have, and gets
	// no invitation.
	inv4, _ := s.CreateInvitation(ctx, "ewa", olga)
	key := webauthntest.New(webauthntest.ES256)
	invited(t, s, inv4.Token, key, "Ewa's key")
	off := true
	if _, err := s.SetUser(ctx, "ewa", UserEdit{Disabled: &off}, olga); err != nil {
		t.Fatal(err)
	}
	if _, err := signIn(s, key, officeAt); err == nil {
		t.Error("a disabled person signed in")
	}
	refusedWith(t, "a disabled person invited", func() error { _, err := s.CreateInvitation(ctx, "ewa", olga); return err }(), CodeBadValue)

	// Every sign-in is in the log, refused ones too, with its address.
	log, err := s.SignIns(ctx, SignInFilter{User: "olga"}, olga)
	var ok, no int
	for _, e := range log {
		if e.Way == WayPasskey && e.Address == officeAt {
			if e.OK {
				ok++
			} else {
				no++
			}
		}
	}
	if err != nil || ok < 4 || no < 3 {
		t.Errorf("Olga's sign-ins: %v, %d let through, %d refused", err, ok, no)
	}

	// Opened again, a session keeps the passkey it began with, and a
	// question asked before holds no more.
	question, _ = s.SignInQuestion(acmeOrigin)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openUsers(t, path, clock)
	if _, _, err := s.SessionCaller(ctx, sess.Token); err != nil {
		t.Fatalf("Olga's session after the database was opened again: %v", err)
	}
	answer, _ = laptop.Use(acmeOrigin, question)
	if _, err := s.SignInWithPasskey(ctx, []byte(answer), acmeOrigin, officeAt, SessionLengths{}); err == nil {
		t.Error("a question asked before the database was opened again held")
	}
	inv5, _ := s.CreateInvitation(ctx, "olga", olga)
	invited(t, s, inv5.Token, webauthntest.New(webauthntest.ES256), "Olga's phone")
	if err := s.RemovePasskey(ctx, first.Passkey, olga); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SessionCaller(ctx, sess.Token); err == nil {
		t.Error("a session read when the database was opened holds after its passkey was removed")
	}
}

// Twenty refused sign-ins from one address within a quarter of an hour
// make the next one wait, whoever it is for.
func TestPasskeySignInsThatKeepFailingWait(t *testing.T) {
	ctx := context.Background()
	clock := &still{now: time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)}
	s := openUsers(t, filepath.Join(t.TempDir(), "acme.fado"), clock)
	if _, err := s.CreateUser(ctx, User{ID: "olga", Kind: ActorPerson, System: []string{GroupAdministrators}}, machine); err != nil {
		t.Fatal(err)
	}
	inv, _ := s.CreateInvitation(ctx, "olga", machine)
	laptop := webauthntest.New(webauthntest.ES256)
	invited(t, s, inv.Token, laptop, "Olga's laptop")
	const from = "192.0.2.9"
	for i := 0; i < 20; i++ {
		_, err := s.SignInWithPasskey(ctx, []byte(`{"type":"public-key"}`), acmeOrigin, from, SessionLengths{})
		refusedWith(t, "a guess", err, CodeSignInRefused)
	}
	_, err := signIn(s, laptop, from)
	refusedWith(t, "after twenty guesses", err, CodeTooManyAttempts)
	if _, err := signIn(s, laptop, officeAt); err != nil {
		t.Errorf("from another address: %v", err)
	}
	clock.add(16 * time.Minute)
	if _, err := signIn(s, laptop, from); err != nil {
		t.Errorf("a quarter of an hour later: %v", err)
	}
}

// Ann removes her two passkeys at the same moment, from two devices:
// one removal goes through and the other is refused, so she is never
// left with none.
func TestTwoLastPasskeysRemovedAtOnce(t *testing.T) {
	ctx := context.Background()
	clock := &still{now: time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)}
	s := openUsers(t, filepath.Join(t.TempDir(), "acme.fado"), clock)
	if _, err := s.CreateUser(ctx, User{ID: "ann", Kind: ActorPerson, Groups: []string{"finance"}}, machine); err != nil {
		t.Fatal(err)
	}
	ann := As(Principal{ID: "ann", Kind: ActorPerson, Groups: []string{"finance"}})
	inv, _ := s.CreateInvitation(ctx, "ann", machine)
	invited(t, s, inv.Token, webauthntest.New(webauthntest.ES256), "Ann's laptop")
	phone := webauthntest.New(webauthntest.EdDSA)
	question, err := s.AddPasskeyQuestion(ctx, acmeOrigin, ann)
	if err != nil {
		t.Fatal(err)
	}
	answer, err := phone.Make(acmeOrigin, question)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddPasskey(ctx, []byte(answer), "Ann's phone", acmeOrigin, ann); err != nil {
		t.Fatal(err)
	}
	keys, err := s.Passkeys(ctx, "ann", ann)
	if err != nil || len(keys) != 2 {
		t.Fatalf("Ann's passkeys: %v %v", err, keys)
	}
	for round := 0; round < 20; round++ {
		errs := make(chan error, 2)
		start := make(chan struct{})
		for _, k := range keys {
			go func(id string) {
				<-start
				errs <- s.RemovePasskey(ctx, id, ann)
			}(k.ID)
		}
		close(start)
		var removed, refused int
		for range keys {
			if err := <-errs; err == nil {
				removed++
			} else if code(err) == CodeForbidden {
				refused++
			} else {
				t.Fatalf("a removal failed: %v", err)
			}
		}
		left, err := s.Passkeys(ctx, "ann", ann)
		if removed != 1 || refused != 1 || err != nil || len(left) != 1 {
			t.Fatalf("round %d: %d removed, %d refused, %d left (%v)", round, removed, refused, len(left), err)
		}
		// Ann adds the phone again for the next round.
		question, _ = s.AddPasskeyQuestion(ctx, acmeOrigin, ann)
		again := webauthntest.New(webauthntest.EdDSA)
		answer, _ = again.Make(acmeOrigin, question)
		if _, err := s.AddPasskey(ctx, []byte(answer), "Ann's phone", acmeOrigin, ann); err != nil {
			t.Fatal(err)
		}
		if keys, err = s.Passkeys(ctx, "ann", ann); err != nil || len(keys) != 2 {
			t.Fatalf("Ann's passkeys again: %v %v", err, keys)
		}
	}
}

// A question for an origin that is none is refused.
func TestAPasskeyForAnOriginThatIsNone(t *testing.T) {
	s := openUsers(t, filepath.Join(t.TempDir(), "acme.fado"), &still{now: time.Now()})
	if _, err := s.SignInQuestion("fado.acme.example"); err == nil {
		t.Error("a question for an origin with no scheme")
	}
}

func hasChange(changes []UserChange, what string) bool {
	for _, c := range changes {
		if c.What == what {
			return true
		}
	}
	return false
}

// A synced passkey is named after the password manager that keeps it,
// by its AAGUID (the owner's ruling, 2026-11-25), and not after the
// device it was made on; one whose manager the browser does not say, as
// Safari does not, and one that stays on its device keep the name given.
func TestASyncedPasskeyIsNamedAfterItsManager(t *testing.T) {
	ctx := context.Background()
	s := openUsers(t, filepath.Join(t.TempDir(), "acme.fado"), &still{now: time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)})
	gpm, zeros, hello := [16]byte{0xea, 0x9b, 0x8d, 0x66, 0x4d, 0x01, 0x1d, 0x21, 0x3c, 0xe4, 0xb6, 0xb4, 0x8c, 0xb5, 0x75, 0xd4}, [16]byte{},
		[16]byte{0x08, 0x98, 0x70, 0x58, 0xca, 0xdc, 0x4b, 0x81, 0xb6, 0xe1, 0x30, 0xde, 0x50, 0xdc, 0xbe, 0x96}
	for _, c := range []struct {
		user   string
		aaguid [16]byte
		synced bool
		given  string
		want   string
	}{
		{"ann", gpm, true, "Ann's Windows PC", "Google Password Manager"},
		{"bob", zeros, true, "Bob's iPhone", "Bob's iPhone"},
		{"cyd", hello, false, "Cyd's laptop", "Cyd's laptop"},
	} {
		if _, err := s.CreateUser(ctx, User{ID: c.user, Kind: ActorPerson}, machine); err != nil {
			t.Fatal(err)
		}
		d := webauthntest.New(webauthntest.ES256)
		d.Synced, d.AAGUID = c.synced, c.aaguid
		if c.synced {
			d.Counts = false
		}
		inv, _ := s.CreateInvitation(ctx, c.user, machine)
		invited(t, s, inv.Token, d, c.given)
		keys, err := s.Passkeys(ctx, c.user, machine)
		if err != nil || len(keys) != 1 || keys[0].Name != c.want || keys[0].Synced != c.synced {
			t.Errorf("%s: %v %+v, want %q", c.user, err, keys, c.want)
		}
	}
}
