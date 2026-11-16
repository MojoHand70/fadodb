package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/MojoHand70/fadodb/internal/mcp"
)

// "fadodb mcp" is the door for agents (ADR-0064): a program that an
// agent's program starts and talks to over standard input and output,
// in the Model Context Protocol. It offers each command of the command
// set as a tool and sends each call to a server with the key of a user,
// as "fadodb exec" does. So it holds no database, and what an agent
// does through it is judged by the rules of the database and written to
// its log under the user whose key the door was given.

const mcpUsage = `fadodb mcp - the door for agents

Usage:
  fadodb mcp [flags]

Offers the commands of a server as tools to an agent, in the Model
Context Protocol. It is started by the agent's program, which talks to
it over standard input and output; started at a terminal it waits for a
message on each line. Each tool is one command: its name, its object
and its answer are the command's, and it is sent to the server as the
user whose key the door is given.

An agent's program is told to start it, with the server and the key:

  {"mcpServers": {"acme": {
     "command": "fadodb", "args": ["mcp"],
     "env": {"FADODB_URL": "https://fado.acme.example",
             "FADODB_KEY": "fado_..."}}}}

  --read-only      offer only the tools that read
  --tools NAMES    offer only these commands, as create,get,transition;
                   left out, every command the door offers
` + serverUsage + `
Two commands are no tool. subscribe is a stream, and a tool is one
question and one answer. create_key answers the secret of a key, which
would stay in the conversation of the agent: a key is made at a
terminal, with fadodb key new or fadodb exec create_key.

Nothing is written to standard output but the answers to the agent's
program. What goes wrong with the door itself is said on standard
error. It ends with 0 when the agent's program closes its input, with
2 when it could not start, and with 3 when an answer could not be
written to the agent's program.
`

// runMCP is "fadodb mcp".
func runMCP(ctx context.Context, args []string, in io.Reader, stdout, stderr io.Writer) int {
	const name = "fadodb mcp"
	fs := flags(name)
	to := serverFlags(fs)
	readOnly := fs.Bool("read-only", false, "")
	tools := fs.String("tools", "", "")
	rest, err := parse(fs, args)
	if err != nil {
		return asked(name, mcpUsage, err, stderr)
	}
	if len(rest) > 0 {
		return asked(name, mcpUsage, fmt.Errorf("%q: the door takes flags and no other argument", rest[0]), stderr)
	}
	var only []string
	for _, tool := range strings.Split(*tools, ",") {
		if tool = strings.TrimSpace(tool); tool != "" {
			only = append(only, tool)
		}
	}
	c, ok := to.caller(name, stderr)
	if !ok {
		return exitUsage
	}
	defer c.close()
	door, err := mcp.New(c.ask, mcp.Options{
		Version:  version,
		Database: strings.TrimSuffix(c.base, "/v1/"),
		ReadOnly: *readOnly,
		Only:     only,
	})
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n  the commands: fadodb exec help\n", name, err)
		return exitUsage
	}
	if err := door.Serve(ctx, in, stdout); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(stderr, "%s: an answer could not be written to the agent's program: %v\n", name, err)
		return exitNoAnswer
	}
	return exitDone
}
