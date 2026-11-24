package store

import (
	"context"
	"strconv"
	"time"
)

// Waiting for changes (ADR-0064, ruling 3 and items 22 to 30): the
// stream of changes as one call with one answer, for a caller that
// cannot hold a stream open, such as an agent through the door for
// agents. The call reads the log from a place, as a stream does, and
// answers the events the caller may hear of with the place to ask from
// next; when there are none yet it waits for them, up to a number of
// seconds. Nothing is kept between two calls: the place travels with the
// caller.

// The bounds of a wait.
const (
	// MaxWaitSeconds is the longest a call waits for a change: a minute,
	// within what the programs of agents give a tool to answer.
	MaxWaitSeconds = 60
	// DefaultWaitLimit is how many events an answer holds at most when
	// the call says nothing, and MaxWaitLimit the most it may ask for.
	DefaultWaitLimit = 100
	MaxWaitLimit     = 1000
)

// Wait says how a call waits for changes.
type Wait struct {
	// Seconds is how long the call waits for an event when the log has
	// none yet that the caller may hear of: 0 answers at once, and
	// MaxWaitSeconds is the most.
	Seconds int
	// Limit is the most events the answer holds: DefaultWaitLimit when
	// 0, and MaxWaitLimit at most.
	Limit int
}

// Waited is the answer of a call that waited for changes.
type Waited struct {
	// Events are the events the caller may hear of, in the order of the
	// commits; none when the time ran out first.
	Events []Event `json:"events"`
	// After is where to ask from next: the last event answered when the
	// answer is full, and otherwise the last event of the log the call
	// read, or LogStart while the log holds none. Given back as After,
	// it misses nothing and tells nothing twice.
	After string `json:"after"`
	// More says the log holds events after After that the call did not
	// read: the next call should be made at once.
	More bool `json:"more"`
}

// WaitForChanges answers the events of the log after the place the
// filter names, or from now when it names none, that the filter lets
// through and the caller may hear of, as a stream of changes would tell
// them (ADR-0058). It reads the log to its end, or until its answer
// holds the limit of w, and answers what it found; when that is nothing
// it waits for the next commit, for the seconds of w, and answers none
// when they pass.
//
// A caller who acts for somebody hears of an event when it may act for
// them on its class now and may read its document so; a stream of
// changes is heard as oneself, and this is how such a caller waits. A
// content administrator hears past the read rule with a reason, and the
// log of reads names this command.
//
// A call that is stopped while it waits, by its context or because the
// program ends its streams, answers what it has, with its place.
func (s *Store) WaitForChanges(ctx context.Context, f ChangeFilter, w Wait, opts ...Option) (*Waited, error) {
	if w.Seconds < 0 || w.Seconds > MaxWaitSeconds {
		e := hintf(CodeBadValue, "", "seconds is "+strconv.Itoa(w.Seconds)+", and a call waits from 0 to "+strconv.Itoa(MaxWaitSeconds)+" seconds",
			"give 0 to be answered at once, or up to "+strconv.Itoa(MaxWaitSeconds)+" and call again with after when the answer is empty")
		e.Path = "/seconds"
		return nil, e
	}
	limit := w.Limit
	if limit == 0 {
		limit = DefaultWaitLimit
	}
	if limit < 0 || limit > MaxWaitLimit {
		e := hintf(CodeBadValue, "", "limit is "+strconv.Itoa(w.Limit)+", and an answer holds from 1 to "+strconv.Itoa(MaxWaitLimit)+" events",
			"leave limit out for "+strconv.Itoa(DefaultWaitLimit)+"; an answer that is full says more, and the next call takes the rest")
		e.Path = "/limit"
		return nil, e
	}
	c, err := s.openChanges(ctx, f, "wait_for_changes", opts)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	var until <-chan time.Time
	if w.Seconds > 0 {
		timer := time.NewTimer(time.Duration(w.Seconds) * time.Second)
		defer timer.Stop()
		until = timer.C
	}
	for {
		more, err := c.fill(ctx, limit)
		if err != nil {
			return nil, err
		}
		if len(c.read) >= limit {
			// The answer is full: the place stops at its last event, and
			// the rest is the next call's.
			return c.waited(true), nil
		}
		if more {
			// The log had events, and may have more: it is read on to its
			// end, unless the call is stopped first.
			select {
			case <-ctx.Done():
				return c.waited(true), nil
			default:
				continue
			}
		}
		// The log is read to its end.
		if len(c.read) > 0 || until == nil {
			return c.waited(false), nil
		}
		select {
		case <-c.wake:
		case <-until:
			return c.waited(false), nil
		case <-ctx.Done():
			return c.waited(false), nil
		case <-s.stop:
			return nil, errf(CodeClosed, "", "the store is closed")
		}
	}
}

// waited is the answer of a wait from what the stream has read.
func (c *Changes) waited(more bool) *Waited {
	w := &Waited{Events: make([]Event, len(c.read)), After: c.at, More: more}
	for i, ev := range c.read {
		w.Events[i] = *ev
	}
	return w
}
