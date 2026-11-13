package store

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MojoHand70/fadodb/internal/model"
)

// A caller may say for whom it acts (ADR-0050), and the engine checks
// that it may (ADR-0053): a call that names somebody in Principal.For is
// let through only while a substitution says so, a document of the class
// the engine brings to every database, model.SubstitutionClass. The
// check reads the dates of the substitution and not its state, so it
// waits for no worker: a substitution holds from its from until its
// until, and one that was withdrawn holds no more from the commit that
// withdrew it.
//
// What a commit queued runs with the "for" of the caller the commit had
// and asks nothing again (item 2): the step was decided while the
// substitution held. A host program that keeps its own substitutions
// says so at Open with Options.HostAnswersFor, and the engine then
// records what it is told and asks nothing (item 8).

// engineActor is the engine itself, as the definition of the class it
// brings records who defined it.
var engineActor = Principal{ID: "fadodb", Kind: ActorService}

// grant is one substitution that stands, as the check reads it.
type grant struct {
	id          string
	from, until int64    // until 0: until it is withdrawn
	classes     []string // upper-cased; none: every class
}

// covers reports whether the grant lets its holder act on the class at
// the moment asked. A grant that has ended by the database's clock lets
// nobody through, whatever moment the call names: a call that reads at a
// past moment is asked of the substitutions that stand now. class is ""
// for a call that names none, which only a grant for every class covers.
func (g *grant) covers(class string, moment, now int64) bool {
	if moment < g.from || g.until != 0 && (moment >= g.until || now >= g.until) {
		return false
	}
	return g.on(class)
}

// on reports whether the grant is for the class.
func (g *grant) on(class string) bool {
	if len(g.classes) == 0 {
		return true
	}
	for _, name := range g.classes {
		if strings.EqualFold(name, class) {
			return true
		}
	}
	return false
}

// pair is who acts and for whom.
type pair struct{ by, acts string }

// held is what the engine remembers of a pair: the substitutions that
// stood when it last read them, and the count of commits that had
// written a substitution by then.
type held struct {
	gen    int64
	grants []grant
}

// maxPairs bounds what the engine remembers: past it, it forgets
// everything and reads again as the calls come.
const maxPairs = 4096

// substitutions is what the store keeps for the check.
type substitutions struct {
	// host says the host program answers for "for" itself.
	host bool
	// gen counts the commits that wrote a substitution. A pair read
	// before the last of them is read again.
	gen atomic.Int64
	// touched, under the writer's lock, says the transaction in progress
	// wrote one.
	touched bool
	mu      sync.RWMutex
	seen    map[pair]held
	// reads counts the times the substitutions of a pair were read from
	// the database, for the tests of "kept in memory".
	reads atomic.Int64
}

// substituted refuses a call whose caller says it acts for somebody and
// has no substitution that lets it, with forbidden and what is missing
// (ADR-0053, item 2). class is the class the call names, "" when it
// names none; moment is the moment the call asks at, the one chosen with
// At or the database's clock, and now the database's clock. It is called
// only for a caller that names somebody: a call that acts for nobody
// tests one empty text where it is made.
func (s *Store) substituted(ctx context.Context, by, acts, class string, moment, now int64) *Error {
	if s.subs.host {
		return nil
	}
	grants, e := s.grantsOf(ctx, by, acts)
	if e != nil {
		return e
	}
	for i := range grants {
		if grants[i].covers(class, moment, now) {
			return nil
		}
	}
	return noSubstitution(by, acts, class, moment, now, grants)
}

// actsFor asks the substitution of a call outside a command, whose
// options name somebody the caller acts for.
func (s *Store) actsFor(ctx context.Context, o *options, class string) *Error {
	now := s.now().Unix()
	moment := now
	if !o.at.IsZero() {
		moment = o.at.Unix()
	}
	return s.substituted(ctx, o.principal.ID, o.principal.For, class, moment, now)
}

// grantsOf returns the substitutions that stand for the pair, from
// memory when no commit has written a substitution since they were
// read.
func (s *Store) grantsOf(ctx context.Context, by, acts string) ([]grant, *Error) {
	// The count is read before the substitutions are, so that a commit
	// that lands in between leaves the pair to be read again.
	gen := s.subs.gen.Load()
	k := pair{by, acts}
	s.subs.mu.RLock()
	h, ok := s.subs.seen[k]
	s.subs.mu.RUnlock()
	if ok && h.gen == gen {
		return h.grants, nil
	}
	grants, e := s.readGrants(ctx, by, acts)
	if e != nil {
		return nil, e
	}
	s.subs.mu.Lock()
	if s.subs.seen == nil || len(s.subs.seen) >= maxPairs {
		s.subs.seen = map[pair]held{}
	}
	s.subs.seen[k] = held{gen: gen, grants: grants}
	s.subs.mu.Unlock()
	return grants, nil
}

// readGrants reads the substitutions of a pair that are planned or
// active, as committed: on a reading connection, through the index of
// by.
func (s *Store) readGrants(ctx context.Context, by, acts string) ([]grant, *Error) {
	s.subs.reads.Add(1)
	c, e := s.class(model.SubstitutionClass)
	if e != nil {
		return nil, e
	}
	cols := append(quoteAll(systemColumns), quoteAll(columnNames(c.cls.Columns))...)
	cols = append(cols, quoteAll(c.atNames())...)
	text := "SELECT " + strings.Join(cols, ", ") + " FROM " + quote(c.cls.Name) +
		` WHERE "by" = ? AND "for" = ? AND "state" IN ('` + model.SubstitutionPlanned + `', '` + model.SubstitutionActive + `') ORDER BY "id"`
	st, err := s.filters.acquire(ctx, s.db, text)
	if err != nil {
		return nil, asError(storageErr("prepare the read of the substitutions", err))
	}
	defer s.filters.release(st)
	rows, err := st.stmt.QueryContext(ctx, by, acts)
	if err != nil {
		return nil, asError(storageErr("read the substitutions", err))
	}
	var found []*docRow
	for rows.Next() {
		row, err := c.scanRow(rows, false)
		if err != nil {
			rows.Close()
			return nil, asError(storageErr("read the substitutions", err))
		}
		found = append(found, row)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, asError(storageErr("read the substitutions", err))
	}
	var grants []grant
	for _, row := range found {
		// A substitution given under an earlier definition of the class is
		// read under it, as every document is (ADR-0039).
		vc, vrow, err := s.underVersion(ctx, c, row, false)
		if err != nil {
			return nil, asError(err)
		}
		kids, err := vc.loadChildren(ctx, vrow.id, vrow.version, nil, false)
		if err != nil {
			return nil, asError(err)
		}
		d := vc.document(vrow, kids)
		g := grant{id: d.ID}
		g.from, _ = d.Values["from"].(int64)
		g.until, _ = d.Values["until"].(int64)
		switch list := d.Values["classes"].(type) {
		case []any:
			for _, name := range list {
				if text, ok := name.(string); ok && text != "" {
					g.classes = append(g.classes, strings.ToUpper(text))
				}
			}
		case []string:
			for _, text := range list {
				if text != "" {
					g.classes = append(g.classes, strings.ToUpper(text))
				}
			}
		}
		grants = append(grants, g)
	}
	return grants, nil
}

// noSubstitution is the refusal of a caller that says it acts for
// somebody and may not: who, for whom, on what and when, what the
// substitutions of the pair say when there are some, and who gives one.
func noSubstitution(by, acts, class string, moment, now int64, grants []grant) *Error {
	caller := by
	if caller == "" {
		caller = "a call without a principal"
	}
	on := ""
	if class != "" {
		on = " on " + class
	}
	msg := "no substitution lets " + caller + " act for " + acts + on + " at " + momentText(moment)
	for i := range grants {
		g := &grants[i]
		switch {
		case g.until != 0 && now >= g.until:
			msg += "; " + g.id + " ended at " + momentText(g.until)
		case g.until != 0 && moment >= g.until:
			msg += "; " + g.id + " ends at " + momentText(g.until)
		case moment < g.from:
			msg += "; " + g.id + " begins at " + momentText(g.from)
		case class == "":
			msg += "; " + g.id + " is for " + strings.Join(g.classes, ", ") + " only, and this call names no class"
		default:
			msg += "; " + g.id + " is for " + strings.Join(g.classes, ", ") + " only"
		}
	}
	hint := acts + " gives one by creating a " + model.SubstitutionClass + " with by " + quoted(by) +
		"; when " + acts + " cannot, a database administrator does, with for " + quoted(acts) + " and a reason. Without Principal.For the caller acts for itself"
	e := hintf(CodeForbidden, "", msg, hint)
	e.Path = "/principal/for"
	return e
}

// momentText writes a moment as the formulas do, in UTC.
func momentText(secs int64) string { return time.Unix(secs, 0).UTC().Format("2006-01-02 15:04:05") }

func quoted(text string) string { return `"` + text + `"` }

// bring defines the classes the engine brings, when the database has
// none of that name or another text of it than this engine's: a new
// database gets the class, and one made by an engine with an earlier
// definition gets the next version of it, as a redefinition gives
// (ADR-0039). Open calls it before the store is anyone's.
func (s *Store) bring(ctx context.Context) error {
	s.bringing = true
	defer func() { s.bringing = false }()
	_, err := s.defineClasses(ctx, [][]byte{model.SubstitutionDefinition()}, false, engineActor)
	return err
}

// wroteSubstitution notes, under the writer's lock, that the transaction
// in progress wrote a document of the class; committed moves the count
// once the transaction is committed, so that the pairs are read again.
// A transaction taken back leaves the note for the next commit, which
// costs one read more and no wrong answer.
func (s *Store) wroteSubstitution() { s.subs.touched = true }

func (s *Store) committedSubstitutions() {
	if s.subs.touched {
		s.subs.touched = false
		s.subs.gen.Add(1)
	}
}
