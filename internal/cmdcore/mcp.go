package cmdcore

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/mcp"
)

// Tool is one command as an MCP tool. CallStructured returns the text for the content block and the structured value
// (nil when the result is not a JSON object). An error's text is "error: MSG" plus "\nhint: HINT" when there is a hint.
//
// internal/mcp.Tool has no structured result yet; MCP converts a Tool to one that returns the text only.
type Tool struct {
	Name           string
	Description    string
	Schema         map[string]any
	CallStructured func(ctx context.Context, args json.RawMessage) (text string, structured any, err error)
}

// MCP converts t to an mcp.Tool whose CallStructured returns the text and the result object (the server sends the
// object as structuredContent only when 2025-06-18 was negotiated).
func (t Tool) MCP() mcp.Tool {
	return mcp.Tool{
		Name:           t.Name,
		Description:    t.Description,
		Schema:         t.Schema,
		CallStructured: t.CallStructured,
	}
}

// Schema builds the JSON Schema of an MCP input from the params type P.
func Schema[P any]() map[string]any {
	return schemaOf(paramFields(reflect.TypeOf((*P)(nil)).Elem()))
}

func schemaOf(fields []field) map[string]any {
	props := map[string]any{}
	required := []string{}
	for _, f := range fields {
		if f.json == "" || f.json == "-" {
			continue
		}
		typ, _ := schemaType(f.typ)
		pr := map[string]any{"type": typ}
		if typ == "array" {
			pr["items"] = map[string]any{"type": "string"}
		}
		d := f.desc
		if f.mcpDesc != "" {
			d = f.mcpDesc
		}
		if d != "" {
			pr["description"] = d
		}
		if len(f.enum) > 0 {
			pr["enum"] = append([]string(nil), f.enum...)
		}
		props[f.json] = pr
		if f.required {
			required = append(required, f.json)
		}
	}
	return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
}

// MCPTool adapts c to an MCP tool. The input schema comes from the params tags; the arguments are decoded into P by their
// JSON names after the required fields and enums are checked.
func MCPTool[P any, R Result](c Command[P, R], env EnvFunc) Tool {
	fields := paramFields(reflect.TypeOf((*P)(nil)).Elem())
	return Tool{
		Name:        c.Name,
		Description: c.Summary,
		Schema:      schemaOf(fields),
		CallStructured: func(ctx context.Context, args json.RawMessage) (string, any, error) {
			var p P
			if err := decodeArgs(fields, args, &p); err != nil {
				return "", nil, toolError(err)
			}
			e, err := env(ctx)
			if err != nil {
				return "", nil, toolError(err)
			}
			e.Opts = Opts{NoColor: true, Width: 80}
			r, err := c.Run(ctx, e, p)
			if err != nil {
				return "", nil, toolError(err)
			}
			text := r.Text(e.Opts)
			b, err := marshal(r)
			if err != nil {
				return "", nil, toolError(err)
			}
			if len(b) == 0 || b[0] != '{' {
				return text, nil, nil
			}
			return text, r, nil
		},
	}
}

func decodeArgs[P any](fields []field, args json.RawMessage, p *P) error {
	if len(strings.TrimSpace(string(args))) == 0 || strings.TrimSpace(string(args)) == "null" {
		args = json.RawMessage("{}")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(args, &raw); err != nil {
		return cliframe.Usagef("the arguments must be a JSON object")
	}
	for _, f := range fields {
		if f.json == "" || f.json == "-" {
			continue
		}
		v, ok := raw[f.json]
		if !ok || string(v) == "null" {
			if f.required {
				return cliframe.Usagef("%s is required", f.json)
			}
			continue
		}
		delete(raw, f.json)
		if len(f.enum) > 0 {
			var s string
			if json.Unmarshal(v, &s) == nil {
				ok := false
				for _, e := range f.enum {
					ok = ok || e == s
				}
				if !ok {
					return cliframe.Usagef("%s must be one of %s", f.json, strings.Join(f.enum, ", "))
				}
			}
		}
	}
	for k := range raw {
		return cliframe.Usagef("unknown argument %s", k)
	}
	if err := json.Unmarshal(args, p); err != nil {
		var ute *json.UnmarshalTypeError
		if errors.As(err, &ute) && ute.Field != "" {
			return cliframe.Usagef("%s has the wrong type: want %s", ute.Field, ute.Type)
		}
		return cliframe.Usagef("bad arguments: %v", err)
	}
	return nil
}

// toolErr is the error MCPTool returns to the MCP server: the text is the CLI's "error: MSG" plus "\nhint: HINT", and
// Structured is {"error": MSG, "hint": HINT} (no hint key when there is none).
type toolErr struct{ msg, hint string }

func (e *toolErr) Error() string {
	s := "error: " + e.msg
	if e.hint != "" {
		s += "\nhint: " + e.hint
	}
	return s
}

// Structured implements mcp.StructuredError.
func (e *toolErr) Structured() any {
	m := map[string]any{"error": e.msg}
	if e.hint != "" {
		m["hint"] = e.hint
	}
	return m
}

// toolError renders err the way the CLI reports it, as one error with a structured form.
func toolError(err error) error {
	msg, hint := err.Error(), ""
	var e *cliframe.Error
	if errors.As(err, &e) && e != nil {
		msg, hint = e.Msg, e.Hint
	}
	return &toolErr{msg: strings.TrimPrefix(msg, "error: "), hint: hint}
}
