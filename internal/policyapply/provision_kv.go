package policyapply

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// DefaultProvisionOpTimeout bounds each KV operation of KVProvisionMarkers when none is given.
const DefaultProvisionOpTimeout = 10 * time.Second

// provisionRetries is how many times a marker write retries after losing a compare-and-set.
const provisionRetries = 5

// KVProvisionMarkers keeps provisioning markers in the policy KV bucket, beside the ledger of one account, so they
// outlive the process.
//
// A marker is the key StatusKey(account)+".provisioning.<generation>.<sha256 of address, hex>" with a JSON value
// holding the address. The policy bucket is made by operations and may refuse Delete and Purge, so a marker is cleared
// by writing the tombstone {"cleared":true}; a tombstone is not a marker, and marking the address again replaces it.
type KVProvisionMarkers struct {
	kv     jetstream.KeyValue
	prefix string
	opTO   time.Duration
}

var _ ProvisionMarkers = (*KVProvisionMarkers)(nil)

type markerValue struct {
	Address string `json:"address,omitempty"`
	Cleared bool   `json:"cleared,omitempty"`
}

// NewKVProvisionMarkers returns the markers of account in kv, the bucket the KVLedger of the account uses. A zero
// opTimeout means DefaultProvisionOpTimeout.
func NewKVProvisionMarkers(kv jetstream.KeyValue, account string, opTimeout time.Duration) (*KVProvisionMarkers, error) {
	if kv == nil {
		return nil, errors.New("provisioning markers: no bucket")
	}
	if !accountRE.MatchString(account) {
		return nil, fmt.Errorf("provisioning markers: %q is not an account key", account)
	}
	if opTimeout <= 0 {
		opTimeout = DefaultProvisionOpTimeout
	}
	return &KVProvisionMarkers{kv: kv, prefix: StatusKey(account) + ".provisioning.", opTO: opTimeout}, nil
}

func (m *KVProvisionMarkers) genPrefix(generation uint64) string {
	return m.prefix + strconv.FormatUint(generation, 10) + "."
}

func (m *KVProvisionMarkers) key(address string, generation uint64) string {
	h := sha256.Sum256([]byte(address))
	return m.genPrefix(generation) + hex.EncodeToString(h[:])
}

func (m *KVProvisionMarkers) ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), m.opTO)
}

// MarkProvisioning implements ProvisionMarkers. It is idempotent.
func (m *KVProvisionMarkers) MarkProvisioning(address string, generation uint64) error {
	ctx, cancel := m.ctx()
	defer cancel()
	key := m.key(address, generation)
	val, err := json.Marshal(markerValue{Address: address})
	if err != nil {
		return err
	}
	for i := 0; i < provisionRetries; i++ {
		e, err := m.kv.Get(ctx, key)
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			_, err := m.kv.Create(ctx, key, val)
			if err == nil {
				return nil
			}
			if !errors.Is(err, jetstream.ErrKeyExists) {
				return fmt.Errorf("mark provisioning %s: %w", address, err)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("mark provisioning %s: %w", address, err)
		}
		var cur markerValue
		if json.Unmarshal(e.Value(), &cur) == nil && !cur.Cleared && cur.Address == address {
			return nil
		}
		_, err = m.kv.Update(ctx, key, val, e.Revision())
		if err == nil {
			return nil
		}
		if !errors.Is(err, jetstream.ErrKeyExists) {
			return fmt.Errorf("mark provisioning %s: %w", address, err)
		}
	}
	return fmt.Errorf("mark provisioning %s: too many conflicting writes", address)
}

// Provisioning implements ProvisionMarkers.
func (m *KVProvisionMarkers) Provisioning(generation uint64) ([]string, error) {
	ctx, cancel := m.ctx()
	defer cancel()
	lister, err := m.kv.ListKeysFiltered(ctx, m.genPrefix(generation)+"*")
	if err != nil {
		if errors.Is(err, jetstream.ErrNoKeysFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("list provisioning markers: %w", err)
	}
	var keys []string
	for k := range lister.Keys() {
		keys = append(keys, k)
	}
	_ = lister.Stop()
	var out []string
	for _, k := range keys {
		e, err := m.kv.Get(ctx, k)
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read provisioning marker: %w", err)
		}
		var v markerValue
		if err := json.Unmarshal(e.Value(), &v); err != nil {
			return nil, fmt.Errorf("read provisioning marker %s: unreadable value (counts as standing): %w", k, err)
		}
		if v.Cleared {
			continue
		}
		if v.Address == "" {
			return nil, fmt.Errorf("read provisioning marker %s: no address (counts as standing)", k)
		}
		out = append(out, v.Address)
	}
	sort.Strings(out)
	return out, nil
}

// ClearProvisioning implements ProvisionMarkers. It writes the tombstone; a marker that is not there is not an error.
func (m *KVProvisionMarkers) ClearProvisioning(address string, generation uint64) error {
	ctx, cancel := m.ctx()
	defer cancel()
	key := m.key(address, generation)
	tomb, _ := json.Marshal(markerValue{Cleared: true})
	for i := 0; i < provisionRetries; i++ {
		e, err := m.kv.Get(ctx, key)
		if errors.Is(err, jetstream.ErrKeyNotFound) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("clear provisioning %s: %w", address, err)
		}
		var cur markerValue
		if json.Unmarshal(e.Value(), &cur) == nil && cur.Cleared {
			return nil
		}
		_, err = m.kv.Update(ctx, key, tomb, e.Revision())
		if err == nil {
			return nil
		}
		if !errors.Is(err, jetstream.ErrKeyExists) {
			return fmt.Errorf("clear provisioning %s: %w", address, err)
		}
	}
	return fmt.Errorf("clear provisioning %s: too many conflicting writes", address)
}
