package server

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/MojoHand70/fadodb/internal/command"
	"github.com/MojoHand70/fadodb/internal/store"
)

// The way in for messages from outside (ADR-0065): POST /v1/in/<inlet>.
// A message carries no key of a user. It proves who sent it as its
// inlet says, and the database carries out the one command it becomes
// as the user the inlet names. A sender is outside the database: it is
// answered a status and a code, and no word of a document, of a rule or
// of a hint. The words are for whoever runs the server, in what the
// door reports.

// addressIn begins the name of every address of an inlet, after /v1/.
const addressIn = "in/"

// The limits of a door that takes messages and was given none.
const (
	// DefaultMessageBytes is the largest message: far less than a
	// command may be, since whoever sends it has proved nothing yet.
	DefaultMessageBytes = 1 << 20
	// DefaultMessages is how many messages are worked at once. They
	// have places of their own, apart from the commands', so that a
	// stranger who floods an inlet takes no place from a caller with a
	// key.
	DefaultMessages = 16
)

// arriveWithin is how long the body of a message is given to arrive.
// Whoever sends a message has proved nothing yet, and one that sent
// its body a byte at a time would hold a place for as long as it
// liked.
const arriveWithin = 10 * time.Second

// The answers a sender reads: what happened, and nothing of the
// database.
const (
	messageDone    = `{"ok":true}`
	messageIgnored = `{"ok":true,"ignored":true}`
)

// answerCode answers a message with a status and a code, and no words.
func (o *reply) answerCode(status int, code string) {
	o.send(status, []byte(`{"error":{"code":`+strconv.Quote(code)+`}}`))
}

// message takes a message for the inlet of the name.
func (d *Door) message(out *reply, r *http.Request, told *Request, name string) {
	// A door that takes no messages has no inlet, whatever its
	// database holds (item 8).
	if !d.inbound {
		told.Code = store.CodeUnknownInlet
		out.answerCode(Status(told.Code), told.Code)
		return
	}
	if r.Method != http.MethodPost {
		told.Code = command.CodeBadJSON
		out.w.Header().Set("Allow", http.MethodPost)
		out.answerCode(http.StatusMethodNotAllowed, told.Code)
		return
	}
	// A message has a place of its own or none: nobody waits for one,
	// since a sender sends again.
	select {
	case d.messages <- struct{}{}:
		defer func() { <-d.messages }()
	default:
		told.Code = CodeBusy
		out.w.Header().Set("Retry-After", "1")
		out.answerCode(Status(told.Code), told.Code)
		return
	}
	// The body arrives in a short time or not at all. A door behind a
	// writer that knows no deadline, as in a test, reads without one.
	line := http.NewResponseController(out.w)
	_ = line.SetReadDeadline(time.Now().Add(d.arrive))
	body, code := d.read(out.w, r)
	if code != "" {
		// What was announced did not arrive, or was too much. The rest
		// is not waited for: the time stays up, and the line is closed
		// after the answer.
		told.Code = code
		out.w.Header().Set("Connection", "close")
		out.answerCode(Status(code), code)
		return
	}
	_ = line.SetReadDeadline(time.Time{})
	m := &store.Message{Headers: make(map[string]string, len(r.Header)), Body: body, Form: isForm(r.Header.Get("Content-Type"))}
	for name, values := range r.Header {
		m.Headers[name] = strings.Join(values, ", ")
	}
	if query := r.URL.Query(); len(query) > 0 {
		m.Params = make(map[string]string, len(query))
		for name, values := range query {
			m.Params[name] = values[0]
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), d.limits.CommandTime)
	receipt, err := d.db.Receive(ctx, name, m)
	why := ctx.Err()
	cancel()
	if receipt != nil {
		// The inlet is one of the database: the door tells of it under
		// its own name.
		told.Inlet = receipt.Inlet
		told.Command = addressIn + receipt.Inlet
	}
	switch {
	case errors.Is(err, store.ErrNotProven):
		// One answer whatever is wrong with the proof, as for a key.
		// Nobody is named: the message has not shown who sent it.
		told.Code = CodeUnauthenticated
		out.answerCode(Status(told.Code), told.Code)
		return
	case err == nil && receipt.Ignored:
		told.User, told.Ignored = receipt.As, true
		out.send(http.StatusOK, []byte(messageIgnored))
		return
	case err == nil:
		told.User = receipt.As
		out.send(http.StatusOK, []byte(messageDone))
		return
	}
	code = command.Code(err)
	if receipt != nil && code != store.CodeSecretUnavailable {
		// The proof held, and the database refused what its user sent.
		told.User = receipt.As
	}
	broke := code == command.CodeInternal || code == store.CodeStorage
	status := 0
	switch {
	case why == context.DeadlineExceeded && (broke || errors.Is(err, context.DeadlineExceeded)):
		code = CodeTimeout
	case why == context.Canceled && (broke || errors.Is(err, context.Canceled)) && !d.stops():
		code = CodeCallerLeft
	case code == store.CodeSecretUnavailable:
		// The server cannot check the proof now. The sender is told to
		// come back, and whoever runs the server reads why.
		status = http.StatusServiceUnavailable
		told.Err = err
	default:
		// What the database refused, and why, is said to whoever runs
		// the server and not to the sender.
		told.Err = err
	}
	if status == 0 {
		status = Status(code)
	}
	told.Code = code
	out.answerCode(status, code)
}

// read reads the body of a message, up to the limit of the door, and
// answers the code of its refusal when it cannot.
func (d *Door) read(w http.ResponseWriter, r *http.Request) ([]byte, string) {
	limit := d.limits.MessageBytes
	if r.ContentLength > limit {
		return nil, CodeTooLarge
	}
	var b bytes.Buffer
	if _, err := b.ReadFrom(http.MaxBytesReader(w, r.Body, limit)); err != nil {
		var over *http.MaxBytesError
		if errors.As(err, &over) {
			return nil, CodeTooLarge
		}
		// The body broke off before its end.
		return nil, store.CodeBadMessage
	}
	return b.Bytes(), ""
}

// isForm reports whether a Content-Type says that the body holds the
// fields of a form.
func isForm(contentType string) bool {
	const form = "application/x-www-form-urlencoded"
	if len(contentType) < len(form) || !strings.EqualFold(contentType[:len(form)], form) {
		return false
	}
	rest := contentType[len(form):]
	return rest == "" || rest[0] == ';' || rest[0] == ' ' || rest[0] == '\t'
}
