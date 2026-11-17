package store

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
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
	// RetiredMaxBytes caps the retired-name bucket. The bucket keeps one small marker per retired new-form identity
	// for good and mash never deletes one. A full bucket makes the stream discard new markers, so
	// VerifyRetiredStatus and provisioning then refuse. 64 MiB holds many markers.
	RetiredMaxBytes = 64 << 20
)

// LeaseTombstone is what Deprovision writes to an endpoint's lease key: it fences the name, so a live or
// starting session cannot take the lease back after its endpoint is gone. It expires with the bucket's TTL
// (LeaseTTL) like any lease, and Provision clears it.
func LeaseTombstone() []byte {
	return []byte(`{"deprovisioned":true,"since":"` + time.Now().UTC().Format(time.RFC3339) + `"}`)
}

// SuspendedSubject is the one subject a suspended endpoint's inbox stream takes (ADR 0057). It sits outside agents.>,
// so no endpoint's publish allow-list reaches it. MetaSuspendedSubjects (a JSON list) and MetaSuspendedAt (RFC 3339,
// UTC) are the stream metadata keys that hold the subjects it had and when they were parked. The argument is the
// endpoint's flat key (Address.Flat).
func SuspendedSubject(flat string) string { return "mash.suspended." + flat }

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

// InboxConfig is the inbox stream of the endpoint at a. No subject transform: a stored
// Agent-Sender header must verify against the stored subject (SAP §13.8).
func InboxConfig(a sap.Address) jetstream.StreamConfig {
	return InboxConfigFor(a, DefaultLimits())
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

// InboxConfigFor is InboxConfig under limits l: inbox-<flat key>, filtered on a's own mail subject only (a.MailFilter()).
func InboxConfigFor(a sap.Address, l Limits) jetstream.StreamConfig {
	return inboxStreamConfig(a.Flat(), []string{a.MailFilter()}, l)
}

// inboxStreamConfig is the one stream shape every inbox has; key is the directory key the stream is named for.
func inboxStreamConfig(key string, subjects []string, l Limits) jetstream.StreamConfig {
	return jetstream.StreamConfig{
		Name:              sap.InboxStream(key),
		Description:       "mash inbox for " + key,
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
// deletes from it: INV-002, a retired name is never reassigned, needs the marker to outlive the lease. The KV
// stream jetstream makes for it discards new messages when full (never an old marker); VerifyRetiredStatus refuses
// a bucket whose stream does otherwise.
func RetiredConfig() jetstream.KeyValueConfig {
	return jetstream.KeyValueConfig{Bucket: RetiredBucket, Description: "mash retired new-form identities",
		History: 1, Storage: jetstream.FileStorage, MaxBytes: RetiredMaxBytes}
}

// Retired is the value of one retired-name marker. It names the identity's three tokens and the user public key that
// was retired; it never holds a seed or any other secret. User is empty when the record could not be read.
type Retired struct {
	Owner     string `json:"owner"`
	Group     string `json:"group"`
	Instance  string `json:"instance"`
	User      string `json:"user,omitempty"`
	RetiredAt string `json:"retired_at"` // RFC 3339, UTC
	// State is RetiredPending while a retirement has started and not finished, and RetiredFinal once the record is gone.
	// A marker with no state (written before the two phases existed) counts as final.
	State string `json:"state,omitempty"`
}

// The states of a retired marker. Either one blocks provisioning of the name.
const (
	RetiredPending = "pending"
	RetiredFinal   = "final"
)

// IsPending reports whether the retirement this marker records was started and not finished.
func (r Retired) IsPending() bool { return r.State == RetiredPending }

// VerifyRetiredStatus is the fail-closed check of the retired-name bucket's configuration: a TTL would expire markers,
// a history other than RetiredConfig's is not a bucket mash made, and memory storage would lose every marker on a
// server restart. It never changes the bucket; the error tells the
// admin to fix or recreate it.
func VerifyRetiredStatus(st jetstream.KeyValueStatus) error {
	want := RetiredConfig()
	switch {
	case st.TTL() != 0:
		return fmt.Errorf("mash: bucket %s has a TTL of %s: a TTL would expire retired markers and let a retired name be reassigned (INV-002); fix or recreate the bucket without a TTL (mash does not change it)", RetiredBucket, st.TTL())
	case st.History() != int64(want.History):
		return fmt.Errorf("mash: bucket %s keeps history %d, not %d; fix or recreate the bucket (mash does not change it)", RetiredBucket, st.History(), want.History)
	}
	// A stream that evicts when full (discard old), or by message count or age, would drop a marker and free its name.
	// jetstream creates a KV stream with discard new (a full bucket refuses the write), which is what RetiredConfig gets.
	if bs, ok := st.(*jetstream.KeyValueBucketStatus); ok {
		c := bs.StreamInfo().Config
		switch {
		case c.Storage != jetstream.FileStorage:
			return fmt.Errorf("mash: bucket %s is stored in memory (storage %s, not file): a server restart would erase the retired markers and let a retired name be reassigned (INV-002); fix or recreate the bucket with file storage (mash does not change it)", RetiredBucket, c.Storage)
		case c.Discard != jetstream.DiscardNew:
			return fmt.Errorf("mash: bucket %s evicts its oldest messages when full (discard policy %s, not new): that could drop a retired marker and let a retired name be reassigned (INV-002); fix or recreate the bucket (mash does not change it)", RetiredBucket, c.Discard)
		case c.MaxAge != 0:
			return fmt.Errorf("mash: bucket %s expires messages after %s: that could drop a retired marker and let a retired name be reassigned (INV-002); fix or recreate the bucket (mash does not change it)", RetiredBucket, c.MaxAge)
		case c.MaxMsgs > 0:
			return fmt.Errorf("mash: bucket %s is capped at %d messages: that could evict a retired marker and let a retired name be reassigned (INV-002); fix or recreate the bucket (mash does not change it)", RetiredBucket, c.MaxMsgs)
		}
	}
	return nil
}

// Record is one directory entry (spec 6.1). Owner, Group and Instance are the endpoint's address (Address); Agent is
// the harness that runs it.
type Record struct {
	Owner       string `json:"owner"`
	Group       string `json:"group"`
	Instance    string `json:"instance"`
	Agent       string `json:"agent"`
	Account     string `json:"account"`
	User        string `json:"user"`
	MailSubject string `json:"mail_subject"`
	Created     string `json:"created"`
	Note        string `json:"note,omitempty"`
	// Role, Host and Labels are optional directory metadata: what the endpoint is for, the machine it is meant to run
	// on, and free-form key=value labels (for example a team, project or fleet). They are the identity on record; the
	// lease is the live view.
	Role   string            `json:"role,omitempty"`
	Host   string            `json:"host,omitempty"`
	Labels map[string]string `json:"labels,omitempty"`
	// Description is free text on what the endpoint does, and Caps are up to MaxCaps capability words (sorted), both
	// set by the admin.
	Description string   `json:"description,omitempty"`
	Caps        []string `json:"caps,omitempty"`
}

// Address is the endpoint address r names: sap.NewAddress(r.Owner, r.Group, r.Instance).
func (r Record) Address() (sap.Address, error) {
	return sap.NewAddress(r.Owner, r.Group, r.Instance)
}

// MailSubjectFor is the mail subject of the endpoint at a.
func MailSubjectFor(a sap.Address) string {
	return a.Subjects().Mail
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

// ParseRecord decodes a directory value and checks it is an endpoint of this release stored under key: a valid
// address (owner, group, instance), key equal to the address's flat key, the mail subject of that address, and a user.
// Any other value is refused.
func ParseRecord(key string, value []byte) (Record, error) {
	var r Record
	if err := json.Unmarshal(value, &r); err != nil {
		return Record{}, fmt.Errorf("directory %s: %w", key, err)
	}
	a, err := r.Address()
	if err != nil {
		return Record{}, fmt.Errorf("directory %s: not an endpoint of this release: %w", key, err)
	}
	if key != a.Flat() {
		return Record{}, fmt.Errorf("directory %s: not an endpoint of this release: the record must be keyed by its flat key %q", key, a.Flat())
	}
	if want := a.Subjects().Mail; r.MailSubject != want {
		return Record{}, fmt.Errorf("directory %s: not an endpoint of this release: mail subject %q is not %q", key, r.MailSubject, want)
	}
	if r.User == "" {
		return Record{}, fmt.Errorf("directory %s: not an endpoint of this release: no user", key)
	}
	return r, nil
}

// GetRecord reads the directory record under key (an endpoint's flat key).
func GetRecord(ctx context.Context, kv jetstream.KeyValue, key string) (Record, error) {
	e, err := kv.Get(ctx, key)
	if err != nil {
		return Record{}, fmt.Errorf("directory %s: %w", key, err)
	}
	return ParseRecord(key, e.Value())
}
