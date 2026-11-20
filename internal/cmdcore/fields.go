package cmdcore

import (
	"fmt"
	"reflect"
	"strings"
)

// field is one parameter of a command, read from the struct tags of its params type.
type field struct {
	index    int
	json     string // MCP name
	flag     string // CLI long flag, "--kind"
	short    string // CLI short flag, "-v"
	arg      string // CLI positional name
	optional bool   // positional may be left out on the command line
	desc     string
	mcpDesc  string // overrides desc in the MCP schema
	enum     []string
	required bool // required in the MCP schema
	repeat   bool
	typ      reflect.Type
}

// positional reports whether the field is a command-line positional.
func (f field) positional() bool { return f.arg != "" }

// paramFields reads the tags of the params struct type t. Fields without a json, flag or arg tag are skipped. It panics
// on an unsupported field type, which is a programming error found by the first test.
func paramFields(t reflect.Type) []field {
	if t.Kind() != reflect.Struct {
		panic(fmt.Sprintf("cmdcore: params type %s is not a struct", t))
	}
	var out []field
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		f := field{index: i, typ: sf.Type}
		f.json, _, _ = strings.Cut(sf.Tag.Get("json"), ",")
		f.flag = sf.Tag.Get("flag")
		f.short = sf.Tag.Get("short")
		if a := sf.Tag.Get("arg"); a != "" {
			name, opts, _ := strings.Cut(a, ",")
			f.arg = name
			f.optional = opts == "optional"
		}
		if f.json == "" && f.flag == "" && f.arg == "" {
			continue
		}
		f.desc = sf.Tag.Get("desc")
		f.mcpDesc = sf.Tag.Get("mcpdesc")
		if e := sf.Tag.Get("enum"); e != "" {
			f.enum = strings.Split(e, ",")
		}
		f.required = sf.Tag.Get("required") == "true"
		f.repeat = sf.Tag.Get("repeat") == "true"
		if _, ok := schemaType(sf.Type); !ok {
			panic(fmt.Sprintf("cmdcore: field %s.%s has unsupported type %s", t, sf.Name, sf.Type))
		}
		out = append(out, f)
	}
	return out
}

// schemaType maps a supported Go field type to its JSON Schema type.
func schemaType(t reflect.Type) (string, bool) {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		return "string", true
	case reflect.Bool:
		return "boolean", true
	case reflect.Int:
		return "integer", true
	case reflect.Slice:
		if t.Elem().Kind() == reflect.String {
			return "array", true
		}
	}
	return "", false
}
