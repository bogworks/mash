package cmdcore

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"reflect"
	"sort"
	"strconv"
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
		if f.min != nil {
			pr["minimum"] = *f.min
		}
		if f.max != nil {
			pr["maximum"] = *f.max
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
	desc := c.MCPDescription
	if desc == "" {
		desc = c.Summary
	}
	return Tool{
		Name:        c.Name,
		Description: desc,
		Schema:      schemaOf(fields),
		CallStructured: func(ctx context.Context, args json.RawMessage) (string, any, error) {
			var p P
			if err := decodeArgs(fields, args, &p); err != nil {
				return "", nil, toolError(err)
			}
			if env == nil {
				return "", nil, toolError(errNoEnv(c.Name))
			}
			e, err := env(ctx)
			if err != nil {
				return "", nil, toolError(err)
			}
			if e == nil {
				return "", nil, toolError(errNoEnv(c.Name))
			}
			e.Opts = Opts{NoColor: true, Width: 80}
			r, err := c.Run(ctx, e, p)
			if err != nil {
				return "", nil, toolError(err)
			}
			text := r.Text(e.Opts)
			if h, ok := any(r).(Hinter); ok && h.Hint() != "" {
				text += "\nhint: " + h.Hint()
			}
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
	known := map[string]bool{}
	var names []string
	for _, f := range fields {
		if f.json != "" && f.json != "-" {
			known[f.json] = true
			names = append(names, f.json)
		}
	}
	// Unknown keys come first: an agent sending old names is told which, not that a required one is missing.
	var unknown []string
	for k := range raw {
		if !known[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return cliframe.Usagef("unknown argument %s", unknown[0]).WithHint("the arguments are " + strings.Join(names, ", "))
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
			delete(raw, f.json)
			continue
		}
		if f.repeat && strings.HasPrefix(strings.TrimSpace(string(v)), `"`) {
			return cliframe.Usagef("%s must be an array of strings, not a string", f.json)
		}
		if typ, _ := schemaType(f.typ); typ == "integer" {
			n, err := wholeNumber(f, v)
			if err != nil {
				return err
			}
			raw[f.json] = n
		}
		if f.required {
			var s string
			if json.Unmarshal(v, &s) == nil && strings.TrimSpace(s) == "" {
				return cliframe.Usagef("%s is required", f.json)
			}
		}
		if len(f.enum) > 0 {
			var s string
			if json.Unmarshal(v, &s) == nil {
				c, ok := enumValue(f.enum, s)
				if !ok {
					return cliframe.Usagef("%s must be one of %s", f.json, strings.Join(f.enum, ", "))
				}
				raw[f.json], _ = json.Marshal(c)
			}
		}
	}
	args, _ = json.Marshal(raw)
	if err := json.Unmarshal(args, p); err != nil {
		var ute *json.UnmarshalTypeError
		if errors.As(err, &ute) && ute.Field != "" {
			return cliframe.Usagef("%s has the wrong type: want %s", ute.Field, ute.Type)
		}
		return cliframe.Usagef("bad arguments: %v", err)
	}
	return nil
}

// wholeNumber checks the JSON value v of an integer field: a number with no fraction (2 and 2.0 pass, 2.5 does not)
// that is at least the field's minimum. It returns the value written as a plain integer. A value that is not a JSON
// number is returned as it is, for the decoder's wrong-type error.
func wholeNumber(f field, v json.RawMessage) (json.RawMessage, error) {
	t := strings.TrimSpace(string(v))
	if t == "" || (t[0] != '-' && (t[0] < '0' || t[0] > '9')) {
		return v, nil
	}
	x, ok := new(big.Rat), len(t) <= 40
	if ok {
		_, ok = x.SetString(t)
	}
	if !ok || !x.IsInt() || !x.Num().IsInt64() {
		return nil, cliframe.Usagef("%s must be a whole number", f.json)
	}
	n := x.Num().Int64()
	if err := f.rangeError(n); err != nil {
		return nil, cliframe.Usagef("%s %s", f.json, err.Error())
	}
	return json.RawMessage(strconv.FormatInt(n, 10)), nil
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
