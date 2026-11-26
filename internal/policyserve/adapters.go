package policyserve

import (
	"context"
	"encoding/hex"
	"fmt"
	"slices"
	"time"

	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/policyapply"
	"github.com/bogworks/mash/internal/policydoc"
	"github.com/bogworks/mash/internal/policyintake"
)

// Decode is the intake's Decode: policydoc.Decode, its errors classified for the intake (policyintake.ErrDocument).
// The document is plain JSON, so submitter is not used.
func Decode(raw []byte, submitter string) (policyintake.Doc, error) {
	d, err := policydoc.Decode(raw, policydoc.DecodeOptions{})
	if err != nil {
		return policyintake.Doc{}, fmt.Errorf("%w: %v", policyintake.ErrDocument, err)
	}
	return policyintake.Doc{
		Account: d.Account, Range: d.Owner, Generation: d.Generation, BaseGeneration: d.BaseGeneration,
	}, nil
}

// InForce is the intake's InForce over a ledger: the hex SHA-256 of the bytes of the document in force, "" when none.
func InForce(l policyapply.Ledger) func(ctx context.Context) (string, error) {
	return func(context.Context) (string, error) {
		rec, err := l.InForce()
		if err != nil {
			return "", err
		}
		if rec.Generation == 0 {
			return "", nil
		}
		return hex.EncodeToString(rec.Received[:]), nil
	}
}

// Reporter is the intake's Reporter over a ledger that records refusals (policyapply.KVLedger).
type Reporter struct {
	Ledger policyapply.RefusalWriter
}

var _ policyintake.Reporter = Reporter{}

// Refused records ref as the latest status: state refused, with one refusal naming the stage and the detail.
func (r Reporter) Refused(ctx context.Context, ref policyintake.Refusal) error {
	at := ref.At
	if at.IsZero() {
		at = time.Now()
	}
	st := policyapply.Status{
		Generation: ref.Generation,
		State:      policyapply.StateRefused,
		Refusals:   []listset.Refusal{{Stage: ref.Stage, Entry: "document sha256:" + shortDigest(ref.Digest), Reason: ref.Detail}},
		UpdatedAt:  at.UTC().Format(time.RFC3339),
	}
	return r.Ledger.WriteRefusal(ctx, st)
}

// shortDigestLen is how much of a document's digest a refusal names.
const shortDigestLen = 16

func shortDigest(d string) string {
	if len(d) > shortDigestLen {
		return d[:shortDigestLen]
	}
	return d
}

// HeldKeys is the intake's HeldKeys: the public keys of the operator, system and account keys (held, the key store's
// HeldKeys), every owner group's signing key (groups) and the admin user's key. An error from either read is returned:
// the intake then refuses a planned rotation.
func HeldKeys(held func() (account string, m map[string]string, err error), groups func() (map[string]string, error), adminKey string) func() ([]string, error) {
	return func() ([]string, error) {
		_, m, err := held()
		if err != nil {
			return nil, err
		}
		gs, err := groups()
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(m)+len(gs)+1)
		for k := range m {
			out = append(out, k)
		}
		for _, k := range gs {
			out = append(out, k)
		}
		if adminKey != "" {
			out = append(out, adminKey)
		}
		slices.Sort(out)
		return slices.Compact(out), nil
	}
}

// BeforeWrite adapts the intake's Guard to ApplyDeps.BeforeWrite.
func BeforeWrite(g policyintake.Guard) func(ctx context.Context, what string) error {
	return func(ctx context.Context, what string) error { return g.Before(ctx, what) }
}
