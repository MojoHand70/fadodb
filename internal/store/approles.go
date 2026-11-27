package store

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// The roles of an app (ADR-0100). An app names the parts people play in
// it; a formula of a class of the app asks for one with INROLE; and each
// database says which groups and which users hold it. The holders are the
// database's and never the app's: an app taken to another database
// carries its roles and none of these people.

// rolesDDL makes the tables of the holders: one row a role that somebody
// holds, and the changes, each with who made it and what the role was
// left with.
var rolesDDL = []string{
	`CREATE TABLE IF NOT EXISTS fado_role_holders (app TEXT NOT NULL, role TEXT NOT NULL, groups TEXT NOT NULL, users TEXT NOT NULL, updated_at INTEGER NOT NULL, updated_by TEXT NOT NULL, PRIMARY KEY (app, role))`,
	`CREATE TABLE IF NOT EXISTS fado_role_changes (seq INTEGER PRIMARY KEY AUTOINCREMENT, app TEXT NOT NULL, role TEXT NOT NULL, what TEXT NOT NULL, at INTEGER NOT NULL, by_id TEXT NOT NULL, by_kind TEXT NOT NULL, by_for TEXT NOT NULL, groups TEXT NOT NULL, users TEXT NOT NULL)`,
	`CREATE INDEX IF NOT EXISTS fado_role_changes_role ON fado_role_changes (app, role, seq)`,
}

// The kinds of change to the holders of a role: given new holders, or
// gone with the role when the app no longer names it.
const (
	RoleSet     = "set"
	RoleRemoved = "removed"
)

// RoleHolders says who holds one role of an app on this database: the
// groups whose members hold it, and the users who hold it by name.
type RoleHolders struct {
	App    string   `json:"app"`
	Role   string   `json:"role"`
	Groups []string `json:"groups"`
	Users  []string `json:"users"`
}

// holderSet is every role somebody holds, read by every call that works
// out a caller's roles. It is made whole again after each change and
// never changed once made, so a call reads it without a lock.
type holderSet struct {
	roles []heldRole
}

// heldRole is one role and its holders. name is the role as a formula's
// call asks for it, formula.RoleName of the app and the role.
type heldRole struct {
	RoleHolders
	name string
}

// rolesOf works out the roles the principal holds, each written as
// formula.RoleName writes it: those of which one of its groups, its
// system groups or everyone is a holder, or its id. A database where
// nobody holds a role answers nil and makes nothing.
func (s *Store) rolesOf(p Principal) []string {
	set := s.holders.Load()
	if set == nil || len(set.roles) == 0 {
		return nil
	}
	var out []string
	for i := range set.roles {
		if holdsRole(p, &set.roles[i].RoleHolders) {
			out = append(out, set.roles[i].name)
		}
	}
	return out
}

// holdsRole says whether the principal holds the role. Whoever acts for
// somebody holds their own roles, as they have their own groups.
func holdsRole(p Principal, r *RoleHolders) bool {
	if p.ID != "" && slices.Contains(r.Users, p.ID) {
		return true
	}
	for _, g := range r.Groups {
		if slices.Contains(p.Groups, g) || slices.Contains(p.System, g) || (p.ID != "" && g == formula.GroupEveryone) {
			return true
		}
	}
	return false
}

// contextOf is the context of a formula that runs for the principal at
// the moment: who asks, in which groups, which roles they hold, what
// they are and for whom they act.
func (s *Store) contextOf(p Principal, now int64) formula.Context {
	return formula.Context{User: p.ID, Groups: p.Groups, System: p.System, Roles: s.rolesOf(p), Kind: p.Kind, For: p.For, Now: now}
}

// loadRoleHolders reads who holds each role. The caller is Open, after
// the apps are loaded.
func (s *Store) loadRoleHolders(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, "SELECT app, role, groups, users FROM fado_role_holders ORDER BY app, role")
	if err != nil {
		return storageErr("read the holders of the roles", err)
	}
	defer rows.Close()
	var list []RoleHolders
	for rows.Next() {
		var r RoleHolders
		var groups, users string
		if err := rows.Scan(&r.App, &r.Role, &groups, &users); err != nil {
			return storageErr("read the holders of the roles", err)
		}
		if err := json.Unmarshal([]byte(groups), &r.Groups); err != nil {
			return &Error{Code: CodeStorage, Message: "the holders of the role " + r.App + "." + r.Role + " cannot be read: " + err.Error()}
		}
		if err := json.Unmarshal([]byte(users), &r.Users); err != nil {
			return &Error{Code: CodeStorage, Message: "the holders of the role " + r.App + "." + r.Role + " cannot be read: " + err.Error()}
		}
		list = append(list, r)
	}
	if err := rows.Err(); err != nil {
		return storageErr("read the holders of the roles", err)
	}
	s.publishHolders(list)
	return nil
}

// publishHolders makes the set of holders whole again from a list.
func (s *Store) publishHolders(list []RoleHolders) {
	set := &holderSet{}
	for _, r := range list {
		if len(r.Groups) == 0 && len(r.Users) == 0 {
			continue
		}
		set.roles = append(set.roles, heldRole{RoleHolders: r, name: formula.RoleName(r.App, r.Role)})
	}
	s.holders.Store(set)
}

// heldList is the set as a list, for a change to start from.
func (s *Store) heldList() []RoleHolders {
	set := s.holders.Load()
	if set == nil {
		return nil
	}
	out := make([]RoleHolders, 0, len(set.roles))
	for _, r := range set.roles {
		out = append(out, r.RoleHolders)
	}
	return out
}

// Holders answers who holds each role of an app, in the order the app
// names its roles: a role nobody holds has two empty lists. Every caller
// may ask, as every user may list the users and their groups (ADR-0057,
// ruling 4).
func (s *Store) Holders(app string) ([]RoleHolders, error) {
	s.cmu.RLock()
	a, ok := s.apps[strings.ToUpper(app)]
	if !ok {
		e := s.noSuchApp(app)
		s.cmu.RUnlock()
		return nil, e
	}
	def := a.Def
	s.cmu.RUnlock()
	held := s.heldList()
	out := make([]RoleHolders, 0, len(def.RoleOrder))
	for _, role := range def.RoleOrder {
		r := RoleHolders{App: def.Name, Role: role, Groups: []string{}, Users: []string{}}
		for _, h := range held {
			if strings.EqualFold(h.App, def.Name) && strings.EqualFold(h.Role, role) {
				r.Groups, r.Users = h.Groups, h.Users
			}
		}
		out = append(out, r)
	}
	return out, nil
}

// RolesHeld answers the roles the caller holds, by app, each app's in the
// order it names them (ADR-0100, item 7): what a page gives its VM for
// INROLE, and what an agent reads before it acts.
func (s *Store) RolesHeld(opts ...Option) map[string][]string {
	o := apply(opts)
	held := s.heldList()
	out := map[string][]string{}
	if len(held) == 0 {
		return out
	}
	s.cmu.RLock()
	defer s.cmu.RUnlock()
	for _, name := range s.appOrder {
		a := s.apps[strings.ToUpper(name)]
		for _, role := range a.Def.RoleOrder {
			for i := range held {
				if strings.EqualFold(held[i].App, name) && strings.EqualFold(held[i].Role, role) && holdsRole(o.principal, &held[i]) {
					out[name] = append(out[name], role)
				}
			}
		}
	}
	return out
}

// SetRole gives a role of an app its holders, in place of the ones it
// had: the groups whose members hold it, and the users who hold it by
// name. Two empty lists leave it held by nobody. The same holders again
// change nothing, so an agent may send it twice. It is an
// administrator's, as keeping users and their groups is (ADR-0100,
// item 4); every change is kept.
func (s *Store) SetRole(ctx context.Context, app, role string, groups, users []string, opts ...Option) (*RoleHolders, error) {
	o, e := s.asked(ctx, opts, "")
	if e != nil {
		return nil, e
	}
	if !o.principal.in(GroupAdministrators) {
		e := hintf(CodeForbidden, "", who(o.principal)+" may not give a role to people",
			"this is for the members of the system group "+GroupAdministrators+"; a designer names the roles in the app's definition, an administrator says who holds them")
		e.Path = "/system/" + GroupAdministrators
		return nil, e
	}
	if e := refuseAt(o, "change to who holds a role"); e != nil {
		return nil, e
	}
	if groups == nil {
		groups = []string{}
	}
	if users == nil {
		users = []string{}
	}
	for i, g := range groups {
		if g == "" || controlIn(g) >= 0 {
			return nil, &Error{Code: CodeBadValue, Message: "the name of a group is a text without a control character, and not empty: " + strconv.Quote(g), Path: "/groups/" + strconv.Itoa(i)}
		}
	}
	for i, u := range users {
		if u == "" || controlIn(u) >= 0 {
			return nil, &Error{Code: CodeBadValue, Message: "the id of a user is a text without a control character, and not empty: " + strconv.Quote(u), Path: "/users/" + strconv.Itoa(i)}
		}
	}
	groups, users = once(groups), once(users)
	if s.people.on {
		s.people.mu.RLock()
		for i, u := range users {
			if s.people.users[u] == nil {
				s.people.mu.RUnlock()
				e := unknownUser(u)
				e.Path = "/users/" + strconv.Itoa(i)
				return nil, e
			}
		}
		s.people.mu.RUnlock()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errf(CodeClosed, "", "the store is closed")
	}
	s.cmu.RLock()
	a, ok := s.apps[strings.ToUpper(app)]
	if !ok {
		e := s.noSuchApp(app)
		s.cmu.RUnlock()
		e.Path = "/app"
		return nil, e
	}
	def := a.Def
	s.cmu.RUnlock()
	at := slices.IndexFunc(def.RoleOrder, func(r string) bool { return strings.EqualFold(r, role) })
	if at < 0 {
		hint := "the app " + def.Name + " names no roles; a designer names them in its definition, under roles"
		if len(def.RoleOrder) > 0 {
			hint = "the roles of " + def.Name + " are " + strings.Join(def.RoleOrder, ", ")
		}
		return nil, &Error{Code: CodeUnknownRole, Message: "the app " + def.Name + " has no role " + strconv.Quote(role), Hint: hint, Path: "/role"}
	}
	made := RoleHolders{App: def.Name, Role: def.RoleOrder[at], Groups: groups, Users: users}
	list := s.heldList()
	had := -1
	for i := range list {
		if strings.EqualFold(list[i].App, made.App) && strings.EqualFold(list[i].Role, made.Role) {
			had = i
		}
	}
	if had >= 0 && slices.Equal(list[had].Groups, groups) && slices.Equal(list[had].Users, users) {
		return &made, nil
	}
	if had < 0 && len(groups) == 0 && len(users) == 0 {
		return &made, nil
	}
	w := s.writer
	if _, err := w.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return nil, storageErr("begin", err)
	}
	now := s.now().Unix()
	if err := writeHolders(ctx, w, made, RoleSet, o.principal, now); err != nil {
		_, _ = w.ExecContext(context.WithoutCancel(ctx), "ROLLBACK")
		return nil, err
	}
	if err := s.endTransaction(ctx); err != nil {
		_, _ = w.ExecContext(context.WithoutCancel(ctx), "ROLLBACK")
		return nil, storageErr("commit", err)
	}
	if had >= 0 {
		list[had] = made
	} else {
		list = append(list, made)
	}
	s.publishHolders(list)
	return &made, nil
}

// writeHolders stores the holders of a role, or takes its row away when
// it is left with none, and records the change.
func writeHolders(ctx context.Context, w execer, r RoleHolders, what string, by Principal, now int64) error {
	groups, users := listJSON(r.Groups), listJSON(r.Users)
	var err error
	if len(r.Groups) == 0 && len(r.Users) == 0 {
		_, err = w.ExecContext(ctx, "DELETE FROM fado_role_holders WHERE app = ? AND role = ?", r.App, r.Role)
	} else {
		_, err = w.ExecContext(ctx, "INSERT INTO fado_role_holders (app, role, groups, users, updated_at, updated_by) VALUES (?, ?, ?, ?, ?, ?)"+
			" ON CONFLICT (app, role) DO UPDATE SET groups = excluded.groups, users = excluded.users, updated_at = excluded.updated_at, updated_by = excluded.updated_by",
			r.App, r.Role, groups, users, now, by.ID)
	}
	if err != nil {
		return storageErr("store the holders of a role", err)
	}
	if _, err := w.ExecContext(ctx, "INSERT INTO fado_role_changes (app, role, what, at, by_id, by_kind, by_for, groups, users) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		r.App, r.Role, what, now, by.ID, by.Kind, by.For, groups, users); err != nil {
		return storageErr("record the change of a role", err)
	}
	return nil
}

// RoleChange is one change to who holds a role: what was done, when, who
// did it and for whom, and the holders it left.
type RoleChange struct {
	What   string   `json:"what"`
	At     int64    `json:"at"`
	By     string   `json:"by,omitempty"`
	Kind   string   `json:"kind,omitempty"`
	For    string   `json:"for,omitempty"`
	Groups []string `json:"groups"`
	Users  []string `json:"users"`
}

// RoleChanges answers the changes to who holds a role, the earliest
// first, those of a role the app no longer names too. It is an
// administrator's, as the changes of a group are.
func (s *Store) RoleChanges(ctx context.Context, app, role string, opts ...Option) ([]RoleChange, error) {
	o, e := s.asked(ctx, opts, "")
	if e != nil {
		return nil, e
	}
	if !o.principal.in(GroupAdministrators) {
		e := hintf(CodeForbidden, "", who(o.principal)+" may not read the changes of a role",
			"this is for the members of the system group "+GroupAdministrators)
		e.Path = "/system/" + GroupAdministrators
		return nil, e
	}
	rows, err := s.db.QueryContext(ctx, "SELECT what, at, by_id, by_kind, by_for, groups, users FROM fado_role_changes WHERE app = ? COLLATE NOCASE AND role = ? COLLATE NOCASE ORDER BY seq", app, role)
	if err != nil {
		return nil, storageErr("read the changes of a role", err)
	}
	defer rows.Close()
	out := []RoleChange{}
	for rows.Next() {
		var c RoleChange
		var groups, users string
		if err := rows.Scan(&c.What, &c.At, &c.By, &c.Kind, &c.For, &groups, &users); err != nil {
			return nil, storageErr("read the changes of a role", err)
		}
		_ = json.Unmarshal([]byte(groups), &c.Groups)
		_ = json.Unmarshal([]byte(users), &c.Users)
		out = append(out, c)
	}
	return out, rows.Err()
}

// rolesAsked says, of the roles an app's next version leaves out, which
// a class or a table of the app asks for: each such class or table, by
// name, with the role. The caller holds cmu.
func (s *Store) rolesAsked(app string, gone []string) []string {
	var out []string
	asks := func(kind, name string, roles []string) {
		for _, r := range roles {
			if slices.ContainsFunc(gone, func(g string) bool { return strings.EqualFold(g, r) }) {
				out = append(out, kind+" "+name+" asks for "+r)
			}
		}
	}
	for _, name := range s.order {
		if c, ok := s.classes[strings.ToUpper(name)]; ok && strings.EqualFold(c.cls.App, app) {
			asks("the class", c.cls.Name, c.cls.Roles)
		}
	}
	for _, name := range s.tableOrder {
		if t, ok := s.tables[strings.ToUpper(name)]; ok && strings.EqualFold(t.def.App, app) {
			asks("the table", t.def.Name, t.def.Roles)
		}
	}
	return out
}

// dropHolders takes away, with the statements of an app's next version,
// the holders of the roles it no longer names, and records the change.
// It answers the list the set is made again from.
func (s *Store) dropHolders(ctx context.Context, w execer, app string, gone []string, by Principal, now int64) ([]RoleHolders, error) {
	list := s.heldList()
	kept := list[:0:0]
	for _, h := range list {
		if strings.EqualFold(h.App, app) && slices.ContainsFunc(gone, func(g string) bool { return strings.EqualFold(g, h.Role) }) {
			if err := writeHolders(ctx, w, RoleHolders{App: h.App, Role: h.Role}, RoleRemoved, by, now); err != nil {
				return nil, err
			}
			continue
		}
		kept = append(kept, h)
	}
	return kept, nil
}
