package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// A database made before groups were records (ADR-0086, item 3): its
// users are in groups that have none. Opened again, each group a user is
// in gets one, made by nobody, and a change that says so; a group nobody
// is in gets none. Opened once more, nothing is made twice.
func TestGroupsOfAnOlderDatabase(t *testing.T) {
	ctx := context.Background()
	clock := &still{now: time.Date(2026, 11, 24, 1, 0, 0, 0, time.UTC)}
	path := filepath.Join(t.TempDir(), "older.fado")
	s := openUsers(t, path, clock)
	if _, err := s.CreateUser(ctx, User{ID: "olga", Kind: ActorPerson, System: []string{GroupAdministrators}}, machine); err != nil {
		t.Fatal(err)
	}
	for _, u := range []User{{ID: "ann", Kind: ActorPerson, Groups: []string{"finance", "approvers"}}, {ID: "ap", Kind: ActorAgent, Groups: []string{"finance"}}} {
		if _, err := s.CreateUser(ctx, u, olga); err != nil {
			t.Fatal(err)
		}
	}
	// What a database of the release before holds: users with groups, and
	// no record of a group.
	for _, stmt := range []string{"DELETE FROM fado_groups", "DELETE FROM fado_group_changes"} {
		if _, err := s.writer.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()

	for round := 1; round <= 2; round++ {
		s, err := Open(path, Options{Users: true, Now: clock.Now})
		if err != nil {
			t.Fatal(err)
		}
		groups, err := s.Groups(ctx, olga)
		if err != nil {
			t.Fatal(err)
		}
		if len(groups) != 2 || groups[0].Name != "approvers" || groups[0].Members != 1 || groups[1].Name != "finance" || groups[1].Members != 2 || groups[1].CreatedBy != "" || groups[1].CreatedAt != clock.Now().Unix() {
			t.Errorf("round %d: the groups are %+v %+v", round, groups[0], groups[1])
		}
		changes, err := s.GroupChanges(ctx, "finance", olga)
		if err != nil || len(changes) != 1 || changes[0].What != UserCreated || changes[0].By != "" || changes[0].Group.Name != "finance" {
			t.Errorf("round %d: the changes of finance are %+v, %v", round, changes, err)
		}
		s.Close()
	}
}

// A group a user is put in gets its record in the commit that puts
// them in, made by the same caller; one the user leaves keeps it.
func TestAGroupIsMadeWithItsFirstMember(t *testing.T) {
	ctx := context.Background()
	clock := &still{now: time.Date(2026, 11, 24, 1, 0, 0, 0, time.UTC)}
	s := openUsers(t, filepath.Join(t.TempDir(), "acme.fado"), clock)
	if _, err := s.CreateUser(ctx, User{ID: "olga", Kind: ActorPerson, System: []string{GroupAdministrators}}, machine); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser(ctx, User{ID: "ann", Kind: ActorPerson}, olga); err != nil {
		t.Fatal(err)
	}
	refusedWith(t, "a group before anybody is in it", func() error { _, err := s.GetGroup(ctx, "helpdesk", annUser); return err }(), CodeUnknownGroup)
	groups := []string{"helpdesk"}
	if _, err := s.SetUser(ctx, "ann", UserEdit{Groups: &groups}, olga); err != nil {
		t.Fatal(err)
	}
	g, err := s.GetGroup(ctx, "helpdesk", annUser)
	if err != nil || g.CreatedBy != "olga" || g.Members != 1 || g.CreatedAt != clock.Now().Unix() {
		t.Fatalf("helpdesk is %+v, %v", g, err)
	}
	none := []string{}
	if _, err := s.SetUser(ctx, "ann", UserEdit{Groups: &none}, olga); err != nil {
		t.Fatal(err)
	}
	if g, err := s.GetGroup(ctx, "helpdesk", annUser); err != nil || g.Members != 0 {
		t.Errorf("helpdesk with nobody in it is %+v, %v", g, err)
	}
	// A database whose program says who is calling keeps no groups.
	plain, err := Open(filepath.Join(t.TempDir(), "plain.fado"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	refusedWith(t, "the groups of a database that keeps none", func() error { _, err := plain.Groups(ctx, olga); return err }(), CodeNotSupported)
}
