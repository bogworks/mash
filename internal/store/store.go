package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/randybias/mash/internal/sap"
)

// Inbox limits (spec 5.1).
const (
	InboxMaxAge   = 30 * 24 * time.Hour
	InboxMaxBytes = 16 << 20
	MaxMsgSize    = 1 << 20
	LeaseTTL      = 90 * time.Second
)

// InboxConfig is endpoint name's inbox stream. No subject transform: a stored
// Agent-Sender header must verify against the stored subject (SAP §13.8).
func InboxConfig(owner, name string) jetstream.StreamConfig {
	return jetstream.StreamConfig{
		Name:        sap.InboxStream(name),
		Description: "mash inbox for " + name,
		Subjects:    []string{sap.MailFilter(owner, name)},
		Storage:     jetstream.FileStorage,
		Retention:   jetstream.LimitsPolicy,
		MaxAge:      InboxMaxAge,
		MaxBytes:    InboxMaxBytes,
		MaxMsgs:     -1,
		MaxMsgSize:  MaxMsgSize,
		Discard:     jetstream.DiscardOld,
		Duplicates:  2 * time.Minute,
		DenyDelete:  true,
		DenyPurge:   true,
		AllowDirect: true,
		Replicas:    1,
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
func DirectoryConfig() jetstream.KeyValueConfig {
	return jetstream.KeyValueConfig{Bucket: DirectoryBucket, Description: "mash endpoint directory",
		History: 1, Storage: jetstream.FileStorage}
}

// LeaseConfig is the lease bucket: name -> holder, expiring unless refreshed.
func LeaseConfig() jetstream.KeyValueConfig {
	return jetstream.KeyValueConfig{Bucket: LeaseBucket, Description: "mash endpoint leases",
		History: 1, TTL: LeaseTTL, Storage: jetstream.FileStorage}
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
