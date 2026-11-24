package store

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// Signing in, beside a key checked (BenchmarkWhoIsCalling): ADR-0071
// says a session and a pass are checked as a key is, with one hash and
// one lookup, and that checking a password costs its hash, PBKDF2 of
// 600,000 rounds, which a person waits for once when signing in. None of
// the three has a budget of its own; they are published with the
// release.

// BenchmarkSessionCaller checks the session of one of twenty people
// signed in: the request of a person's browser, before its command.
func BenchmarkSessionCaller(b *testing.B) {
	ctx := context.Background()
	s := openUsers(b, filepath.Join(b.TempDir(), "bench.fado"), &still{now: time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)})
	const at = "10.0.0.7"
	var token string
	// Twenty, not a thousand as the keys are: each costs two hashes of
	// its password, of 60 ms each, and a session is found in a map
	// whatever their number.
	for i := range 20 {
		id := "person-" + strconv.Itoa(i)
		if _, err := s.CreateUser(ctx, User{ID: id, Kind: ActorPerson, Groups: []string{"finance"}}, machine); err != nil {
			b.Fatal(err)
		}
		link, err := s.CreatePasswordLink(ctx, id, machine)
		if err != nil {
			b.Fatal(err)
		}
		if err := s.UsePasswordLink(ctx, link.Token, id+"'s long password", at); err != nil {
			b.Fatal(err)
		}
		in, err := s.BeginSignIn(ctx, id, id+"'s long password", at)
		if err != nil {
			b.Fatal(err)
		}
		session, err := s.FinishSignIn(ctx, in.ID, "", at, SessionLengths{})
		if err != nil {
			b.Fatal(err)
		}
		if i == 10 {
			token = session.Token
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if p, _, err := s.SessionCaller(ctx, token); err != nil || p.ID != "person-10" {
			b.Fatal(p, err)
		}
	}
}

// BenchmarkPassCaller checks the pass of the agent Ann connected,
// Claude, at the door for agents: the request of Claude at /mcp, before
// its tool.
func BenchmarkPassCaller(b *testing.B) {
	ctx := context.Background()
	s := openUsers(b, filepath.Join(b.TempDir(), "bench.fado"), &still{now: time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)})
	oauthPeople(b, s)
	passes := connect(b, s, claude, annUser, 0)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if p, err := s.PassCaller(ctx, passes.Pass, AddressAgents); err != nil || p.ID != "ann~claude.ai" {
			b.Fatal(p, err)
		}
	}
}

// BenchmarkPasswordChecked is a person's password checked as they sign
// in: what the page of signing in waits for.
func BenchmarkPasswordChecked(b *testing.B) {
	ctx := context.Background()
	s := openUsers(b, filepath.Join(b.TempDir(), "bench.fado"), &still{now: time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)})
	const at = "10.0.0.7"
	if _, err := s.CreateUser(ctx, User{ID: "ann", Kind: ActorPerson, Groups: []string{"finance"}}, machine); err != nil {
		b.Fatal(err)
	}
	link, err := s.CreatePasswordLink(ctx, "ann", machine)
	if err != nil {
		b.Fatal(err)
	}
	if err := s.UsePasswordLink(ctx, link.Token, "ann's long password", at); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if in, err := s.BeginSignIn(ctx, "ann", "ann's long password", at); err != nil || in.Next != NextSession {
			b.Fatal(in, err)
		}
	}
}
