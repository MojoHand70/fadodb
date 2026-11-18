package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Signing in (ADR-0071), from the first administrator on the machine to
// a session that ends: a password set through a link, the second step
// an administrator must set up, a code from the app and one for when the
// phone is lost, a person who needs none, a session that is used and
// that ends, and the log of every sign-in.
func TestSigningIn(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	clock := &still{now: now}
	path := filepath.Join(t.TempDir(), "acme.fado")
	s := openUsers(t, path, clock)
	for _, u := range []User{
		{ID: "olga", Kind: ActorPerson, System: []string{GroupAdministrators}},
		{ID: "ann", Kind: ActorPerson, Groups: []string{"finance"}},
		{ID: "ap-agent", Kind: ActorAgent, Groups: []string{"finance"}},
	} {
		if _, err := s.CreateUser(ctx, u, machine); err != nil {
			t.Fatal(err)
		}
	}
	const at = "10.0.0.7"

	// Nobody has a password yet: a sign-in is refused in the same words
	// as a wrong password, for a user the database does not have too.
	_, none := s.BeginSignIn(ctx, "olga", "correct horse battery", at)
	_, ghost := s.BeginSignIn(ctx, "nobody", "correct horse battery", at)
	if refusedWith(t, "no password", none, CodeSignInRefused).Message != refusedWith(t, "no user", ghost, CodeSignInRefused).Message {
		t.Errorf("the refusals differ: %v / %v", none, ghost)
	}

	// The machine makes Olga a link; she sets her password with it.
	link, err := s.CreatePasswordLink(ctx, "olga", machine)
	if err != nil || !strings.HasPrefix(link.Token, linkPrefix) || link.Ends != now.Add(24*time.Hour).Unix() || link.MadeBy != "" {
		t.Fatalf("the link: %v %+v", err, link)
	}
	if user, err := s.PasswordLinkUser(ctx, link.Token); err != nil || user != "olga" {
		t.Errorf("the link's user: %v %q", err, user)
	}
	refusedWith(t, "a short password", s.UsePasswordLink(ctx, link.Token, "short", at), CodeBadValue)
	if err := s.UsePasswordLink(ctx, link.Token, "olga's long password", at); err != nil {
		t.Fatal(err)
	}
	refusedWith(t, "the link used twice", s.UsePasswordLink(ctx, link.Token, "another long password", at), CodeSignInRefused)
	if _, err := s.CreatePasswordLink(ctx, "ap-agent", olga); err == nil {
		t.Error("an agent was given a password")
	}

	// Olga is an administrator: after her password she sets up the
	// second step, and its first code gives her the session.
	refusedWith(t, "a wrong password", func() error { _, err := s.BeginSignIn(ctx, "olga", "olga's wrong password", at); return err }(), CodeSignInRefused)
	in, err := s.BeginSignIn(ctx, "olga", "olga's long password", at)
	if err != nil || in.Next != NextSetUp {
		t.Fatalf("Olga signs in: %v %+v", err, in)
	}
	step, err := s.SetUpSecondStep(ctx, in.ID)
	if err != nil || len(strings.ReplaceAll(step.Secret, " ", "")) != 32 || !strings.HasPrefix(step.Link, "otpauth://totp/FadoDB:olga?secret=") {
		t.Fatalf("the second step: %v %+v", err, step)
	}
	if _, err := s.FinishSignIn(ctx, in.ID, "000000", at, SessionLengths{}); err == nil {
		t.Error("a wrong code gave a session")
	}
	sess, err := s.FinishSignIn(ctx, in.ID, StepCode(step.Secret, now), at, SessionLengths{})
	if err != nil || !strings.HasPrefix(sess.Token, sessionPrefix) || len(sess.Codes) != 10 || sess.Way != WaySecondStep || sess.Ends != now.Add(30*24*time.Hour).Unix() {
		t.Fatalf("Olga's session: %v %+v", err, sess)
	}
	if _, err := s.FinishSignIn(ctx, in.ID, StepCode(step.Secret, now), at, SessionLengths{}); err == nil {
		t.Error("a sign-in was finished twice")
	}
	p, _, err := s.SessionCaller(ctx, sess.Token)
	if err != nil || p.ID != "olga" || len(p.System) != 1 {
		t.Fatalf("the caller of the session: %v %+v", err, p)
	}

	// The next day she gives a code from her app; a code used once is
	// refused the second time; a code for when the phone is lost holds
	// once.
	clock.set(now.Add(24 * time.Hour))
	in, _ = s.BeginSignIn(ctx, "olga", "olga's long password", at)
	if in.Next != NextCode {
		t.Fatalf("the next sign-in asks %s", in.Next)
	}
	code := StepCode(step.Secret, clock.Now())
	if _, err := s.FinishSignIn(ctx, in.ID, code, at, SessionLengths{}); err != nil {
		t.Fatal(err)
	}
	in, _ = s.BeginSignIn(ctx, "olga", "olga's long password", at)
	refusedWith(t, "a code used twice", func() error { _, err := s.FinishSignIn(ctx, in.ID, code, at, SessionLengths{}); return err }(), CodeSignInRefused)
	lost := strings.ToLower(sess.Codes[3])
	if _, err := s.FinishSignIn(ctx, in.ID, lost, at, SessionLengths{}); err != nil {
		t.Errorf("a code for a lost phone: %v", err)
	}
	in, _ = s.BeginSignIn(ctx, "olga", "olga's long password", at)
	refusedWith(t, "a code for a lost phone used twice", func() error { _, err := s.FinishSignIn(ctx, in.ID, lost, at, SessionLengths{}); return err }(), CodeSignInRefused)

	// Ann needs no second step. Her session holds while she uses it, ends
	// after eight hours unused, and ends when she is disabled.
	annLink, _ := s.CreatePasswordLink(ctx, "ann", olga)
	if err := s.UsePasswordLink(ctx, annLink.Token, "ann's long password", "10.0.0.8"); err != nil {
		t.Fatal(err)
	}
	in, err = s.BeginSignIn(ctx, "ann", "ann's long password", "10.0.0.8")
	if err != nil || in.Next != NextSession {
		t.Fatalf("Ann signs in: %v %+v", err, in)
	}
	ann, err := s.FinishSignIn(ctx, in.ID, "", "10.0.0.8", SessionLengths{Idle: time.Hour})
	if err != nil || ann.Idle != 3600 || len(ann.Codes) != 0 {
		t.Fatalf("Ann's session: %v %+v", err, ann)
	}
	clock.set(clock.Now().Add(50 * time.Minute))
	if _, _, err := s.SessionCaller(ctx, ann.Token); err != nil {
		t.Errorf("Ann's session after 50 minutes: %v", err)
	}
	clock.set(clock.Now().Add(55 * time.Minute))
	if _, _, err := s.SessionCaller(ctx, ann.Token); err != nil {
		t.Errorf("Ann's session 55 minutes after she used it: %v", err)
	}
	clock.set(clock.Now().Add(61 * time.Minute))
	if _, _, err := s.SessionCaller(ctx, ann.Token); err != ErrUnknownKey {
		t.Errorf("Ann's session an hour unused: %v", err)
	}
	in, _ = s.BeginSignIn(ctx, "ann", "ann's long password", "10.0.0.8")
	ann, _ = s.FinishSignIn(ctx, in.ID, "", "10.0.0.8", SessionLengths{})
	if list, err := s.Sessions(ctx, "", annUser); err != nil || len(list) != 1 || list[0].ID != ann.ID {
		t.Errorf("Ann's sessions: %v %+v", err, list)
	}
	if _, err := s.Sessions(ctx, "olga", annUser); err == nil {
		t.Error("Ann listed Olga's sessions")
	}
	disabled := true
	if _, err := s.SetUser(ctx, "ann", UserEdit{Disabled: &disabled}, olga); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SessionCaller(ctx, ann.Token); err != ErrUnknownKey {
		t.Errorf("the session of a disabled user: %v", err)
	}
	enabled := false
	s.SetUser(ctx, "ann", UserEdit{Disabled: &enabled}, olga)

	// Ann changes her password, giving the old one; Olga ends her
	// session.
	refusedWith(t, "a wrong old password", s.ChangePassword(ctx, "not the password", "ann's new long password", "10.0.0.8", annUser), CodeSignInRefused)
	if err := s.ChangePassword(ctx, "ann's long password", "ann's new long password", "10.0.0.8", annUser); err != nil {
		t.Fatal(err)
	}
	if err := s.EndSession(ctx, ann.ID, olga); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SessionCaller(ctx, ann.Token); err != ErrUnknownKey {
		t.Errorf("a session Olga ended: %v", err)
	}
	refusedWith(t, "a session that is another's", s.EndSession(ctx, sess.ID, annUser), CodeUnknownSession)

	// Ann sets up a second step of her own, and clears it; Dan's is
	// required, so he may not clear his own, and an administrator may.
	set, err := s.SetUpMySecondStep(ctx, annUser)
	if err != nil {
		t.Fatal(err)
	}
	if codes, err := s.ConfirmMySecondStep(ctx, StepCode(set.Secret, clock.Now()), "10.0.0.8", annUser); err != nil || len(codes) != 10 {
		t.Fatalf("Ann's own second step: %v %v", err, codes)
	}
	if has, required, err := s.HasSecondStep(ctx, "", annUser); err != nil || !has || required {
		t.Errorf("Ann's second step: %v %v %v", has, required, err)
	}
	if err := s.ClearSecondStep(ctx, "", annUser); err != nil {
		t.Errorf("Ann clears her own: %v", err)
	}
	s.CreateUser(ctx, User{ID: "dan", Kind: ActorPerson, System: []string{GroupDesigners}}, olga)
	refusedWith(t, "Dan clears his own required one", s.ClearSecondStep(ctx, "", danUser), CodeForbidden)
	if err := s.ClearSecondStep(ctx, "dan", olga); err != nil {
		t.Errorf("Olga clears Dan's: %v", err)
	}

	// The log keeps every sign-in, refused ones too; it is an
	// administrator's.
	log, err := s.SignIns(ctx, SignInFilter{User: "olga"}, olga)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range log {
		mark := "ok"
		if !e.OK {
			mark = "refused"
		}
		got = append(got, e.Way+" "+mark)
	}
	if want := "password refused link ok password refused second_step refused second_step ok second_step ok second_step refused second_step ok second_step refused"; strings.Join(got, " ") != want {
		t.Errorf("Olga's sign-ins:\n%s\nwant\n%s", strings.Join(got, " "), want)
	}
	if _, err := s.SignIns(ctx, SignInFilter{}, annUser); err == nil {
		t.Error("Ann read the log of sign-ins")
	}

	// Opened again, a session that holds is there, and one that was
	// ended is not.
	in, _ = s.BeginSignIn(ctx, "ann", "ann's new long password", "10.0.0.8")
	kept, err := s.FinishSignIn(ctx, in.ID, "", "10.0.0.8", SessionLengths{})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s = openUsers(t, path, clock)
	if p, _, err := s.SessionCaller(ctx, kept.Token); err != nil || p.ID != "ann" {
		t.Errorf("Ann's session after the database was opened again: %v %+v", err, p)
	}
	if _, _, err := s.SessionCaller(ctx, ann.Token); err != ErrUnknownKey {
		t.Errorf("an ended session after the database was opened again: %v", err)
	}
}

// Sign-ins that keep failing wait: five within a quarter of an hour for
// one user, twenty from one address, and the wait grows.
func TestSignInsThatKeepFailingWait(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)
	clock := &still{now: now}
	s := openUsers(t, filepath.Join(t.TempDir(), "acme.fado"), clock)
	s.CreateUser(ctx, User{ID: "ann", Kind: ActorPerson}, machine)
	link, _ := s.CreatePasswordLink(ctx, "ann", machine)
	s.UsePasswordLink(ctx, link.Token, "ann's long password", "10.0.0.8")
	for i := 0; i < 5; i++ {
		_, err := s.BeginSignIn(ctx, "ann", "a guess at it", "10.0.0.9")
		refusedWith(t, "a guess", err, CodeSignInRefused)
	}
	_, err := s.BeginSignIn(ctx, "ann", "ann's long password", "10.0.0.8")
	refusedWith(t, "the right password after five guesses", err, CodeTooManyAttempts)
	clock.set(now.Add(2 * time.Second))
	if _, err := s.BeginSignIn(ctx, "ann", "ann's long password", "10.0.0.8"); err != nil {
		t.Errorf("after the wait: %v", err)
	}
	// Twenty guesses from one address, at as many users, and the address
	// waits; another address does not.
	for i := 0; i < 20; i++ {
		s.BeginSignIn(ctx, "user"+string(rune('a'+i)), "a guess at it", "10.0.0.66")
	}
	_, err = s.BeginSignIn(ctx, "ann", "ann's long password", "10.0.0.66")
	refusedWith(t, "the right password from a guessing address", err, CodeTooManyAttempts)
	if _, err := s.BeginSignIn(ctx, "ann", "ann's long password", "10.0.0.8"); err != nil {
		t.Errorf("Ann from her own address: %v", err)
	}
}
