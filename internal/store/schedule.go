package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/MojoHand70/fadodb/internal/model"
)

// A kept value that follows the clock (ADR-0093). A triggered variable
// that lists schedule:<when> is worked out again at the moments its
// words name, by the clock of the database's own time zone, in every
// document of its class that is not finished. Each schedule of a class
// has one row of the outbox that waits for its next moment (follow.go
// runs it); the row that ran queues the next, and a class that is
// defined, and a database that is opened, give a schedule its row when
// it has none. So a server that was down at three in the night works
// the values out once when it is started, and not once for each night
// it missed.

// DefaultTimeZone is the time zone of a database that names none.
const DefaultTimeZone = "UTC"

// zoneOf reads a time zone by its name, as the database of time zones
// writes it: Europe/Warsaw, America/New_York, UTC.
func zoneOf(name string) (*time.Location, *Error) {
	if name == "" || name == "Local" {
		return nil, hintf(CodeBadValue, "", "a time zone is named as the database of time zones names it, got "+quoteText(name),
			"as Europe/Warsaw or America/New_York; UTC is the zone of a database that names none")
	}
	zone, err := time.LoadLocation(name)
	if err != nil {
		return nil, hintf(CodeBadValue, "", "the time zone "+quoteText(name)+" is not known here: "+err.Error(),
			"name it as the database of time zones does, Europe/Warsaw; a program on a machine that has no such database imports time/tzdata, as fadodb does")
	}
	return zone, nil
}

func quoteText(s string) string { return `"` + s + `"` }

// TimeZone is the database's time zone, by which its schedules go
// (ADR-0093).
func (s *Store) TimeZone() string { return s.zone.String() }

// scheduleRow writes the row that waits for the next moment of a
// schedule of a class, in the caller's transaction, unless one waits
// already. The row names no principal: the engine itself brings the
// value (ADR-0093, item 3). It tells the worker of the moment, as a
// wake-up does.
func (s *Store) scheduleRow(ctx context.Context, c *class, text string, now time.Time) error {
	rule := "schedule:" + text
	var waits int
	switch err := s.writer.QueryRowContext(ctx, `SELECT 1 FROM outbox WHERE class = ? AND doc_id = '' AND status = ? AND action = ? AND rule = ? LIMIT 1`,
		c.cls.Name, ActionPending, ActionFollow, rule).Scan(&waits); {
	case err == nil:
		return nil
	case err != sql.ErrNoRows:
		return storageErr("look for the row of "+rule, err)
	}
	sch, err := model.ParseSchedule(text)
	if err != nil {
		// The class was taken with it, so it reads.
		return errf(CodeStorage, "", "the schedule %q of %s does not read: %v", text, c.cls.Name, err)
	}
	at := sch.Next(now, s.zone).Unix()
	args, _ := json.Marshal(followArgs{Schedule: text})
	if _, err := s.stmts.insertOutbox.ExecContext(ctx, s.ids.next(now), 0, c.cls.Name, "", 0, c.cls.Version, rule, ActionFollow, string(args),
		nil, "null", 0, ActionPending, at, nil, nil, now.Unix(), nil, nil); err != nil {
		return storageErr("queue "+rule+" of "+c.cls.Name, err)
	}
	if s.wakeAt == 0 || at < s.wakeAt {
		s.wakeAt = at
	}
	return nil
}

// queueSchedule writes the row of the next moment of a schedule, when
// the class still has it: the row that ran calls it, in its own
// transaction.
func (s *Store) queueSchedule(ctx context.Context, class, text string, now time.Time) error {
	c, e := s.class(class)
	if e != nil {
		return nil
	}
	for _, has := range c.schedules {
		if strings.EqualFold(has, text) {
			return s.scheduleRow(ctx, c, has, now)
		}
	}
	return nil
}

// scheduleRows gives every schedule of the classes a row, in the
// caller's transaction.
func (s *Store) scheduleRows(ctx context.Context, classes []*class, now time.Time) error {
	for _, c := range classes {
		for _, text := range c.schedules {
			if err := s.scheduleRow(ctx, c, text, now); err != nil {
				return err
			}
		}
	}
	return nil
}

// openSchedules is what Open does for the schedules, once the classes
// are loaded: with rezoned, the rows that wait for a moment of the zone
// the database had are taken away, and then every schedule that has no
// row gets one. A row that is due, because the database was closed at
// its moment, is left to run.
func (s *Store) openSchedules(ctx context.Context, rezoned bool) error {
	var classes []*class
	for _, c := range s.classes {
		if len(c.schedules) > 0 {
			classes = append(classes, c)
		}
	}
	if len(classes) == 0 && !rezoned {
		return nil
	}
	now := s.now()
	w := s.writer
	if err := s.beginWriter(ctx, "BEGIN IMMEDIATE"); err != nil {
		return storageErr("begin", err)
	}
	if rezoned {
		if _, err := w.ExecContext(ctx, `DELETE FROM outbox WHERE status = ? AND action = ? AND rule LIKE 'schedule:%' AND attempts = 0 AND due_at > ?`,
			ActionPending, ActionFollow, now.Unix()); err != nil {
			_, _ = w.ExecContext(ctx, "ROLLBACK")
			return storageErr("take the rows of the schedules away", err)
		}
	}
	if err := s.scheduleRows(ctx, classes, now); err != nil {
		_, _ = w.ExecContext(ctx, "ROLLBACK")
		return err
	}
	if err := s.endTransaction(ctx); err != nil {
		_, _ = w.ExecContext(ctx, "ROLLBACK")
		return storageErr("commit", err)
	}
	// The worker is not started yet, and looks when it is.
	s.wakeAt = 0
	return nil
}
