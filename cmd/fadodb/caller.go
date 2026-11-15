package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
)

// The tool as a caller of a server (ADR-0062): what "fadodb exec" and
// "fadodb file" share. Each is told the server and the key the same
// way, sends its requests through one client, and ends the same way
// when the server refuses or when no answer comes through.

// toServer are the flags that say which server and whose key.
type toServer struct {
	url, keyFile, ca *string
	plain            *bool
}

// serverFlags adds the flags of a caller to the flags of a command.
func serverFlags(fs *flag.FlagSet) *toServer {
	return &toServer{
		url:     fs.String("url", "", ""),
		keyFile: fs.String("key-file", "", ""),
		ca:      fs.String("ca", "", ""),
		plain:   fs.Bool("plain-http", false, ""),
	}
}

// serverUsage is the part of a command's help on those flags.
const serverUsage = `  --url URL        the server, such as https://fado.acme.example; left
                   out, FADODB_URL, and without it http://127.0.0.1:8080,
                   where fadodb serve listens when it is told nothing
  --key-file FILE  a file that holds the key; left out, the key is read
                   from FADODB_KEY
  --ca FILE        a certificate, in PEM, to know the server by, when
                   the machine does not know who signed the server's
  --plain-http     the key may travel in the clear beyond this machine,
                   because something between the tool and the server
                   encrypts

The key is never an argument: a line of a terminal is kept in its
history and shown to the machine's other users.
`

// caller is the tool as a caller: the server it calls, the key its
// requests carry, and where it says what went wrong.
type caller struct {
	name   string // the command of the tool, which its messages begin with
	base   string // the address of the server, up to and with /v1/
	key    string
	client *http.Client
	stderr io.Writer
}

// caller makes the caller the flags describe. When they describe none
// it says why, and nothing is sent.
func (to *toServer) caller(name string, stderr io.Writer) (*caller, bool) {
	c := &caller{name: name, stderr: stderr}
	var err error
	if c.base, err = baseOf(*to.url, *to.plain); err == nil {
		if c.key, err = keyOf(*to.keyFile); err == nil {
			c.client, err = clientOf(*to.ca)
		}
	}
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return nil, false
	}
	return c, true
}

// close gives back the connections the caller kept.
func (c *caller) close() { c.client.CloseIdleConnections() }

// send sends a request to an address of the server, with the key. what
// names the answer it waits for, for the message when none comes. With
// no reply it says so and answers how the tool ends.
func (c *caller) send(ctx context.Context, method, address string, body io.Reader, kind, what string) (*http.Response, int) {
	req, err := http.NewRequestWithContext(ctx, method, c.base+address, body)
	if err != nil {
		fmt.Fprintf(c.stderr, "%s: %v\n", c.name, err)
		return nil, exitUsage
	}
	if kind != "" {
		req.Header.Set("Content-Type", kind)
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("User-Agent", "fadodb/"+version)
	resp, err := c.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, c.interrupted(what)
		}
		fmt.Fprintf(c.stderr, "%s: no answer from %s: %v\n  is a server serving the database there? fadodb serve FILE\n", c.name, req.URL.Redacted(), unwrapped(err))
		return nil, exitNoAnswer
	}
	return resp, exitDone
}

// answer reads a reply that is one JSON object: the answer of a
// command, which it gives back, or a refusal, which it prints to
// standard error as the server gave it. Whether a reply is a refusal is
// read from its status, and not from a member named error.
func (c *caller) answer(ctx context.Context, resp *http.Response, what string) ([]byte, int) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		if ctx.Err() != nil {
			return nil, c.interrupted(what)
		}
		fmt.Fprintf(c.stderr, "%s: the answer of %s broke off: %v\n", c.name, what, err)
		return nil, exitNoAnswer
	}
	switch {
	case resp.StatusCode == http.StatusOK && isObject(body):
		return body, exitDone
	case resp.StatusCode != http.StatusOK && isRefusal(body):
		printJSON(c.stderr, body)
		return nil, exitRefused
	}
	return nil, c.notFadoDB(resp, body)
}

// command sends a command and reads its answer.
func (c *caller) command(ctx context.Context, sent string, object []byte) ([]byte, int) {
	resp, code := c.send(ctx, http.MethodPost, sent, bytes.NewReader(object), "application/json", "the answer of "+sent)
	if resp == nil {
		return nil, code
	}
	defer resp.Body.Close()
	return c.answer(ctx, resp, "the answer of "+sent)
}

// notFadoDB is how the tool ends when what answered is not a FadoDB
// server.
func (c *caller) notFadoDB(resp *http.Response, body []byte) int {
	fmt.Fprintf(c.stderr, "%s: what answered at %s is not a FadoDB server: %s\n%s", c.name, resp.Request.URL.Redacted(), resp.Status, beginning(body))
	return exitNoAnswer
}

// interrupted is how the tool ends when it is told to stop while it
// waits: it has no answer, and cannot say whether the server did what
// it was asked.
func (c *caller) interrupted(what string) int {
	fmt.Fprintf(c.stderr, "%s: interrupted before %s came; the server may have done what it was asked\n", c.name, what)
	return exitNoAnswer
}

// written ends the tool after it wrote an answer: when the answer
// could not be written, the tool has none to show, though the server
// did what it was asked.
func (c *caller) written(err error) int {
	if err != nil {
		fmt.Fprintf(c.stderr, "%s: the server answered, and the answer could not be written: %v\n", c.name, err)
		return exitNoAnswer
	}
	return exitDone
}

// deliver writes what the tool has to show: to the file named with
// --out, or to standard output.
func deliver(stdout io.Writer, file string, data []byte) error {
	if file == "" {
		_, err := stdout.Write(data)
		return err
	}
	return os.WriteFile(file, data, 0o666)
}
