package cmdcore

import (
	"fmt"
	"reflect"
	"strconv"
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
	value    string // CLI value placeholder in help, "number"
	min      *int   // smallest accepted whole number
	max      *int   // largest accepted whole number
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
	seen := map[string]string{} // "json x", "flag --x", "arg x" -> field name
	sawOptional := ""
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		f := field{index: i, typ: sf.Type}
		f.json, _, _ = strings.Cut(sf.Tag.Get("json"), ",")
		f.flag = sf.Tag.Get("flag")
		f.short = sf.Tag.Get("short")
		bad := func(format string, a ...any) {
			panic(fmt.Sprintf("cmdcore: field %s.%s %s", t, sf.Name, fmt.Sprintf(format, a...)))
		}
		if a, ok := sf.Tag.Lookup("arg"); ok {
			name, opts, _ := strings.Cut(a, ",")
			if name == "" || (opts != "" && opts != "optional") {
				bad("has the arg tag %q: want name or name,optional", a)
			}
			f.arg = name
			f.optional = opts == "optional"
		}
		if f.json == "" && f.flag == "" && f.arg == "" {
			continue
		}
		if f.arg != "" && f.flag != "" {
			bad("has both arg and flag tags")
		}
		if f.short != "" && f.flag == "" {
			bad("has a short tag without a flag tag")
		}
		f.desc = sf.Tag.Get("desc")
		f.mcpDesc = sf.Tag.Get("mcpdesc")
		if e := sf.Tag.Get("enum"); e != "" {
			f.enum = strings.Split(e, ",")
		}
		if v, ok := sf.Tag.Lookup("required"); ok {
			if v != "true" {
				bad("has required:%q: want \"true\" or no tag", v)
			}
			f.required = true
		}
		if v, ok := sf.Tag.Lookup("repeat"); ok {
			if v != "true" {
				bad("has repeat:%q: want \"true\" or no tag", v)
			}
			f.repeat = true
		}
		f.value = sf.Tag.Get("value")
		if f.value != "" && f.flag == "" {
			bad("has a value tag without a flag tag")
		}
		typ, ok := schemaType(sf.Type)
		if !ok {
			bad("has unsupported type %s", sf.Type)
		}
		if f.repeat && typ != "array" {
			bad("has a repeat tag but is not a []string")
		}
		if v := sf.Tag.Get("min"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || typ != "integer" {
				bad("has min:%q: want a whole number on an integer field", v)
			}
			f.min = &n
		}
		if v := sf.Tag.Get("max"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || typ != "integer" {
				bad("has max:%q: want a whole number on an integer field", v)
			}
			f.max = &n
		}
		if len(f.enum) > 0 && typ != "string" {
			bad("has an enum tag but is not a string")
		}
		if f.arg != "" {
			if f.optional {
				sawOptional = sf.Name
			} else if sawOptional != "" {
				bad("is a required positional after the optional positional %s", sawOptional)
			}
		}
		for _, k := range []string{"json " + f.json, "flag " + f.flag, "arg " + f.arg, "short " + f.short} {
			if strings.HasSuffix(k, " ") || k == "json -" {
				continue
			}
			if prev, dup := seen[k]; dup {
				bad("repeats the %s of field %s", k, prev)
			}
			seen[k] = sf.Name
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

// rangeError is the error for a whole number n outside the field's min and max, worded to follow the field's name:
// "must be a whole number of 1 or more" with only a minimum, "must be a whole number from 1 to 100" with both, "must be
// a whole number of 100 or less" with only a maximum.
func (f field) rangeError(n int64) error {
	switch {
	case f.min != nil && f.max != nil && (n < int64(*f.min) || n > int64(*f.max)):
		return fmt.Errorf("must be a whole number from %d to %d", *f.min, *f.max)
	case f.min != nil && n < int64(*f.min):
		return fmt.Errorf("must be a whole number of %d or more", *f.min)
	case f.max != nil && n > int64(*f.max):
		return fmt.Errorf("must be a whole number of %d or less", *f.max)
	}
	return nil
}
