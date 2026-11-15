package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/MojoHand70/fadodb/fado"
	"github.com/MojoHand70/fadodb/internal/command"
)

// "fadodb exec" sends a command to a server (ADR-0062). The tool is a
// caller as any other: it holds no database, it sends the name of a
// command and its object to the address of the command, with the key of
// a user, and it prints what the server answers. So a command sent from
// a terminal is judged by the rules of the database and written to its
// log under the user whose key it carries, as one sent by a program is.

const execUsage = `fadodb exec - send a command to a server

Usage:
  fadodb exec COMMAND [OBJECT] [flags]
  fadodb exec help [COMMAND]

Sends the command to a server that serves a database, as the user whose
key it is given, and prints the answer. OBJECT is the object of the
command, as JSON: the same object the command takes over HTTP. "-"
reads it from standard input and "@FILE" from a file; left out, it is
{}.

  fadodb exec get '{"class": "Invoice", "id": "FV/2026/11/0042"}'
  fadodb exec define_class --from definition=invoice.json
  fadodb exec query '{"class": "Invoice"}' --csv > invoices.csv

  --from MEMBER=FILE
                   a member of the object is the JSON a file holds, such
                   as a definition: --from definition=invoice.json; may
                   be given several times
  --url URL        the server, such as https://fado.acme.example; left
                   out, FADODB_URL, and without it http://127.0.0.1:8080,
                   where fadodb serve listens when it is told nothing
  --key-file FILE  a file that holds the key; left out, the key is read
                   from FADODB_KEY
  --ca FILE        a certificate, in PEM, to know the server by, when
                   the machine does not know who signed the server's
  --plain-http     the key may travel in the clear beyond this machine,
                   because something between the tool and the server
                   encrypts
  --csv            print the documents of query, or the rows of sql, as
                   CSV

The key is never an argument: a line of a terminal is kept in its
history and shown to the machine's other users.

"fadodb exec help" lists the commands, and "fadodb exec help COMMAND"
the members of one and of its answer.

The answer is printed to standard output. A command the server refuses
prints the refusal to standard error, as the server gave it. The stream
of subscribe prints one event to a line until it is interrupted; the
tool says on standard error when the stream is open, and from then on
no event is missed.

It ends with:
  0  the command was carried out
  1  the server refused it
  2  the line could not be read, and nothing was sent
  3  no answer came through: the server was not reached, what answered
     is not a FadoDB server, or the answer was cut off. The command may
     have been carried out; one that carries a "key" is safely sent
     again
`

// The ways "fadodb exec" ends.
const (
	exitDone     = 0
	exitRefused  = 1
	exitUsage    = 2
	exitNoAnswer = 3
)

// environment reads a variable of the environment.
var environment = os.Getenv

// streamIsOpen is what the tool says once a server has opened a stream.
const streamIsOpen = "the stream is open"

// dialWithin is how long the tool waits for a server to take its
// connection.
const dialWithin = 10 * time.Second

// runExec is "fadodb exec COMMAND [OBJECT]". A stream it has opened
// ends when ctx does, which an interrupt of the program does.
func runExec(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	const name = "fadodb exec"
	fs := flags(name, execUsage, stderr)
	server := fs.String("url", "", "")
	keyFile := fs.String("key-file", "", "")
	ca := fs.String("ca", "", "")
	plain := fs.Bool("plain-http", false, "")
	asCSV := fs.Bool("csv", false, "")
	var from several
	fs.Var(&from, "from", "")
	rest, err := parse(fs, args)
	if err != nil {
		return asked(name, execUsage, err, stderr)
	}
	if len(rest) == 0 {
		return asked(name, execUsage, errors.New("say which command to send"), stderr)
	}
	if rest[0] == "help" {
		return execHelp(rest[1:], stdout, stderr)
	}
	sent := rest[0]
	if len(rest) > 2 {
		return asked(name, execUsage, fmt.Errorf("%d arguments after %s: its object is one argument, in single quotes", len(rest)-1, sent), stderr)
	}
	if !commandName(sent) {
		fmt.Fprintf(stderr, "%s: %q is not the name of a command\n  the commands: fadodb exec help\n", name, sent)
		return exitUsage
	}
	if *asCSV && sent != "query" && sent != "sql" {
		fmt.Fprintf(stderr, "%s: --csv prints the documents of query and the rows of sql; %s answers neither\n", name, sent)
		return exitUsage
	}
	object := []byte("{}")
	if len(rest) == 2 {
		if object, err = objectOf(rest[1]); err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", name, err)
			return exitUsage
		}
	}
	for _, member := range from {
		if object, err = withMember(object, member); err != nil {
			fmt.Fprintf(stderr, "%s: %v\n", name, err)
			return exitUsage
		}
	}

	address, err := addressOf(*server, sent, *plain)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return exitUsage
	}
	key, err := keyOf(*keyFile)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return exitUsage
	}
	client, err := clientOf(*ca)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return exitUsage
	}
	defer client.CloseIdleConnections()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, address, bytes.NewReader(object))
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return exitUsage
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("User-Agent", "fadodb/"+version)
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return interruptedBeforeTheAnswer(name, sent, stderr)
		}
		fmt.Fprintf(stderr, "%s: no answer from %s: %v\n  is a server serving the database there? fadodb serve FILE\n", name, address, unwrapped(err))
		return exitNoAnswer
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK && strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		return follow(ctx, name, resp.Body, stdout, stderr)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		if ctx.Err() != nil {
			return interruptedBeforeTheAnswer(name, sent, stderr)
		}
		fmt.Fprintf(stderr, "%s: the answer of %s broke off: %v\n", name, address, err)
		return exitNoAnswer
	}
	switch {
	case resp.StatusCode == http.StatusOK && isObject(body):
		if *asCSV {
			err = writeCSV(stdout, sent, object, body)
		} else {
			err = printJSON(stdout, body)
		}
		if err != nil {
			fmt.Fprintf(stderr, "%s: the command was carried out, and its answer could not be written: %v\n", name, err)
			return exitNoAnswer
		}
		return exitDone
	case resp.StatusCode != http.StatusOK && isRefusal(body):
		printJSON(stderr, body)
		return exitRefused
	}
	fmt.Fprintf(stderr, "%s: what answered at %s is not a FadoDB server: %s\n%s", name, address, resp.Status, beginning(body))
	return exitNoAnswer
}

// interruptedBeforeTheAnswer is how the tool ends when it is told to
// stop while it waits for an answer: it has none, and cannot say
// whether the command was carried out.
func interruptedBeforeTheAnswer(name, sent string, stderr io.Writer) int {
	fmt.Fprintf(stderr, "%s: interrupted before the answer of %s came; the command may have been carried out\n", name, sent)
	return exitNoAnswer
}

// commandName reports whether a word can be the name of a command:
// small letters and underscores. The tool sends a name it does not
// know, since a server may be newer than the tool; it sends nothing
// that would change the address.
func commandName(word string) bool {
	if word == "" {
		return false
	}
	for _, r := range word {
		if (r < 'a' || r > 'z') && r != '_' {
			return false
		}
	}
	return true
}

// objectOf reads the object of a command from its argument: the JSON
// itself, - for standard input, or @FILE for a file. It must be one
// JSON object; what its members are is for the server to judge.
func objectOf(arg string) ([]byte, error) {
	var object []byte
	var from string
	switch {
	case arg == "-":
		read, err := io.ReadAll(stdin)
		if err != nil {
			return nil, fmt.Errorf("reading the object from standard input: %v", err)
		}
		object, from = read, "standard input"
	case strings.HasPrefix(arg, "@"):
		read, err := os.ReadFile(arg[1:])
		if err != nil {
			return nil, fmt.Errorf("reading the object: %v", err)
		}
		object, from = read, arg[1:]
	default:
		object, from = []byte(arg), "the line"
	}
	object = bytes.TrimSpace(object)
	if !isObject(object) {
		return nil, fmt.Errorf("the object of a command is one JSON object, such as '{\"class\": \"Invoice\"}'; %s holds %s", from, beginningOfLine(object))
	}
	return object, nil
}

// withMember adds a member to the object of a command from a file:
// given as MEMBER=FILE, the member is the JSON the file holds. The
// bytes of the file go into the object as they are, so a definition
// reaches the server as its author wrote it.
func withMember(object []byte, given string) ([]byte, error) {
	member, file, ok := strings.Cut(given, "=")
	if !ok || member == "" || file == "" {
		return nil, fmt.Errorf("--from is given a member and a file, as --from definition=invoice.json; it was given %q", given)
	}
	value, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("reading the member %s: %v", member, err)
	}
	value = bytes.TrimSpace(value)
	if !json.Valid(value) {
		return nil, fmt.Errorf("%s does not hold JSON, so it cannot be the member %s", file, member)
	}
	names, _, err := membersInOrder(object)
	if err != nil {
		return nil, err
	}
	for _, n := range names {
		if n == member {
			return nil, fmt.Errorf("the object has a member %s, and --from gives it another", member)
		}
	}
	name, _ := json.Marshal(member)
	inside := bytes.TrimSpace(object[1 : len(object)-1])
	out := make([]byte, 0, len(object)+len(name)+len(value)+3)
	out = append(out, '{')
	if len(inside) > 0 {
		out = append(append(out, inside...), ',')
	}
	out = append(append(append(out, name...), ':'), value...)
	return append(out, '}'), nil
}

// isObject reports whether the bytes are one JSON object.
func isObject(b []byte) bool {
	b = bytes.TrimSpace(b)
	return len(b) > 0 && b[0] == '{' && json.Valid(b)
}

// isRefusal reports whether the bytes are a refusal of the command set:
// an object with a code under "error".
func isRefusal(b []byte) bool {
	var refusal struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	return isObject(b) && json.Unmarshal(b, &refusal) == nil && refusal.Error.Code != ""
}

// beginningOfLine shows the start of what was given where an object
// was expected.
func beginningOfLine(b []byte) string {
	if len(b) == 0 {
		return "nothing"
	}
	const most = 40
	if len(b) > most {
		return fmt.Sprintf("%q…", b[:most])
	}
	return fmt.Sprintf("%q", b)
}

// beginning shows the start of an answer that is not FadoDB's, each
// line set in.
func beginning(body []byte) string {
	const most = 300
	text := strings.TrimSpace(string(body))
	if len(text) > most {
		text = text[:most] + "…"
	}
	if text == "" {
		return ""
	}
	return "  " + strings.ReplaceAll(text, "\n", "\n  ") + "\n"
}

// addressOf is the address of a command at a server: the server given
// with --url, or by FADODB_URL, or the place fadodb serve listens at
// when it is told nothing. A key does not travel in the clear beyond
// this machine unless plain says that it may.
func addressOf(given, sent string, plain bool) (string, error) {
	from := "--url"
	if given == "" {
		given, from = environment("FADODB_URL"), "FADODB_URL"
	}
	if given == "" {
		given = "http://" + defaultListen
	}
	u, err := url.Parse(given)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", fmt.Errorf("%s is %q: a server is given as https://HOST or http://HOST:PORT", from, given)
	}
	if u.Scheme == "http" && !plain && !onThisMachine(u.Hostname()) {
		return "", fmt.Errorf("%s is beyond this machine and not https, so the key would travel in the clear\n  give an https address, or --plain-http when something between encrypts", given)
	}
	return strings.TrimRight(u.String(), "/") + "/v1/" + sent, nil
}

// onThisMachine reports whether a host is this machine.
func onThisMachine(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// keyOf is the key the tool sends: the one in the file given, or the
// one in FADODB_KEY.
func keyOf(file string) (string, error) {
	const how = "\n  a database administrator makes a key with the command create_key, and the first one on the machine that holds the database:\n    fadodb key new USER --db FILE"
	key, from := environment("FADODB_KEY"), "FADODB_KEY"
	if file != "" {
		read, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("reading the key: %v", err)
		}
		key, from = string(read), file
	}
	key = strings.TrimSpace(key)
	if key == "" {
		if file != "" {
			return "", fmt.Errorf("%s holds no key%s", file, how)
		}
		return "", fmt.Errorf("say whose key the command carries: set FADODB_KEY, or give --key-file FILE%s", how)
	}
	for _, r := range key {
		if r <= ' ' || r > '~' {
			return "", fmt.Errorf("%s does not hold a key: a key is one word, as fadodb key new printed it", from)
		}
	}
	return key, nil
}

// clientOf is the client the tool calls a server with. It follows no
// redirect: a command is sent to the server it was addressed to, and
// its key to nobody else.
func clientOf(ca string) (*http.Client, error) {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: dialWithin, KeepAlive: 15 * time.Second}).DialContext,
		TLSHandshakeTimeout:   dialWithin,
		ExpectContinueTimeout: time.Second,
		ForceAttemptHTTP2:     true,
	}
	if ca != "" {
		pem, err := os.ReadFile(ca)
		if err != nil {
			return nil, fmt.Errorf("reading the certificate: %v", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("%s holds no certificate in PEM", ca)
		}
		transport.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	}
	return &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, nil
}

// unwrapped is the cause of a failed request, without the method and
// the address the client puts before it.
func unwrapped(err error) error {
	var failed *url.Error
	if errors.As(err, &failed) {
		return failed.Err
	}
	return err
}

// printJSON prints an answer set out over lines. Only the space
// between its values changes: every number and every text is printed
// as the server wrote it.
func printJSON(w io.Writer, body []byte) error {
	var out bytes.Buffer
	if err := json.Indent(&out, bytes.TrimSpace(body), "", "  "); err != nil {
		out.Reset()
		out.Write(bytes.TrimSpace(body))
	}
	out.WriteByte('\n')
	_, err := w.Write(out.Bytes())
	return err
}

// follow prints the stream of changes (ADR-0058): each event on a line
// of its own, as the server wrote it. It ends with the stream: when
// the program is interrupted, which is how a caller closes a stream;
// when the server ends it and says why, which is a refusal; or when
// the connection breaks.
//
// A server answers a stream once it has opened it, so the tool says
// that the stream is open before it reads the first line: a script
// that waits for those words misses no event of what it does next.
func follow(ctx context.Context, name string, stream io.Reader, stdout, stderr io.Writer) int {
	fmt.Fprintf(stderr, "%s: %s\n", name, streamIsOpen)
	lines := bufio.NewReader(stream)
	kind := ""
	var data []byte
	for {
		line, err := lines.ReadBytes('\n')
		if err != nil {
			if ctx.Err() != nil {
				return exitDone
			}
			fmt.Fprintf(stderr, "%s: the stream broke off: %v\n  open it again with the id of the last event as \"after\", and nothing is lost\n", name, err)
			return exitNoAnswer
		}
		line = bytes.TrimRight(line, "\r\n")
		switch {
		case len(line) == 0:
			switch {
			case kind == "end":
				printJSON(stderr, data)
				return exitRefused
			case data != nil:
				if _, err := stdout.Write(append(data, '\n')); err != nil {
					fmt.Fprintf(stderr, "%s: an event could not be written: %v\n", name, err)
					return exitNoAnswer
				}
			}
			kind, data = "", nil
		case line[0] == ':':
			// A comment: the stream is open, or silent and alive.
		case bytes.HasPrefix(line, []byte("event:")):
			kind = string(bytes.TrimSpace(line[len("event:"):]))
		case bytes.HasPrefix(line, []byte("data:")):
			data = append(data, bytes.TrimPrefix(line[len("data:"):], []byte(" "))...)
		}
	}
}

// writeCSV prints the answer of query or of sql as CSV: a line of
// names, then a line for each document or row.
func writeCSV(w io.Writer, sent string, object, body []byte) error {
	out := csv.NewWriter(w)
	var err error
	if sent == "sql" {
		err = sqlCSV(out, body)
	} else {
		err = queryCSV(out, object, body)
	}
	if err != nil {
		return err
	}
	out.Flush()
	return out.Error()
}

// sqlCSV prints the columns and the rows of an answer of sql.
func sqlCSV(out *csv.Writer, body []byte) error {
	var answer struct {
		Columns []string            `json:"columns"`
		Rows    [][]json.RawMessage `json:"rows"`
	}
	if err := json.Unmarshal(body, &answer); err != nil {
		return fmt.Errorf("the answer of sql could not be read as columns and rows: %v", err)
	}
	if err := out.Write(answer.Columns); err != nil {
		return err
	}
	line := make([]string, len(answer.Columns))
	for _, row := range answer.Rows {
		line = line[:0]
		for _, value := range row {
			line = append(line, cell(value))
		}
		if err := out.Write(line); err != nil {
			return err
		}
	}
	return nil
}

// queryCSV prints the documents of an answer of query: the id and the
// state of each, then its variables. The variables are the ones the
// command asked for with "fields", in that order; when it asked for
// none, every variable the documents hold, in the order they are first
// met.
func queryCSV(out *csv.Writer, object, body []byte) error {
	var asked struct {
		Fields []string `json:"fields"`
	}
	if err := json.Unmarshal(object, &asked); err != nil {
		asked.Fields = nil
	}
	var answer struct {
		Documents []struct {
			ID     string          `json:"id"`
			State  string          `json:"state"`
			Values json.RawMessage `json:"values"`
		} `json:"documents"`
	}
	if err := json.Unmarshal(body, &answer); err != nil {
		return fmt.Errorf("the answer of query could not be read as documents: %v", err)
	}
	variables := asked.Fields
	values := make([]map[string]json.RawMessage, len(answer.Documents))
	seen := map[string]bool{}
	for i, d := range answer.Documents {
		names, held, err := membersInOrder(d.Values)
		if err != nil {
			return fmt.Errorf("the values of %s could not be read: %v", d.ID, err)
		}
		values[i] = held
		if len(asked.Fields) > 0 {
			continue
		}
		for _, n := range names {
			if !seen[n] {
				seen[n] = true
				variables = append(variables, n)
			}
		}
	}
	if err := out.Write(append([]string{"id", "state"}, variables...)); err != nil {
		return err
	}
	line := make([]string, 0, len(variables)+2)
	for i, d := range answer.Documents {
		line = append(line[:0], d.ID, d.State)
		for _, v := range variables {
			line = append(line, cell(values[i][v]))
		}
		if err := out.Write(line); err != nil {
			return err
		}
	}
	return nil
}

// membersInOrder reads a JSON object into its members, and their names
// in the order they are written.
func membersInOrder(object json.RawMessage) ([]string, map[string]json.RawMessage, error) {
	held := map[string]json.RawMessage{}
	if len(bytes.TrimSpace(object)) == 0 || string(bytes.TrimSpace(object)) == "null" {
		return nil, held, nil
	}
	dec := json.NewDecoder(bytes.NewReader(object))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, nil, errors.New("not an object")
	}
	var names []string
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, nil, err
		}
		name, _ := t.(string)
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, nil, err
		}
		names = append(names, name)
		held[name] = value
	}
	return names, held, nil
}

// cell is a value as a cell of CSV: a text as it is, a number as the
// server wrote it, true or false, nothing for a value that is not
// there, and a list or an object as its JSON.
func cell(value json.RawMessage) string {
	value = bytes.TrimSpace(value)
	switch {
	case len(value) == 0 || string(value) == "null":
		return ""
	case value[0] == '"':
		var text string
		if json.Unmarshal(value, &text) == nil {
			return text
		}
	case value[0] == '{' || value[0] == '[':
		var out bytes.Buffer
		if json.Compact(&out, value) == nil {
			return out.String()
		}
	}
	return string(value)
}

// execHelp is "fadodb exec help [COMMAND]": the commands, or the
// members of one, from the table the engine keeps (ADR-0055, item 10).
// It asks no server, so it says what this build of the tool knows.
func execHelp(args []string, stdout, stderr io.Writer) int {
	const name = "fadodb exec help"
	commands := fado.Commands()
	if len(args) == 0 {
		fmt.Fprint(stdout, "The commands a server takes, each sent as: fadodb exec COMMAND 'OBJECT'\n")
		width := 0
		for _, c := range commands {
			width = max(width, len(c.Name))
		}
		group := ""
		for _, c := range commands {
			if c.Group != group {
				group = c.Group
				fmt.Fprintf(stdout, "\n%s\n", group)
			}
			fmt.Fprintf(stdout, "  %-*s  %s  %s\n", width, c.Name, markWord(c.Mark), c.Does)
		}
		fmt.Fprint(stdout, "\nThe members of a command and of its answer: fadodb exec help COMMAND\n")
		return exitDone
	}
	if len(args) > 1 {
		fmt.Fprintf(stderr, "%s: one command at a time\n", name)
		return exitUsage
	}
	for _, c := range commands {
		if c.Name != args[0] {
			continue
		}
		fmt.Fprintf(stdout, "%s - %s\n\nIt %s. In a Go program it is the call %s.\n", c.Name, c.Does, markSentence(c.Mark), c.Call)
		var own, shared []fado.CommandField
		for _, f := range c.Fields {
			switch {
			case f.Refused:
			case f.Option:
				shared = append(shared, f)
			default:
				own = append(own, f)
			}
		}
		if len(own) == 0 && len(shared) == 0 {
			fmt.Fprint(stdout, "\nIts object has no member: {}\n")
		}
		printMembers(stdout, "\nThe members of its object:\n", own)
		printMembers(stdout, "\nAnd the members that mean the same in every command:\n", shared)
		switch {
		case c.Stream:
			fmt.Fprint(stdout, "\nIts answer is a stream: one event to a line, until it is interrupted.\n")
		case len(c.Answer) == 0:
			fmt.Fprint(stdout, "\nIts answer has no member: {}\n")
		default:
			fmt.Fprint(stdout, "\nThe members of its answer:\n")
			width := 0
			for _, m := range c.Answer {
				width = max(width, len(m.Name))
			}
			for _, m := range c.Answer {
				fmt.Fprintf(stdout, "  %-*s  %s\n", width, m.Name, m.Does)
			}
		}
		return exitDone
	}
	fmt.Fprintf(stderr, "%s: this build of the tool knows no command %q\n  the commands: fadodb exec help\n", name, args[0])
	return exitUsage
}

// printMembers prints the members of an object under a heading.
func printMembers(w io.Writer, heading string, fields []fado.CommandField) {
	if len(fields) == 0 {
		return
	}
	fmt.Fprint(w, heading)
	name, kind := 0, 0
	for _, f := range fields {
		name, kind = max(name, len(f.Name)), max(kind, len(f.Kind.String()))
	}
	for _, f := range fields {
		needed := "      "
		if f.Required {
			needed = "needed"
		}
		fmt.Fprintf(w, "  %-*s  %-*s  %s  %s\n", name, f.Name, kind, f.Kind.String(), needed, f.Does)
	}
}

// markWord says in a word what a command does to the database.
func markWord(m command.Mark) string {
	switch m {
	case command.Writes:
		return "writes  "
	case command.Destroys:
		return "destroys"
	}
	return "reads   "
}

// markSentence says the same in the middle of a sentence.
func markSentence(m command.Mark) string {
	switch m {
	case command.Writes:
		return "changes the database"
	case command.Destroys:
		return "removes something from the database"
	}
	return "reads, and writes nothing"
}
