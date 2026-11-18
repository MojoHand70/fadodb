package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/MojoHand70/fadodb/fado/httpdoor"
	"github.com/MojoHand70/fadodb/internal/model"
	"github.com/MojoHand70/fadodb/internal/server"
	"github.com/MojoHand70/fadodb/internal/store"
)

// settings are what the operator tells the server in one JSON file,
// given with --settings (ADR-0059, item 4): the hosts the actions of the
// database may call, and how much the server takes on. They are the
// operator's: none is kept in the database, and no command changes one
// (item 11). A secret is never among them (item 5).
//
//	{
//	  "outbound": {
//	    "hosts": ["hooks.acme.example"],
//	    "connections": {
//	      "slack": ["slack.com"],
//	      "books": ["books.acme.internal:8443"]
//	    }
//	  },
//	  "limits": {
//	    "request_bytes": 16777216,
//	    "command_seconds": 30,
//	    "commands": 64,
//	    "streams": 256
//	  },
//	  "inbound": {
//	    "message_bytes": 1048576,
//	    "messages": 16
//	  },
//	  "url": "https://fado.acme.example",
//	  "sessions": {
//	    "idle_hours": 8,
//	    "days": 30
//	  }
//	}
type settings struct {
	// hosts and connections are the lists of package fado/outbound: the
	// hosts a request without a connection may go to, and for each
	// connection the hosts its requests, and so its secrets, may go to.
	hosts       []string
	connections map[string][]string
	// limits are the limits of the door; a limit the file does not give
	// is zero, which is the door's own.
	limits httpdoor.Limits
	// inbound says that the server takes messages from outside at the
	// inlets of its database (ADR-0065, item 8): the file has the
	// member inbound, whatever is in it. The limits of the messages are
	// among the limits of the door.
	inbound bool
	// url is the address people and clients reach the server at, from
	// which its pages, its sessions and their cookie are named (ADR-0071,
	// item 12); empty, the pages answer this machine alone. sessions is
	// how long a session holds, the usual 8 hours unused and 30 days at
	// most, which the file may shorten.
	url      string
	sessions store.SessionLengths
}

// The names a settings file may hold, at each place.
var (
	settingsMembers = []string{"outbound", "limits", "inbound", "url", "sessions"}
	sessionMembers  = []string{"idle_hours", "days"}
	outboundMembers = []string{"hosts", "connections"}
	limitMembers    = []string{"request_bytes", "command_seconds", "commands", "streams"}
	inboundMembers  = []string{"message_bytes", "messages"}
)

// longestCommand is the longest a command may be given to run.
const longestCommand = 24 * time.Hour

// calls reports whether the settings let an action call anything.
func (s *settings) calls() bool { return len(s.hosts) > 0 || len(s.connections) > 0 }

// readSettings reads the file of settings at the path.
func readSettings(path string) (*settings, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		var pe *os.PathError
		if errors.As(err, &pe) {
			err = pe.Err
		}
		return nil, fmt.Errorf("the file cannot be read: %v", err)
	}
	return parseSettings(data)
}

// parseSettings reads settings as written. Like a command, the file is
// refused for a member it should not have (ADR-0059, item 4), and for a
// member written twice, of which one would be dropped unseen.
func parseSettings(data []byte) (*settings, error) {
	var whole any
	if err := json.Unmarshal(data, &whole); err != nil {
		var syntax *json.SyntaxError
		if errors.As(err, &syntax) {
			return nil, fmt.Errorf("it is not JSON, at line %d: %v", 1+bytes.Count(data[:min(int(syntax.Offset), len(data))], []byte("\n")), err)
		}
		return nil, fmt.Errorf("it is not JSON: %v", err)
	}
	top, err := members(data, "the file", settingsMembers)
	if err != nil {
		return nil, err
	}
	s := &settings{}
	if raw, given := top["outbound"]; given {
		out, err := members(raw, "outbound", outboundMembers)
		if err != nil {
			return nil, err
		}
		if raw, given := out["hosts"]; given {
			if s.hosts, err = hostList(raw, "outbound.hosts"); err != nil {
				return nil, err
			}
		}
		if raw, given := out["connections"]; given {
			if s.connections, err = connections(raw); err != nil {
				return nil, err
			}
		}
	}
	if raw, given := top["limits"]; given {
		lim, err := members(raw, "limits", limitMembers)
		if err != nil {
			return nil, err
		}
		if raw, given := lim["request_bytes"]; given {
			if s.limits.RequestBytes, err = countOf(raw, "limits.request_bytes"); err != nil {
				return nil, err
			}
		}
		if raw, given := lim["command_seconds"]; given {
			if s.limits.CommandTime, err = secondsOf(raw, "limits.command_seconds"); err != nil {
				return nil, err
			}
		}
		if raw, given := lim["commands"]; given {
			n, err := countOf(raw, "limits.commands")
			if err != nil {
				return nil, err
			}
			s.limits.Commands = int(n)
		}
		if raw, given := lim["streams"]; given {
			n, err := countOf(raw, "limits.streams")
			if err != nil {
				return nil, err
			}
			s.limits.Streams = int(n)
		}
	}
	// The member alone turns the messages on: an empty object takes
	// them with the usual limits.
	if raw, given := top["inbound"]; given {
		in, err := members(raw, "inbound", inboundMembers)
		if err != nil {
			return nil, err
		}
		s.inbound = true
		if raw, given := in["message_bytes"]; given {
			if s.limits.MessageBytes, err = countOf(raw, "inbound.message_bytes"); err != nil {
				return nil, err
			}
		}
		if raw, given := in["messages"]; given {
			n, err := countOf(raw, "inbound.messages")
			if err != nil {
				return nil, err
			}
			s.limits.Messages = int(n)
		}
	}
	if raw, given := top["url"]; given {
		if err := json.Unmarshal(raw, &s.url); err != nil || s.url == "" {
			return nil, fmt.Errorf("url is the address people reach the server at, as text such as \"https://fado.acme.example\", and here it is %s", shown(raw))
		}
		if _, err := server.NewPages(nil, server.PagesOptions{URL: s.url}); err != nil {
			return nil, fmt.Errorf("url is %s: %v", shown(raw), err)
		}
	}
	if raw, given := top["sessions"]; given {
		ses, err := members(raw, "sessions", sessionMembers)
		if err != nil {
			return nil, err
		}
		if raw, given := ses["idle_hours"]; given {
			n, err := countOf(raw, "sessions.idle_hours")
			if err != nil || n > 8 {
				return nil, fmt.Errorf("sessions.idle_hours is a whole number of hours from 1 to 8, and here it is %s", shown(raw))
			}
			s.sessions.Idle = time.Duration(n) * time.Hour
		}
		if raw, given := ses["days"]; given {
			n, err := countOf(raw, "sessions.days")
			if err != nil || n > 30 {
				return nil, fmt.Errorf("sessions.days is a whole number of days from 1 to 30, and here it is %s", shown(raw))
			}
			s.sessions.Max = time.Duration(n) * 24 * time.Hour
		}
	}
	return s, nil
}

// members reads a JSON object into its members. It refuses what is no
// object, a member written twice and, when known is given, a member that
// is not one of them.
func members(raw []byte, where string, known []string) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, fmt.Errorf("%s is an object, written in braces, and here it is %s", where, shown(raw))
	}
	out := map[string]json.RawMessage{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("%s does not read: %v", where, err)
		}
		name, _ := tok.(string)
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, fmt.Errorf("%s does not read: %v", where, err)
		}
		if _, twice := out[name]; twice {
			return nil, fmt.Errorf("%s has %q twice", where, name)
		}
		if known != nil && !contains(known, name) {
			return nil, fmt.Errorf("%s has %q, which is no setting: it may have %s", where, name, strings.Join(known, ", "))
		}
		out[name] = value
	}
	if _, err := dec.Token(); err != nil {
		return nil, fmt.Errorf("%s does not read: %v", where, err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("something follows %s", where)
	}
	return out, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// shown is a value of the file as a refusal shows it: its beginning.
func shown(raw []byte) string {
	text := strings.Join(strings.Fields(string(raw)), " ")
	if len(text) > 40 {
		text = text[:40] + "…"
	}
	return text
}

// hostList reads a list of hosts, each as package fado/outbound writes
// one. What a host may look like is that package's to say.
func hostList(raw []byte, where string) ([]string, error) {
	var list []string
	if err := json.Unmarshal(raw, &list); err != nil || list == nil {
		return nil, fmt.Errorf(`%s is a list of hosts, such as ["hooks.acme.example"], and here it is %s`, where, shown(raw))
	}
	return list, nil
}

// connections reads the connections, each with its hosts. A connection
// has a name a definition can give it, and no two differ in case only:
// the secrets of a connection are kept under its name without regard to
// case, so two such connections would share them.
func connections(raw []byte) (map[string][]string, error) {
	named, err := members(raw, "outbound.connections", nil)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(named))
	for name := range named {
		names = append(names, name)
	}
	sort.Strings(names)
	out, seen := make(map[string][]string, len(named)), map[string]string{}
	for _, name := range names {
		if reason := model.NameReason(name); reason != "" {
			return nil, fmt.Errorf("outbound.connections has %q, which no definition can name: %s", name, reason)
		}
		if other, twice := seen[strings.ToUpper(name)]; twice {
			return nil, fmt.Errorf("outbound.connections has %s and %s, which differ in case only", other, name)
		}
		seen[strings.ToUpper(name)] = name
		if out[name], err = hostList(named[name], "outbound.connections."+name); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// countOf reads a limit that is a whole number above zero.
func countOf(raw []byte, where string) (int64, error) {
	n, err := strconv.ParseInt(string(bytes.TrimSpace(raw)), 10, 64)
	if err != nil || n < 1 || n > 1<<40 {
		return 0, fmt.Errorf("%s is a whole number above zero, and here it is %s; leave it out for the usual", where, shown(raw))
	}
	return n, nil
}

// secondsOf reads a limit of time in seconds, which may have a
// fraction.
func secondsOf(raw []byte, where string) (time.Duration, error) {
	secs, err := strconv.ParseFloat(string(bytes.TrimSpace(raw)), 64)
	if err != nil || secs <= 0 || secs > longestCommand.Seconds() {
		return 0, fmt.Errorf("%s is a number of seconds above zero and at most %d, and here it is %s; leave it out for the usual", where, int(longestCommand.Seconds()), shown(raw))
	}
	d := time.Duration(secs * float64(time.Second))
	if d < time.Millisecond {
		return 0, fmt.Errorf("%s is at least a millisecond, and here it is %s", where, shown(raw))
	}
	return d, nil
}
