package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/MojoHand70/fadodb/internal/command"
	"github.com/MojoHand70/fadodb/internal/store"
)

// The stream of changes over HTTP (ADR-0058): the command subscribe,
// whose answer is a reply that stays open. The reply is in the form of
// server-sent events, text/event-stream, and carries one entry for each
// event the caller may hear of:
//
//	id: 01KA2F8Q7ZJ4M0X3T5V9B1C6DE
//	event: change
//	data: {"event_id": "01KA2F8Q7ZJ4M0X3T5V9B1C6DE", "class": "Invoice", …}
//
// The door keeps nothing for a stream but the stream itself, which
// keeps its place in the log. A caller that was away says the last
// event it handled, as after in the object or in the header
// Last-Event-ID, and is told everything since.

// Stream is a stream of changes for one caller, as a database opens
// one (ADR-0058): the door has it follow its caller, asks it for the
// next event, and closes it. A *store.Changes is one.
type Stream interface {
	Follow(who func(context.Context) (store.Principal, error))
	Next(ctx context.Context) (*store.Event, error)
	Close()
}

// addressSubscribe is the name of the command a stream is opened with.
const addressSubscribe = "subscribe"

// lastEventID is the header in which a caller that connects again says
// the last event it handled, as server-sent events resume.
const lastEventID = "Last-Event-ID"

const (
	// aliveEvery is how long a stream says nothing before it shows that
	// it is alive (ADR-0058, item 6).
	aliveEvery = 15 * time.Second
	// takeWithin is how long a caller is given to take what a stream
	// sent it. One that reads nothing for so long is cut, so that a
	// caller that went away without a word holds no stream.
	takeWithin = 30 * time.Second
)

// The lines of a stream that are no event. A line that begins with a
// colon is a comment, which a reader of server-sent events passes over.
const (
	lineOpen  = ": the stream is open\n\n"
	lineAlive = ": nothing new\n\n"
)

// subscription is one open stream as the door holds it: how it is ended
// when the door is told to end its streams.
type subscription struct{ end context.CancelFunc }

// Streams is how many streams of changes the door holds open now.
func (d *Door) Streams() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.streams)
}

// EndStreams ends the streams of changes the door holds open, each with
// a last entry that says the server stops (ADR-0058, item 7), and
// refuses with closed the ones asked for after it. A program calls it
// when it is told to stop, before it waits for its requests to end: a
// stream does not end by itself. It does not wait for the streams, and
// commands are answered as before. A command the program cuts after
// this is not taken for one whose caller left.
//
//	srv.RegisterOnShutdown(door.EndStreams)
func (d *Door) EndStreams() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.ended = true
	for s := range d.streams {
		s.end()
	}
}

// stops reports whether the door was told that the server stops.
func (d *Door) stops() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.ended
}

// hold takes a place among the open streams, or says why there is none.
func (d *Door) hold(end context.CancelFunc) (*subscription, *store.Error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ended {
		return nil, stopping()
	}
	if len(d.streams) >= d.limits.Streams {
		return nil, &store.Error{
			Code:    CodeBusy,
			Message: "the server holds " + strconv.Itoa(d.limits.Streams) + " streams of changes open, as many as it takes on",
			Hint:    "nothing was opened; a stream that its caller closes makes room for another, and whoever runs the server can let it hold more",
		}
	}
	s := &subscription{end: end}
	d.streams[s] = struct{}{}
	return s, nil
}

// release gives the place of a stream back.
func (d *Door) release(s *subscription) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.streams, s)
}

// stopping is what a stream is told when the server stops, and what a
// caller that asks for one after that is refused with.
func stopping() *store.Error {
	return &store.Error{
		Code:    store.CodeClosed,
		Message: "the server stops, and its streams of changes end with it",
		Hint:    "open the stream again when the server answers, with the id of the last event handled as after: nothing is lost",
	}
}

// subscribe opens a stream of changes and writes it until the caller
// closes it or the server ends it (ADR-0058, items 2 to 7).
func (d *Door) subscribe(out *reply, r *http.Request, told *Request) {
	p, ok := d.asCommand(out, r, told)
	if !ok {
		return
	}
	ctx, end := context.WithCancel(r.Context())
	defer end()
	// A stream holds its place from before its body is read, as a
	// command holds its slot.
	held, refusal := d.hold(end)
	if refusal != nil {
		told.Code = refusal.Code
		out.refusal(Status(refusal.Code), refusal)
		return
	}
	defer d.release(held)

	object, refusal := d.body(out.w, r)
	if refusal != nil {
		told.Code = refusal.Code
		out.refusal(Status(refusal.Code), refusal)
		return
	}
	f, p, err := command.Subscription(p, object)
	if err == nil {
		// A caller that connects again says where it was in the header,
		// beside the object it sent the first time: the header counts.
		if last := r.Header.Get(lastEventID); last != "" {
			f.After = last
		}
	}
	var stream Stream
	if err == nil {
		stream, err = d.db.Subscribe(ctx, p, f)
	}
	if err != nil {
		var refused *store.Error
		if !errors.As(err, &refused) || refused.Code == store.CodeStorage {
			told.Err = err
			refused = failed()
		}
		told.Code = refused.Code
		out.refusal(Status(refused.Code), refused)
		return
	}
	defer stream.Close()
	// The stream follows its caller: who the request is from is asked
	// again before an event is told, so that a key that was revoked
	// ends the stream and a group that was given or taken counts from
	// the next event on (item 4).
	gone := false
	stream.Follow(func(context.Context) (store.Principal, error) {
		now, err := d.who(r)
		gone = err != nil
		return now, err
	})

	h := out.w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	// A proxy that would collect the reply before passing it on is told
	// not to, in the one header such proxies read.
	h.Set("X-Accel-Buffering", "no")
	to := http.NewResponseController(out.w)
	// The time a server gives a reply to be written is for a command. A
	// stream stays open, so each line it writes has a time of its own,
	// and the connection is given back without one.
	defer func() { _ = to.SetWriteDeadline(time.Time{}) }()
	out.status, out.wrote = http.StatusOK, true
	out.w.WriteHeader(http.StatusOK)
	send := func(text []byte) bool {
		_ = to.SetWriteDeadline(time.Now().Add(d.take))
		if _, err := out.w.Write(text); err != nil {
			return false
		}
		return to.Flush() == nil
	}
	// The stream is open: the caller is told so with the first line, and
	// every event written from here on reaches it.
	line := make([]byte, 0, 1024)
	if !send(append(line, lineOpen...)) {
		return
	}
	opened := time.Now()
	told.Stream, told.Took = true, max(opened.Sub(told.At), 1)
	defer func() { told.Lasted = time.Since(opened) }()
	// last ends the stream with the entry that says why (item 7).
	last := func(e *store.Error) {
		told.Code = e.Code
		text, err := command.Marshal(struct {
			Error *store.Error `json:"error"`
		}{e})
		if err != nil {
			return
		}
		line = append(append(append(line[:0], "event: end\ndata: "...), text...), '\n', '\n')
		send(line)
	}
	for {
		wait, stop := context.WithTimeout(ctx, d.alive)
		ev, err := stream.Next(wait)
		stop()
		switch {
		case err == nil:
			text, err := command.Marshal(ev)
			if err != nil {
				told.Err = err
				last(broken())
				return
			}
			line = append(append(line[:0], "id: "...), ev.ID...)
			line = append(append(append(line, "\nevent: change\ndata: "...), text...), '\n', '\n')
			if !send(line) {
				return
			}
			told.Entries++
		case gone:
			// The key was revoked, or its user disabled.
			last(unauthenticated())
			return
		case r.Context().Err() != nil:
			// The caller closed the stream.
			return
		case ctx.Err() != nil:
			// The door was told to end its streams.
			last(stopping())
			return
		case errors.Is(err, context.DeadlineExceeded):
			// Nothing to tell for a while. The stream shows it is alive
			// (item 6), to a caller the server still knows.
			if _, err := d.who(r); err != nil {
				last(unauthenticated())
				return
			}
			if !send(append(line[:0], lineAlive...)) {
				return
			}
		case command.Code(err) == store.CodeClosed:
			// The database was closed under the stream.
			last(stopping())
			return
		default:
			told.Err = err
			last(broken())
			return
		}
	}
}

// broken is the last entry of a stream that failed in the engine. The
// words of the failure are for whoever runs the server.
func broken() *store.Error {
	return &store.Error{
		Code:    command.CodeInternal,
		Message: "the stream failed in the engine; the program that runs it has the words",
		Hint:    "open the stream again with the id of the last event handled as after",
	}
}
