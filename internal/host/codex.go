package host

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bogworks/mash/internal/endpoint"
)

// Codex is the adapter for the Codex CLI. Codex runs hooks with Claude Code's contract (stdin JSON with
// hook_event_name and session_id, stdout hookSpecificOutput.additionalContext), so Trigger, Present and Notice are
// Claude's and are not repeated here. It ignores the MCP channel notification, so mail is spooled from the first
// message and an idle session is woken by running `codex queue` (WakeCommand).
//
// Ownership is Claude's rules with one change. Codex puts no session id in serve's environment: the id is the hook's
// stdin session_id and the thread id on each tools/call (_meta.threadId), which serve records in the owner record once
// it sees it. With one shared Codex app-server every thread's serve and hook descend from the app-server, so a hook
// cannot be tied to a session by its ancestry: rule 3 (process ancestry) would let one thread's hook drain another's
// mail. SessionSources.AncestryUnique is false, and Match skips rule 3 for it; the session-id and launch-token rules
// apply as before. A launch token (MASH_LAUNCH_TOKEN passed through env_vars) is therefore what identifies the session
// from the first prompt; without one the hook is recognised only once serve has seen the first tool call.
type Codex struct{ Claude }

func (Codex) Name() string { return "codex" }

// SessionSources: Codex sets no session variable that reaches serve; the hook's session_id is the thread id.
func (Codex) SessionSources() SessionSources {
	return SessionSources{Env: "", HookField: "session_id", AncestryUnique: false}
}

// Wake: Codex takes no channel notification; serve runs `codex queue`.
func (Codex) Wake() WakeKind { return WakeCommand }

// Words names Codex in notices and status lines.
func (Codex) Words() Words {
	return Words{Product: "Codex", Process: "Codex process", ServicePrefix: "Codex (mash): "}
}

// ClaudeSettingsChecks: Claude Code's settings files say nothing about Codex.
func (Codex) ClaudeSettingsChecks() bool { return false }

// SpoolsFromStart: Codex ignores the channel notification, so mail is spooled from the first message.
func (c Codex) SpoolsFromStart() bool { return c.Wake() != WakeChannel }

// OwnerToken is Claude's record with the host named, so a hook that cannot see the MCP client reads this adapter from
// it. serve has no session id yet: OwnerTokenFor adds it.
func (c Codex) OwnerToken(env func(string) string) string { return ownerTokenRec(c, env, c.Name(), "") }

// OwnerTokenFor is OwnerToken with the thread id serve has seen, which is the hook's session_id.
func (c Codex) OwnerTokenFor(env func(string) string, session string) string {
	return ownerTokenRec(c, env, c.Name(), session)
}

// Owns reports whether the hook may drain a spool recorded with owner; see Match.
func (c Codex) Owns(owner string, h HookIdentity) bool {
	ok, _ := c.Match(owner, h)
	return ok
}

// Match is Claude's Match without rule 3 (see Codex).
func (c Codex) Match(owner string, h HookIdentity) (bool, string) { return matchFor(c, owner, h) }

// Waker does nothing for the channel: Codex ignores it. serve uses WakerWith, which can reach `codex queue`.
func (Codex) Waker(func(string, map[string]string) error) func(string, map[string]string) error {
	return func(string, map[string]string) error { return nil }
}

// SessionOwner is an adapter whose owner record can carry a session id that serve learns after it bound the spool.
type SessionOwner interface {
	OwnerTokenFor(env func(string) string, session string) string
}

// WakeContext is what a command wake needs from serve.
type WakeContext struct {
	// Ctx ends the wake's commands when serve stops.
	Ctx context.Context
	// ThreadID is the session's thread id, or "" while none has been seen.
	ThreadID func() string
	// Env reads serve's environment (MASH_CODEX_BIN, MASH_CODEX_REMOTE).
	Env func(string) string
	// Stderr gets a command failure, once.
	Stderr io.Writer
}

// ContextWaker is an adapter whose wake needs more than the channel notification.
type ContextWaker interface {
	WakerWith(c WakeContext, notify func(text string, meta map[string]string) error) func(text string, meta map[string]string) error
}

// WakerFor is the delivery serve gives the endpoint for adapter a: a ContextWaker gets the context, every other
// adapter its Waker as before (Claude's and Pi's paths are unchanged).
func WakerFor(a Adapter, c WakeContext, notify func(text string, meta map[string]string) error) func(text string, meta map[string]string) error {
	if cw, ok := a.(ContextWaker); ok {
		return cw.WakerWith(c, notify)
	}
	return a.Waker(notify)
}

// CodexWakeTimeout bounds one `codex queue` run. A variable so tests can shorten it.
var CodexWakeTimeout = 15 * time.Second

// threadIDPattern is what a thread id may look like before it goes into an argument list: Codex's are UUIDs. Anything
// else (it comes from the client, and an argument starting with "-" would read as an option) is not used.
var threadIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

// WakerWith wakes the idle session with `codex queue --thread <id> --message <text>`, run as an argument list (never
// a shell). The text is mash's own and fixed: it carries at most the count of waiting messages, never a message body,
// a sender or any other text a sender controls; the mail itself reaches the model through the hook. With no thread id
// yet it fails (the endpoint retries at its next check) and nothing is run: the mail stays spooled. A command failure
// is written to Stderr once and is not fatal.
func (Codex) WakerWith(c WakeContext, _ func(string, map[string]string) error) func(string, map[string]string) error {
	var once sync.Once
	return func(text string, meta map[string]string) error {
		var thread string
		if c.ThreadID != nil {
			thread = c.ThreadID()
		}
		if thread == "" {
			return errors.New("codex wake skipped: no thread id seen yet (the model has not called a mash tool); the mail stays spooled")
		}
		if !threadIDPattern.MatchString(thread) {
			return errors.New("codex wake skipped: the thread id is not one codex queue can take")
		}
		bin := "codex"
		var remote string
		if c.Env != nil {
			if v := c.Env("MASH_CODEX_BIN"); v != "" {
				bin = v
			}
			remote = c.Env("MASH_CODEX_REMOTE")
		}
		args := []string{"queue", "--thread", thread, "--message", WakeText(text, meta)}
		if remote != "" && !strings.HasPrefix(remote, "-") {
			args = append(args, "--remote", remote)
		}
		parent := c.Ctx
		if parent == nil {
			parent = context.Background()
		}
		ctx, cancel := context.WithTimeout(parent, CodexWakeTimeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, args...)
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		cmd.WaitDelay = time.Second // a child that keeps the pipes open after the kill does not hold the wake
		if err := cmd.Run(); err != nil {
			if ctx.Err() == context.DeadlineExceeded {
				err = fmt.Errorf("timed out after %s", CodexWakeTimeout)
			}
			once.Do(func() {
				if c.Stderr != nil {
					fmt.Fprintf(c.Stderr, "mash: codex wake: %s queue failed: %v: %s (mail stays spooled; later wakes are tried quietly)\n", bin, err, firstLine(out.String()))
				}
			})
			return fmt.Errorf("codex queue: %w", err)
		}
		return nil
	}
}

// WakeText is the message `codex queue` is given. It is built here, from the count in the wake's meta and nothing
// else: the endpoint's own wake line names senders, and sender-controlled text must not become a user turn.
func WakeText(line string, meta map[string]string) string {
	n, err := strconv.Atoi(meta["count"])
	subject := "mash: mail is waiting for this endpoint."
	if err == nil && n > 0 && n < 1_000_000 {
		subject = fmt.Sprintf("mash: %d message(s) waiting for this endpoint.", n)
	}
	if strings.HasSuffix(line, endpoint.HooklessReadHint) { // mash's own constant: hooks are not delivering
		return subject + " " + endpoint.HooklessReadHint
	}
	return subject + " mash's hook delivers it with this prompt; if it does not appear, read it with the inbox tool."
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
