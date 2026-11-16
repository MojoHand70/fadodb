// Package httpdoor is the HTTP door of a FadoDB database: it lets a
// program that has opened a database answer the command set over HTTP
// (ADR-0055).
//
// A command is sent as POST /v1/<command>, its object as the body, and
// the answer is the body of the reply:
//
//	POST /v1/transition
//	Content-Type: application/json
//
//	{"class": "Invoice", "id": "FV/2026/11/0042", "transition": "approve"}
//
// The door is an http.Handler. The program that makes it decides where
// it listens, and gives it the one thing the engine never decides: who
// sends a request.
//
//	door, err := httpdoor.New(db, httpdoor.Options{
//		Who: func(r *http.Request) (fado.Principal, error) {
//			user, ok := mySession(r)
//			if !ok {
//				return fado.Principal{}, errors.New("no session")
//			}
//			return fado.Principal{ID: user.ID, Groups: user.Groups, Kind: fado.ActorPerson}, nil
//		},
//	})
//	http.ListenAndServe("127.0.0.1:8080", door)
//
// A door serves one database (ADR-0056). A program that serves several
// makes one door for each and chooses between them by the host name or
// by the start of the path, which it takes off before the door sees the
// request; two doors share nothing.
//
// Only a program that answers HTTP imports this package, so that a
// program that embeds the engine and never listens carries no server.
//
// The bytes of a file travel beside the commands (ADR-0033), at three
// addresses that the door also answers: POST /v1/blobs?name=… keeps the
// body and answers the facts of the file, GET /v1/file?class=…&id=…&hash=…
// streams a file under the rules of the document that holds it, and
// GET /v1/blob?hash=… streams bytes by their hash alone, for a content
// administrator.
//
// One command answers a stream and not an object: POST /v1/subscribe
// opens the stream of changes (ADR-0058), a reply that stays open and
// carries, in the form of server-sent events, one entry for each event
// the caller may read of. A program that stops tells the door to end
// its streams first, since a stream does not end by itself:
//
//	srv := &http.Server{Handler: door}
//	srv.RegisterOnShutdown(door.EndStreams)
//
// A database that keeps its own users, groups and keys (ADR-0057) has
// its Who ready: Keys. The program `fadodb serve` is this door with
// such a database behind it.
package httpdoor

import (
	"context"

	"github.com/MojoHand70/fadodb/fado"
	"github.com/MojoHand70/fadodb/internal/server"
)

// Door is the HTTP door of one database. It is an http.Handler, safe for
// any number of requests at once. Its EndStreams ends the streams of
// changes it holds open, each with a last entry that says the server
// stops, and Streams says how many are open.
type Door = server.Door

// Options are what a door is made with: Who, which is required, the
// limits and a function that is told of every request.
type Options = server.Options

// Who says who sends a request: the principal its command runs as, or an
// error, which the door answers with unauthenticated in the same words
// whatever the error says. For whom the caller acts is said in the
// command, under "for", and the engine checks it.
type Who = server.Who

// Limits are what one door takes on: the largest request, the time one
// command may run, how many run at once and how many streams of changes
// are open at once. A zero is the default.
type Limits = server.Limits

// Request is what a door tells Options.Report of one request. It holds
// no body, no value of a document and no credential. A stream of
// changes is told once, when it ended, with how long it stayed open
// and how many events its caller was told.
type Request = server.Request

// The limits of a door that was given none (ADR-0059, item 2).
const (
	DefaultRequestBytes = server.DefaultRequestBytes
	DefaultCommandTime  = server.DefaultCommandTime
	DefaultCommands     = server.DefaultCommands
	DefaultStreams      = server.DefaultStreams
)

// The codes a door answers with for what happens before a command runs
// or beside it. Every other code is the engine's; fado.ErrorCode and the
// chapter on refusals list them.
const (
	CodeUnauthenticated = server.CodeUnauthenticated
	CodeTooLarge        = server.CodeTooLarge
	CodeTimeout         = server.CodeTimeout
	CodeBusy            = server.CodeBusy
	// CodeCallerLeft is answered to nobody: it is what Request.Code says
	// of a command whose caller closed the request before it ended, with
	// the status StatusCallerLeft.
	CodeCallerLeft = server.CodeCallerLeft
)

// StatusCallerLeft is the status of a request whose caller left before
// it was answered, 499, as proxies write it in their logs.
const StatusCallerLeft = server.StatusCallerLeft

// New makes the door of an open database. It fails when Options.Who is
// not given: a program that means to let everybody in says so with a
// function that answers the same principal for every request.
func New(db *fado.DB, o Options) (*Door, error) {
	if db == nil {
		// A nil *fado.DB in the interface would not be nil.
		return server.New(nil, o)
	}
	return server.New(database{db}, o)
}

// database is a *fado.DB as the door sees a database: its stream of
// changes is opened for the caller the door names, and answered as the
// stream the door reads.
type database struct{ *fado.DB }

func (d database) Subscribe(ctx context.Context, p fado.Principal, f fado.ChangeFilter) (server.Stream, error) {
	changes, err := d.DB.Subscribe(ctx, f, fado.As(p))
	if err != nil {
		return nil, err
	}
	return changes, nil
}

// Keys is the Who of a database that keeps its own users (ADR-0057),
// one opened with fado.Options.Users: a request carries a key of a
// user, as the header Authorization: Bearer fado_…, and the command
// runs as the user the key belongs to, with the groups the database
// has for it at that moment. A key the database does not know, one
// that was revoked or has ended, and the key of a disabled user are
// all answered with unauthenticated.
//
//	db, err := fado.Open("acme.fado", fado.Options{Users: true})
//	door, err := httpdoor.New(db, httpdoor.Options{Who: httpdoor.Keys(db)})
func Keys(db *fado.DB) Who { return server.Bearer(db.Authenticate) }

// Status is the HTTP status the door answers a code with, 200 for the
// empty code of a command that ran. A caller reads the code; the status
// is for the proxies and the tools in between.
func Status(code string) int { return server.Status(code) }
