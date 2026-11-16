package main

import (
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/host"
	"github.com/bogworks/mash/internal/mcp"
	"github.com/bogworks/mash/internal/spool"
)

// deliverySource is a session of adapter a that holds its spool since a minute ago. wakes channel notifications have
// been sent; hooked says a hook has run since the bind; refused says one was refused.
func deliverySource(t *testing.T, a host.Adapter, wakes int, hooked, refused bool) *statusSource {
	t.Helper()
	cfg := config.Config{Name: "a", Owner: "o", StateDir: t.TempDir()}
	dir, err := endpoint.SpoolDir(cfg)
	if err != nil {
		t.Fatal(err)
	}
	sp := spool.Spool{Dir: dir}
	if err := sp.Init(); err != nil {
		t.Fatal(err)
	}
	own := time.Now().Add(-time.Minute)
	if hooked {
		if err := sp.Touch(); err != nil {
			t.Fatal(err)
		}
	}
	if refused {
		if err := sp.NoteRefused(spool.Refusal{Time: time.Now().UTC(), Why: "session id differs"}); err != nil {
			t.Fatal(err)
		}
	}
	src := &statusSource{cfg: cfg, adapter: a, srv: &atomic.Pointer[mcp.Server]{}, state: &atomic.Pointer[sessionState]{}}
	src.owned.Store(&own)
	srv := mcp.New(strings.NewReader(""), io.Discard, "mash", "0", "", nil)
	for i := 0; i < wakes; i++ {
		if err := srv.Notify("wake", nil); err != nil {
			t.Fatal(err)
		}
	}
	src.srv.Store(srv)
	return src
}

func deliveryText(src *statusSource) string { return strings.Join(deliveryLines(src), "\n") }

// "the last wake started a turn" is said only when a wake was sent.
func TestDeliveryWakeClauseOnlyAfterAWake(t *testing.T) {
	const ran = "delivery: hooks have run in this session (last at "
	noWake := deliveryText(deliverySource(t, host.Claude{}, 0, true, false))
	if !strings.Contains(noWake, ran) || strings.Contains(noWake, "last wake started a turn") ||
		!strings.Contains(noWake, "); mail also arrives with tool results") {
		t.Errorf("no wake sent:\n%s", noWake)
	}
	woke := deliveryText(deliverySource(t, host.Claude{}, 1, true, false))
	if !strings.Contains(woke, "); the last wake started a turn; mail also arrives with tool results") {
		t.Errorf("a wake was sent:\n%s", woke)
	}
}

// Claude's channel line is printed and the REFUSED line is byte for byte what it was; Pi has no channel line and is
// told to launch pi.
func TestDeliveryLinesClaudeAndPi(t *testing.T) {
	claude := deliveryText(deliverySource(t, host.Claude{}, 0, false, true))
	wantC := "Next: start Claude Code with MASH_LAUNCH_TOKEN=$(uuidgen) claude ... (a fresh value per launch; one reused across launches defeats the check), and report your setup (see docs/operations.md)"
	if !strings.Contains(claude, "(session id differs, last at ") || !strings.Contains(claude, wantC) {
		t.Errorf("claude REFUSED line:\n%s", claude)
	}
	if !strings.Contains(claude, "delivery: channel notifications sent: 0 "+channelNote) {
		t.Errorf("claude has no channel line:\n%s", claude)
	}
	pi := deliveryText(deliverySource(t, host.Pi{}, 0, false, true))
	if !strings.Contains(pi, "Next: start Pi with MASH_LAUNCH_TOKEN=$(uuidgen) pi ... (a fresh value") {
		t.Errorf("pi REFUSED line:\n%s", pi)
	}
	if strings.Contains(pi, "claude") || strings.Contains(pi, "channel notifications") || strings.Contains(pi, "--channels") {
		t.Errorf("pi status speaks Claude:\n%s", pi)
	}
}

// The version tool's plugin line: Claude's is unchanged, Pi's says there is none.
func TestVersionTextPluginLinePerHost(t *testing.T) {
	c := versionText("a", "o", "", host.Claude{})
	if !strings.Contains(c, "\nplugin unknown (MASH_PLUGIN_ROOT is not set: an older plugin, or serve run by hand)\n") {
		t.Errorf("claude:\n%s", c)
	}
	p := versionText("a", "o", "", host.Pi{})
	if !strings.Contains(p, "\nplugin: none (host pi)\n") || strings.Contains(p, "MASH_PLUGIN_ROOT") {
		t.Errorf("pi:\n%s", p)
	}
}

// The help line names no one host.
func TestUsageServeLineIsHostNeutral(t *testing.T) {
	if !strings.Contains(usage, "  mash serve                                   the MCP server an agent host runs (Claude Code, Pi)\n") {
		t.Error("usage serve line")
	}
	if strings.Contains(usage, "the Claude Code channel server") {
		t.Error("usage still says Claude Code channel server")
	}
}
