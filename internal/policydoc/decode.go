// Package policydoc decodes the list-set document: the one value per account that homerange keeps and mash applies. It
// decodes the document (raw canonical JSON, or a compact JWS signed by the range's submitter key) into a
// listset.ListSet and checks the format's rules.
//
// DECISION D211 (and DEC-004) IS PENDING: the document's schema value and field names are the proposed homerange-mash
// seam, not an agreement. Everything that spells them is in this file.
package policydoc

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/listset"
)

// DocumentSchema is the schema value a document must carry. The seam has not fixed this value (D211 is pending): it is
// kept here, in one place, and a document with any other value is refused.
const DocumentSchema = "mash.listset/1"

// Errors Decode returns, wrapped with detail.
var (
	// ErrUnsigned: a submitter key was given and the document is raw JSON.
	ErrUnsigned = errors.New("the document is not signed")
	// ErrBadSignature: the JWS does not verify under the submitter key.
	ErrBadSignature = errors.New("the document signature does not verify")
	// ErrBadDocument: the document is malformed, has an unknown or missing field, or breaks a rule of the format.
	ErrBadDocument = errors.New("the document is refused")
)

// DecodeOptions configures Decode.
type DecodeOptions struct {
	// Submitter is the nkeys public key of the range's submitter. When set, the document must be a JWS whose signature
	// verifies under it. When empty, a JWS's signature is not checked and the result is not Verified.
	Submitter string
}

// Document is a decoded list-set document.
type Document struct {
	Schema         string
	Account        string
	Range          string
	Generation     uint64
	BaseGeneration uint64
	IssuedAt       time.Time
	ListSet        listset.ListSet
	// Raw is the exact bytes received (the JWS when it was one).
	Raw []byte
	// Verified is true only when a Submitter was given and the signature verified.
	Verified bool
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
	Range          *string                `json:"range"`
	Generation     *uint64                `json:"generation"`
	BaseGeneration *uint64                `json:"base_generation"`
	Groups         map[string]*wireGroup  `json:"groups"`
	Senders        map[string]*wireSender `json:"senders"`
	IssuedAt       *string                `json:"issued_at"`
}

func bad(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrBadDocument, fmt.Sprintf(format, a...))
}

// Decode decodes b: raw JSON, or a compact JWS (three base64url parts, header alg EdDSA) whose payload is the JSON.
func Decode(b []byte, opts DecodeOptions) (Document, error) {
	trimmed := bytes.TrimSpace(b)
	payload := trimmed
	verified := false
	if len(trimmed) > 0 && trimmed[0] != '{' {
		var err error
		if payload, verified, err = openJWS(string(trimmed), opts.Submitter); err != nil {
			return Document{}, err
		}
	} else if opts.Submitter != "" {
		return Document{}, fmt.Errorf("%w: a signed document is required", ErrUnsigned)
	}
	d, err := decodePayload(payload)
	if err != nil {
		return Document{}, err
	}
	d.Raw, d.Verified = bytes.Clone(b), verified
	return d, nil
}

func openJWS(s, submitter string) (payload []byte, verified bool, err error) {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return nil, false, bad("neither JSON nor a compact JWS")
	}
	var raw [3][]byte
	for i, p := range parts {
		if raw[i], err = base64.RawURLEncoding.DecodeString(p); err != nil {
			return nil, false, bad("JWS part %d is not base64url: %v", i+1, err)
		}
	}
	var hdr struct {
		Alg string `json:"alg"`
		Typ string `json:"typ"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw[0]))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&hdr); err != nil {
		return nil, false, bad("JWS header: %v", err)
	}
	if hdr.Alg != "EdDSA" {
		return nil, false, bad("JWS alg is %q, not EdDSA", hdr.Alg)
	}
	if submitter != "" {
		kp, err := nkeys.FromPublicKey(submitter)
		if err != nil {
			return nil, false, fmt.Errorf("submitter key %q is not an nkeys public key: %w", submitter, err)
		}
		if len(raw[2]) != ed25519.SignatureSize || kp.Verify([]byte(parts[0]+"."+parts[1]), raw[2]) != nil {
			return nil, false, ErrBadSignature
		}
		verified = true
	}
	return raw[1], verified, nil
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
		{"schema", w.Schema == nil}, {"account", w.Account == nil}, {"range", w.Range == nil},
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
	if *w.Account == "" || *w.Range == "" {
		return Document{}, bad("account and range must not be empty")
	}
	if *w.BaseGeneration == math.MaxUint64 || *w.Generation != *w.BaseGeneration+1 {
		return Document{}, bad("generation %d is not base_generation %d plus one", *w.Generation, *w.BaseGeneration)
	}
	at, err := time.Parse(time.RFC3339, *w.IssuedAt)
	if err != nil {
		return Document{}, bad("issued_at: %v", err)
	}
	d := Document{Schema: *w.Schema, Account: *w.Account, Range: *w.Range, Generation: *w.Generation,
		BaseGeneration: *w.BaseGeneration, IssuedAt: at}
	d.ListSet = listset.ListSet{Range: d.Range, Base: d.BaseGeneration, Switches: []listset.SwitchEntry{}, Senders: []listset.SenderLists{}}

	owners := make([]string, 0, len(w.Groups))
	for o := range w.Groups {
		owners = append(owners, o)
	}
	sort.Strings(owners)
	for _, o := range owners {
		g := w.Groups[o]
		if g == nil {
			return Document{}, bad("group %q is null", o)
		}
		for _, v := range []struct {
			verb listset.Verb
			val  *string
		}{{listset.Mail, g.Mail}, {listset.Prompt, g.Prompt}, {listset.Status, g.Status}, {listset.Heartbeat, g.Heartbeat}} {
			if v.val == nil {
				return Document{}, bad("group %q: verb %q is missing", o, v.verb)
			}
			if *v.val != string(listset.Shared) && *v.val != string(listset.Closed) {
				return Document{}, bad("group %q: verb %q is %q, not shared or closed", o, v.verb, *v.val)
			}
			d.ListSet.Switches = append(d.ListSet.Switches, listset.SwitchEntry{Group: d.Range + "." + o, Verb: v.verb, Switch: listset.Switch(*v.val)})
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
