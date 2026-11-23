package command

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MojoHand70/fadodb/internal/store"
)

// The groups of a database (ADR-0086), command by command: Olga, the
// administrator, makes auditors before anybody is in it, says what it is
// for, puts Ann in it and takes her off, and removes it; finance gets
// its record when Ann is made in it; Ann reads the groups and changes
// none.
func TestTheCommandsOfGroups(t *testing.T) {
	o := store.Options{Users: true, Now: func() time.Time { return clock }}
	o.Outbox.Manual = true
	s, err := store.Open(filepath.Join(t.TempDir(), "acme.fado"), o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	machine := store.Principal{System: []string{store.GroupAdministrators}}
	olga := store.Principal{ID: "olga", Kind: store.ActorPerson, System: []string{store.GroupAdministrators}}
	ann := store.Principal{ID: "ann", Kind: store.ActorPerson, Groups: []string{"finance"}}
	run(t, s, machine, "create_user", `{"id": "olga", "kind": "person", "system_groups": ["administrators"]}`)

	// A group a user is put in gets its record in the same commit
	// (item 3): no label, no description, made by who made the user.
	run(t, s, olga, "create_user", `{"id": "ann", "kind": "person", "groups": ["finance"]}`)
	if got, want := string(mustJSON(run(t, s, ann, "get_group", `{"name": "finance"}`))), `{"group":{"created_at":`+itoa(clock.Unix())+`,"created_by":"olga","members":1,"name":"finance"}}`; got != want {
		t.Errorf("get_group answers\n%s\nwant\n%s", got, want)
	}

	// A group made before anybody is in it (item 4), with its label and
	// what it is for.
	made := run(t, s, olga, "create_group", `{"name": "auditors", "label": "Auditors", "description": "May read every expense; changes nothing."}`)
	if got, want := string(mustJSON(made)), `{"group":{"created_at":`+itoa(clock.Unix())+`,"created_by":"olga","description":"May read every expense; changes nothing.","label":"Auditors","members":0,"name":"auditors"}}`; got != want {
		t.Errorf("create_group answers\n%s\nwant\n%s", got, want)
	}
	if e := refused(t, s, olga, "create_group", `{"name": "auditors"}`); e.Code != store.CodeDuplicate || e.Path != "/name" {
		t.Errorf("a second auditors is refused with %+v", e)
	}
	for _, name := range []string{"designers", "everyone", ""} {
		if e := refused(t, s, olga, "create_group", `{"name": "`+name+`"}`); e.Code != store.CodeBadValue && e.Code != store.CodeRequired {
			t.Errorf("a group named %q is refused with %+v", name, e)
		}
	}

	// Every user reads the groups, in the order of their names, each with
	// how many are in it; the system groups are not among them (item 9).
	groups := run(t, s, ann, "list_groups", `{}`)["groups"].([]any)
	if len(groups) != 2 || groups[0].(map[string]any)["name"] != "auditors" || groups[0].(map[string]any)["members"] != float64(0) || groups[1].(map[string]any)["members"] != float64(1) {
		t.Errorf("list_groups answers %v", groups)
	}

	// Only an administrator makes, changes or removes one.
	for _, c := range []struct{ name, object string }{{"create_group", `{"name": "x"}`}, {"set_group", `{"name": "auditors", "label": "x"}`}, {"remove_group", `{"name": "auditors"}`}, {"group_changes", `{"name": "auditors"}`}} {
		if e := refused(t, s, ann, c.name, c.object); e.Code != store.CodeForbidden {
			t.Errorf("Ann's %s is refused with %+v", c.name, e)
		}
	}

	// set_group changes the members given and no other; the name never
	// changes.
	g := run(t, s, olga, "set_group", `{"name": "auditors", "label": "External auditors"}`)["group"].(map[string]any)
	if g["label"] != "External auditors" || g["description"] != "May read every expense; changes nothing." {
		t.Errorf("set_group answers %v", g)
	}
	if e := refused(t, s, olga, "set_group", `{"name": "auditors"}`); e.Code != store.CodeBadValue {
		t.Errorf("set_group with nothing to change is refused with %+v", e)
	}
	if e := refused(t, s, olga, "set_group", `{"name": "nobody", "label": "x"}`); e.Code != store.CodeUnknownGroup || e.Path != "/name" {
		t.Errorf("set_group of a group that is none is refused with %+v", e)
	}
	if e := refused(t, s, ann, "get_group", `{"name": "nobody"}`); e.Code != store.CodeUnknownGroup {
		t.Errorf("get_group of a group that is none is refused with %+v", e)
	}

	// Ann is put in auditors with set_user, as before; the group counts
	// her, and list_users with the group says who she is.
	run(t, s, olga, "set_user", `{"id": "ann", "groups": ["finance", "auditors"]}`)
	if g := run(t, s, ann, "get_group", `{"name": "auditors"}`)["group"].(map[string]any); g["members"] != float64(1) {
		t.Errorf("auditors with Ann is %v", g)
	}
	if users := run(t, s, ann, "list_users", `{"group": "auditors"}`)["users"].([]any); len(users) != 1 || users[0].(map[string]any)["id"] != "ann" {
		t.Errorf("the users of auditors are %v", users)
	}

	// A group somebody is in is not removed (item 6); once nobody is, it
	// is, and a group of that name may be made again.
	if e := refused(t, s, olga, "remove_group", `{"name": "auditors"}`); e.Code != store.CodeNotAllowed || !strings.Contains(e.Message, "1 members") {
		t.Errorf("removing auditors with Ann in it is refused with %+v", e)
	}
	run(t, s, olga, "set_user", `{"id": "ann", "groups": ["finance"]}`)
	if answer := run(t, s, olga, "remove_group", `{"name": "auditors"}`); len(answer) != 0 {
		t.Errorf("remove_group answers %v", answer)
	}
	if e := refused(t, s, ann, "get_group", `{"name": "auditors"}`); e.Code != store.CodeUnknownGroup {
		t.Errorf("auditors after its removal is %+v", e)
	}
	if e := refused(t, s, olga, "remove_group", `{"name": "auditors"}`); e.Code != store.CodeUnknownGroup {
		t.Errorf("removing auditors again is refused with %+v", e)
	}

	// Every change is kept, a removed group's too (item 7).
	changes := run(t, s, olga, "group_changes", `{"name": "auditors"}`)["changes"].([]any)
	var what []string
	for _, c := range changes {
		what = append(what, c.(map[string]any)["what"].(string)+":"+c.(map[string]any)["by"].(string))
	}
	if strings.Join(what, ",") != "created:olga,changed:olga,removed:olga" {
		t.Errorf("the changes of auditors are %v", changes)
	}
	if last := changes[2].(map[string]any)["group"].(map[string]any); last["label"] != "External auditors" {
		t.Errorf("the removal keeps the group as it was: %v", last)
	}
	if e := refused(t, s, olga, "group_changes", `{"name": "nobody"}`); e.Code != store.CodeUnknownGroup {
		t.Errorf("the changes of a group that never was are refused with %+v", e)
	}
	run(t, s, olga, "create_group", `{"name": "auditors"}`)
}

func itoa(n int64) string { return strings.TrimSpace(string(mustJSON(n))) }
