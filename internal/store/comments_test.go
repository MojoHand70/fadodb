package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// Comments on a document (ADR-0098). Ann writes memos; an auditor reads
// every memo; Bob reads none; an agent acts for Ann.
const remarkClass = `{"name": "Remark",
 "variables": {"what": {"type": "text"}, "amount": {"type": "number"}},
 "states": {"initial": "open", "list": ["open", "closed"], "final": ["closed"]},
 "transitions": [{"name": "close", "from": "open", "to": "closed"}],
 "access": {"read": "CREATEDBY() = USER() OR CREATEDBY() = ACTINGFOR() OR INGROUP(\"auditors\")", "create": "TRUE", "update": "CREATEDBY() = USER()", "delete": "CREATEDBY() = USER()"}}`

var (
	memoAnn     = Principal{ID: "ann", Kind: ActorPerson}
	memoBob     = Principal{ID: "bob", Kind: ActorPerson}
	memoAuditor = Principal{ID: "eve", Kind: ActorPerson, Groups: []string{"auditors"}}
	memoAgent   = Principal{ID: "reader-1", Kind: ActorAgent, For: "ann"}
)

func codeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	if err != nil {
		return err.Error()
	}
	return ""
}

func TestComments(t *testing.T) {
	now := &clock{now: start}
	tick := func() { now.mu.Lock(); now.now = now.now.Add(time.Minute); now.mu.Unlock() }
	s, path := openStore(t, Options{Now: now.Now})
	ctx := context.Background()
	if _, err := s.defineOpen(ctx, []byte(remarkClass), asSetup); err != nil {
		t.Fatal(err)
	}
	doc, err := s.Create(ctx, "Remark", Values{"what": "Hotel", "amount": 980}, As(memoAnn))
	if err != nil {
		t.Fatal(err)
	}

	// Ann comments: the comment is hers, on version 1, and the document stays at version 1.
	tick()
	first, err := s.Comment(ctx, "Remark", doc.ID, "Why two nights?\r\nThe trip was one day.", As(memoAnn))
	if err != nil || first.ActorID != "ann" || first.ActorKind != ActorPerson || first.Version != 1 || first.Text != "Why two nights?\nThe trip was one day." || first.DocID != doc.ID || first.ID == "" {
		t.Fatalf("Ann's comment: %v %s", err, dump(first))
	}
	if again, err := s.Get(ctx, "Remark", doc.ID, As(memoAnn)); err != nil || again.Version != 1 {
		t.Fatalf("a comment makes no version of the document: %v %s", err, dump(again))
	}
	// Its event: commented, the comment's id, the version it was written on, no version after.
	evs, err := s.Events(ctx, "Remark", doc.ID, As(memoAnn))
	last := evs[len(evs)-1]
	if err != nil || len(evs) != 2 || last.Kind != EventCommented || last.ID != first.ID || last.BeforeVersion != 1 || last.AfterVersion != 0 || last.ActorID != "ann" || last.ActorKind != ActorPerson {
		t.Fatalf("the event of a comment: %v %s", err, dump(evs))
	}

	// Bob may not see the memo: his comment is refused as if there were no memo, and he reads none.
	if _, err := s.Comment(ctx, "Remark", doc.ID, "Nice.", As(memoBob)); codeOf(err) != CodeUnknownDocument {
		t.Fatalf("Bob comments on a memo he may not see: %v", err)
	}
	if got, err := s.Comments(ctx, "Remark", doc.ID, As(memoBob)); err != nil || got != nil {
		t.Fatalf("Bob reads the comments of a memo he may not see: %v %s", err, dump(got))
	}

	// The auditor reads every memo and comments; an agent comments for Ann.
	tick()
	if _, err := s.Comment(ctx, "Remark", doc.ID, "Policy allows one night.", As(memoAuditor)); err != nil {
		t.Fatal(err)
	}
	gives(t, s, "reader-1", "ann")
	tick()
	byAgent, err := s.Comment(ctx, "Remark", doc.ID, "The receipt shows 2 nights.", As(memoAgent))
	if err != nil || byAgent.ActorID != "reader-1" || byAgent.ActorKind != ActorAgent || byAgent.ActorFor != "ann" {
		t.Fatalf("an agent comments for Ann: %v %s", err, dump(byAgent))
	}
	all, err := s.Comments(ctx, "Remark", doc.ID, As(memoAnn))
	if err != nil || len(all) != 3 || all[0].ActorID != "ann" || all[1].ActorID != "eve" || all[2].ActorID != "reader-1" || all[2].ActorFor != "ann" {
		t.Fatalf("the comments, oldest first: %v %s", err, dump(all))
	}
	// Read at a moment: what was written up to it.
	if got, err := s.Comments(ctx, "Remark", doc.ID, As(memoAnn), At(time.Unix(all[1].TS, 0))); err != nil || len(got) != 2 {
		t.Fatalf("the comments up to a moment: %v %s", err, dump(got))
	}

	// The words: empty once trimmed, a control character, too long.
	for _, c := range []struct{ text, code string }{
		{"  \n\t ", CodeRequired}, {"a\x00b", CodeBadValue}, {strings.Repeat("x", s.Limits().MaxText+1), CodeOutOfRange},
	} {
		if _, err := s.Comment(ctx, "Remark", doc.ID, c.text, As(memoAnn)); codeOf(err) != c.code {
			t.Fatalf("the words %.20q: %v", c.text, err)
		}
	}

	// Repeated safely: the same key and words answer the first comment and write nothing; other words are refused.
	tick()
	k1, err := s.Comment(ctx, "Remark", doc.ID, "Checked.", As(memoAnn), Key("ann-k1"))
	if err != nil {
		t.Fatal(err)
	}
	tick()
	k1again, err := s.Comment(ctx, "Remark", doc.ID, "Checked.", As(memoAnn), Key("ann-k1"))
	if err != nil || k1again.ID != k1.ID || k1again.TS != k1.TS {
		t.Fatalf("the same key again: %v %s %s", err, dump(k1), dump(k1again))
	}
	if _, err := s.Comment(ctx, "Remark", doc.ID, "Checked twice.", As(memoAnn), Key("ann-k1")); codeOf(err) != CodeKeyReused {
		t.Fatalf("the key with other words: %v", err)
	}
	if _, err := s.Set(ctx, "Remark", doc.ID, Values{"amount": 490}, As(memoAnn), Key("ann-set")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Comment(ctx, "Remark", doc.ID, "Checked.", As(memoAnn), Key("ann-set")); codeOf(err) != CodeKeyReused {
		t.Fatalf("a set's key on a comment: %v", err)
	}
	if got, _ := s.Comments(ctx, "Remark", doc.ID, As(memoAnn)); len(got) != 4 {
		t.Fatalf("one comment for the key: %s", dump(got))
	}

	// The version its sender read: the memo is at 2 now.
	if _, err := s.Comment(ctx, "Remark", doc.ID, "On what I read.", As(memoAnn), Saw(1)); codeOf(err) != CodeStaleVersion {
		t.Fatalf("a comment on a version that moved on: %v", err)
	}
	if made, err := s.Comment(ctx, "Remark", doc.ID, "On version 2.", As(memoAnn), Saw(2)); err != nil || made.Version != 2 {
		t.Fatalf("a comment on the version read: %v %s", err, dump(made))
	}
	// A write runs at the database's clock.
	if _, err := s.Comment(ctx, "Remark", doc.ID, "Then.", As(memoAnn), At(start)); codeOf(err) != CodeNotAllowed {
		t.Fatalf("a comment at another moment: %v", err)
	}

	// A closed memo takes comments: an auditor's note after the fact.
	if _, err := s.Transition(ctx, "Remark", doc.ID, "close", nil, As(memoAnn)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Comment(ctx, "Remark", doc.ID, "Paid back in full.", As(memoAuditor)); err != nil {
		t.Fatalf("a comment on a closed memo: %v", err)
	}

	// The table is the database's: it stays through a reopening.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(path, Options{Now: now.Now, Outbox: OutboxOptions{Manual: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if got, err := s2.Comments(ctx, "Remark", doc.ID, As(memoAnn)); err != nil || len(got) != 6 || got[0].Text != first.Text {
		t.Fatalf("the comments after a reopening: %v %s", err, dump(got))
	}
	// A deleted document takes no comment, and its comments are still read, as its events are.
	other, err := s2.Create(ctx, "Remark", Values{"what": "Taxi"}, As(memoAnn))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Comment(ctx, "Remark", other.ID, "Wrong memo.", As(memoAnn)); err != nil {
		t.Fatal(err)
	}
	if err := s2.Delete(ctx, "Remark", other.ID, As(memoAnn)); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Comment(ctx, "Remark", other.ID, "Too late.", As(memoAnn)); codeOf(err) != CodeUnknownDocument {
		t.Fatalf("a comment on a deleted memo: %v", err)
	}
	if got, err := s2.Comments(ctx, "Remark", other.ID, As(memoAnn)); err != nil || len(got) != 1 {
		t.Fatalf("the comments of a deleted memo: %v %s", err, dump(got))
	}
	// No class may take the table's name.
	if _, err := s2.defineOpen(ctx, []byte(`{"name": "fado_comments", "variables": {"x": {"type": "text"}}, "states": {"initial": "a", "list": ["a"]}}`), asSetup); err == nil {
		t.Fatal("a class named fado_comments was defined")
	}
}
