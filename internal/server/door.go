package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MojoHand70/fadodb/internal/command"
	"github.com/MojoHand70/fadodb/internal/store"
)

// Database is the one database a door serves: what runs a command of the
// command set, and keeps and opens the files that travel beside the
// commands. A *fado.DB is one.
type Database interface {
	Exec(ctx context.Context, p store.Principal, name string, object []byte) ([]byte, error)
	PutBlob(ctx context.Context, name, mediaType string, r io.Reader, opts ...store.Option) (store.Values, error)
	OpenFile(ctx context.Context, class, id, hash string, opts ...store.Option) (io.ReadCloser, *store.FileInfo, error)
	OpenBlob(ctx context.Context, hash string, opts ...store.Option) (io.ReadCloser, int64, error)
	// Subscribe opens a stream of changes for the caller p (ADR-0058).
	Subscribe(ctx context.Context, p store.Principal, f store.ChangeFilter) (Stream, error)
	// Receive takes a message from outside for an inlet (ADR-0065).
	Receive(ctx context.Context, inlet string, m *store.Message) (*store.Receipt, error)
}

// Who says who sends a request (ADR-0027; ADR-0057, item 9): the
// principal the command runs as, or an error when the request does not
// say, or says it with a credential the function does not know. The door
// answers any error with unauthenticated, in the same words, and passes
// none of the error's own to the caller.
//
// A principal with no id is nobody, and the engine lets nobody through
// only where a rule lets everybody through. For whom the caller acts is
// said in the command, under "for", and the engine checks it; a Who
// leaves Principal.For empty.
type Who func(r *http.Request) (store.Principal, error)

// Bearer is the Who of a door whose callers prove who they are with a
// key (ADR-0057, item 3): a request carries the key in one place, the
// header Authorization: Bearer <key>, and whose says who the key belongs
// to. Nothing else is a credential: no cookie, no key in the address,
// no name and password. A database that keeps its own users gives its
// Authenticate as whose.
func Bearer(whose func(key string) (store.Principal, error)) Who {
	return func(r *http.Request) (store.Principal, error) {
		const scheme = "Bearer "
		sent := r.Header.Get("Authorization")
		if len(sent) <= len(scheme) || !strings.EqualFold(sent[:len(scheme)], scheme) {
			return store.Principal{}, store.ErrUnknownKey
		}
		return whose(sent[len(scheme):])
	}
}

// Limits are what one door takes on (ADR-0059, item 2). A zero is the
// default.
type Limits struct {
	// RequestBytes is the largest body of a request. A larger one is
	// refused with too_large and none of it is run.
	RequestBytes int64
	// CommandTime is how long one command may run. One that runs longer
	// is stopped and refused with timeout.
	CommandTime time.Duration
	// Commands is how many commands run at once. A request beyond them
	// waits one second for one to end and is then refused with busy.
	Commands int
	// Streams is how many streams of changes are open at once. One
	// beyond them is refused with busy, at once: a stream stays open
	// for as long as its caller listens, so waiting for one to end
	// would be waiting for nothing.
	Streams int
	// MessageBytes is the largest message from outside, and Messages
	// how many are worked at once (ADR-0065, item 8), for a door that
	// takes messages. A message beyond them is refused with too_large
	// or, at once, with busy.
	MessageBytes int64
	Messages     int
}

// The limits of a door that was given none.
const (
	DefaultRequestBytes = 16 << 20
	DefaultCommandTime  = 30 * time.Second
	DefaultCommands     = 64
	DefaultStreams      = 256
)

// busyWait is how long a request waits for a command to end before it
// is refused with busy.
const busyWait = time.Second

// Options are what a door is made with.
type Options struct {
	// Who says who sends a request. A door is not made without it: a
	// program that means to let everybody in says so, with a function
	// that answers the same principal for every request.
	Who Who
	// Limits are the door's limits; the zero value gives the defaults.
	Limits Limits
	// Inbound says that the door takes messages from outside, at
	// POST /v1/in/<inlet> (ADR-0065). Without it every such address
	// answers as one of a name that is no inlet. A program that sets it
	// opened its database with the option of the same name.
	Inbound bool
	// Report, when set, is called once for every request, after it was
	// answered. It is how a program writes its log and counts.
	Report func(Request)
}

// Request is what a door tells of one request (ADR-0059, item 9). It
// holds no body, no value of a document and no credential.
type Request struct {
	// At is when the request came, and Took how long until it was
	// answered.
	At   time.Time
	Took time.Duration
	// Command is the name the request was sent to, as it was sent; User
	// is the id of who sent it, empty when the door could not tell.
	Command string
	User    string
	// Status is the HTTP status answered. Code is the code of the
	// refusal, empty when the command ran. A command whose caller left
	// before it ended has StatusCallerLeft and CodeCallerLeft: nobody
	// was answered.
	Status int
	Code   string
	// Inlet is the name of the inlet a message from outside was for, as
	// the database declares it; empty for every other request, and for
	// a message for a name that is no inlet. Command is then in/<Inlet>,
	// User the user the inlet acts as, and Err what the database
	// refused, in the words the sender was not given. Ignored says the
	// inlet does not act on the message: nothing was done.
	Inlet   string
	Ignored bool
	// Err is set when the command failed in the engine, with code
	// internal: the words the caller was not given.
	Err error
	// Stream says the request opened a stream of changes, and is told
	// now that the stream ended. Lasted is how long it stayed open and
	// Entries how many events its caller was told. Took is then the
	// time until the stream was open, the status 200, and Code the code
	// the server ended the stream with, empty when the caller closed it.
	Stream  bool
	Lasted  time.Duration
	Entries int
}

// Door is the HTTP door of one database. It is an http.Handler, safe for
// any number of requests at once.
type Door struct {
	db     Database
	who    Who
	limits Limits
	report func(Request)
	// slots holds one entry for each command that runs; wait is how long
	// a request waits for one.
	slots chan struct{}
	wait  time.Duration
	// inbound says the door takes messages from outside, and messages
	// holds one entry for each message that is worked (ADR-0065).
	inbound  bool
	messages chan struct{}
	// arrive is how long the body of a message is given to arrive.
	arrive time.Duration
	// alive is how long a stream of changes says nothing before it shows
	// that it is alive, and take how long its caller is given to take
	// what it was sent.
	alive time.Duration
	take  time.Duration
	// streams are the streams of changes that are open, each with how
	// it is ended; ended says the door opens no more of them.
	mu      sync.Mutex
	streams map[*subscription]struct{}
	ended   bool
}

// New makes the door of a database.
func New(db Database, o Options) (*Door, error) {
	if db == nil {
		return nil, errors.New("server: a door needs a database")
	}
	if o.Who == nil {
		return nil, errors.New("server: a door needs Options.Who, the function that says who sends a request")
	}
	l := o.Limits
	if l.RequestBytes < 0 || l.CommandTime < 0 || l.Commands < 0 || l.Streams < 0 || l.MessageBytes < 0 || l.Messages < 0 {
		return nil, errors.New("server: a limit of a door is negative; leave it zero for the default")
	}
	if l.RequestBytes == 0 {
		l.RequestBytes = DefaultRequestBytes
	}
	if l.CommandTime == 0 {
		l.CommandTime = DefaultCommandTime
	}
	if l.Commands == 0 {
		l.Commands = DefaultCommands
	}
	if l.Streams == 0 {
		l.Streams = DefaultStreams
	}
	if l.MessageBytes == 0 {
		l.MessageBytes = DefaultMessageBytes
	}
	if l.Messages == 0 {
		l.Messages = DefaultMessages
	}
	return &Door{
		db:       db,
		who:      o.Who,
		limits:   l,
		report:   o.Report,
		inbound:  o.Inbound,
		messages: make(chan struct{}, l.Messages),
		arrive:   arriveWithin,
		slots:    make(chan struct{}, l.Commands),
		wait:     busyWait,
		alive:    aliveEvery,
		take:     takeWithin,
		streams:  map[*subscription]struct{}{},
	}, nil
}

// Limits are the limits the door runs with, the defaults filled in.
func (d *Door) Limits() Limits { return d.limits }

// prefix is where the commands are: POST /v1/<command>.
const prefix = "/v1/"

// ServeHTTP answers one request.
func (d *Door) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	told := Request{At: time.Now()}
	out := &reply{w: w}
	defer func() {
		if x := recover(); x != nil {
			if x == http.ErrAbortHandler {
				panic(x)
			}
			told.Err = fmt.Errorf("a panic while the request was answered: %v", x)
			told.Code = command.CodeInternal
			if !out.wrote {
				out.refusal(Status(command.CodeInternal), failed())
			}
		}
		if d.report != nil {
			told.Status = out.status
			if told.Took == 0 {
				// A stream said how long it took to open.
				told.Took = time.Since(told.At)
			}
			d.report(told)
		}
	}()

	if !strings.HasPrefix(r.URL.Path, prefix) {
		told.Code = command.CodeUnknownCommand
		out.refusal(Status(told.Code), &store.Error{
			Code:    command.CodeUnknownCommand,
			Message: "there is nothing at " + strconv.Quote(r.URL.Path),
			Hint:    "a command is sent as POST /v1/<command>, such as POST /v1/get",
		})
		return
	}
	name := r.URL.Path[len(prefix):]
	told.Command = name
	// The files travel beside the commands (ADR-0055, item 9), at three
	// addresses that are not commands.
	if inlet, ok := strings.CutPrefix(name, addressIn); ok {
		// A message from outside: it proves itself, and carries no key
		// of a user (ADR-0065).
		d.message(out, r, &told, inlet)
		return
	}
	switch name {
	case addressPut:
		d.put(out, r, &told)
	case addressFile:
		d.file(out, r, &told)
	case addressBlob:
		d.blob(out, r, &told)
	case addressSubscribe:
		// The one command whose answer is a stream (ADR-0058).
		d.subscribe(out, r, &told)
	default:
		d.command(out, r, &told, name)
	}
}

// asCommand holds a request to what every command is sent as: a POST
// with no member in its address, from a sender the door can tell, whose
// body is said to be JSON. It answers who sends it, or refuses it.
func (d *Door) asCommand(out *reply, r *http.Request, told *Request) (store.Principal, bool) {
	if r.Method != http.MethodPost {
		d.notSentWith(out, told, http.MethodPost, "a command is sent with POST, and this request is a "+r.Method,
			"send POST "+r.URL.Path+" with the object of the command as the body; a command that reads is a POST too")
		return store.Principal{}, false
	}
	if r.URL.RawQuery != "" {
		// A member in the address would be passed over, and the caller
		// would have sent a command that does something else.
		told.Code = command.CodeBadJSON
		out.refusal(Status(told.Code), &store.Error{
			Code:    command.CodeBadJSON,
			Message: "a command takes its members in its object, and this request has some in its address: ?" + r.URL.RawQuery,
			Hint:    "send POST " + r.URL.Path + " and put the members in the JSON object of the body",
		})
		return store.Principal{}, false
	}
	p, ok := d.sender(out, r, told)
	if !ok {
		return p, false
	}
	if !isJSON(r.Header.Get("Content-Type")) {
		told.Code = command.CodeBadJSON
		out.refusal(http.StatusUnsupportedMediaType, &store.Error{
			Code:    command.CodeBadJSON,
			Message: "the body of a command is JSON, and this request does not say its body is",
			Hint:    "send the header Content-Type: application/json",
		})
		return p, false
	}
	return p, true
}

// command runs the command a request was sent to.
func (d *Door) command(out *reply, r *http.Request, told *Request, name string) {
	p, ok := d.asCommand(out, r, told)
	if !ok {
		return
	}
	// A command holds its slot from before its body is read, so that the
	// door never holds more bodies than it runs commands.
	if !d.slot(out, r, told) {
		return
	}
	defer d.leave()

	object, refusal := d.body(out.w, r)
	if refusal != nil {
		told.Code = refusal.Code
		out.refusal(Status(refusal.Code), refusal)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), d.limits.CommandTime)
	answer, err := d.db.Exec(ctx, p, name, object)
	// Why the command was told to stop, if it was: its time was up, or
	// the request it came in was closed.
	why := ctx.Err()
	cancel()
	if err == nil {
		out.send(http.StatusOK, answer)
		return
	}
	code := command.Code(err)
	// A command that was stopped fails in the engine or in its storage;
	// one that was refused before that is answered with its refusal.
	broke := code == command.CodeInternal || code == store.CodeStorage
	if why == context.DeadlineExceeded && (broke || errors.Is(err, context.DeadlineExceeded)) {
		told.Code = CodeTimeout
		out.refusal(Status(CodeTimeout), &store.Error{
			Code:    CodeTimeout,
			Message: "the command ran longer than " + d.limits.CommandTime.String() + ", the longest the server lets one run, and was stopped",
			Hint:    "a command that is one commit wrote nothing; migrate and drain commit as they go and keep what they had done",
		})
		return
	}
	// A command whose caller left is stopped the same way. Nobody reads
	// the reply: the code is for the log and the counts, which would
	// otherwise say that the storage failed (ADR-0059, item 2). A
	// request the server itself cuts because it stops is not one whose
	// caller left.
	if why == context.Canceled && (broke || errors.Is(err, context.Canceled)) && !d.stops() {
		told.Code = CodeCallerLeft
		out.refusal(Status(CodeCallerLeft), &store.Error{
			Code:    CodeCallerLeft,
			Message: "the caller closed the request before the command ended, and the command was stopped",
			Hint:    "a command that is one commit wrote nothing; migrate and drain commit as they go and keep what they had done",
		})
		return
	}
	told.Code = code
	if code == command.CodeInternal {
		told.Err = err
	}
	out.send(Status(code), answer)
}

// notSentWith refuses a request that has not the method its address
// takes. Its words are made only for a request that is refused.
func (d *Door) notSentWith(out *reply, told *Request, method, message, hint string) {
	told.Code = command.CodeBadJSON
	out.w.Header().Set("Allow", method)
	out.refusal(http.StatusMethodNotAllowed, &store.Error{Code: command.CodeBadJSON, Message: message, Hint: hint})
}

// sender asks who sends the request, and refuses it when nobody can
// tell.
func (d *Door) sender(out *reply, r *http.Request, told *Request) (store.Principal, bool) {
	p, err := d.who(r)
	if err != nil {
		told.Code = CodeUnauthenticated
		out.w.Header().Set("WWW-Authenticate", "Bearer")
		out.refusal(Status(told.Code), unauthenticated())
		return store.Principal{}, false
	}
	told.User = p.ID
	return p, true
}

// slot takes the slot of a command for the request, and refuses it with
// busy when none comes free in time. Whoever is given one leaves it.
func (d *Door) slot(out *reply, r *http.Request, told *Request) bool {
	if d.enter(r.Context()) {
		return true
	}
	told.Code = CodeBusy
	out.w.Header().Set("Retry-After", "1")
	out.refusal(Status(told.Code), &store.Error{
		Code:    CodeBusy,
		Message: "the server runs " + strconv.Itoa(d.limits.Commands) + " commands, as many as it takes on at once, and none ended in time",
		Hint:    "nothing was run; send the request again",
	})
	return false
}

// unauthenticated is the one answer to a request whose sender the door
// could not tell, whatever was wrong with it.
func unauthenticated() *store.Error {
	return &store.Error{
		Code:    CodeUnauthenticated,
		Message: "the request does not say who sends it, or says it with a credential the server does not know",
	}
}

// failed is the answer to a request that failed in the door or in the
// engine. The words of the failure are for whoever runs the server.
func failed() *store.Error {
	return &store.Error{
		Code:    command.CodeInternal,
		Message: "the command failed in the engine; the program that runs it has the words",
	}
}

// enter takes the slot of a command, waiting a short time for one, and
// reports whether it got it.
func (d *Door) enter(ctx context.Context) bool {
	select {
	case d.slots <- struct{}{}:
		return true
	default:
	}
	wait := time.NewTimer(d.wait)
	defer wait.Stop()
	select {
	case d.slots <- struct{}{}:
		return true
	case <-wait.C:
		return false
	case <-ctx.Done():
		return false
	}
}

// leave gives the slot back.
func (d *Door) leave() { <-d.slots }

// small is the size up to which a body whose length the request states
// is read into one allocation of that length.
const small = 64 << 10

// body reads the object of a command, up to the limit of the door.
func (d *Door) body(w http.ResponseWriter, r *http.Request) ([]byte, *store.Error) {
	limit := d.limits.RequestBytes
	if r.ContentLength > limit {
		return nil, d.tooLarge()
	}
	from := http.MaxBytesReader(w, r.Body, limit)
	var object []byte
	var err error
	if n := r.ContentLength; n >= 0 && n <= small {
		object = make([]byte, n)
		_, err = io.ReadFull(from, object)
	} else {
		// A long body, or one whose length is not stated, is read as it
		// comes, so that a request that announces much and sends nothing
		// holds nothing.
		var b bytes.Buffer
		_, err = b.ReadFrom(from)
		object = b.Bytes()
	}
	if err != nil {
		var over *http.MaxBytesError
		if errors.As(err, &over) {
			return nil, d.tooLarge()
		}
		return nil, &store.Error{
			Code:    command.CodeBadJSON,
			Message: "the body of the request broke off before its end",
		}
	}
	if len(bytes.TrimSpace(object)) == 0 {
		return nil, &store.Error{
			Code:    command.CodeBadJSON,
			Message: "the request has no body",
			Hint:    "the body is the object of the command; a command that takes nothing is sent as {}",
		}
	}
	return object, nil
}

func (d *Door) tooLarge() *store.Error {
	return &store.Error{
		Code:    CodeTooLarge,
		Message: "the request is larger than " + strconv.FormatInt(d.limits.RequestBytes, 10) + " bytes, the most the server reads",
		Hint:    "nothing of it was run; a file travels beside the commands, and many rows or documents go in several commands",
	}
}

// isJSON reports whether a Content-Type says application/json, with or
// without parameters.
func isJSON(contentType string) bool {
	const json = "application/json"
	if len(contentType) < len(json) || !strings.EqualFold(contentType[:len(json)], json) {
		return false
	}
	rest := contentType[len(json):]
	return rest == "" || rest[0] == ';' || rest[0] == ' ' || rest[0] == '\t'
}

// reply writes the answer of one request and remembers what it wrote.
type reply struct {
	w      http.ResponseWriter
	status int
	wrote  bool
}

// send writes an answer, one JSON object, and a line break after it.
func (o *reply) send(status int, answer []byte) {
	h := o.w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	answer = append(answer, '\n')
	h.Set("Content-Length", strconv.Itoa(len(answer)))
	o.status, o.wrote = status, true
	o.w.WriteHeader(status)
	// A caller that left is no failure of the door.
	_, _ = o.w.Write(answer)
}

// refusal writes a refusal of the door's own, in the shape of every
// refusal: {"error": …}.
func (o *reply) refusal(status int, e *store.Error) {
	text, err := command.Marshal(struct {
		Error *store.Error `json:"error"`
	}{e})
	if err != nil {
		text = []byte(`{"error":{"code":"internal","message":"the command failed in the engine; the program that runs it has the words"}}`)
	}
	o.send(status, text)
}
