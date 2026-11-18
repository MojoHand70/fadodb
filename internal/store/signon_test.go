package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A company's sign-on (ADR-0071, item 4): Olga gives Ewa her company
// e-mail; Ewa's first sign-in ties her Microsoft account to her user by
// the e-mail Microsoft vouches for, and every later one by Microsoft's
// id for her. Nobody else gets in.
func TestACompanysSignOn(t *testing.T) {
	ctx := context.Background()
	clock := &still{now: time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)}
	path := filepath.Join(t.TempDir(), "acme.fado")
	s := openUsers(t, path, clock)
	s.CreateUser(ctx, User{ID: "olga", Kind: ActorPerson, System: []string{GroupAdministrators}}, machine)
	ewa, err := s.CreateUser(ctx, User{ID: "ewa", Kind: ActorPerson, Email: " Ewa@Acme.example ", Groups: []string{"approvers"}}, olga)
	if err != nil || ewa.Email != "ewa@acme.example" {
		t.Fatalf("Ewa with her e-mail: %v %+v", err, ewa)
	}
	_, err = s.CreateUser(ctx, User{ID: "ewa2", Kind: ActorPerson, Email: "ewa@ACME.example"}, olga)
	refusedWith(t, "the e-mail of another user", err, CodeDuplicate)
	_, err = s.CreateUser(ctx, User{ID: "bot", Kind: ActorAgent, Email: "bot@acme.example"}, olga)
	refusedWith(t, "an agent with an e-mail", err, CodeBadValue)
	_, err = s.CreateUser(ctx, User{ID: "bad", Kind: ActorPerson, Email: "ewa at acme"}, olga)
	refusedWith(t, "an e-mail that is none", err, CodeBadValue)

	const issuer = "https://login.microsoftonline.com/7d1e/v2.0"
	at := "10.0.0.9"
	first := SignedOn{Provider: "microsoft", Issuer: issuer, Subject: "oid-ewa", Email: "EWA@acme.example", EmailVerified: true}
	sess, err := s.SignOn(ctx, first, at, SessionLengths{})
	if err != nil || sess.User != "ewa" || sess.Way != WaySignOn || !strings.HasPrefix(sess.Token, sessionPrefix) {
		t.Fatalf("Ewa's first sign-on: %v %+v", err, sess)
	}
	if p, _, err := s.SessionCaller(ctx, sess.Token); err != nil || p.ID != "ewa" || len(p.Groups) != 1 {
		t.Errorf("the caller of Ewa's session: %v %+v", err, p)
	}
	changes, _ := s.UserChanges(ctx, "ewa", olga)
	if last := changes[len(changes)-1]; last.What != SignOnLinked {
		t.Errorf("the last change of Ewa is %s", last.What)
	}

	// Later, by Microsoft's id alone: her e-mail at Microsoft may change.
	later := first
	later.Email, later.EmailVerified = "ewa.nowak@acme.example", false
	if again, err := s.SignOn(ctx, later, at, SessionLengths{}); err != nil || again.User != "ewa" {
		t.Errorf("Ewa again, with another e-mail: %v", err)
	}

	// Nobody else gets in, each in the same words as a wrong password.
	for name, who := range map[string]SignedOn{
		"an e-mail Microsoft does not vouch for": {Provider: "microsoft", Issuer: issuer, Subject: "oid-ann", Email: "ann@acme.example"},
		"an account with no user":                {Provider: "microsoft", Issuer: issuer, Subject: "oid-zed", Email: "zed@acme.example", EmailVerified: true},
		"another account with Ewa's e-mail":      {Provider: "microsoft", Issuer: issuer, Subject: "oid-new", Email: "ewa@acme.example", EmailVerified: true},
		"no subject":                             {Provider: "microsoft", Issuer: issuer, Email: "ewa@acme.example", EmailVerified: true},
	} {
		_, err := s.SignOn(ctx, who, "10.0.0."+name[:1], SessionLengths{})
		if e := refusedWith(t, name, err, CodeSignInRefused); e.Message != refused().Message {
			t.Errorf("%s is refused in other words: %s", name, e.Message)
		}
	}
	// The same account of another issuer is another account.
	other := first
	other.Issuer = "https://accounts.google.com"
	if _, err := s.SignOn(ctx, other, at, SessionLengths{}); err != nil {
		t.Errorf("Ewa's Google account, tied too: %v", err)
	}

	// Disabled, she is refused.
	off := true
	s.SetUser(ctx, "ewa", UserEdit{Disabled: &off}, olga)
	_, err = s.SignOn(ctx, first, at, SessionLengths{})
	refusedWith(t, "a disabled user", err, CodeSignInRefused)
	on := false
	s.SetUser(ctx, "ewa", UserEdit{Disabled: &on}, olga)

	// The e-mail is kept when the database is opened again, and is taken
	// away with an empty one.
	s.Close()
	s = openUsers(t, path, clock)
	if u, err := s.GetUser(ctx, "ewa", olga); err != nil || u.Email != "ewa@acme.example" {
		t.Errorf("Ewa after the database was opened again: %v %+v", err, u)
	}
	empty := ""
	if u, err := s.SetUser(ctx, "ewa", UserEdit{Email: &empty}, olga); err != nil || u.Email != "" {
		t.Errorf("Ewa's e-mail taken away: %v %+v", err, u)
	}
	if _, err := s.CreateUser(ctx, User{ID: "ewa2", Kind: ActorPerson, Email: "ewa@acme.example"}, olga); err != nil {
		t.Errorf("the e-mail given to another once free: %v", err)
	}
	if log, _ := s.SignIns(ctx, SignInFilter{}, olga); len(log) == 0 || log[0].Way != WaySignOn {
		t.Errorf("the log of sign-ins: %+v", log)
	}
}
