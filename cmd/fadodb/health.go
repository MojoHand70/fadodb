package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// "fadodb health" asks a server whether it answers and can read its
// database: GET /v1/health (ADR-0059, item 9). It is for whatever
// watches a server from the machine it runs on and has nothing but the
// tool to ask with: the health check of a container, whose image holds
// the tool and no other program, a script that waits for a server it
// has just started, and a person at a terminal. It sends no key, since
// anybody may ask.

const healthUsage = `fadodb health - ask a server whether it answers

Usage:
  fadodb health [flags]

Asks a server for its health, GET /v1/health, and prints what it
answers, such as {"ok":true,"version":"0.6.0"}. It sends no key:
anybody may ask, and the answer says nothing of what the database
holds.

  --url URL   the server, such as https://fado.acme.example; left out,
              FADODB_URL, and without it http://127.0.0.1:8080, where
              fadodb serve listens when it is told nothing
  --ca FILE   a certificate, in PEM, to know the server by, when the
              machine does not know who signed the server's

It waits five seconds for the answer, and ends with 0 when the server
answers and can read its database, with 1 when it answers that it
cannot, with 2 when the line could not be read and with 3 when no
answer came. The health check of a container is this command:

  HEALTHCHECK CMD ["/fadodb", "health"]
`

// healthWithin is how long the tool waits for the health of a server.
const healthWithin = 5 * time.Second

// runHealth is "fadodb health".
func runHealth(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	const name = "fadodb health"
	fs := flags(name)
	given := fs.String("url", "", "")
	ca := fs.String("ca", "", "")
	rest, err := parse(fs, args)
	if err != nil {
		return asked(name, healthUsage, err, stderr)
	}
	if len(rest) != 0 {
		return asked(name, healthUsage, errors.New("the command takes flags only"), stderr)
	}
	// No key is sent, so the address may be beyond this machine and in
	// the clear.
	base, err := baseOf(*given, true)
	var client *http.Client
	if err == nil {
		client, err = clientOf(*ca)
	}
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return exitUsage
	}
	defer client.CloseIdleConnections()

	ctx, cancel := context.WithTimeout(ctx, healthWithin)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"health", nil)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return exitUsage
	}
	req.Header.Set("User-Agent", "fadodb/"+version)
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(stderr, "%s: no answer from %s: %v\n  is a server serving the database there? fadodb serve FILE\n", name, req.URL.Redacted(), unwrapped(err))
		return exitNoAnswer
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		fmt.Fprintf(stderr, "%s: the answer of %s broke off: %v\n", name, req.URL.Redacted(), err)
		return exitNoAnswer
	}
	// The health of a FadoDB server is one object that says ok, with
	// the status that goes with it. Anything else that answers at the
	// address is no such server.
	var said struct {
		OK *bool `json:"ok"`
	}
	healthy := resp.StatusCode == http.StatusOK
	if json.Unmarshal(body, &said) != nil || said.OK == nil || *said.OK != healthy ||
		(!healthy && resp.StatusCode != http.StatusServiceUnavailable) {
		fmt.Fprintf(stderr, "%s: what answered at %s is not a FadoDB server: %s\n%s", name, req.URL.Redacted(), resp.Status, beginning(body))
		return exitNoAnswer
	}
	if _, err := stdout.Write(body); err != nil {
		fmt.Fprintf(stderr, "%s: the server answered, and the answer could not be written: %v\n", name, err)
		return exitNoAnswer
	}
	if !healthy {
		fmt.Fprintf(stderr, "%s: the server at %s answers, and cannot read its database\n", name, req.URL.Redacted())
		return exitRefused
	}
	return exitDone
}
