// Command server is the program of the manual's chapter "Serving a
// database" (docs/manual/serving-a-database.md), section "A server of
// your own": what fadodb serve does, with one thing added, an action
// that a Go function of the company runs. fadodb serve runs no Go
// function; a program like this one does.
//
//	fadodb init acme.fado
//	fadodb user add olga --kind person --system administrators --system designers --db acme.fado
//	fadodb key new olga --label laptop --db acme.fado
//	go run ./examples/server acme.fado
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
	"time"

	"github.com/MojoHand70/fadodb/fado"
	"github.com/MojoHand70/fadodb/fado/httpdoor"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: server FILE, the database that fadodb init made")
		os.Exit(2)
	}
	// On this machine only: a key travels with every request, and this
	// program has no certificate.
	ln, err := net.Listen("tcp", "127.0.0.1:8080")
	if err == nil {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		err = serve(ctx, os.Args[1], ln, os.Stdout)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// serve answers the callers of the database at the path, each by the
// key of a user of the database, until ctx ends.
func serve(ctx context.Context, path string, ln net.Listener, w io.Writer) error {
	db, err := fado.Open(path, fado.Options{
		// The database keeps its users and their keys, and this program
		// holds it alone, as a server must.
		Users: true,
		Alone: "the server of Acme",
		// The one thing added: the Go function behind the action
		// mail.send. A definition of that name, with no request, is
		// taken here and refused by fadodb serve.
		Actions: map[string]fado.ActionFunc{
			"mail.send": func(ctx context.Context, call *fado.ActionCall) (fado.Values, error) {
				fmt.Fprintf(w, "a mail goes to %v\n", call.Args["to"])
				return nil, nil
			},
		},
	})
	if err != nil {
		return err
	}
	defer db.Close()

	// The door: every command of the database over HTTP, for whoever
	// sends a key.
	door, err := httpdoor.New(db, httpdoor.Options{Who: httpdoor.Keys(db)})
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: door, ReadHeaderTimeout: 10 * time.Second}
	ended := make(chan error, 1)
	go func() { ended <- srv.Serve(ln) }()
	select {
	case err := <-ended:
		return err
	case <-ctx.Done():
	}
	// Told to stop: the commands that run end first, then the database
	// is closed.
	within, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(within); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return nil
}
