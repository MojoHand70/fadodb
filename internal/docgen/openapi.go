package docgen

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/MojoHand70/fadodb/internal/command"
	"github.com/MojoHand70/fadodb/internal/server"
)

// The description of the commands for other languages (ADR-0055, item
// 10): one OpenAPI document, api/openapi.json, written from the table
// the engine turns a command into a call with. A client in any language
// is generated from it, and it cannot fall behind the engine: the test
// that holds the generated chapters holds it too.

// APIVersion is the version of the addresses the document describes:
// the v1 every address begins with.
const APIVersion = "1"

// The tags of the addresses that are not commands.
const (
	tagFiles  = "Files"
	tagServer = "The server"
)

// object is a JSON object under construction. encoding/json writes its
// members in the order of their names, so the document is the same on
// every run.
type object = map[string]any

// kindSchema is the JSON Schema of a member of a command's object.
func kindSchema(k command.Kind) object {
	switch k {
	case command.Text:
		return object{"type": "string"}
	case command.Integer:
		return object{"type": "integer"}
	case command.Flag:
		return object{"type": "boolean"}
	case command.Object, command.JSON:
		return object{"type": "object"}
	case command.Texts:
		return object{"type": "array", "items": object{"type": "string"}}
	case command.Objects, command.JSONs:
		return object{"type": "array", "items": object{"type": "object"}}
	case command.Scalars:
		return object{"type": "array", "items": object{"type": []string{"string", "number", "boolean", "null"}}}
	case command.Moment:
		return object{"type": []string{"string", "integer"}}
	}
	panic("docgen: a kind of member the description does not know: " + k.String())
}

// statusCodes are the codes the engine, the command set and the door
// declare, by the HTTP status the door answers each with. The status of
// every refusal the table does not name is other.
func statusCodes(root string) (map[int][]string, error) {
	byStatus := map[int][]string{}
	seen := map[string]bool{}
	for _, file := range []string{"store/errors.go", "command/object.go", "server/codes.go"} {
		codes, err := codesOf(filepath.Join(root, filepath.FromSlash(file)), "Code")
		if err != nil {
			return nil, err
		}
		for _, c := range codes {
			status := server.Status(c.value)
			if seen[c.value] || status == other {
				continue
			}
			seen[c.value] = true
			byStatus[status] = append(byStatus[status], c.value)
		}
	}
	if len(byStatus) < 8 {
		return nil, fmt.Errorf("docgen: only %d statuses found for the codes under %s", len(byStatus), root)
	}
	return byStatus, nil
}

// OpenAPI is the description of the commands as an OpenAPI 3.1 document.
// root is the directory that holds the engine's packages, internal: the
// statuses of the HTTP door are worked out for the codes declared there.
func OpenAPI(root string) (string, error) {
	byStatus, err := statusCodes(root)
	if err != nil {
		return "", err
	}
	statuses := make([]int, 0, len(byStatus))
	for status := range byStatus {
		statuses = append(statuses, status)
	}
	sort.Ints(statuses)
	var refused strings.Builder
	refused.WriteString("The command was not carried out. The body holds the refusal under `error`, with its code; a caller reads the code, and the status is worked out from it:\n\n| Status | Codes |\n|---|---|\n")
	for _, status := range statuses {
		fmt.Fprintf(&refused, "| %d | %s |\n", status, strings.Join(code(byStatus[status]), ", "))
	}
	fmt.Fprintf(&refused, "| %d | every other refusal |\n", other)

	marks := map[command.Mark]string{command.Reads: "reads", command.Writes: "writes", command.Destroys: "destroys"}
	paths := object{}
	for _, c := range command.Commands() {
		properties, required := object{}, []string{}
		for _, f := range c.Fields {
			if f.Refused {
				continue
			}
			member := kindSchema(f.Kind)
			member["description"] = f.Does
			properties[f.Name] = member
			if f.Required {
				required = append(required, f.Name)
			}
		}
		body := object{"type": "object", "additionalProperties": false, "properties": properties}
		if len(required) > 0 {
			body["required"] = required
		}
		does := "It " + marks[c.Mark] + ". In Go it is the call `" + c.Call + "`."
		if c.Users {
			does += " It is offered only by a database that keeps its own users, groups and keys; any other answers `unknown_command`."
		}
		op := object{
			"operationId": c.Name,
			"tags":        []string{c.Group},
			"summary":     c.Does,
			"description": does,
			"x-fado-mark": marks[c.Mark],
			"x-fado-call": c.Call,
			"requestBody": object{"required": true, "content": object{"application/json": object{"schema": body}}},
			"responses":   object{"default": object{"$ref": "#/components/responses/Refused"}},
		}
		ran := object{"description": "The command ran."}
		switch {
		case c.Stream:
			op["x-fado-stream"] = true
			ran["description"] = "The stream is open. The reply does not end: it carries one entry for each event the caller may read of, in the form of server-sent events. An entry is three lines and an empty one: `id:` with the id of the event, `event: change`, and `data:` with the event as one JSON object on one line. A line that begins with a colon is a comment. When the server ends the stream, the last entry is `event: end`, whose data is a refusal."
			ran["content"] = object{"text/event-stream": object{"schema": object{"type": "string"}}}
		default:
			answer, members := object{}, []string{}
			for _, m := range c.Answer {
				answer[m.Name] = object{"description": m.Does}
				members = append(members, m.Name)
			}
			schema := object{"type": "object", "properties": answer}
			if len(members) > 0 {
				schema["required"] = members
			}
			ran["content"] = object{"application/json": object{"schema": schema}}
		}
		op["responses"].(object)["200"] = ran
		paths["/v1/"+c.Name] = object{"post": op}
	}

	// The files, beside the commands (ADR-0055, item 9).
	bytesOf := object{"*/*": object{"schema": object{"type": "string", "format": "binary"}}}
	query := func(name, does string, required bool, schema object) object {
		return object{"name": name, "in": "query", "required": required, "description": does, "schema": schema}
	}
	text := object{"type": "string"}
	fromDefault := object{"default": object{"$ref": "#/components/responses/Refused"}}
	with := func(responses object) object {
		for status, r := range fromDefault {
			responses[status] = r
		}
		return responses
	}
	paths["/v1/blobs"] = object{"post": object{
		"operationId": "put_blob",
		"tags":        []string{tagFiles},
		"summary":     "Keeps the bytes of a file and answers its facts.",
		"description": "The body is the bytes themselves, and the `Content-Type` of the request is their kind. The facts answered are a value like any other: a command sets them on a document.",
		"parameters":  []object{query("name", "the name of the file", false, text)},
		"requestBody": object{"required": true, "content": bytesOf},
		"responses": with(object{"200": object{"description": "The bytes are kept.", "content": object{"application/json": object{"schema": object{
			"type": "object", "required": []string{"file"},
			"properties": object{"file": object{"type": "object", "description": "the facts of the file: its hash, name, media_type and size"}},
		}}}}}),
	}}
	paths["/v1/file"] = object{"get": object{
		"operationId": "open_file",
		"tags":        []string{tagFiles},
		"summary":     "Streams a file a document holds, under the rules of the document.",
		"description": "A caller who may not read the document is answered `unknown_document`, and one who may not read the variable that holds the file `unknown_blob`. The hash is the tag of the bytes, sent as `ETag`.",
		"parameters": []object{
			query("class", "the name of the class", true, text),
			query("id", "the id of the document", true, text),
			query("hash", "the hash of the file, as its facts give it", true, text),
			query("version", "the version of the document to read the file from; the current one when left out", false, object{"type": "integer"}),
			query("for", "the id of whoever the caller acts for", false, text),
		},
		"responses": with(object{
			"200": object{"description": "The bytes of the file.", "content": bytesOf},
			"304": object{"description": "The caller holds these bytes already: it sent their tag in `If-None-Match`."},
		}),
	}}
	paths["/v1/blob"] = object{"get": object{
		"operationId": "open_blob",
		"tags":        []string{tagFiles},
		"summary":     "Streams bytes by their hash alone, for a content administrator.",
		"parameters":  []object{query("hash", "the hash of the bytes", true, text)},
		"responses": with(object{
			"200": object{"description": "The bytes.", "content": bytesOf},
			"304": object{"description": "The caller holds these bytes already: it sent their tag in `If-None-Match`."},
		}),
	}}

	// What the server itself answers (ADR-0059, item 9). A program that
	// mounts the door answers its own.
	paths["/v1/health"] = object{"get": object{
		"operationId": "health",
		"tags":        []string{tagServer},
		"summary":     "Says that the server answers and can read its database.",
		"description": "It asks for no key and says nothing of what the database holds. It is answered by `fadodb serve`.",
		"security":    []object{},
		"responses": object{
			"200": object{"description": "The server answers.", "content": object{"application/json": object{"schema": object{"$ref": "#/components/schemas/Health"}}}},
			"503": object{"description": "The server cannot read its database.", "content": object{"application/json": object{"schema": object{"$ref": "#/components/schemas/Health"}}}},
		},
	}}
	paths["/v1/metrics"] = object{"get": object{
		"operationId": "metrics",
		"tags":        []string{tagServer},
		"summary":     "Answers the counts of the server, for a database administrator.",
		"description": "The requests by command and status, how long they took, the streams that are open, the queue of actions and the sizes of the files, in the text form Prometheus reads. It is answered by `fadodb serve`.",
		"responses":   with(object{"200": object{"description": "The counts.", "content": object{"text/plain": object{"schema": object{"type": "string"}}}}}),
	}}

	tags := []object{}
	for _, group := range []string{command.Documents, command.Definitions, command.Operations, command.Users} {
		tags = append(tags, object{"name": group})
	}
	tags = append(tags,
		object{"name": tagFiles, "description": "The bytes of a file are not JSON, so they travel beside the commands."},
		object{"name": tagServer, "description": "What `fadodb serve` answers besides the commands."})

	doc := object{
		"openapi": "3.1.0",
		"info": object{
			"title":   "FadoDB",
			"summary": "The command set of a FadoDB database over HTTP.",
			"description": "Every call that reads or changes a database is a command: a name and one JSON object, answered by one JSON object. " +
				"A command is sent as `POST /v1/<command>` with its object as the body. A command that reads is a `POST` too, and the id of a document travels in the JSON and never in the address.\n\n" +
				"Who sends a request is never in the command. A database that keeps its own users is sent a key of a user, as `Authorization: Bearer fado_…`. " +
				"A member that is `null` is a member left out, and a member a command does not have is refused.\n\n" +
				"This document is written from the engine's own table of the commands. The manual's chapter on the commands is written from the same table.",
			"version": APIVersion,
		},
		"servers":  []object{{"url": "http://127.0.0.1:8080", "description": "`fadodb serve`, told nothing: this machine only"}},
		"security": []object{{"key": []string{}}},
		"tags":     tags,
		"paths":    paths,
		"components": object{
			"securitySchemes": object{"key": object{
				"type": "http", "scheme": "bearer",
				"description": "A key of a user of the database, made by `create_key` or by `fadodb key new`. A program that mounts the door for callers of its own says itself who sends a request.",
			}},
			"responses": object{"Refused": object{
				"description": refused.String(),
				"content":     object{"application/json": object{"schema": object{"$ref": "#/components/schemas/Refusal"}}},
			}},
			"schemas": object{
				"Refusal": object{
					"type": "object", "required": []string{"error"},
					"properties": object{
						"error":       object{"$ref": "#/components/schemas/Error"},
						"diagnostics": object{"type": "array", "items": object{"type": "object"}, "description": "every finding of a definition that was refused, beside the code `bad_definition`"},
					},
				},
				"Error": object{
					"type": "object", "required": []string{"code", "message"},
					"properties": object{
						"code":     object{"type": "string", "description": "what a caller branches on"},
						"message":  object{"type": "string", "description": "what happened, in words"},
						"hint":     object{"type": "string", "description": "what to do about it, where the engine knows"},
						"path":     object{"type": "string", "description": "the place: in the object of the command, or in the class whose rule refused"},
						"variable": object{"type": "string", "description": "the variable the refusal is about"},
						"pos":      object{"type": "integer", "description": "the place inside a formula, where the refusal is about one"},
					},
				},
				"Health": object{
					"type": "object", "required": []string{"ok", "version"},
					"properties": object{
						"ok":      object{"type": "boolean"},
						"version": object{"type": "string", "description": "the version of the server"},
					},
				},
			},
		},
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return "", err
	}
	return out.String(), nil
}
