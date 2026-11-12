package policyapply

import (
	"encoding/json"
	"sort"
	"time"

	"github.com/bogworks/mash/internal/listset"
)

// State is where a generation stands.
type State string

// The four states. Gate gives applied or partial; Run gives refused and applying.
const (
	// StateRefused: the list set was refused before any push.
	StateRefused State = "refused"
	// StateApplying: a push is in progress.
	StateApplying State = "applying"
	// StateApplied: every check holds and every server holds the generation.
	StateApplied State = "applied"
	// StatePartial: not applied. Servers may differ, or a check failed.
	StatePartial State = "partial"
)

// The checks a Failure names.
const (
	CheckServerSet      = "server_set"
	CheckGeneration     = "generation"
	CheckRevoked        = "revoked"
	CheckWitness        = "witness"
	CheckRefused        = "refused"
	CheckRefusedCurrent = "refused_current"
	CheckRollback       = "rollback"
)

// ServerGeneration is the generation one server holds; Generation is nil (JSON null) when it is unknown.
type ServerGeneration struct {
	Server     string  `json:"server"`
	Generation *uint64 `json:"generation"`
}

// Failure is one failed check. Server or Sender is empty when the check does not name one.
type Failure struct {
	Server string `json:"server"`
	Sender string `json:"sender"`
	Check  string `json:"check"`
	Detail string `json:"detail"`
}

// Status is the applied-policy report. InForce is true only when State is applied: otherwise no account-wide policy
// is in force. Its JSON is stable: snake_case, slices never null and sorted.
type Status struct {
	Generation   uint64             `json:"generation"`
	Content      string             `json:"content"`
	Signed       string             `json:"signed"`
	State        State              `json:"state"`
	Refusals     []listset.Refusal  `json:"refusals"`
	Servers      []ServerGeneration `json:"servers"`
	NotConnected []string           `json:"not_connected"`
	Failing      []Failure          `json:"failing"`
	InForce      bool               `json:"in_force"`
	UpdatedAt    string             `json:"updated_at"`
}

// MarshalJSON writes the stable form: nil slices become [] and the slices are sorted.
func (s Status) MarshalJSON() ([]byte, error) {
	type plain Status
	p := plain(s)
	p.Refusals = append([]listset.Refusal{}, s.Refusals...)
	p.Servers = append([]ServerGeneration{}, s.Servers...)
	sort.SliceStable(p.Servers, func(i, j int) bool { return p.Servers[i].Server < p.Servers[j].Server })
	p.NotConnected = append([]string{}, s.NotConnected...)
	sort.Strings(p.NotConnected)
	p.Failing = append([]Failure{}, s.Failing...)
	sortFailures(p.Failing)
	return json.Marshal(p)
}

func sortFailures(f []Failure) {
	sort.SliceStable(f, func(i, j int) bool {
		a, b := f[i], f[j]
		if a.Server != b.Server {
			return a.Server < b.Server
		}
		if a.Sender != b.Sender {
			return a.Sender < b.Sender
		}
		if a.Check != b.Check {
			return a.Check < b.Check
		}
		return a.Detail < b.Detail
	})
}

func newStatus(rec listset.Record, state State, now func() time.Time) Status {
	if now == nil {
		now = time.Now
	}
	return Status{
		Generation: rec.Generation,
		Content:    rec.Content.String(),
		Signed:     rec.Signed.String(),
		State:      state,
		UpdatedAt:  now().UTC().Format(time.RFC3339),
	}
}
