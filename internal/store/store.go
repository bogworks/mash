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
)

// LeaseTombstone is what Deprovision writes to an endpoint's lease key: it fences the name, so a live or
// starting session cannot take the lease back after its endpoint is gone. It expires with the bucket's TTL
// (LeaseTTL) like any lease, and Provision clears it.
func LeaseTombstone() []byte {
	return []byte(`{"deprovisioned":true,"since":"` + time.Now().UTC().Format(time.RFC3339) + `"}`)
}

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

// MaxLabels is the most labels one record may carry.
const MaxLabels = 8

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
			return fmt.Errorf("mash: label key %q: a letter, then up to 31 of a-z 0-9 _ . -", k)
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
			return fmt.Errorf("mash: capability %q: a letter, then up to 31 of a-z 0-9 _ . -", c)
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

// ParseRecord decodes a directory value and checks it names key.
func ParseRecord(key string, value []byte) (Record, error) {
	var r Record
	if err := json.Unmarshal(value, &r); err != nil {
		return Record{}, fmt.Errorf("directory %s: %w", key, err)
	}
	if r.Name != key || r.User == "" || r.MailSubject == "" {
		return Record{}, fmt.Errorf("directory %s: incomplete record", key)
	}
	if r.Zone != "" && !sap.ValidZone(r.Zone) {
		return Record{}, fmt.Errorf("directory %s: invalid zone %q", key, r.Zone)
	}
	if want := MailSubjectFor(r.Owner, r.Zone, r.Name); r.MailSubject != want {
		return Record{}, fmt.Errorf("directory %s: mail subject %q is not %q", key, r.MailSubject, want)
	}
	return r, nil
}

// GetRecord reads name's directory record.
func GetRecord(ctx context.Context, kv jetstream.KeyValue, name string) (Record, error) {
	e, err := kv.Get(ctx, name)
	if err != nil {
		return Record{}, fmt.Errorf("directory %s: %w", name, err)
	}
	return ParseRecord(name, e.Value())
}
