package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"

	"github.com/MojoHand70/fadodb/internal/command"
)

// "fadodb file put" and "fadodb file get" (ADR-0062, item 10): a file
// on a document, from a terminal. The bytes of a file are no JSON, so
// they travel beside the commands, at addresses of their own (ADR-0055,
// item 9), and "fadodb exec" does not send them. These two words do,
// with the same key and under the same rules: a file is read through
// the document that holds it, by a caller who may read that variable.

const fileUsage = `fadodb file - put a file on a document, or read one

Usage:
  fadodb file put FILE --class CLASS --id ID --variable VARIABLE [flags]
  fadodb file put FILE [flags]
  fadodb file get --class CLASS --id ID --variable VARIABLE --out FILE [flags]
  fadodb file get --class CLASS --id ID --hash HASH --out FILE [flags]

"put" sends the bytes of FILE to the server and writes the file into a
variable of a document, as the user whose key it is given; it prints
the document after the change. Without a document it only sends the
bytes, and prints the facts of the file: the value to give a variable
in a command of your own, such as create.

"get" reads the file a variable of a document holds, and writes it to
--out, or to standard output when --out is left out.

  fadodb file put scan.pdf --class Invoice --id FV/2026/11/0042 --variable scan
  fadodb file get --class Invoice --id FV/2026/11/0042 --variable scan --out scan.pdf

  --class CLASS    the class of the document
  --id ID          the id of the document
  --variable NAME  the variable that holds the file
  --name NAME      put: the name the file is kept under; the name of
                   FILE when left out
  --type TYPE      put: the kind of file, such as application/pdf; the
                   server reads it from the first bytes when left out
  --hash HASH      get: the file by its hash, in place of --variable:
                   one of a list of files, or one an earlier version of
                   the document held
  --version N      get, with --hash: the version of the document that
                   held the file
  --out FILE       get: where the file is written
` + serverUsage + `
It ends as fadodb exec does: 0 done, 1 the server refused, 2 the line
could not be read or the variable holds no file, 3 no answer came
through.
`

// runFile is "fadodb file put …" and "fadodb file get …".
func runFile(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	const name = "fadodb file"
	fs := flags(name)
	to := serverFlags(fs)
	class := fs.String("class", "", "")
	id := fs.String("id", "", "")
	variable := fs.String("variable", "", "")
	as := fs.String("name", "", "")
	kind := fs.String("type", "", "")
	hash := fs.String("hash", "", "")
	version := fs.Int("version", 0, "")
	out := fs.String("out", "", "")
	rest, err := parse(fs, args)
	if err != nil {
		return asked(name, fileUsage, err, stderr)
	}
	wrong := func(why string) int { return asked(name, fileUsage, errors.New(why), stderr) }
	if len(rest) == 0 || (rest[0] != "put" && rest[0] != "get") {
		return wrong("say put or get")
	}
	word, rest := rest[0], rest[1:]
	named := 0
	for _, given := range []string{*class, *id, *variable} {
		if given != "" {
			named++
		}
	}
	switch {
	case word == "put" && len(rest) != 1:
		return wrong("put takes one file")
	case word == "put" && named != 0 && named != 3:
		return wrong("a document is named with --class, --id and --variable, all three")
	case word == "put" && (*hash != "" || *version != 0 || *out != ""):
		return wrong("--hash, --version and --out are for get")
	case word == "get" && len(rest) != 0:
		return wrong("get takes no file: --out says where the file is written")
	case word == "get" && (*class == "" || *id == ""):
		return wrong("say which document holds the file, with --class and --id")
	case word == "get" && (*variable == "") == (*hash == ""):
		return wrong("say which file of the document, with --variable or with --hash")
	case word == "get" && *version != 0 && *hash == "":
		return wrong("--version goes with --hash: an earlier version of a document is read by the hash of its file")
	case word == "get" && (*as != "" || *kind != ""):
		return wrong("--name and --type are for put")
	}
	c, ok := to.caller(name+" "+word, stderr)
	if !ok {
		return exitUsage
	}
	defer c.close()
	if word == "put" {
		return c.put(ctx, rest[0], *as, *kind, *class, *id, *variable, stdout)
	}
	return c.get(ctx, *class, *id, *variable, *hash, *version, *out, stdout)
}

// put sends the bytes of a file and, when a document is named, writes
// the file into its variable.
func (c *caller) put(ctx context.Context, path, name, kind, class, id, variable string, stdout io.Writer) int {
	file, err := os.Open(path)
	if err != nil {
		fmt.Fprintf(c.stderr, "%s: %v\n", c.name, err)
		return exitUsage
	}
	defer file.Close()
	if info, err := file.Stat(); err != nil || info.IsDir() {
		fmt.Fprintf(c.stderr, "%s: %s is not a file\n", c.name, path)
		return exitUsage
	}
	if name == "" {
		name = filepath.Base(path)
	}
	resp, code := c.send(ctx, http.MethodPost, "blobs?"+url.Values{"name": {name}}.Encode(), file, kind, "the answer to the file")
	if resp == nil {
		return code
	}
	defer resp.Body.Close()
	body, code := c.answer(ctx, resp, "the answer to the file")
	if code != exitDone {
		return code
	}
	if class == "" {
		return c.written(printJSON(stdout, body))
	}
	var put struct {
		File json.RawMessage `json:"file"`
	}
	if err := json.Unmarshal(body, &put); err != nil || len(put.File) == 0 {
		fmt.Fprintf(c.stderr, "%s: the server took the bytes and answered no file: %s\n", c.name, beginning(body))
		return exitNoAnswer
	}
	object, _ := command.Marshal(map[string]any{"class": class, "id": id, "values": map[string]any{variable: put.File}})
	body, code = c.command(ctx, "set", object)
	if code != exitDone {
		return code
	}
	return c.written(printJSON(stdout, body))
}

// get reads a file through the document that holds it, and writes it
// where it is told.
func (c *caller) get(ctx context.Context, class, id, variable, hash string, version int, out string, stdout io.Writer) int {
	if hash == "" {
		object, _ := command.Marshal(map[string]any{"class": class, "id": id})
		body, code := c.command(ctx, "get", object)
		if code != exitDone {
			return code
		}
		var read struct {
			Document struct {
				Values map[string]json.RawMessage `json:"values"`
			} `json:"document"`
		}
		var held struct {
			Hash string `json:"hash"`
		}
		_ = json.Unmarshal(body, &read)
		if json.Unmarshal(read.Document.Values[variable], &held) != nil || held.Hash == "" {
			fmt.Fprintf(c.stderr, "%s: the variable %s of %s %s holds no file\n  a file of a list, or of an earlier version, is read by its hash: --hash HASH\n", c.name, variable, class, id)
			return exitUsage
		}
		hash = held.Hash
	}
	asked := url.Values{"class": {class}, "id": {id}, "hash": {hash}}
	if version != 0 {
		asked.Set("version", strconv.Itoa(version))
	}
	resp, code := c.send(ctx, http.MethodGet, "file?"+asked.Encode(), nil, "", "the file")
	if resp == nil {
		return code
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// A refusal, which answer prints; or not a FadoDB server.
		_, code := c.answer(ctx, resp, "the file")
		if code == exitDone {
			return c.notFadoDB(resp, nil)
		}
		return code
	}
	if out == "" {
		return c.copied(ctx, stdout, resp.Body)
	}
	file, err := os.Create(out)
	if err != nil {
		return c.written(err)
	}
	code = c.copied(ctx, file, resp.Body)
	if err := file.Close(); err != nil && code == exitDone {
		code = c.written(err)
	}
	if code != exitDone {
		// What is left is no file: half of one is taken away.
		os.Remove(out)
	}
	return code
}

// copied writes the bytes of a file as they come.
func (c *caller) copied(ctx context.Context, to io.Writer, from io.Reader) int {
	if _, err := io.Copy(to, from); err != nil {
		if ctx.Err() != nil {
			return c.interrupted("the whole file")
		}
		fmt.Fprintf(c.stderr, "%s: the file broke off: %v\n", c.name, err)
		return exitNoAnswer
	}
	return exitDone
}
