package store

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/internal/webauthn/webauthntest"
)

// byPhone adds a passkey on a phone through the link of a code, and
// answers the phone's session.
func byPhone(s *Store, link string, phone *webauthntest.Device, device, from string) (*Session, error) {
	ctx := context.Background()
	question, err := s.PhoneQuestion(ctx, link, acmeOrigin)
	if err != nil {
		return nil, err
	}
	answer, err := phone.Make(acmeOrigin, question)
	if err != nil {
		return nil, err
	}
	return s.UsePhoneLink(ctx, link, []byte(answer), device, acmeOrigin, from, SessionLengths{})
}

// The code of an invitation (ADR-0088, item 3): Ann's computer shows it,
// her phone opens its link and adds her passkey, and the computer that
// showed the code enters too, once. The phone's passkey ends both.
func TestThePhoneCodeOfAnInvitation(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)
	clock := &still{now: now}
	s := openUsers(t, filepath.Join(t.TempDir(), "acme.fado"), clock)
	for _, u := range []User{{ID: "olga", Kind: ActorPerson, System: []string{GroupAdministrators}}, {ID: "ann", Kind: ActorPerson, Name: "Ann Nowak"}} {
		if _, err := s.CreateUser(ctx, u, machine); err != nil {
			t.Fatal(err)
		}
	}
	olgaAs := As(Principal{ID: "olga", Kind: ActorPerson, System: []string{GroupAdministrators}})
	inv, err := s.CreateInvitation(ctx, "ann", olgaAs)
	if err != nil {
		t.Fatal(err)
	}
	code, err := s.InvitationPhoneCode(ctx, inv.Token)
	if err != nil || !strings.HasPrefix(code.Link, "fadop_") || code.Computer == "" || code.Ends != now.Add(10*time.Minute).Unix() {
		t.Fatalf("the code: %v %+v", err, code)
	}
	if got := s.PhoneCodeState(code.Computer); got != PhoneWaiting {
		t.Errorf("before the phone, the code is %s", got)
	}
	if _, err := s.EnterByPhone(ctx, code.Computer, officeAt, SessionLengths{}); err == nil {
		t.Error("the computer entered before the phone added its passkey")
	}
	if who, err := s.PhoneLink(ctx, code.Link); err != nil || who.User != "ann" || who.Name != "Ann Nowak" || !who.Invited {
		t.Errorf("whom the link is for: %v %+v", err, who)
	}

	phone := webauthntest.New(webauthntest.EdDSA)
	clock.add(3 * time.Minute)
	onPhone, err := byPhone(s, code.Link, phone, "Ann's iPhone", "10.0.0.9")
	if err != nil || onPhone.User != "ann" || onPhone.Way != WayPasskey || onPhone.Passkey == "" {
		t.Fatalf("the phone's session: %v %+v", err, onPhone)
	}
	if _, err := s.Invited(ctx, inv.Token); err == nil {
		t.Error("the invitation holds after the phone used it")
	}
	if _, err := s.PhoneLink(ctx, code.Link); err == nil {
		t.Error("the link holds after the phone used it")
	}
	if _, err := byPhone(s, code.Link, webauthntest.New(webauthntest.ES256), "Eve's phone", "10.0.0.66"); err == nil {
		t.Error("the link was used twice")
	}
	if got := s.PhoneCodeState(code.Computer); got != PhoneDone {
		t.Errorf("after the phone, the code is %s", got)
	}

	onComputer, err := s.EnterByPhone(ctx, code.Computer, officeAt, SessionLengths{})
	if err != nil || onComputer.User != "ann" || onComputer.Way != WayPhone || onComputer.Passkey != onPhone.Passkey || !strings.HasPrefix(onComputer.Token, sessionPrefix) {
		t.Fatalf("the computer's session: %v %+v", err, onComputer)
	}
	if _, err := s.EnterByPhone(ctx, code.Computer, officeAt, SessionLengths{}); err == nil {
		t.Error("the computer entered twice")
	}
	if got := s.PhoneCodeState(code.Computer); got != PhoneEnded {
		t.Errorf("after the computer entered, the code is %s", got)
	}
	keys, err := s.Passkeys(ctx, "ann", olgaAs)
	if err != nil || len(keys) != 1 || keys[0].Name != "Ann's iPhone" || keys[0].AddedWith != AddedWithInvitation || keys[0].UsedAt == 0 {
		t.Fatalf("Ann's passkeys: %v %+v", err, keys)
	}
	ins, err := s.SignIns(ctx, SignInFilter{User: "ann"}, olgaAs)
	if err != nil || len(ins) != 2 || ins[0].Way != WayPasskey || ins[1].Way != WayPhone || ins[1].Address != officeAt {
		t.Errorf("Ann's sign-ins: %v %+v", err, ins)
	}

	// Her phone is lost: Olga removes its passkey, and both sessions end.
	if err := s.RemovePasskey(ctx, onPhone.Passkey, olgaAs); err != nil {
		t.Fatal(err)
	}
	for _, sess := range []*Session{onPhone, onComputer} {
		if _, _, err := s.SessionCaller(ctx, sess.Token); err == nil {
			t.Errorf("the session %s holds after the passkey was removed", sess.Way)
		}
	}
}

// The account page's code adds a passkey of Ann's on her phone; the
// computer, signed in already, only sees it done.
func TestThePhoneCodeOfTheAccount(t *testing.T) {
	ctx := context.Background()
	clock := &still{now: time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)}
	s := openUsers(t, filepath.Join(t.TempDir(), "acme.fado"), clock)
	if _, err := s.CreateUser(ctx, User{ID: "ann", Kind: ActorPerson}, machine); err != nil {
		t.Fatal(err)
	}
	ann := As(Principal{ID: "ann", Kind: ActorPerson})
	inv, _ := s.CreateInvitation(ctx, "ann", machine)
	invited(t, s, inv.Token, webauthntest.New(webauthntest.ES256), "Ann's laptop")

	code, err := s.AccountPhoneCode(ctx, ann)
	if err != nil {
		t.Fatal(err)
	}
	if who, err := s.PhoneLink(ctx, code.Link); err != nil || who.Invited {
		t.Errorf("whom the account's link is for: %v %+v", err, who)
	}
	if _, err := byPhone(s, code.Link, webauthntest.New(webauthntest.EdDSA), "Ann's Pixel", "10.0.0.9"); err != nil {
		t.Fatal(err)
	}
	if got := s.PhoneCodeState(code.Computer); got != PhoneDone {
		t.Errorf("after the phone, the code is %s", got)
	}
	keys, _ := s.Passkeys(ctx, "", ann)
	if len(keys) != 2 || keys[1].Name != "Ann's Pixel" || keys[1].AddedWith != AddedOnTheAccount {
		t.Errorf("Ann's passkeys: %+v", keys)
	}
	if _, err := s.AccountPhoneCode(ctx, As(Principal{ID: "nobody", Kind: ActorPerson})); err == nil {
		t.Error("a code for a user the database does not have")
	}
}

// A code ends after 10 minutes; a new code of the same invitation ends
// the one before; an invitation used on the computer leaves its code
// nothing to add; and a computer that comes after the 10 minutes does
// not enter, though the phone added its passkey in time.
func TestAPhoneCodeEnds(t *testing.T) {
	ctx := context.Background()
	clock := &still{now: time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)}
	s := openUsers(t, filepath.Join(t.TempDir(), "acme.fado"), clock)
	for _, id := range []string{"ann", "bob", "ewa"} {
		if _, err := s.CreateUser(ctx, User{ID: id, Kind: ActorPerson}, machine); err != nil {
			t.Fatal(err)
		}
	}

	ann, _ := s.CreateInvitation(ctx, "ann", machine)
	first, _ := s.InvitationPhoneCode(ctx, ann.Token)
	clock.add(10 * time.Minute)
	if _, err := s.PhoneLink(ctx, first.Link); err == nil {
		t.Error("a code holds after 10 minutes")
	}
	if got := s.PhoneCodeState(first.Computer); got != PhoneEnded {
		t.Errorf("after 10 minutes the code is %s", got)
	}
	second, _ := s.InvitationPhoneCode(ctx, ann.Token)
	third, _ := s.InvitationPhoneCode(ctx, ann.Token)
	if _, err := s.PhoneLink(ctx, second.Link); err == nil || s.PhoneCodeState(second.Computer) != PhoneEnded {
		t.Error("a code holds after a new one was made")
	}
	if _, err := s.PhoneLink(ctx, third.Link); err != nil {
		t.Errorf("the newest code: %v", err)
	}

	bob, _ := s.CreateInvitation(ctx, "bob", machine)
	code, _ := s.InvitationPhoneCode(ctx, bob.Token)
	invited(t, s, bob.Token, webauthntest.New(webauthntest.ES256), "Bob's PC")
	if _, err := byPhone(s, code.Link, webauthntest.New(webauthntest.ES256), "Bob's phone", "10.0.0.9"); err == nil {
		t.Error("a phone used the code of an invitation the computer used")
	}

	ewa, _ := s.CreateInvitation(ctx, "ewa", machine)
	late, _ := s.InvitationPhoneCode(ctx, ewa.Token)
	clock.add(9 * time.Minute)
	if _, err := byPhone(s, late.Link, webauthntest.New(webauthntest.ES256), "Ewa's phone", "10.0.0.9"); err != nil {
		t.Fatal(err)
	}
	clock.add(2 * time.Minute)
	if _, err := s.EnterByPhone(ctx, late.Computer, officeAt, SessionLengths{}); err == nil {
		t.Error("the computer entered after the code's 10 minutes")
	}
}

// Two phones that read the same code at once add one passkey between
// them: the other is refused.
func TestTwoPhonesReadOneCode(t *testing.T) {
	ctx := context.Background()
	clock := &still{now: time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)}
	s := openUsers(t, filepath.Join(t.TempDir(), "acme.fado"), clock)
	if _, err := s.CreateUser(ctx, User{ID: "ann", Kind: ActorPerson}, machine); err != nil {
		t.Fatal(err)
	}
	inv, _ := s.CreateInvitation(ctx, "ann", machine)
	code, _ := s.InvitationPhoneCode(ctx, inv.Token)
	var answers []string
	for range 2 {
		question, err := s.PhoneQuestion(ctx, code.Link, acmeOrigin)
		if err != nil {
			t.Fatal(err)
		}
		answer, err := webauthntest.New(webauthntest.ES256).Make(acmeOrigin, question)
		if err != nil {
			t.Fatal(err)
		}
		answers = append(answers, answer)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i, answer := range answers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.UsePhoneLink(ctx, code.Link, []byte(answer), "phone", acmeOrigin, "10.0.0."+string(rune('1'+i)), SessionLengths{})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	ok := 0
	for err := range errs {
		if err == nil {
			ok++
		}
	}
	keys, _ := s.Passkeys(ctx, "ann", machine)
	if ok != 1 || len(keys) != 1 {
		t.Errorf("%d phones added a passkey, and Ann has %d", ok, len(keys))
	}
}

// Ann's passkey is synced: her password manager keeps it on her laptop
// and her phone alike, so her phone makes no other. Her computer shows
// the code of a new invitation, her phone signs in with the passkey it
// holds, and the computer enters with the same passkey. The invitation
// is used (the owner's ruling, 2026-11-25). A passkey of another person,
// and a question asked for another thing, are refused.
func TestAPhoneThatHoldsThePasskeyAlready(t *testing.T) {
	ctx := context.Background()
	clock := &still{now: time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)}
	s := openUsers(t, filepath.Join(t.TempDir(), "acme.fado"), clock)
	for _, id := range []string{"ann", "bob"} {
		if _, err := s.CreateUser(ctx, User{ID: id, Kind: ActorPerson, Name: map[string]string{"ann": "Ann Nowak", "bob": "Bob Lis"}[id]}, machine); err != nil {
			t.Fatal(err)
		}
	}
	synced := webauthntest.New(webauthntest.ES256)
	first, _ := s.CreateInvitation(ctx, "ann", machine)
	held := invited(t, s, first.Token, synced, "Ann's laptop")
	bobs := webauthntest.New(webauthntest.ES256)
	bobInv, _ := s.CreateInvitation(ctx, "bob", machine)
	invited(t, s, bobInv.Token, bobs, "Bob's laptop")

	inv, _ := s.CreateInvitation(ctx, "ann", machine)
	if who, err := s.Invited(ctx, inv.Token); err != nil || !who.Passkeys {
		t.Errorf("the invitation of a person with a passkey: %v %+v", err, who)
	}
	code, err := s.InvitationPhoneCode(ctx, inv.Token)
	if err != nil {
		t.Fatal(err)
	}
	if who, err := s.PhoneLink(ctx, code.Link); err != nil || !who.Passkeys {
		t.Errorf("whom the link is for: %v %+v", err, who)
	}
	use := func(d *webauthntest.Device) (*Session, error) {
		q, err := s.PhoneSignInQuestion(ctx, code.Link, acmeOrigin)
		if err != nil {
			return nil, err
		}
		answer, err := d.Use(acmeOrigin, q)
		if err != nil {
			return nil, err
		}
		return s.SignInByPhoneLink(ctx, code.Link, []byte(answer), acmeOrigin, "10.0.0.9", SessionLengths{})
	}
	if _, err := use(bobs); err == nil {
		t.Error("Bob's passkey signed in with Ann's code")
	}
	// The sign-in page's question is no question of the code.
	q, _ := s.SignInQuestion(acmeOrigin)
	answer, _ := synced.Use(acmeOrigin, q)
	if _, err := s.SignInByPhoneLink(ctx, code.Link, []byte(answer), acmeOrigin, "10.0.0.9", SessionLengths{}); err == nil {
		t.Error("the sign-in page's question signed in with the code")
	}
	if got := s.PhoneCodeState(code.Computer); got != PhoneWaiting {
		t.Fatalf("after the refusals, the code is %s", got)
	}

	onPhone, err := use(synced)
	if err != nil || onPhone.User != "ann" || onPhone.Way != WayPasskey || onPhone.Passkey != held.Passkey {
		t.Fatalf("the phone's session: %v %+v", err, onPhone)
	}
	if _, err := s.Invited(ctx, inv.Token); err == nil {
		t.Error("the invitation holds after the phone signed in with it")
	}
	if _, err := use(synced); err == nil {
		t.Error("the link was used twice")
	}
	onComputer, err := s.EnterByPhone(ctx, code.Computer, officeAt, SessionLengths{})
	if err != nil || onComputer.Way != WayPhone || onComputer.Passkey != held.Passkey {
		t.Fatalf("the computer's session: %v %+v", err, onComputer)
	}
	if keys, _ := s.Passkeys(ctx, "ann", machine); len(keys) != 1 {
		t.Errorf("Ann has %d passkeys, want the one", len(keys))
	}

	// The account page's code: the phone signs in, and nothing is added.
	ann := As(Principal{ID: "ann", Kind: ActorPerson})
	code, _ = s.AccountPhoneCode(ctx, ann)
	if _, err := use(synced); err != nil {
		t.Fatalf("the account's code: %v", err)
	}
	if got := s.PhoneCodeState(code.Computer); got != PhoneDone {
		t.Errorf("after the phone, the account's code is %s", got)
	}
	if keys, _ := s.Passkeys(ctx, "ann", machine); len(keys) != 1 {
		t.Errorf("Ann has %d passkeys, want the one", len(keys))
	}
}

// The page of an invitation, opened on a device that holds Ann's synced
// passkey already: the device signs in with it, and the invitation is
// used. Bob's passkey is refused, and leaves the invitation as it was.
func TestAnInvitationOnADeviceThatHoldsThePasskey(t *testing.T) {
	ctx := context.Background()
	clock := &still{now: time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)}
	s := openUsers(t, filepath.Join(t.TempDir(), "acme.fado"), clock)
	for _, id := range []string{"ann", "bob"} {
		if _, err := s.CreateUser(ctx, User{ID: id, Kind: ActorPerson}, machine); err != nil {
			t.Fatal(err)
		}
	}
	synced, bobs := webauthntest.New(webauthntest.ES256), webauthntest.New(webauthntest.EdDSA)
	first, _ := s.CreateInvitation(ctx, "ann", machine)
	held := invited(t, s, first.Token, synced, "Ann's laptop")
	bobInv, _ := s.CreateInvitation(ctx, "bob", machine)
	invited(t, s, bobInv.Token, bobs, "Bob's laptop")

	inv, _ := s.CreateInvitation(ctx, "ann", machine)
	use := func(d *webauthntest.Device) (*Session, error) {
		q, err := s.InvitationSignInQuestion(ctx, inv.Token, acmeOrigin)
		if err != nil {
			return nil, err
		}
		answer, err := d.Use(acmeOrigin, q)
		if err != nil {
			return nil, err
		}
		return s.SignInWithInvitation(ctx, inv.Token, []byte(answer), acmeOrigin, officeAt, SessionLengths{})
	}
	if _, err := use(bobs); err == nil {
		t.Error("Bob's passkey signed in with Ann's invitation")
	}
	if _, err := s.Invited(ctx, inv.Token); err != nil {
		t.Fatalf("the invitation after Bob's passkey: %v", err)
	}
	sess, err := use(synced)
	if err != nil || sess.User != "ann" || sess.Way != WayPasskey || sess.Passkey != held.Passkey {
		t.Fatalf("the session: %v %+v", err, sess)
	}
	if _, err := s.Invited(ctx, inv.Token); err == nil {
		t.Error("the invitation holds after it was used")
	}
	if _, err := use(synced); err == nil {
		t.Error("the invitation was used twice")
	}
}
