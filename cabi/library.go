package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MojoHand70/fadodb/fado"
	"github.com/MojoHand70/fadodb/fado/outbound"
	"github.com/MojoHand70/fadodb/internal/secret"
)

// The library in Go: the databases it opened and what each function of
// fadodb.h does with them (ADR-0101). The functions of main.go turn the
// strings of C into Go and back, and nothing else; everything a test
// holds to is here, so that it is tried without a C compiler.

// version is the version the library says it is, set by the release
// build through -ldflags, as the programs' is.
var version = "dev"

// The codes of the command set this file answers with (ADR-0055, item
// 7): a database the library does not hold, a member of the options or
// of the caller that is not taken, and a failure that is no refusal.
const (
	codeClosed   = "closed"
	codeBadJSON  = "bad_json"
	codeBadValue = "bad_value"
	codeInternal = "internal"
)

// database is one database the library opened. Every command holds mu
// for reading while it runs, and closing holds it for writing, so a
// database is closed once the commands that run on it have ended; done
// ends the ones that wait, as wait_for_changes does.
type database struct {
	mu   sync.RWMutex
	db   *fado.DB
	ctx  context.Context
	done context.CancelFunc
}

// The databases by their numbers. A number is given once in the life of
// the process, so a number kept after its database was closed never
// reaches another.
var (
	mu        sync.Mutex
	databases = map[uint64]*database{}
	last      uint64
)

// openDatabase is fado_open: the number of the database it opened, or 0
// and the refusal that says why it did not.
func openDatabase(path, options string) (n uint64, refusal []byte) {
	defer caught("fado_open", &refusal)
	if path == "" {
		return 0, refuse(&fado.Error{Code: codeBadValue, Message: "fado_open was given no file",
			Hint: "give the path of the database file; it is made when it does not exist"})
	}
	o, e := readOptions(options)
	if e != nil {
		return 0, refuse(e)
	}
	db, err := fado.Open(path, o)
	if err != nil {
		return 0, failed(err)
	}
	ctx, done := context.WithCancel(context.Background())
	mu.Lock()
	last++
	n = last
	databases[n] = &database{db: db, ctx: ctx, done: done}
	mu.Unlock()
	return n, nil
}

// execCommand is fado_exec: the answer of one command, as the caller,
// on the database of number n.
func execCommand(n uint64, caller, name, object string) (answer []byte) {
	defer caught(name, &answer)
	return onDatabase(n, caller, func(d *database, p fado.Principal) []byte {
		if strings.TrimSpace(object) == "" {
			object = "{}"
		}
		answer, err := d.db.Exec(d.ctx, p, name, []byte(object))
		if err != nil && fado.ErrorCode(err) == codeInternal {
			return failed(err)
		}
		return answer
	})
}

// onDatabase runs do on the database of number n, as the caller, while
// the database is held open: a close waits for it to end.
func onDatabase(n uint64, caller string, do func(*database, fado.Principal) []byte) []byte {
	mu.Lock()
	d := databases[n]
	mu.Unlock()
	if d == nil {
		return notOpen(n)
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.db == nil {
		return notOpen(n)
	}
	p, e := readCaller(caller)
	if e != nil {
		return refuse(e)
	}
	return do(d, p)
}

// closeDatabase is fado_close: nil once the database of number n is
// closed, or the refusal that says why it was not.
func closeDatabase(n uint64) (refusal []byte) {
	defer caught("fado_close", &refusal)
	mu.Lock()
	d := databases[n]
	delete(databases, n)
	mu.Unlock()
	if d == nil {
		return notOpen(n)
	}
	d.done()
	d.mu.Lock()
	defer d.mu.Unlock()
	db := d.db
	d.db = nil
	if err := db.Close(); err != nil {
		return failed(err)
	}
	return nil
}

// notOpen is the refusal of a number the library holds no database
// under.
func notOpen(n uint64) []byte {
	return refuse(&fado.Error{Code: codeClosed,
		Message: "no database is open under the number " + strconv.FormatUint(n, 10) + ": it was closed, or fado_open never answered it",
		Hint:    "send a command to the number fado_open answered, before fado_close is called with it"})
}

// refuse is the answer of a refusal, as the command set writes one:
// {"error": …}.
func refuse(e *fado.Error) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(struct {
		Error *fado.Error `json:"error"`
	}{e})
	return bytes.TrimRight(b.Bytes(), "\n")
}

// failed is the answer of an error of the engine. A refusal keeps its
// code; a failure that is no refusal is answered internal with its
// words, since the program that loaded the library is the one that runs
// the engine and has no other place to read them from (ADR-0101, item
// 8).
func failed(err error) []byte {
	var e *fado.Error
	if errors.As(err, &e) {
		return refuse(e)
	}
	return refuse(&fado.Error{Code: codeInternal, Message: "the engine failed: " + err.Error(),
		Hint: "this is no refusal of what was sent; the words say what failed, and a report of them helps"})
}

// caught answers a panic in the engine as a failure, so that it ends
// the call and not the program that loaded the library.
func caught(what string, answer *[]byte) {
	if r := recover(); r != nil {
		*answer = refuse(&fado.Error{Code: codeInternal,
			Message: fmt.Sprintf("the engine failed in %s: %v", what, r),
			Hint:    "this is no refusal of what was sent; a report of these words and of the stack helps: " + firstLines(string(debug.Stack()), 12)})
	}
}

func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, " | ")
}

// readOptions reads the options of fado_open (ADR-0101, item 4): a JSON
// object whose members are named as the settings of fadodb serve name
// the same things.
func readOptions(text string) (fado.Options, *fado.Error) {
	var o fado.Options
	members, e := readObject(text, "the options")
	if e != nil {
		return o, e
	}
	var calls outbound.Options
	var secrets string
	for name, raw := range members {
		var err error
		switch name {
		case "synchronous":
			err = json.Unmarshal(raw, &o.Synchronous)
		case "timezone":
			err = json.Unmarshal(raw, &o.TimeZone)
		case "max_text":
			err = whole(raw, &o.MaxText)
		case "max_indexed_rows":
			err = whole(raw, &o.MaxIndexedRows)
		case "max_question_seconds":
			err = duration(raw, &o.MaxQuestion)
		case "max_blob_bytes":
			var n int
			err = whole(raw, &n)
			o.MaxBlob = int64(n)
		case "blob_store":
			err = json.Unmarshal(raw, &o.BlobStore)
			if err == nil && o.BlobStore == "external" {
				return o, &fado.Error{Code: codeBadValue, Path: "/blob_store",
					Message: "a blob store of the embedder's is a Go value, which the options of the library cannot hold",
					Hint:    "sqlite keeps the files in the database file and dir in a directory beside it"}
			}
		case "host_answers_for":
			err = json.Unmarshal(raw, &o.HostAnswersFor)
		case "outbox":
			if e := readOutbox(raw, &o.Outbox); e != nil {
				return o, e
			}
		case "outbound":
			if e := readOutbound(raw, &calls); e != nil {
				return o, e
			}
		case "secrets_dir":
			err = json.Unmarshal(raw, &secrets)
		default:
			return o, &fado.Error{Code: codeBadJSON, Path: pointer(name),
				Message: "the options have no member " + strconv.Quote(name),
				Hint:    "the options are synchronous, timezone, max_text, max_indexed_rows, max_question_seconds, max_blob_bytes, blob_store, host_answers_for, outbox, outbound and secrets_dir"}
		}
		if err != nil {
			return o, wrongType(name, "the options", err)
		}
	}
	// The database sends requests only when the options list a host,
	// and only to the hosts they list, as fadodb serve does (ADR-0059,
	// items 2 and 4); the secrets of the connections are in the
	// directory of secrets or the environment of the program (item 5).
	if len(calls.Hosts) > 0 || len(calls.Connections) > 0 {
		sender, err := outbound.Allow(calls)
		if err != nil {
			return o, &fado.Error{Code: codeBadValue, Path: "/outbound", Message: "the outbound hosts: " + err.Error(),
				Hint: "a host is slack.com, .slack.com for every host under it, books.internal:8443 with its port, or http://localhost:8080 over http"}
		}
		o.Outbound = sender
		o.Secrets = (&secret.Finder{Dir: secrets, Env: os.LookupEnv, Holder: "program"}).Find
	}
	return o, nil
}

// readOutbound reads the hosts the actions of the database may call, in
// the words of the settings of fadodb serve: {"hosts": [...],
// "connections": {"slack": ["slack.com"]}}.
func readOutbound(text []byte, calls *outbound.Options) *fado.Error {
	members, e := readObject(string(text), "the outbound hosts")
	if e != nil {
		e.Path = "/outbound" + e.Path
		return e
	}
	for name, raw := range members {
		var err error
		switch name {
		case "hosts":
			err = json.Unmarshal(raw, &calls.Hosts)
		case "connections":
			err = json.Unmarshal(raw, &calls.Connections)
		default:
			return &fado.Error{Code: codeBadJSON, Path: "/outbound" + pointer(name),
				Message: "the outbound hosts have no member " + strconv.Quote(name),
				Hint:    "outbound takes hosts, a list, and connections, each a list of hosts by the connection's name"}
		}
		if err != nil {
			e := wrongType(name, "the outbound hosts", err)
			e.Path = "/outbound" + e.Path
			return e
		}
	}
	return nil
}

func readOutbox(text []byte, o *fado.OutboxOptions) *fado.Error {
	members, e := readObject(string(text), "the outbox")
	if e != nil {
		e.Path = "/outbox" + e.Path
		return e
	}
	for name, raw := range members {
		var err error
		switch name {
		case "manual":
			err = json.Unmarshal(raw, &o.Manual)
		case "workers":
			err = whole(raw, &o.Workers)
		case "max_attempts":
			err = whole(raw, &o.MaxAttempts)
		case "backoff_seconds":
			err = duration(raw, &o.Backoff)
		default:
			return &fado.Error{Code: codeBadJSON, Path: "/outbox" + pointer(name),
				Message: "the outbox has no member " + strconv.Quote(name),
				Hint:    "the outbox takes manual, workers, max_attempts and backoff_seconds"}
		}
		if err != nil {
			e := wrongType(name, "the outbox", err)
			e.Path = "/outbox" + e.Path
			return e
		}
	}
	return nil
}

// readCaller reads the caller of fado_exec (ADR-0101, item 2): who the
// program that loaded the library says is calling, with the members a
// user has in the command set. Nothing, or {}, is nobody, as a Principal
// left empty is in Go.
func readCaller(text string) (fado.Principal, *fado.Error) {
	var p fado.Principal
	members, e := readObject(text, "the caller")
	if e != nil {
		return p, e
	}
	for name, raw := range members {
		var err error
		switch name {
		case "id":
			err = json.Unmarshal(raw, &p.ID)
		case "kind":
			err = json.Unmarshal(raw, &p.Kind)
		case "groups":
			err = json.Unmarshal(raw, &p.Groups)
		case "system_groups":
			err = json.Unmarshal(raw, &p.System)
		case "today":
			var day string
			if err = json.Unmarshal(raw, &day); err == nil {
				p.Today, err = time.Parse(time.DateOnly, day)
				if err != nil {
					return p, &fado.Error{Code: codeBadValue, Path: "/today",
						Message: "the caller's today is " + strconv.Quote(day) + ", which is no day",
						Hint:    "a day is written 2026-11-27"}
				}
			}
		default:
			return p, &fado.Error{Code: codeBadJSON, Path: pointer(name),
				Message: "the caller has no member " + strconv.Quote(name),
				Hint:    "the caller is id, kind, groups, system_groups and today; whom the caller acts for is the command's for"}
		}
		if err != nil {
			return p, wrongType(name, "the caller", err)
		}
	}
	return p, nil
}

// readObject reads a JSON object into its members, each given once.
// Text that is empty is the empty object.
func readObject(text, what string) (map[string]json.RawMessage, *fado.Error) {
	members := map[string]json.RawMessage{}
	if strings.TrimSpace(text) == "" {
		return members, nil
	}
	dec := json.NewDecoder(strings.NewReader(text))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return nil, &fado.Error{Code: codeBadJSON, Message: "the text given for " + what + " is not a JSON object",
			Hint: `write them as one object, such as {"id": "ann"}; nothing at all is the same as {}`}
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		name := tok.(string)
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			break
		}
		if _, twice := members[name]; twice {
			return nil, &fado.Error{Code: codeBadJSON, Path: pointer(name),
				Message: strconv.Quote(name) + " is given twice in " + what, Hint: "give each member once"}
		}
		members[name] = raw
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') || dec.More() {
		return nil, &fado.Error{Code: codeBadJSON, Message: "the text given for " + what + " is not one whole JSON object",
			Hint: "the text is cut off, or something follows the object"}
	}
	if _, err := dec.Token(); err == nil {
		return nil, &fado.Error{Code: codeBadJSON, Message: "the text given for " + what + " holds more than one JSON object",
			Hint: "give one object"}
	}
	return members, nil
}

func whole(raw json.RawMessage, n *int) error { return json.Unmarshal(raw, n) }

// duration reads a number of seconds, which may have a fraction.
func duration(raw json.RawMessage, d *time.Duration) error {
	var s float64
	if err := json.Unmarshal(raw, &s); err != nil {
		return err
	}
	*d = time.Duration(s * float64(time.Second))
	return nil
}

func wrongType(name, what string, err error) *fado.Error {
	want := "of another JSON type"
	var te *json.UnmarshalTypeError
	if errors.As(err, &te) {
		switch te.Type.Kind().String() {
		case "int", "int64":
			want = "a whole number"
		case "float64":
			want = "a number"
		case "slice":
			want = "a list of text"
		case "string":
			want = "text"
		case "bool":
			want = "true or false"
		}
	}
	return &fado.Error{Code: codeBadJSON, Path: pointer(name),
		Message: "the member " + strconv.Quote(name) + " of " + what + " is not " + want,
		Hint:    "write it as " + want}
}

// pointer is the JSON pointer of a member.
func pointer(name string) string {
	if strings.ContainsAny(name, "~/") {
		name = strings.ReplaceAll(strings.ReplaceAll(name, "~", "~0"), "/", "~1")
	}
	return "/" + name
}
