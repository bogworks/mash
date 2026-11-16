package credential

// The outcomes of Issue.
const (
	// OutcomeIssued: a credential was signed and recorded (exit 0).
	OutcomeIssued = "issued"
	// OutcomeRefused: the request, the policy or the key's record refuses it; nothing was signed or recorded (exit 1).
	OutcomeRefused = "refused"
	// OutcomeError: the configuration is unusable (exit 6).
	OutcomeError = "error"
)

// Result is what Issue reports. It is plain data and encodes as JSON as it is. Address and PublicKey are echoed only
// when they have the shape of an address and a user public key, so a seed put in the wrong field is never in a Result.
type Result struct {
	Outcome   string   `json:"outcome"`
	Address   string   `json:"address,omitempty"`
	PublicKey string   `json:"public_key,omitempty"`
	JWT       string   `json:"jwt,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	// Generation is the generation of the list set the credential was issued under.
	Generation int `json:"generation,omitempty"`
	// IssuedAt is RFC 3339, UTC.
	IssuedAt string   `json:"issued_at,omitempty"`
	Refusal  *Refusal `json:"refusal,omitempty"`
}

// ExitCode is the exit-code group of the outcome: 0 issued, 1 refused, 6 error and anything unknown.
func (r Result) ExitCode() int {
	switch r.Outcome {
	case OutcomeIssued:
		return 0
	case OutcomeRefused:
		return 1
	}
	return 6
}

// The outcomes of a revoke, besides OutcomeRefused and OutcomeError.
const (
	// OutcomeRevoked: the key is revoked in the account JWT and every server asked acknowledged it (exit 0).
	OutcomeRevoked = "revoked"
	// OutcomeUnchanged: the account JWT already revoked the key (exit 0).
	OutcomeUnchanged = "unchanged"
	// OutcomePartial: the revocation reached fewer servers than were asked for; running it again is safe (exit 5).
	OutcomePartial = "partial"
)

// RefusalNotFound is the refusal code of a revoke of a key with no issue record that no server's account JWT revokes.
const RefusalNotFound = "not_found"

// RevokeResult is what `mashctl credential revoke` reports. PublicKey is echoed only when it is a user public key.
type RevokeResult struct {
	Outcome             string   `json:"outcome"`
	PublicKey           string   `json:"public_key,omitempty"`
	ServersAcknowledged int      `json:"servers_acknowledged"`
	Refusal             *Refusal `json:"refusal,omitempty"`
}

// ExitCode is the exit-code group of the outcome: 0 revoked or unchanged, 1 refused, 5 partial, 6 error and anything
// unknown.
func (r RevokeResult) ExitCode() int {
	switch r.Outcome {
	case OutcomeRevoked, OutcomeUnchanged:
		return 0
	case OutcomeRefused:
		return 1
	case OutcomePartial:
		return 5
	}
	return 6
}
