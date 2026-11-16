package credential

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/store"
)

// The answers of a question Show asks that may not be answerable.
const (
	StateYes     = "yes"
	StateNo      = "no"
	StateUnknown = "unknown"
)

// History is every issue time the record keeps of a key.
type History struct {
	Revisions int `json:"revisions"`
	// Latest is the newest revision's issued_at, Max the greatest; RFC 3339, UTC.
	Latest string `json:"latest"`
	Max    string `json:"max"`
	// Truncated is true when the history is at the bucket's cap, so the oldest revisions may have been dropped.
	Truncated bool `json:"truncated"`
}

// Connection says whether the key is connected now, on which servers, and whether the scan saw every server.
type Connection struct {
	// State is yes, no or unknown. A scan that did not see every server can say yes but never no.
	State   string   `json:"state"`
	Servers []string `json:"servers"`
	// Incomplete says why the scan is not the whole picture, "" when it is.
	Incomplete string `json:"incomplete,omitempty"`
}

// Report is what Show finds out about a public key, from the key alone: no creds file and no address.
type Report struct {
	PublicKey string `json:"public_key"`
	// Recorded is whether the issue record holds the key; Record is its latest entry.
	Recorded bool          `json:"recorded"`
	Record   *store.Issued `json:"record,omitempty"`
	History  *History      `json:"history,omitempty"`
	// AccountRevokes is yes, no or unknown: whether the account JWT of any server revokes the key.
	AccountRevokes string     `json:"account_revokes"`
	Connection     Connection `json:"connection"`
	// Notes says, for each question that could not be answered, why. A partial answer is not clean.
	Notes []string `json:"notes"`
	// Complete is true when every question was answered whole.
	Complete bool `json:"complete"`
	// Clean is true when Complete and the key is not both revoked (by the record or the account JWT) and connected.
	Clean bool `json:"clean"`
}

// Show reports what is known of publicKey: its issue record and history, whether the account JWT revokes it and whether
// it is connected now. A question that cannot be answered is reported as unknown with the reason in Notes, never
// passed over as a no. The error is not nil only for a public key that is not a user public key, or an unusable cfg.
func Show(ctx context.Context, cfg Config, publicKey string) (Report, error) {
	if !nkeys.IsValidPublicUserKey(publicKey) {
		return Report{}, errors.New("credential: not a NATS user public key")
	}
	if cfg.Records == nil || cfg.Revoked == nil || cfg.Scan == nil {
		return Report{}, fmt.Errorf("%w: Show needs the issue record, the account JWT and the connection scan", errConfig)
	}
	rep := Report{PublicKey: publicKey, AccountRevokes: StateUnknown, Connection: Connection{State: StateUnknown, Servers: []string{}}, Notes: []string{}}

	rec, found, err := cfg.Records.IssuedByKey(ctx, publicKey)
	recordKnown := err == nil
	switch {
	case err != nil:
		rep.Notes = append(rep.Notes, "the issue record could not be read: "+err.Error())
	case found:
		rep.Recorded, rep.Record = true, &rec
		t, hfound, herr := cfg.Records.IssuedHistoryByKey(ctx, publicKey)
		switch {
		case herr != nil:
			rep.Notes = append(rep.Notes, "the history of the key could not be read: "+herr.Error())
		case hfound:
			rep.History = historyOf(t)
		}
	}

	revoked, err := cfg.Revoked(ctx, publicKey)
	switch {
	case err != nil:
		rep.Notes = append(rep.Notes, "the account JWT could not be read from every server: "+err.Error())
	case revoked:
		rep.AccountRevokes = StateYes
	default:
		rep.AccountRevokes = StateNo
	}

	scan, err := cfg.Scan(ctx)
	switch {
	case err != nil:
		rep.Notes = append(rep.Notes, "the connections could not be read: "+err.Error())
	default:
		rep.Connection.Incomplete = scan.Incomplete
		for _, s := range scan.Conns {
			for _, c := range s.Conns {
				if c.Key == publicKey {
					rep.Connection.Servers = append(rep.Connection.Servers, s.Name)
					break
				}
			}
		}
		switch {
		case len(rep.Connection.Servers) > 0:
			rep.Connection.State = StateYes
		case scan.Incomplete == "":
			rep.Connection.State = StateNo
		}
		if scan.Incomplete != "" {
			rep.Notes = append(rep.Notes, "the connection scan is partial: "+scan.Incomplete)
		}
	}

	rep.Complete = recordKnown && rep.AccountRevokes != StateUnknown && rep.Connection.State != StateUnknown && rep.Connection.Incomplete == "" && len(rep.Notes) == 0
	recRevoked := rep.Record != nil && rep.Record.RevokedAt != ""
	rep.Clean = rep.Complete && !(rep.Connection.State == StateYes && (rep.AccountRevokes == StateYes || recRevoked))
	return rep, nil
}

func historyOf(t admin.IssuedTimes) *History {
	f := func(x time.Time) string { return x.UTC().Format(time.RFC3339) }
	return &History{Revisions: t.Revisions, Latest: f(t.Latest), Max: f(t.Max), Truncated: t.Truncated}
}
