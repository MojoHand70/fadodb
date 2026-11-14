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
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/MojoHand70/fadodb/fado"
	"github.com/MojoHand70/fadodb/fado/httpdoor"
)

const serveUsage = `fadodb serve - serve a database over HTTP

Usage:
  fadodb serve FILE [flags]

Serves the database at FILE, which fadodb init made, to callers who
send a key of a user of the database:

  POST /v1/<command>     a command, its object as the body
  POST /v1/blobs         a file, put
  GET  /v1/file          a file, read through its document
  GET  /v1/health        that the server answers, for anybody

Told nothing, it listens on this machine only, at 127.0.0.1:8080.

  --listen ADDR      where it listens, such as 127.0.0.1:8080 or :8443
  --tls-cert FILE    the certificate it shows, with --tls-key
  --tls-key FILE     the key of the certificate
  --plain-http       it listens beyond this machine without a
                     certificate, because something in front of it
                     encrypts: a proxy, or the network of a container

A key travels with every request, so the server refuses to listen
beyond this machine in the clear unless --plain-http says that it may.
While it serves, the server holds the database alone. It writes one
line for each request to standard error, and stops when it is told to
(an interrupt), letting the commands that run end first.
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
	if !exists(path) {
		return noDatabase(name, path, stderr)
	}

	db, err := fado.Open(path, fado.Options{Users: true, Alone: name})
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return 1
	}
	defer db.Close()
	log := &requestLog{w: stderr}
	door, err := httpdoor.New(db, httpdoor.Options{Who: httpdoor.Keys(db), Report: log.write})
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
		Handler: &served{db: db, door: door},
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
	// Told to stop: no new request is taken, the commands that run end,
	// and the database is closed (item 10).
	fmt.Fprintf(stderr, "%s: stopping\n", name)
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
	door http.Handler
}

func (s *served) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/v1/health" {
		s.health(w, r)
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
// value of a document or a key.
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
	if r.Err != nil {
		// What failed in the engine is said here and not to the caller.
		b = append(b, `,"error":`...)
		b = strconv.AppendQuote(b, r.Err.Error())
	}
	b = append(b, '}', '\n')
	l.line = b
	_, _ = l.w.Write(b)
}
