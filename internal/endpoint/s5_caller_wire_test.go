package endpoint

// The S5 seam (contract 0.6.0-sdk-adoption, amendment 5 (A) and (i)). Under (A) discovery, presence and the prompt
// sender stay in mash, so the "caller" the S5 acceptance tests drive is main's own code: an Endpoint over the asker's
// connection with the directory the test gives it, its presence probe (startPresence, awaitPresence, applyPresence),
// its recipient discovery (Endpoint.DiscoverRecipient, discoverSubject) and its prompt sender (Prompt). There is no
// second caller here, and no SDK caller.

import (
	"context"
	"io"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/sapsdk"
	"github.com/bogworks/mash/internal/sapsdk/discovery"
	"github.com/bogworks/mash/internal/store"
)

func init() { newS5Caller = newS5Main }

// s5On is the S5 switch-on: the verify step of internal/sapsdk/discovery, the secret presence reply subject, and the
// directory check in the recipient discovery of prompt. The endpoint package cannot import that package (0.5.x
// commands do not link it), so a test hands it in.
func s5On() *s5Hooks {
	return &s5Hooks{
		verify: func(reg registration, d *dirView) (string, bool) {
			return discovery.Verify(discovery.Registration{Subject: reg.Subject, Metadata: reg.Metadata},
				discovery.Directory{HasRecord: d.hasRec, User: d.rec.User, Owner: d.rec.Owner, Account: d.rec.Account})
		},
		secretPresence:  true,
		promptDirectory: true,
	}
}

// setS5Hook sets the switch for the test and restores what it was when the test ends. The switch is atomic, so a poller
// left over from an earlier test can read it while this one stores it.
func setS5Hook(t *testing.T, h *s5Hooks) {
	t.Helper()
	prev := s5Switch.Swap(h)
	t.Cleanup(func() { s5Switch.Store(prev) })
}

// s5Main drives main's discovery, presence and prompt sender over env.NC.
type s5Main struct {
	env s5Env
	ep  *Endpoint
}

func newS5Main(t *testing.T, env s5Env) s5Caller {
	t.Helper()
	if s5Hook() == nil { // a test that set the switch itself (the mutation of AT-010) keeps its own
		setS5Hook(t, s5On())
	}
	ep := &Endpoint{cfg: config.Config{Owner: env.Asker.Owner}, ident: env.Asker, nc: env.NC,
		log: log.New(io.Discard, "", 0), dir: map[string]store.Record{}, byUser: map[string]string{},
		lastSeen: map[string]time.Time{}}
	for name, r := range env.Directory {
		ep.dir[name] = store.Record{Owner: r.Address.Owner, Group: r.Address.Group, Instance: r.Address.Instance,
			Account: r.Account, User: r.User}
		ep.byUser[r.User] = name
	}
	return &s5Main{env: env, ep: ep}
}

// Discover is one presence probe: the request goes out, the window closes as main's does (PresenceQuiet after the
// last answer, PresenceCap after the request), and the answers are applied. The target is accepted when the probe
// counted a verified registration for it.
func (c *s5Main) Discover(ctx context.Context, target sap.Address) (s5Discovery, error) {
	before := c.ep.AnswersNotVerified()
	p := c.ep.startPresence()
	if err := c.ep.awaitPresence(ctx, p); err != nil {
		return s5Discovery{}, err
	}
	out := s5Discovery{NotVerified: c.ep.AnswersNotVerified() - before}
	for _, pr := range c.ep.Who() {
		if flatOf(pr.Record) == target.Flat() && pr.Online {
			out.Accepted = append(out.Accepted, s5Registration{Address: target, PromptSubject: mustSubj(target).Prompt, UserNKey: pr.Record.User})
		}
	}
	return out, nil
}

// Prompt is `mash prompt`: the recipient discovery, then main's prompt sender.
func (c *s5Main) Prompt(ctx context.Context, target sap.Address, text string) ([]string, error) {
	subj, err := c.ep.DiscoverRecipient(ctx, target.Flat())
	if err != nil {
		return nil, err
	}
	id, err := sapsdk.IdentityFromCredsAt("asker.creds", c.env.Creds) // Prompt takes the SDK identity (W1)
	if err != nil {
		return nil, err
	}
	var out strings.Builder
	if err := Prompt(ctx, c.env.NC, id, c.env.Name, subj, text, &out); err != nil {
		return nil, err
	}
	return []string{out.String()}, nil
}

// ReplySubjects: main does not keep the subjects it used; the tests take them from the server.
func (c *s5Main) ReplySubjects() []string { return nil }

func (c *s5Main) NotVerified() int { return c.ep.AnswersNotVerified() }

func (c *s5Main) Close() error { return nil }
