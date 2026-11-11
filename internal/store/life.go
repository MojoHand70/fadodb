package store

import (
	"context"
	"database/sql"
	"sort"

	"github.com/MojoHand70/fadodb/internal/formula"
)

// What a document's life says of its states (ADR-0047, item 6): the
// functions of a state read it from the document's own events, once for
// a command however many of them it evaluates.

// lifeSQL reads the events of one document that its life by state is
// told from, up to a version: its creation, its moves, and the changes
// that left it where it was, a set and a reset, as 1, 2 and 3. A refresh
// and a migration made a version too and are no change of the document:
// the one computes again, the other moves it to a new version of the
// class. The index of the log by class and document
// finds them and gives them by their time, which is the order of the
// versions while the clock does not go back; tellLife puts them right
// when it did. movesSQL reads the creation and the moves alone, which is
// all a class needs that does not ask for editors: a row that SQLite
// keeps to itself costs half of one it hands over.
const (
	lifeSQL  = lifeSelect + `, '` + EventSet + `', '` + EventReset + `') ORDER BY ts`
	movesSQL = lifeSelect + `) ORDER BY ts`

	lifeSelect = `SELECT CASE kind WHEN '` + EventCreated + `' THEN 1 WHEN '` + EventTransition + `' THEN 2 ELSE 3 END, from_state, to_state, actor_id, ts, after_version FROM events` +
		` WHERE class = ? AND doc_id = ? AND after_version <= ? AND kind IN ('` + EventCreated + `', '` + EventTransition + `'`
)

// lifeRow is one event as lifeSQL reads it, with the version it made.
type lifeRow struct {
	ev      formula.LifeEvent
	version int64
}

// lifeReader is what the writer keeps between reads of a document's
// events: the rows of the last read, and the texts it has seen, so that
// a state or a person named in every event is one text.
type lifeReader struct {
	rows  []lifeRow
	texts []string
}

// text gives the text of the bytes of a column, one it has given before
// where it can: a document's events name few states and few people.
func (r *lifeReader) text(raw sql.RawBytes) string {
	if len(raw) == 0 {
		return ""
	}
	for _, t := range r.texts {
		if t == string(raw) {
			return t
		}
	}
	t := string(raw)
	if len(r.texts) < 16 {
		r.texts = append(r.texts, t)
	}
	return t
}

// tellLife tells a row what its document's life says of its states: the
// events the log has up to the version the row was read at, and then the
// steps of the command in progress, which the log does not have yet. So
// a formula sees the life up to the commit being computed, the entry
// that commit makes included. It runs on the writer's connection, inside
// the transaction of the command.
func (s *Store) tellLife(c *class, row *docRow) error {
	var states []formula.StateLife
	if row.logged > 0 {
		s.lifeReads++
		r := &s.life
		r.rows, r.texts = r.rows[:0], r.texts[:0]
		read := s.stmts.selectMoves
		if c.editors {
			read = s.stmts.selectLife
		}
		rows, err := read.QueryContext(context.Background(), c.nameArg, row.id, row.logged)
		if err != nil {
			return lifeErr(err)
		}
		defer rows.Close()
		var from, to, by sql.RawBytes
		var kind, ts, version int64
		ordered := true
		for rows.Next() {
			if err := rows.Scan(&kind, &from, &to, &by, &ts, &version); err != nil {
				return lifeErr(err)
			}
			if n := len(r.rows); n > 0 && version < r.rows[n-1].version {
				ordered = false
			}
			r.rows = append(r.rows, lifeRow{version: version,
				ev: formula.LifeEvent{Kind: formula.LifeEventKind(kind), From: r.text(from), To: r.text(to), At: ts, By: r.text(by)}})
		}
		if err := rows.Err(); err != nil {
			return lifeErr(err)
		}
		if !ordered {
			// The clock went back between two commits, or two fell in
			// one second: the versions say the order.
			sort.Slice(r.rows, func(i, j int) bool { return r.rows[i].version < r.rows[j].version })
		}
		for i := range r.rows {
			states = formula.TellLife(states, r.rows[i].ev)
		}
	}
	for _, ev := range row.pending {
		states = formula.TellLife(states, ev)
	}
	row.life.States, row.told = states, true
	return nil
}

func lifeErr(err error) error {
	return &formula.EvalError{Code: formula.ErrQueryFailed, Pos: -1, Msg: "read the document's events: " + err.Error()}
}
