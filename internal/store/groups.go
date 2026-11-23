package store

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
)

// The groups a database keeps (ADR-0086).
//
// A group is a record of its own: a name that never changes, which a
// user's groups and INGROUP name, a label a screen shows, and a
// description that says what the group is for. Membership stays on the
// user (ADR-0057, item 2): a group is answered with how many users are
// in it, and list_users with the group says who. Every group a user is
// in has a record, made in the commit that first puts somebody in it;
// a group may also be made with nobody in it, and is removed only when
// nobody is.

// Group is a group of the database, as it is answered: its record, and
// how many users are in it, the disabled ones among them.
type Group struct {
	Name        string `json:"name"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
	CreatedAt   int64  `json:"created_at"`
	CreatedBy   string `json:"created_by,omitempty"`
	Members     int    `json:"members"`
}

// GroupEdit is what SetGroup changes: the members that are set, and no
// other.
type GroupEdit struct {
	Label       *string
	Description *string
}

// GroupChange is one change to a group, as the database keeps it
// (ADR-0086, item 7): what was done, who did it, what they were, for
// whom they acted and when, with the group as the change left it.
type GroupChange struct {
	// What is created, changed or removed.
	What  string `json:"what"`
	At    int64  `json:"at"`
	By    string `json:"by,omitempty"`
	Kind  string `json:"kind,omitempty"`
	For   string `json:"for,omitempty"`
	Group *Group `json:"group,omitempty"`
}

// The kinds of change to a group; created and changed are those of a
// user's.
const GroupRemoved = "removed"

// groupsDDL makes the tables of the groups.
var groupsDDL = []string{
	`CREATE TABLE IF NOT EXISTS fado_groups (name TEXT PRIMARY KEY, label TEXT NOT NULL, description TEXT NOT NULL, created_at INTEGER NOT NULL, created_by TEXT NOT NULL, updated_at INTEGER NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS fado_group_changes (seq INTEGER PRIMARY KEY AUTOINCREMENT, grp TEXT NOT NULL, what TEXT NOT NULL, at INTEGER NOT NULL, by_id TEXT NOT NULL, by_kind TEXT NOT NULL, by_for TEXT NOT NULL, detail TEXT NOT NULL)`,
	`CREATE INDEX IF NOT EXISTS fado_group_changes_grp ON fado_group_changes (grp, seq)`,
}

// recordGroupsOfUsers makes, when a database is opened, a record for every
// group a user is in that has none, as a database made before groups were
// records holds (ADR-0086, item 3): made by nobody, at the store's moment,
// with a change that says so. Opened again, it makes nothing.
func recordGroupsOfUsers(ctx context.Context, w execer, now int64) error {
	if _, err := w.ExecContext(ctx, `INSERT INTO fado_group_changes (grp, what, at, by_id, by_kind, by_for, detail)
	  SELECT g, 'created', ?1, '', '', '', json_object('name', g, 'created_at', ?1, 'members', 0)
	  FROM (SELECT DISTINCT j.value AS g FROM fado_users, json_each(fado_users.groups) AS j) WHERE g NOT IN (SELECT name FROM fado_groups) ORDER BY g`, now); err != nil {
		return storageErr("record the groups of the users", err)
	}
	if _, err := w.ExecContext(ctx, `INSERT OR IGNORE INTO fado_groups (name, label, description, created_at, created_by, updated_at)
	  SELECT DISTINCT j.value, '', '', ?1, '', ?1 FROM fado_users, json_each(fado_users.groups) AS j`, now); err != nil {
		return storageErr("record the groups of the users", err)
	}
	return nil
}

// loadGroups reads every group into memory. loadPeople calls it.
func (s *Store) loadGroups(ctx context.Context) (map[string]*Group, error) {
	groups := map[string]*Group{}
	rows, err := s.writer.QueryContext(ctx, "SELECT name, label, description, created_at, created_by FROM fado_groups")
	if err != nil {
		return nil, storageErr("read the groups", err)
	}
	for rows.Next() {
		g := &Group{}
		if err := rows.Scan(&g.Name, &g.Label, &g.Description, &g.CreatedAt, &g.CreatedBy); err != nil {
			rows.Close()
			return nil, storageErr("read a group", err)
		}
		groups[g.Name] = g
	}
	if err := rows.Close(); err != nil {
		return nil, storageErr("read the groups", err)
	}
	return groups, nil
}

// checkGroupName refuses a name a group cannot have: the rule of a
// user's groups (ADR-0057), at the path name.
func checkGroupName(name string) *Error {
	if name == "" {
		return &Error{Code: CodeRequired, Message: "a group needs a name", Path: "/name"}
	}
	if _, _, e := checkGroups([]string{name}, nil); e != nil {
		e.Path = "/name"
		return e
	}
	return nil
}

func unknownGroup(name string) *Error {
	return &Error{Code: CodeUnknownGroup, Message: "the database has no group " + strconv.Quote(name), Hint: "list_groups lists them", Path: "/name"}
}

// answered is a group as a call answers it: a copy, with how many users
// are in it now. The caller holds the lock.
func (s *Store) answered(g *Group) *Group {
	out := *g
	out.Members = 0
	for _, u := range s.people.users {
		for _, name := range u.Groups {
			if name == g.Name {
				out.Members++
				break
			}
		}
	}
	return &out
}

// recordGroupChange writes the row of a change with the statements that
// make it.
func recordGroupChange(ctx context.Context, w execer, name, what string, by Principal, now int64, g *Group) error {
	text, err := json.Marshal(g)
	if err != nil {
		return &Error{Code: CodeStorage, Message: "write the change of a group: " + err.Error()}
	}
	if _, err := w.ExecContext(ctx, "INSERT INTO fado_group_changes (grp, what, at, by_id, by_kind, by_for, detail) VALUES (?, ?, ?, ?, ?, ?, ?)",
		name, what, now, by.ID, by.Kind, by.For, string(text)); err != nil {
		return storageErr("record the change of a group", err)
	}
	return nil
}

// newGroups makes, inside the transaction of a change to a user, a
// record for every group of the user that has none (ADR-0086, item 3),
// and answers them, for the caller to publish. The caller holds the
// writer, so nobody makes a group meanwhile.
func (s *Store) newGroups(ctx context.Context, w execer, names []string, by Principal, now int64) ([]*Group, error) {
	var made []*Group
	s.people.mu.RLock()
	for _, name := range names {
		if s.people.groups[name] == nil {
			made = append(made, &Group{Name: name, CreatedAt: now, CreatedBy: by.ID})
		}
	}
	s.people.mu.RUnlock()
	for _, g := range made {
		if _, err := w.ExecContext(ctx, "INSERT INTO fado_groups (name, label, description, created_at, created_by, updated_at) VALUES (?, '', '', ?, ?, ?)",
			g.Name, now, by.ID, now); err != nil {
			return nil, storageErr("store a group", err)
		}
		if err := recordGroupChange(ctx, w, g.Name, UserCreated, by, now, g); err != nil {
			return nil, err
		}
	}
	return made, nil
}

// publishGroups puts the groups a change to a user made where readers
// find them. The caller holds the lock.
func (s *Store) publishGroups(made []*Group) {
	for _, g := range made {
		s.people.groups[g.Name] = g
	}
}

// CreateGroup makes a group, with nobody in it. It is a database
// administrator's.
func (s *Store) CreateGroup(ctx context.Context, g Group, opts ...Option) (*Group, error) {
	o, e := s.keeper(ctx, opts, "make a group")
	if e != nil {
		return nil, e
	}
	if e := refuseAt(o, "change to a group"); e != nil {
		return nil, e
	}
	if e := checkGroupName(g.Name); e != nil {
		return nil, e
	}
	made := &Group{Name: g.Name, Label: g.Label, Description: g.Description}
	err := s.writeUsers(ctx, func(w execer, now int64) error {
		s.people.mu.RLock()
		exists := s.people.groups[g.Name] != nil
		s.people.mu.RUnlock()
		if exists {
			return &Error{Code: CodeDuplicate, Message: "a group " + strconv.Quote(g.Name) + " exists", Hint: "set_group changes its label and its description", Path: "/name"}
		}
		made.CreatedAt, made.CreatedBy = now, o.principal.ID
		if _, err := w.ExecContext(ctx, "INSERT INTO fado_groups (name, label, description, created_at, created_by, updated_at) VALUES (?, ?, ?, ?, ?, ?)",
			made.Name, made.Label, made.Description, now, made.CreatedBy, now); err != nil {
			return storageErr("store the group", err)
		}
		return recordGroupChange(ctx, w, made.Name, UserCreated, o.principal, now, made)
	}, func() { s.people.groups[made.Name] = made })
	if err != nil {
		return nil, err
	}
	s.people.mu.RLock()
	defer s.people.mu.RUnlock()
	return s.answered(made), nil
}

// SetGroup changes a group's label and its description: the members
// set, and no other. Its name never changes. It is a database
// administrator's.
func (s *Store) SetGroup(ctx context.Context, name string, edit GroupEdit, opts ...Option) (*Group, error) {
	o, e := s.keeper(ctx, opts, "change a group")
	if e != nil {
		return nil, e
	}
	if e := refuseAt(o, "change to a group"); e != nil {
		return nil, e
	}
	if edit.Label == nil && edit.Description == nil {
		return nil, hintf(CodeBadValue, "", "set_group was given nothing to change", "give label, description or both; a group's name never changes")
	}
	var changed Group
	err := s.writeUsers(ctx, func(w execer, now int64) error {
		s.people.mu.RLock()
		old := s.people.groups[name]
		s.people.mu.RUnlock()
		if old == nil {
			return unknownGroup(name)
		}
		changed = *old
		if edit.Label != nil {
			changed.Label = *edit.Label
		}
		if edit.Description != nil {
			changed.Description = *edit.Description
		}
		if _, err := w.ExecContext(ctx, "UPDATE fado_groups SET label = ?, description = ?, updated_at = ? WHERE name = ?",
			changed.Label, changed.Description, now, name); err != nil {
			return storageErr("store the group", err)
		}
		return recordGroupChange(ctx, w, name, UserChanged, o.principal, now, &changed)
	}, func() { s.people.groups[name] = &changed })
	if err != nil {
		return nil, err
	}
	s.people.mu.RLock()
	defer s.people.mu.RUnlock()
	return s.answered(&changed), nil
}

// RemoveGroup takes a group away. It is refused while somebody is in
// it (ADR-0086, item 6). It is a database administrator's.
func (s *Store) RemoveGroup(ctx context.Context, name string, opts ...Option) error {
	o, e := s.keeper(ctx, opts, "remove a group")
	if e != nil {
		return e
	}
	if e := refuseAt(o, "change to a group"); e != nil {
		return e
	}
	return s.writeUsers(ctx, func(w execer, now int64) error {
		s.people.mu.RLock()
		old := s.people.groups[name]
		var in *Group
		if old != nil {
			in = s.answered(old)
		}
		s.people.mu.RUnlock()
		if old == nil {
			return unknownGroup(name)
		}
		if in.Members > 0 {
			e := hintf(CodeNotAllowed, "", "the group "+strconv.Quote(name)+" has "+strconv.Itoa(in.Members)+" members",
				"a group is removed when nobody is in it: list_users with the group says who is, and set_user takes them off")
			e.Path = "/name"
			return e
		}
		if _, err := w.ExecContext(ctx, "DELETE FROM fado_groups WHERE name = ?", name); err != nil {
			return storageErr("remove the group", err)
		}
		return recordGroupChange(ctx, w, name, GroupRemoved, o.principal, now, in)
	}, func() { delete(s.people.groups, name) })
}

// GetGroup answers one group. Every user of the database may ask.
func (s *Store) GetGroup(ctx context.Context, name string, opts ...Option) (*Group, error) {
	if _, e := s.known(ctx, opts, "read a group"); e != nil {
		return nil, e
	}
	s.people.mu.RLock()
	defer s.people.mu.RUnlock()
	g := s.people.groups[name]
	if g == nil {
		return nil, unknownGroup(name)
	}
	return s.answered(g), nil
}

// Groups lists the groups of the database in the order of their names,
// each with how many users are in it. Every user may ask.
func (s *Store) Groups(ctx context.Context, opts ...Option) ([]*Group, error) {
	if _, e := s.known(ctx, opts, "list the groups"); e != nil {
		return nil, e
	}
	s.people.mu.RLock()
	count := make(map[string]int, len(s.people.groups))
	for _, u := range s.people.users {
		for _, name := range u.Groups {
			count[name]++
		}
	}
	list := make([]*Group, 0, len(s.people.groups))
	for _, g := range s.people.groups {
		out := *g
		out.Members = count[g.Name]
		list = append(list, &out)
	}
	s.people.mu.RUnlock()
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list, nil
}

// GroupChanges lists every change to a group, oldest first, a removed
// group's too. It is a database administrator's.
func (s *Store) GroupChanges(ctx context.Context, name string, opts ...Option) ([]GroupChange, error) {
	if _, e := s.keeper(ctx, opts, "read the changes of a group"); e != nil {
		return nil, e
	}
	rows, err := s.db.QueryContext(ctx, "SELECT what, at, by_id, by_kind, by_for, detail FROM fado_group_changes WHERE grp = ? ORDER BY seq", name)
	if err != nil {
		return nil, storageErr("read the changes of a group", err)
	}
	defer rows.Close()
	var changes []GroupChange
	for rows.Next() {
		var c GroupChange
		var detail string
		if err := rows.Scan(&c.What, &c.At, &c.By, &c.Kind, &c.For, &detail); err != nil {
			return nil, storageErr("read a change of a group", err)
		}
		c.Group = &Group{}
		if json.Unmarshal([]byte(detail), c.Group) != nil {
			return nil, &Error{Code: CodeStorage, Message: "a change of the group " + strconv.Quote(name) + " cannot be read"}
		}
		changes = append(changes, c)
	}
	if err := rows.Err(); err != nil {
		return nil, storageErr("read the changes of a group", err)
	}
	if len(changes) == 0 {
		s.people.mu.RLock()
		g := s.people.groups[name]
		s.people.mu.RUnlock()
		if g == nil {
			return nil, unknownGroup(name)
		}
	}
	return changes, nil
}
