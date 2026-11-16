package command

// The object of a command as a JSON Schema. The description for other
// languages (api/openapi.json) and the door for agents (ADR-0064) both
// write it from here, so that the two cannot say different things of
// one command.

// Schema is the JSON Schema of a member of this kind.
func (k Kind) Schema() map[string]any {
	switch k {
	case Text:
		return map[string]any{"type": "string"}
	case Integer:
		return map[string]any{"type": "integer"}
	case Flag:
		return map[string]any{"type": "boolean"}
	case Object, JSON:
		return map[string]any{"type": "object"}
	case Texts:
		return map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	case Objects, JSONs:
		return map[string]any{"type": "array", "items": map[string]any{"type": "object"}}
	case Scalars:
		return map[string]any{"type": "array", "items": map[string]any{"type": []string{"string", "number", "boolean", "null"}}}
	case Moment:
		return map[string]any{"type": []string{"string", "integer"}}
	}
	panic("command: a kind of member with no schema: " + k.String())
}

// Schema is the JSON Schema of the object the command takes: each
// member with what it is, the members it needs, and none beyond them,
// since a member a command does not know is refused (ADR-0055). A
// member the command knows only to refuse it is left out.
func (c *Command) Schema() map[string]any {
	properties, required := map[string]any{}, []string{}
	for _, f := range c.Fields {
		if f.Refused {
			continue
		}
		member := f.Kind.Schema()
		member["description"] = f.Does
		properties[f.Name] = member
		if f.Required {
			required = append(required, f.Name)
		}
	}
	schema := map[string]any{"type": "object", "additionalProperties": false, "properties": properties}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}
