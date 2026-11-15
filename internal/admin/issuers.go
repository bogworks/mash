package admin

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/store"
)

// IssuerProblem is one user whose JWT is signed by a signing key that does not stay in the account JWT.
type IssuerProblem struct {
	// Role is "admin" (the user of the admin credentials) or "exempt" (a connected user the adoption marker exempts).
	Role string `json:"role"`
	// User is the user's public key ("" for the admin user when its key is not known to the caller).
	User string `json:"user"`
	// Issuer is the signing key that signed the user's JWT: the one that does not stay.
	Issuer string `json:"issuer"`
}

// IssuersNotKeptError is returned by CheckIssuersKept: the users it names would stop working when the signing key that
// signed them leaves the account JWT. The fix is to reissue them under the account key first.
type IssuersNotKeptError struct {
	Problems []IssuerProblem
}

func (e *IssuersNotKeptError) Error() string {
	parts := make([]string, 0, len(e.Problems))
	for _, p := range e.Problems {
		who := p.Role + " user"
		if p.User != "" {
			who += " " + p.User
		}
		issuer := p.Issuer
		if issuer == "" {
			issuer = "(issuer unknown)"
		}
		parts = append(parts, fmt.Sprintf("%s is signed by %s", who, issuer))
	}
	return "mash: " + strings.Join(parts, "; ") + ", a signing key that does not stay in the account JWT: reissue them under the account key first"
}

// CheckIssuersKept refuses when the admin user, or any connected exempt user, is signed by a key that is not kept.
// account is the account's own identity key, which always counts as kept; kept holds the signing keys that stay: every
// scoped key the key store holds for an owner group. adminIssuer is the `iss` of the admin credentials' user JWT; exemptIssuers maps the public
// key of each connected exempt user to every issuer CONNZ reported for it, one per connection (two connections of one
// user may carry JWTs signed by different keys, and every one of them must be kept). An empty issuer is not known to be kept, so it
// is refused. The error is an *IssuersNotKeptError naming every user and key, sorted; nothing is changed.
func CheckIssuersKept(account, adminUser, adminIssuer string, exemptIssuers map[string][]string, kept map[string]bool) error {
	ok := func(issuer string) bool { return issuer != "" && (issuer == account || kept[issuer]) }
	var ps []IssuerProblem
	if !ok(adminIssuer) {
		ps = append(ps, IssuerProblem{Role: "admin", User: adminUser, Issuer: adminIssuer})
	}
	for user, issuers := range exemptIssuers {
		for _, issuer := range issuers {
			if !ok(issuer) {
				ps = append(ps, IssuerProblem{Role: "exempt", User: user, Issuer: issuer})
			}
		}
	}
	if len(ps) == 0 {
		return nil
	}
	slices.SortFunc(ps, func(a, b IssuerProblem) int {
		if c := strings.Compare(a.Role, b.Role); c != 0 {
			return c
		}
		if c := strings.Compare(a.User, b.User); c != 0 {
			return c
		}
		return strings.Compare(a.Issuer, b.Issuer)
	})
	return &IssuersNotKeptError{Problems: ps}
}

// IssuedBucketProof says whether the issue record can prove that no record of a user was lost, so that a user missing
// from it was never recorded: the bucket exists and is hardened as mash makes it (store.VerifyIssuedBucket: refuses
// delete, purge and rollup, no TTL, discards new messages when full), and its stream metadata carries the history
// marker store.IssuedMarkerKey as "full" or "raised:N:S". why says what is missing when ok is false. An error is a
// read that failed: the caller must not take it for either answer.
func (a *Admin) IssuedBucketProof(ctx context.Context) (ok bool, why string, err error) {
	if _, err := a.js.KeyValue(ctx, store.IssuedBucket); errors.Is(err, jetstream.ErrBucketNotFound) {
		return false, "there is no issue record bucket", nil
	} else if err != nil {
		return false, "", fmt.Errorf("mash: issue record bucket: %w", err)
	}
	if err := store.VerifyIssuedBucket(ctx, a.js); err != nil {
		if errors.Is(err, store.ErrIssuedBucket) {
			return false, err.Error(), nil
		}
		return false, "", err
	}
	s, err := a.js.Stream(ctx, store.IssuedStreamName)
	if err != nil {
		return false, "", fmt.Errorf("mash: issue record stream: %w", err)
	}
	info, err := s.Info(ctx)
	if err != nil {
		return false, "", fmt.Errorf("mash: issue record stream: %w", err)
	}
	m, perr := store.ParseIssuedMarker(info.Config.Metadata)
	switch {
	case perr != nil:
		return false, perr.Error(), nil
	case m.Kind == "":
		return false, "the issue record's stream has no " + store.IssuedMarkerKey + " marker (run `mash admin init`)", nil
	}
	return true, "", nil
}
