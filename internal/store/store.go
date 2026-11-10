package store

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/sap"
)

// Inbox limits (spec 5.1).
const (
	InboxMaxAge   = 30 * 24 * time.Hour
	InboxMaxBytes = 16 << 20
	MaxMsgSize    = 1 << 20
	LeaseTTL      = 90 * time.Second
)

// Bucket size caps. Hosted NATS accounts (Synadia Cloud) reject a stream config with no MaxBytes, so every bucket
// sets one. Directory records and leases are tiny; these are generous.
const (
	DirectoryMaxBytes = 16 << 20
	LeaseMaxBytes     = 4 << 20
	// RetiredMaxBytes caps the retired-name bucket: one small marker per retired new-form identity, kept for good.
	RetiredMaxBytes = 4 << 20
)

// LeaseTombstone is what Deprovision writes to an endpoint's lease key: it fences the name, so a live or
// starting session cannot take the lease back after its endpoint is gone. It expires with the bucket's TTL
// (LeaseTTL) like any lease, and Provision clears it.
func LeaseTombstone() []byte {
	return []byte(`{"deprovisioned":true,"since":"` + time.Now().UTC().Format(time.RFC3339) + `"}`)
}

// SuspendedSubject is the one subject a suspended endpoint's inbox stream takes (ADR 0057). It sits outside agents.>,
// so no endpoint's publish allow-list reaches it. MetaSuspendedSubjects (a JSON list) and MetaSuspendedAt (RFC 3339,
// UTC) are the stream metadata keys that hold the subjects it had and when they were parked.
func SuspendedSubject(name string) string { return "mash.suspended." + name }

const (
	MetaSuspendedSubjects = "mash_suspended_subjects"
	MetaSuspendedAt       = "mash_suspended_at"
	MetaSuspendedID       = "mash_suspended_id" // random 128-bit hex token naming one suspension
	// MetaSuspendedUser is the user public key of the directory record at suspend time; empty or absent when there was
	// no record. The drained deprovision requires the record to still carry it.
	MetaSuspendedUser = "mash_suspended_user"
)

// InboxDuplicateWindow is how long the inbox stream remembers a Nats-Msg-Id: a publish with the same id inside it is
// dropped, after it the message is stored again. It bounds what "send --id" can promise.
const InboxDuplicateWindow = 2 * time.Minute

// InboxConfig is endpoint name's inbox stream. No subject transform: a stored
// Agent-Sender header must verify against the stored subject (SAP §13.8).
func InboxConfig(owner, name string) jetstream.StreamConfig {
	return InboxConfigFor(owner, name, DefaultLimits())
}

// Limits are the configurable caps: the inbox's size, age and consumer count, and the two buckets' sizes.
// DefaultLimits is what mash has always used.
type Limits struct {
	InboxMaxBytes     int64
	InboxMaxAge       time.Duration
	DirectoryMaxBytes int64
	LeaseMaxBytes     int64
	InboxMaxConsumers int // -1 is unlimited
}

// DefaultLimits returns the built-in limits.
func DefaultLimits() Limits {
	return Limits{InboxMaxBytes: InboxMaxBytes, InboxMaxAge: InboxMaxAge,
		DirectoryMaxBytes: DirectoryMaxBytes, LeaseMaxBytes: LeaseMaxBytes, InboxMaxConsumers: -1}
}

// Validate checks l against the sane minimums, naming the offending field.
func (l Limits) Validate() error {
	switch {
	case l.InboxMaxBytes < 2*MaxMsgSize:
		return fmt.Errorf("mash: limits inbox_max_bytes %d is below the minimum %d (twice the largest message)", l.InboxMaxBytes, 2*MaxMsgSize)
	case l.InboxMaxAge < time.Hour:
		return fmt.Errorf("mash: limits inbox_max_age %s is below the minimum 1h", l.InboxMaxAge)
	case l.DirectoryMaxBytes < 1<<20:
		return fmt.Errorf("mash: limits directory_max_bytes %d is below the minimum 1MiB", l.DirectoryMaxBytes)
	case l.LeaseMaxBytes < 1<<20:
		return fmt.Errorf("mash: limits lease_max_bytes %d is below the minimum 1MiB", l.LeaseMaxBytes)
	case l.InboxMaxConsumers != -1 && l.InboxMaxConsumers < 1:
		return fmt.Errorf("mash: limits inbox_max_consumers %d must be -1 (unlimited) or at least 1", l.InboxMaxConsumers)
	}
	return nil
}

// InboxConfigFor is InboxConfig under limits l.
func InboxConfigFor(owner, name string, l Limits) jetstream.StreamConfig {
	return InboxConfigZoned(owner, "", name, l)
}

// InboxConfigZoned is InboxConfigFor for an endpoint in zone. A non-empty zone adds the exact filter of that zone next
// to the legacy one, so mail from a sender that still publishes to the legacy subject lands too, and mail addressed
// to any other zone does not (ADR 0054).
func InboxConfigZoned(owner, zone, name string, l Limits) jetstream.StreamConfig {
	subjects := []string{sap.MailFilter(owner, name)}
	if zone != "" {
		subjects = append(subjects, sap.MailFilterZone(owner, zone, name))
	}
	return inboxStreamConfig(name, subjects, l)
}

// InboxConfigIdentity is the inbox stream of the endpoint id under limits l: inbox-<flat key> filtered on the
// identity's own mail subject only (id.MailFilter()). For a legacy identity it is InboxConfigFor(owner, name, l).
func InboxConfigIdentity(id sap.EndpointIdentity, l Limits) jetstream.StreamConfig {
	if id.IsLegacy() {
		return InboxConfigFor(id.Owner, id.Name, l)
	}
	return inboxStreamConfig(id.Flat(), []string{id.MailFilter()}, l)
}

// inboxStreamConfig is the one stream shape every inbox has; key is the directory key the stream is named for.
func inboxStreamConfig(name string, subjects []string, l Limits) jetstream.StreamConfig {
	return jetstream.StreamConfig{
		Name:              sap.InboxStream(name),
		Description:       "mash inbox for " + name,
		Subjects:          subjects,
		Storage:           jetstream.FileStorage,
		Retention:         jetstream.LimitsPolicy,
		MaxAge:            l.InboxMaxAge,
		MaxBytes:          l.InboxMaxBytes,
		MaxMsgs:           -1,
		MaxConsumers:      l.InboxMaxConsumers,
		MaxMsgsPerSubject: -1,
		MaxMsgSize:        MaxMsgSize,
		Discard:           jetstream.DiscardOld,
		Duplicates:        InboxDuplicateWindow,
		DenyDelete:        true,
		DenyPurge:         true,
		AllowDirect:       true,
		Replicas:          1,
	}
}

// ConsumerConfig is the durable consumer the endpoint manager reads its inbox through.
func ConsumerConfig() jetstream.ConsumerConfig {
	return jetstream.ConsumerConfig{
		Durable:       Consumer,
		AckPolicy:     jetstream.AckExplicitPolicy,
		AckWait:       30 * time.Second,
		MaxAckPending: 1000,
		MaxDeliver:    -1,
		DeliverPolicy: jetstream.DeliverAllPolicy,
	}
}

// DirectoryConfig is the directory bucket: name -> Record, written by the admin.
func DirectoryConfig() jetstream.KeyValueConfig { return DirectoryConfigFor(DefaultLimits()) }

// DirectoryConfigFor is DirectoryConfig under limits l.
func DirectoryConfigFor(l Limits) jetstream.KeyValueConfig {
	return jetstream.KeyValueConfig{Bucket: DirectoryBucket, Description: "mash endpoint directory",
		History: 1, Storage: jetstream.FileStorage, MaxBytes: l.DirectoryMaxBytes}
}

// LeaseConfig is the lease bucket: name -> holder, expiring unless refreshed.
func LeaseConfig() jetstream.KeyValueConfig { return LeaseConfigFor(DefaultLimits()) }

// LeaseConfigFor is LeaseConfig under limits l.
func LeaseConfigFor(l Limits) jetstream.KeyValueConfig {
	return jetstream.KeyValueConfig{Bucket: LeaseBucket, Description: "mash endpoint leases",
		History: 1, TTL: LeaseTTL, Storage: jetstream.FileStorage, MaxBytes: l.LeaseMaxBytes}
}

// RetiredConfig is the retired-name bucket: flat identity -> Retired, written when a new-form identity is
// deprovisioned. It has no TTL (unlike the lease bucket, whose tombstone lapses after LeaseTTL) and nothing in mash
// deletes from it: INV-002, a retired name is never reassigned, needs the marker to outlive the lease.
func RetiredConfig() jetstream.KeyValueConfig {
	return jetstream.KeyValueConfig{Bucket: RetiredBucket, Description: "mash retired new-form identities",
		History: 1, Storage: jetstream.FileStorage, MaxBytes: RetiredMaxBytes}
}

// Retired is the value of one retired-name marker. It names the identity's three tokens and the user public key that
// was retired; it never holds a seed or any other secret. User is empty when the record could not be read.
type Retired struct {
	Agent     string `json:"agent"`
	Owner     string `json:"owner"`
	Name      string `json:"name"`
	User      string `json:"user,omitempty"`
	RetiredAt string `json:"retired_at"` // RFC 3339, UTC
}

// Record is one directory entry (spec 6.1).
type Record struct {
	Name        string `json:"name"`
	Owner       string `json:"owner"`
	Agent       string `json:"agent"`
	AgentToken  string `json:"agent_token"`
	Account     string `json:"account"`
	User        string `json:"user"`
	MailSubject string `json:"mail_subject"`
	Created     string `json:"created"`
	Note        string `json:"note,omitempty"`
	// Role, Host and Labels are optional directory metadata: what the endpoint is for, the machine it is meant to run
	// on, and free-form key=value labels (for example a team, project or fleet). They are the identity on record; the
	// lease is the live view. Readers from before these fields ignore them.
	Role   string            `json:"role,omitempty"`
	Host   string            `json:"host,omitempty"`
	Labels map[string]string `json:"labels,omitempty"`
	// Description is free text on what the endpoint does, and Caps are up to MaxCaps capability words (sorted), both
	// set by the admin. Readers from before these fields ignore them, and set-meta keeps fields it does not know.
	Description string   `json:"description,omitempty"`
	Caps        []string `json:"caps,omitempty"`
	// Zone is the zone token in the endpoint's mail subject (ADR 0054). Empty means the legacy 5-token mail subject,
	// not the default zone. Readers from before this field ignore it and, for a zoned endpoint, still reach it
	// because its inbox stream keeps the legacy subject too.
	Zone string `json:"zone,omitempty"`
	// Alias is the legacy identity a new-form record answers to during the transition window (ADR 0076). Only a
	// new-form record may carry it, only provisioning writes it, and the endpoint never does. Readers from before
	// this field ignore it. It is removed with the legacy form.
	Alias *Alias `json:"alias,omitempty"`
}

// Alias names a legacy identity (owner and name under the legacy rule) that a new-form record answers to.
type Alias struct {
	Owner string `json:"owner"`
	Name  string `json:"name"`
}

// MailSubjectFor is the mail subject an endpoint with this owner, zone and name has: the legacy one when zone is empty.
func MailSubjectFor(owner, zone, name string) string {
	if zone == "" {
		return sap.For(owner, name).Mail
	}
	return sap.ForZone(owner, zone, name).Mail
}

// MaxMetaLen is the longest Role, Host or label value.
const MaxMetaLen = 64

// MaxLabels is the most labels one record may carry. It is enforced where labels are written (ValidLabels), not
// where a record is read, so a record with more is still parsed (ADR 0072).
const MaxLabels = 64

var labelKey = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,31}$`)

// ValidLabelKey reports whether k is a legal label key.
func ValidLabelKey(k string) bool { return labelKey.MatchString(k) }

// ValidLabels checks a label set: at most MaxLabels keys, each key [a-z][a-z0-9_.-]{0,31}, each value passing ValidMeta.
func ValidLabels(l map[string]string) error {
	if len(l) > MaxLabels {
		return fmt.Errorf("mash: %d labels; at most %d", len(l), MaxLabels)
	}
	for k, v := range l {
		if !ValidLabelKey(k) {
			return fmt.Errorf("mash: label key: a letter, then up to 31 of a-z 0-9 _ . - (the value is not shown)")
		}
		if err := ValidMeta("label "+k, v); err != nil {
			return err
		}
	}
	return nil
}

// MaxDescriptionLen is the longest Description.
const MaxDescriptionLen = 256

// MaxCaps is the most capability words one record may carry.
const MaxCaps = 8

// ValidCap reports whether c is a legal capability word: [a-z][a-z0-9_.-]{0,31}.
func ValidCap(c string) bool { return labelKey.MatchString(c) }

// ValidCaps checks a capability list: at most MaxCaps words, each passing ValidCap, none repeated.
func ValidCaps(caps []string) error {
	if len(caps) > MaxCaps {
		return fmt.Errorf("mash: %d capabilities; at most %d", len(caps), MaxCaps)
	}
	seen := map[string]bool{}
	for _, c := range caps {
		if !ValidCap(c) {
			return fmt.Errorf("mash: capability: a letter, then up to 31 of a-z 0-9 _ . - (the value is not shown)")
		}
		if seen[c] {
			return fmt.Errorf("mash: capability %q is listed twice", c)
		}
		seen[c] = true
	}
	return nil
}

// ValidDescription checks a description: at most MaxDescriptionLen characters, printable, no tab or newline.
func ValidDescription(v string) error {
	return validText("description", v, MaxDescriptionLen)
}

// ValidMeta checks one metadata value: at most MaxMetaLen characters, printable, no tab or newline (they end up in
// tab-separated output). Empty is valid (unset).
func ValidMeta(field, v string) error { return validText(field, v, MaxMetaLen) }

func validText(field, v string, max int) error {
	if utf8.RuneCountInString(v) > max {
		return fmt.Errorf("mash: %s is longer than %d characters", field, max)
	}
	for _, r := range v {
		if !unicode.IsPrint(r) {
			return fmt.Errorf("mash: %s has a tab, newline or other non-printable character", field)
		}
	}
	return nil
}

// legacyCheck applies the legacy record checks to r as the record for key.
func legacyCheck(key string, r Record) error {
	if r.Name != key || r.User == "" || r.MailSubject == "" {
		return fmt.Errorf("directory %s: incomplete record", key)
	}
	if r.Zone != "" && !sap.ValidZone(r.Zone) {
		return fmt.Errorf("directory %s: invalid zone %q", key, r.Zone)
	}
	if want := MailSubjectFor(r.Owner, r.Zone, r.Name); r.MailSubject != want {
		return fmt.Errorf("directory %s: mail subject %q is not %q", key, r.MailSubject, want)
	}
	return nil
}

// looksNewForm reports whether r, read under key, was meant as a new-form record: only when key contains an
// underscore, which a legacy key never has (the legacy name rule forbids it). Every other record keeps its legacy
// error text.
func looksNewForm(key string) bool {
	return strings.Contains(key, "_")
}

// newFormCheck applies the new-form record rules to r as the record for key and returns its identity.
func newFormCheck(key string, r Record) (sap.EndpointIdentity, error) {
	id, err := sap.New(r.AgentToken, r.Owner, r.Name)
	if err != nil {
		return sap.EndpointIdentity{}, fmt.Errorf("directory %s: new-form identity (agent_token, owner, name) is invalid: %w", key, err)
	}
	if key != id.Flat() {
		return sap.EndpointIdentity{}, fmt.Errorf("directory %s: new-form record must be keyed by its flat identity %q", key, id.Flat())
	}
	if want := id.Subjects().Mail; r.MailSubject != want {
		return sap.EndpointIdentity{}, fmt.Errorf("directory %s: new-form mail subject %q is not %q", key, r.MailSubject, want)
	}
	if r.Zone != "" {
		return sap.EndpointIdentity{}, fmt.Errorf("directory %s: new-form record must not have a zone (got %q)", key, r.Zone)
	}
	if r.User == "" {
		return sap.EndpointIdentity{}, fmt.Errorf("directory %s: incomplete record", key)
	}
	return id, nil
}

// ParseRecord decodes a directory value and checks it names key. A record that passes the legacy checks is a legacy
// record; otherwise it may be a new-form record (three-token identity, keyed by the flat form), else the legacy error
// is returned, unless the record looks new-form, when the broken new-form rule is named.
func ParseRecord(key string, value []byte) (Record, error) {
	var r Record
	if err := json.Unmarshal(value, &r); err != nil {
		return Record{}, fmt.Errorf("directory %s: %w", key, err)
	}
	lerr := legacyCheck(key, r)
	if lerr == nil {
		if r.Alias != nil {
			return Record{}, fmt.Errorf("directory %s: a legacy record cannot carry an alias", key)
		}
		return r, nil
	}
	if _, nerr := newFormCheck(key, r); nerr != nil {
		if looksNewForm(key) {
			return Record{}, nerr
		}
		return Record{}, lerr
	}
	if err := aliasCheck(key, r); err != nil {
		return Record{}, err
	}
	return r, nil
}

// aliasCheck applies the alias rules to a new-form record r read under key: the alias, if any, must be a valid legacy
// identity whose owner differs from r's own owner (mash's admission rule, so the legacy wildcard filter of the alias
// never reaches r).
func aliasCheck(key string, r Record) error {
	if r.Alias == nil {
		return nil
	}
	if _, _, err := r.AliasIdentity(); err != nil {
		return fmt.Errorf("directory %s: invalid alias: %w", key, err)
	}
	if r.Alias.Owner == r.Owner {
		return fmt.Errorf("directory %s: alias owner %q equals the record's own owner", key, r.Alias.Owner)
	}
	return nil
}

// AliasIdentity is the legacy identity r's alias names. ok is false (and err nil) when r carries no alias, and false
// with an error when the alias is not a valid legacy identity.
func (r Record) AliasIdentity() (id sap.EndpointIdentity, ok bool, err error) {
	if r.Alias == nil {
		return sap.EndpointIdentity{}, false, nil
	}
	id, err = sap.Legacy(r.Alias.Owner, r.Alias.Name)
	if err != nil {
		return sap.EndpointIdentity{}, false, err
	}
	return id, true, nil
}

// Identity is the endpoint identity r names: the legacy identity when r satisfies the legacy checks (keyed by its
// name) and its owner and name are valid legacy tokens, else the new-form identity of (agent_token, owner, name).
// An odd legacy record that ParseRecord accepts can have no identity here.
func (r Record) Identity() (sap.EndpointIdentity, error) {
	if legacyCheck(r.Name, r) == nil {
		if id, err := sap.Legacy(r.Owner, r.Name); err == nil {
			return id, nil
		}
	}
	id, err := sap.New(r.AgentToken, r.Owner, r.Name)
	if err != nil {
		return sap.EndpointIdentity{}, fmt.Errorf("directory %s: record names no identity: %w", r.Name, err)
	}
	return newFormCheck(id.Flat(), r)
}

// Key is the directory key of r: the name for a record that passes the legacy checks (even when its owner or name
// is not a valid legacy token, so Identity errors), the flat identity for a new-form record. It is empty when r is
// neither. Key is the writer's key for every record ParseRecord accepts.
func (r Record) Key() string {
	if legacyCheck(r.Name, r) == nil {
		return r.Name
	}
	id, err := r.Identity()
	if err != nil {
		return ""
	}
	return id.Flat()
}

// KeyOrName is Key(), or Name for a record that names no identity (a bare value that never went through
// ParseRecord). A reader that indexes the directory, a lease or a live-presence map by a record uses this, so a
// new-form record (keyed by its flat identity) is never confused with a legacy one of the same name; for every legacy
// record it is Name.
func (r Record) KeyOrName() string {
	if k := r.Key(); k != "" {
		return k
	}
	return r.Name
}

// GetRecord reads name's directory record.
func GetRecord(ctx context.Context, kv jetstream.KeyValue, name string) (Record, error) {
	e, err := kv.Get(ctx, name)
	if err != nil {
		return Record{}, fmt.Errorf("directory %s: %w", name, err)
	}
	return ParseRecord(name, e.Value())
}
