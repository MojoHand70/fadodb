package store

import (
	"context"
	"database/sql"
	"sync"
)

// filterStmts caches the prepared statements of Query and Count by their
// SQL text (ADR-0038). SQLite's parse is where a filter of a few words
// spends half its time, and where a read right compiled into the
// predicate (ADR-0041) would otherwise cost its share on every call; a
// repeated filter pays it once. The cache holds at most maxFilterStmts
// statements, evicts the least recently used one no call is running, and
// is emptied when a class is redefined.
type filterStmts struct {
	mu    sync.Mutex
	tick  uint64
	stmts map[string]*filterStmt
}

const maxFilterStmts = 64

// filterStmt is one cached statement with the count of the calls running
// it. A statement that leaves the cache while in use is retired: the last
// call to release it closes it.
type filterStmt struct {
	stmt    *sql.Stmt
	used    uint64
	inUse   int
	retired bool
}

// acquire returns the statement for the text, preparing it when the cache
// lacks it, and counts the caller as running it until release. When every
// cached statement is in use the new one is used once and closed.
func (f *filterStmts) acquire(ctx context.Context, db *sql.DB, text string) (*filterStmt, error) {
	f.mu.Lock()
	if e, ok := f.stmts[text]; ok {
		f.tick++
		e.used, e.inUse = f.tick, e.inUse+1
		f.mu.Unlock()
		return e, nil
	}
	f.mu.Unlock()
	st, err := db.PrepareContext(ctx, text)
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if prev, ok := f.stmts[text]; ok {
		// Another call prepared it meanwhile.
		st.Close()
		f.tick++
		prev.used, prev.inUse = f.tick, prev.inUse+1
		return prev, nil
	}
	e := &filterStmt{stmt: st, inUse: 1}
	if f.stmts == nil {
		f.stmts = map[string]*filterStmt{}
	}
	if len(f.stmts) >= maxFilterStmts && !f.evict() {
		e.retired = true
		return e, nil
	}
	f.tick++
	e.used = f.tick
	f.stmts[text] = e
	return e, nil
}

// evict drops the least recently used statement no call is running and
// reports whether there was one. The lock is held.
func (f *filterStmts) evict() bool {
	var victim string
	var oldest *filterStmt
	for text, e := range f.stmts {
		if e.inUse == 0 && (oldest == nil || e.used < oldest.used) {
			victim, oldest = text, e
		}
	}
	if oldest == nil {
		return false
	}
	delete(f.stmts, victim)
	oldest.stmt.Close()
	return true
}

// release ends a call's run of the statement, closing one that has left
// the cache.
func (f *filterStmts) release(e *filterStmt) {
	f.mu.Lock()
	e.inUse--
	last := e.retired && e.inUse == 0
	f.mu.Unlock()
	if last {
		e.stmt.Close()
	}
}

// clear empties the cache: a statement no call is running closes now, the
// others when released.
func (f *filterStmts) clear() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for text, e := range f.stmts {
		delete(f.stmts, text)
		if e.inUse == 0 {
			e.stmt.Close()
		} else {
			e.retired = true
		}
	}
}

// size is the number of cached statements.
func (f *filterStmts) size() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.stmts)
}
