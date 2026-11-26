package endpoint

// The wire of the S5 acceptance seam (see s5_caller_at_helpers_test.go): newS5Caller returns mash's own caller of
// internal/sapsdk/discover over the asker's connection. It is a test file, so the endpoint package takes no new import
// in non-test code, and a 0.5.x binary does not link the new package (REQ-009).

import (
	"context"
	"testing"

	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/sapsdk/discover"
)

func init() { newS5Caller = newDiscoverCaller }

// s5Adapter is the seam over a discover.Caller.
type s5Adapter struct{ c *discover.Caller }

func newDiscoverCaller(t *testing.T, env s5Env) s5Caller {
	t.Helper()
	dir := discover.Directory{}
	for k, r := range env.Directory {
		dir[k] = discover.Record{Address: r.Address, User: r.User, Account: r.Account}
	}
	c, err := discover.New(discover.Config{
		NC: env.NC, Asker: env.Asker, Creds: env.Creds, Name: env.Name, Directory: dir, Window: env.Window,
		Now: env.Inputs.Now, Nonce: env.Inputs.Nonce,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &s5Adapter{c: c}
}

func (a *s5Adapter) Discover(ctx context.Context, target sap.Address) (s5Discovery, error) {
	d, err := a.c.Discover(ctx, target)
	out := s5Discovery{NotVerified: d.NotVerified}
	for _, r := range d.Accepted {
		out.Accepted = append(out.Accepted, s5Registration{Address: r.Address, PromptSubject: r.PromptSubject, UserNKey: r.UserNKey})
	}
	return out, err
}

func (a *s5Adapter) Prompt(ctx context.Context, target sap.Address, text string) ([]string, error) {
	return a.c.Prompt(ctx, target, text)
}

func (a *s5Adapter) ReplySubjects() []string { return a.c.ReplySubjects() }
func (a *s5Adapter) NotVerified() int        { return a.c.NotVerified() }
func (a *s5Adapter) Close() error            { return a.c.Close() }

// TestS5Wire_ConstantsMatchTheEndpoint: the new package cannot import this one, so it repeats the prompt limit and the
// refusal words; they must be the endpoint's.
func TestS5Wire_ConstantsMatchTheEndpoint(t *testing.T) {
	if discover.MaxPromptBytes != MaxPromptBytes || discover.PromptTooLargeCode != PromptTooLargeCode || discover.PromptTooLargeText != promptTooLarge {
		t.Fatalf("discover has %d %q %q, endpoint has %d %q %q", discover.MaxPromptBytes, discover.PromptTooLargeCode, discover.PromptTooLargeText,
			MaxPromptBytes, PromptTooLargeCode, promptTooLarge)
	}
}
