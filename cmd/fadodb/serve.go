package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/MojoHand70/fadodb/fado"
	"github.com/MojoHand70/fadodb/fado/httpdoor"
	"github.com/MojoHand70/fadodb/fado/outbound"
	"github.com/MojoHand70/fadodb/internal/model"
	"github.com/MojoHand70/fadodb/internal/store"
)

const serveUsage = `fadodb serve - serve a database over HTTP

Usage:
  fadodb serve FILE [flags]

Serves the database at FILE, which fadodb init made, to callers who
send a key of a user of the database:

  POST /v1/<command>     a command, its object as the body
  POST /v1/subscribe     the stream of changes: a reply that stays open
                         and tells the caller of every event it may
                         read of
  POST /v1/blobs         a file, put
  GET  /v1/file          a file, read through its document
  GET  /v1/health        that the server answers, for anybody
  GET  /v1/metrics       its counts, for a database administrator, in
                         the text form Prometheus reads

Told nothing, it listens on this machine only, at 127.0.0.1:8080.

  --listen ADDR      where it listens, such as 127.0.0.1:8080 or :8443
  --tls-cert FILE    the certificate it shows, with --tls-key
  --tls-key FILE     the key of the certificate
  --plain-http       it listens beyond this machine without a
                     certificate, because something in front of it
                     encrypts: a proxy, or the network of a container
  --settings FILE    the settings, a JSON file: the hosts the actions
                     of the database may call, and how much the server
                     takes on
  --secrets-dir DIR  where the secrets of the connections are: the
                     secret token of the connection slack is the file
                     DIR/slack/token

A key travels with every request, so the server refuses to listen
beyond this machine in the clear unless --plain-http says that it may.
While it serves, the server holds the database alone. It writes one
line for each request to standard error, and stops when it is told to
(an interrupt), ending its streams and letting the commands that run
end first.

Told nothing, the actions of the database call no host. The settings
list the hosts they may call:

  {
    "outbound": {
      "hosts": ["hooks.acme.example"],
      "connections": {"slack": ["slack.com"]}
    },
    "limits": {
      "request_bytes": 16777216,
      "command_seconds": 30,
      "commands": 64,
      "streams": 256
    }
  }

A secret is never in the settings. Without --secrets-dir, or when the
file is not there, the secret token of the connection slack is read
from the environment, as FADODB_SECRET_SLACK__TOKEN. A secret is read
again for every request, so it is changed without a restart.

The server runs no Go function: an action is the engine's own, or a
request the server sends.
`

// defaultListen is where the server listens when it is told nothing:
// this machine only (ADR-0059, item 2).
const defaultListen = "127.0.0.1:8080"

// stopWithin is how long the commands that run are given to end when
// the server is told to stop (ADR-0059, item 10).
const stopWithin = 10 * time.Second

// runServe is "fadodb serve FILE". It serves until ctx ends, which an
// interrupt of the program does. ready, when given, is told where the
// server listens once it does.
func runServe(ctx context.Context, args []string, stdout, stderr io.Writer, ready func(addr string)) int {
	const name = "fadodb serve"
	fs := flags(name, serveUsage, stderr)
	listen := fs.String("listen", defaultListen, "")
	cert := fs.String("tls-cert", "", "")
	key := fs.String("tls-key", "", "")
	plain := fs.Bool("plain-http", false, "")
	settingsFile := fs.String("settings", "", "")
	secretsDir := fs.String("secrets-dir", "", "")
	rest, err := parse(fs, args)
	if err != nil {
		return asked(name, serveUsage, err, stderr)
	}
	if len(rest) != 1 {
		return asked(name, serveUsage, errors.New("give the file of the database to serve"), stderr)
	}
	path := rest[0]
	if (*cert == "") != (*key == "") {
		return asked(name, serveUsage, errors.New("a certificate needs both --tls-cert and --tls-key"), stderr)
	}
	encrypted := *cert != ""
	if encrypted && *plain {
		return asked(name, serveUsage, errors.New("--plain-http says the server sends in the clear, and a certificate was given"), stderr)
	}
	// A key never travels in the clear by accident (item 3).
	if !encrypted && !*plain && !thisMachineOnly(*listen) {
		fmt.Fprintf(stderr, "%s: listening at %s without a certificate would send every key in the clear beyond this machine\n"+
			"  give the server a certificate:        --tls-cert FILE --tls-key FILE\n"+
			"  or say that something in front encrypts: --plain-http\n"+
			"  or listen on this machine only:          --listen %s\n", name, *listen, defaultListen)
		return 2
	}
	// What the operator tells the server beyond its flags (item 4). A
	// mistake in it is found now, before anything is opened.
	told := &settings{}
	if *settingsFile != "" {
		if told, err = readSettings(*settingsFile); err != nil {
			fmt.Fprintf(stderr, "%s: the settings %s: %v\n", name, *settingsFile, err)
			return 2
		}
	}
	if *secretsDir != "" {
		if info, err := os.Stat(*secretsDir); err != nil || !info.IsDir() {
			fmt.Fprintf(stderr, "%s: there is no directory of secrets at %s\n", name, *secretsDir)
			return 2
		}
	}
	// The database is opened with no Go function: the server runs none
	// (item 6). It sends requests only when the settings list a host,
	// and only to the hosts they list (items 2 and 4).
	opened := fado.Options{Users: true, Alone: name}
	kept := &secrets{dir: *secretsDir, env: os.LookupEnv}
	if told.calls() {
		if opened.Outbound, err = outbound.Allow(outbound.Options{Hosts: told.hosts, Connections: told.connections}); err != nil {
			fmt.Fprintf(stderr, "%s: the settings %s: %v\n", name, *settingsFile, err)
			return 2
		}
		opened.Secrets = kept.find
	}
	if !exists(path) {
		return noDatabase(name, path, stderr)
	}

	db, err := fado.Open(path, opened)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return 1
	}
	defer db.Close()
	// Every request is told once, when it was answered: it gets its line
	// and is counted (item 9).
	log := &requestLog{w: stderr}
	counted := newMetrics(version, time.Now())
	report := func(r httpdoor.Request) {
		log.write(r)
		counted.record(r)
	}
	who := httpdoor.Keys(db)
	door, err := httpdoor.New(db, httpdoor.Options{Who: who, Limits: told.limits, Report: report})
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return 1
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return 1
	}
	srv := &http.Server{
		Handler: &served{db: db, door: door, path: path, who: who, metrics: counted, report: report},
		// A caller that connects and says nothing is not waited for
		// forever. A body and a reply have no limit of time here: a
		// large file takes as long as its line allows.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	scheme := "http"
	if encrypted {
		scheme = "https"
	}
	fmt.Fprintf(stderr, "%s: serving %s at %s://%s\n", name, path, scheme, ln.Addr())
	if users, err := db.Users(ctx, fado.UserFilter{}, onTheMachine); err == nil && len(users) == 0 {
		fmt.Fprintf(stderr, "%s: the database has no user yet, so nobody can call it. Stop the server and make the first:\n"+
			"  fadodb user add olga --kind person --system administrators --db %s\n  fadodb key new olga --label laptop --db %s\n", name, path, path)
	}
	fmt.Fprintf(stderr, "%s: %s\n", name, told.mayCall())
	for _, line := range waiting(db.Actions(), kept) {
		fmt.Fprintf(stderr, "%s: %s\n", name, line)
	}
	if ready != nil {
		ready(ln.Addr().String())
	}

	failed := make(chan error, 1)
	go func() {
		if encrypted {
			failed <- srv.ServeTLS(ln, *cert, *key)
		} else {
			failed <- srv.Serve(ln)
		}
	}()
	select {
	case err := <-failed:
		fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return 1
	case <-ctx.Done():
	}
	// Told to stop: no new request is taken, the streams of changes are
	// ended, each told why (ADR-0058, item 7), the commands that run
	// end, and the database is closed (item 10).
	fmt.Fprintf(stderr, "%s: stopping\n", name)
	door.EndStreams()
	within, cancel := context.WithTimeout(context.Background(), stopWithin)
	defer cancel()
	if err := srv.Shutdown(within); err != nil {
		fmt.Fprintf(stderr, "%s: some requests did not end within %s and were cut: %v\n", name, stopWithin, err)
		_ = srv.Close()
	}
	if err := db.Close(); err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return 1
	}
	fmt.Fprintf(stderr, "%s: stopped\n", name)
	return 0
}

// mayCall says, in a line, what the actions of the database may call.
func (s *settings) mayCall() string {
	if !s.calls() {
		return "its actions may call no host; the settings list the hosts they may call (--settings)"
	}
	var parts []string
	if len(s.hosts) > 0 {
		parts = append(parts, strings.Join(s.hosts, ", "))
	}
	names := make([]string, 0, len(s.connections))
	for name := range s.connections {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		hosts := strings.Join(s.connections[name], ", ")
		if hosts == "" {
			hosts = "no host"
		}
		parts = append(parts, "over the connection "+name+", "+hosts)
	}
	return "its actions may call " + strings.Join(parts, "; ")
}

// waiting says which actions of the database this server cannot run as
// it was started, a line for each reason, so that the operator learns
// it now and not from an action that died: an action that is a Go
// function (ADR-0059, item 6), one whose connection the settings do
// not have, and one whose secret is in neither place.
func waiting(actions []fado.ActionInfo, kept *secrets) []string {
	var functions, lines []string
	for _, a := range actions {
		switch {
		case a.Kind == store.ActionRegistered && !a.Available:
			functions = append(functions, a.Name)
		case a.Kind != store.ActionDeclared || a.Name == model.ActionHTTPCall:
		case !a.Available && a.Connection != "":
			lines = append(lines, "the action "+a.Name+" sends its request over the connection "+a.Connection+", which the settings do not have: it does not run")
		case !a.Available:
			lines = append(lines, "the action "+a.Name+" sends a request, and the settings list no host: it does not run")
		default:
			for _, name := range a.Secrets {
				if _, err := kept.find(context.Background(), a.Connection, name); err != nil {
					lines = append(lines, "the action "+a.Name+" reads the secret "+name+" of the connection "+a.Connection+", and "+err.Error()+": it does not run until the secret is there")
				}
			}
		}
	}
	if len(functions) > 0 {
		lines = append([]string{"the database has actions that a Go function runs, and this server runs none: " + strings.Join(functions, ", ") +
			". They do not run here; they run in a program of your own that opens the database with the functions"}, lines...)
	}
	return lines
}

// thisMachineOnly reports whether an address to listen at can be
// reached from this machine alone.
func thisMachineOnly(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// interrupted is a context that ends when the program is told to stop.
func interrupted() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// served is what the server answers: the health of the server, for
// anybody, and everything else through the door of the database.
type served struct {
	db   *fado.DB
	door *httpdoor.Door
	// path is the file of the database; who says who sends a request,
	// as the door asks it; metrics are the counts of the server, and
	// report is what every request is told to once it was answered.
	path    string
	who     httpdoor.Who
	metrics *metrics
	report  func(httpdoor.Request)
}

func (s *served) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/v1/health":
		s.health(w, r)
		return
	case metricsPath:
		s.counts(w, r)
		return
	}
	s.door.ServeHTTP(w, r)
}

// health answers that the server answers and can read its database, and
// which version it is (ADR-0059, item 9). It asks for no key and says
// nothing of what the database holds.
func (s *served) health(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Type", "application/json")
	h.Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		h.Set("Allow", http.MethodGet)
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = io.WriteString(w, `{"error":{"code":"bad_json","message":"the health of the server is read with GET"}}`+"\n")
		return
	}
	if err := s.db.Ping(r.Context()); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"ok":false,"version":`+strconv.Quote(version)+`}`+"\n")
		return
	}
	_, _ = io.WriteString(w, `{"ok":true,"version":`+strconv.Quote(version)+`}`+"\n")
}

// requestLog writes one line for each request, as JSON (ADR-0059,
// item 9): when it came, the command, the user, the status, the code of
// the refusal if there was one and how long it took. Never a body, a
// value of a document or a key. A stream of changes has its line when
// it ended: ms is then the time until it was open, and the line says
// for how many seconds it stayed open and how many events it told.
type requestLog struct {
	mu   sync.Mutex
	w    io.Writer
	line []byte
}

func (l *requestLog) write(r httpdoor.Request) {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.line[:0]
	b = append(b, `{"time":"`...)
	b = r.At.UTC().AppendFormat(b, "2006-01-02T15:04:05.000Z")
	b = append(b, `","command":`...)
	b = strconv.AppendQuote(b, r.Command)
	b = append(b, `,"user":`...)
	b = strconv.AppendQuote(b, r.User)
	b = append(b, `,"status":`...)
	b = strconv.AppendInt(b, int64(r.Status), 10)
	if r.Code != "" {
		b = append(b, `,"code":`...)
		b = strconv.AppendQuote(b, r.Code)
	}
	b = append(b, `,"ms":`...)
	b = strconv.AppendFloat(b, float64(r.Took.Microseconds())/1000, 'f', 3, 64)
	if r.Stream {
		b = append(b, `,"open_s":`...)
		b = strconv.AppendFloat(b, float64(r.Lasted.Milliseconds())/1000, 'f', 3, 64)
		b = append(b, `,"events":`...)
		b = strconv.AppendInt(b, int64(r.Entries), 10)
	}
	if r.Err != nil {
		// What failed in the engine is said here and not to the caller.
		b = append(b, `,"error":`...)
		b = strconv.AppendQuote(b, r.Err.Error())
	}
	b = append(b, '}', '\n')
	l.line = b
	_, _ = l.w.Write(b)
}
