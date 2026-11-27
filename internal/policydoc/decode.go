// Package policydoc decodes the list-set document: the one value per account that a policy author keeps and mash applies. It
// decodes the document (plain canonical JSON, unsigned) into a
// listset.ListSet and checks the format's rules.
//
// The seam between the policy author and mash is not settled yet: the document's schema value and field names are the
// proposed author-mash seam, not an agreement. Everything that spells them is in this file.
package policydoc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/bogworks/mash/internal/listset"
)

// DocumentSchema is the schema value a document must carry. The seam's design is not settled yet, so it has not fixed
// this value: it is kept here, in one place, and a document with any other value is refused.
const DocumentSchema = "mash.listset/2"

// ErrBadDocument: the document is malformed, has an unknown or missing field, or breaks a rule of the format.
var ErrBadDocument = errors.New("the document is refused")

// DecodeOptions configures Decode. It has no options; the document is plain JSON.
type DecodeOptions struct{}

// Document is a decoded list-set document.
type Document struct {
	Schema         string
	Account        string
	Owner          string
	Generation     uint64
	BaseGeneration uint64
	IssuedAt       time.Time
	ListSet        listset.ListSet
	// Raw is the exact bytes received.
	Raw []byte
}

type wireGroup struct {
	Mail      *string `json:"mail"`
	Prompt    *string `json:"prompt"`
	Status    *string `json:"status"`
	Heartbeat *string `json:"heartbeat"`
}

type wireSender struct {
	Allow *[]string `json:"allow"`
	Deny  *[]string `json:"deny"`
}

type wireDoc struct {
	Schema         *string                `json:"schema"`
	Account        *string                `json:"account"`
	Owner          *string                `json:"owner"`
	Generation     *uint64                `json:"generation"`
	BaseGeneration *uint64                `json:"base_generation"`
	Groups         map[string]*wireGroup  `json:"groups"`
	Senders        map[string]*wireSender `json:"senders"`
	IssuedAt       *string                `json:"issued_at"`
}

func bad(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrBadDocument, fmt.Sprintf(format, a...))
}

// Decode decodes b, a plain JSON document. Any unknown field, including next_submitter, is refused; so is input that
// is not a JSON object (a compact JWS, for one).
func Decode(b []byte, _ DecodeOptions) (Document, error) {
	trimmed := bytes.TrimSpace(b)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return Document{}, bad("not a JSON document")
	}
	d, err := decodePayload(trimmed)
	if err != nil {
		return Document{}, err
	}
	d.Raw = bytes.Clone(b)
	return d, nil
}

func decodePayload(p []byte) (Document, error) {
	if err := noDuplicateKeys(p); err != nil {
		return Document{}, bad("%v", err)
	}
	var w wireDoc
	dec := json.NewDecoder(bytes.NewReader(p))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&w); err != nil {
		return Document{}, bad("%v", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return Document{}, bad("data after the document")
	}
	for _, f := range []struct {
		name    string
		missing bool
	}{
		{"schema", w.Schema == nil}, {"account", w.Account == nil}, {"owner", w.Owner == nil},
		{"generation", w.Generation == nil}, {"base_generation", w.BaseGeneration == nil},
		{"groups", w.Groups == nil}, {"senders", w.Senders == nil}, {"issued_at", w.IssuedAt == nil},
	} {
		if f.missing {
			return Document{}, bad("field %q is missing", f.name)
		}
	}
	if *w.Schema != DocumentSchema {
		return Document{}, bad("schema is %q, not %q", *w.Schema, DocumentSchema)
	}
	if *w.Account == "" || *w.Owner == "" {
		return Document{}, bad("account and owner must not be empty")
	}
	if *w.BaseGeneration == math.MaxUint64 || *w.Generation != *w.BaseGeneration+1 {
		return Document{}, bad("generation %d is not base_generation %d plus one", *w.Generation, *w.BaseGeneration)
	}
	at, err := time.Parse(time.RFC3339, *w.IssuedAt)
	if err != nil {
		return Document{}, bad("issued_at: %v", err)
	}
	d := Document{Schema: *w.Schema, Account: *w.Account, Owner: *w.Owner, Generation: *w.Generation,
		BaseGeneration: *w.BaseGeneration, IssuedAt: at}
	d.ListSet = listset.ListSet{Owner: d.Owner, Base: d.BaseGeneration, Switches: []listset.SwitchEntry{}, Senders: []listset.SenderLists{}}

	groupKeys := make([]string, 0, len(w.Groups))
	for k := range w.Groups {
		groupKeys = append(groupKeys, k)
	}
	sort.Strings(groupKeys)
	for _, k := range groupKeys {
		g := w.Groups[k]
		if g == nil {
			return Document{}, bad("group %q is null", k)
		}
		if strings.Contains(k, ".") {
			return Document{}, bad("group %q: a group is keyed by its group part only (no dots); the owner is the document's, and a switch is set for a group, never for a single sender", k)
		}
		for _, v := range []struct {
			verb listset.Verb
			val  *string
		}{{listset.Mail, g.Mail}, {listset.Prompt, g.Prompt}, {listset.Status, g.Status}, {listset.Heartbeat, g.Heartbeat}} {
			if v.val == nil {
				return Document{}, bad("group %q: verb %q is missing", k, v.verb)
			}
			if *v.val != string(listset.Shared) && *v.val != string(listset.Closed) {
				return Document{}, bad("group %q: verb %q is %q, not shared or closed", k, v.verb, *v.val)
			}
			d.ListSet.Switches = append(d.ListSet.Switches, listset.SwitchEntry{Group: d.Owner + "." + k, Verb: v.verb, Switch: listset.Switch(*v.val)})
		}
	}
	names := make([]string, 0, len(w.Senders))
	for n := range w.Senders {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		s := w.Senders[n]
		if s == nil || s.Allow == nil || s.Deny == nil {
			return Document{}, bad("sender %q: allow and deny are both required", n)
		}
		allow, deny := slices.Clone(*s.Allow), slices.Clone(*s.Deny)
		sort.Strings(allow)
		sort.Strings(deny)
		d.ListSet.Senders = append(d.ListSet.Senders, listset.SenderLists{Sender: n, Allow: allow, Deny: deny})
	}
	return d, nil
}

// noDuplicateKeys refuses a JSON object that repeats a key at any depth.
func noDuplicateKeys(b []byte) error { return walkJSON(json.NewDecoder(bytes.NewReader(b))) }

func walkJSON(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	d, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch d {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return err
			}
			k, _ := kt.(string)
			if seen[k] {
				return fmt.Errorf("duplicate key %q", k)
			}
			seen[k] = true
			if err := walkJSON(dec); err != nil {
				return err
			}
		}
	case '[':
		for dec.More() {
			if err := walkJSON(dec); err != nil {
				return err
			}
		}
	}
	_, err = dec.Token()
	return err
}
