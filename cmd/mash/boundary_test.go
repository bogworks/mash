package main

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/host"
	"github.com/bogworks/mash/internal/render"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/spool"
)

// Boundary tests for the hook's output (issue #48): Claude Code truncates additionalContext over 10,000 UTF-16
// units to a preview plus a file path, so a hook run must never print more, never cut a surrogate pair or a rune,
// and never mark as drained a message whose body it did not print in full. The tests use the real hook entry point
// (run "hook"), the helpers of hook_test.go and hook_crash_test.go, and a spool filled directly.
//
// The 10-second hook timeout (plugin/hooks/hooks.json, "timeout": 10) is NOT simulated here: a slow disk cannot be
// made reproducible and a test that sleeps 10 s breaks the 30 s budget. What the hook does when Claude Code kills it
// at the timeout is the same as for any kill: "killed, mail stays spooled and un-acked". A message is marked drained
// only after the batch was printed, and acked by serve only after that, so a hook killed before, between or after the
// print and the drained marks shows the batch again (marked re-delivered) and never loses it. That is covered by
// TestHookCrashMatrix (hook_crash_test.go: SIGKILL at MASH_HOOK_FAILPOINT before-print, after-print and
// after-first-drained) and TestHookShownOnceAcrossAServeKilledBeforeAck (hook_chain_test.go).

var (
	listedLine = regexp.MustCompile(`(?m)^#(\d+)\tfrom `)
	// A UTF-16 surrogate written as a JSON escape: valid JSON, but half of a pair is a lone surrogate to a reader that
	// does not join it with its other half.
	surrogateEscape = regexp.MustCompile(`\\u[dD][89a-fA-F][0-9a-fA-F]{2}`)
)

// alphabets are the body texts of the Unicode cases: the rune, its UTF-16 units and its UTF-8 bytes.
var alphabets = []struct {
	name  string
	runes string // cycled through to build a body
	units int    // UTF-16 units of one rune of runes (all of its runes are alike)
}{
	{"emoji", "😀🚀🎉", 2},    // astral: a surrogate pair, 4 bytes
	{"cjk", "漢字仮名交じり文", 1}, // BMP: 1 unit, 3 bytes
}

// unitsBody is a one-line body of exactly want UTF-16 units: a leading "x", then runes of alpha (cycled), then "x"
// padding for what a whole rune cannot fill.
func unitsBody(alpha string, units, want int) string {
	rs := []rune(alpha)
	var b strings.Builder
	b.WriteString("x")
	left := want - 1
	for i := 0; left >= units; i++ {
		b.WriteRune(rs[i%len(rs)])
		left -= units
	}
	b.WriteString(strings.Repeat("x", left))
	return b.String()
}

// mixedBody is unitsBody over a text that mixes ASCII, CJK and emoji, so a cut would have to be rune-safe at every width.
func mixedBody(want int) string {
	var b strings.Builder
	b.WriteString("x")
	left := want - 1
	for i := 0; left > 0; i++ {
		switch r := []rune("a漢😀b字🚀")[i%6]; {
		case r > 0xFFFF && left >= 2:
			b.WriteRune(r)
			left -= 2
		case r > 0xFFFF:
			b.WriteString("x")
			left--
		default:
			b.WriteRune(r)
			left--
		}
	}
	return b.String()
}

func entries(t *testing.T, sp spool.Spool, bodies []string, urgentLast bool) {
	t.Helper()
	for i, body := range bodies {
		e := spool.Entry{Seq: uint64(i + 1), From: "b", Trust: "verified", At: time.Now(), Body: []byte(body), Urgent: urgentLast && i == len(bodies)-1}
		if err := sp.Put(e); err != nil {
			t.Fatal(err)
		}
	}
}

func hookEnv(t *testing.T, extra map[string]string) map[string]string {
	env := map[string]string{"HOME": t.TempDir(), "MASH_NAME": "a", "MASH_OWNER": "o", "MASH_URL": "nats://127.0.0.1:1", "MASH_CREDS": "/none"}
	for k, v := range extra {
		env[k] = v
	}
	return env
}

// hookRaw runs `mash hook` and returns its raw stdout and the additionalContext in it.
func hookRaw(t *testing.T, env map[string]string) (raw, ctx string) {
	t.Helper()
	var out, errb bytes.Buffer
	if code := run([]string{"hook"}, envFrom(env), strings.NewReader(`{"hook_event_name":"PostToolUse"}`), &out, &errb); code != 0 {
		t.Fatalf("hook exited %d: %s", code, errb.String())
	}
	return out.String(), hookContext(t, out.Bytes())
}

// checkContext is what Claude Code needs of one run's output.
func checkContext(t *testing.T, raw, ctx string) {
	t.Helper()
	if n := host.Utf16Len(ctx); n > 10000 {
		t.Fatalf("context is %d UTF-16 units, over Claude Code's 10,000 (%d runes, %d bytes)", n, len([]rune(ctx)), len(ctx))
	}
	// The hook's own budget leaves a margin under the cap; only a single entry may pass it (nothing is left to drop).
	if n := host.Utf16Len(ctx); n > 9500 && strings.Count(ctx, "\n#")+strings.Count(ctx, "--- #") > 1 {
		t.Fatalf("context is %d UTF-16 units, over the hook's own 9,500 budget with more than one message", n)
	}
	if !utf8.ValidString(raw) || !utf8.ValidString(ctx) || strings.ContainsRune(ctx, utf8.RuneError) {
		t.Fatal("the output is not valid UTF-8, or has a replacement character: something was cut mid-rune")
	}
	if surrogateEscape.MatchString(raw) {
		t.Fatalf("the JSON output escapes a surrogate: %q", surrogateEscape.FindString(raw))
	}
	// Re-encoded as UTF-16 (as Claude Code reads it) and back, nothing changes: no lone surrogate, no cut pair.
	rs := []rune(ctx)
	if back := utf16.Decode(utf16.Encode(rs)); string(back) != ctx {
		t.Fatal("the context does not survive a UTF-16 round trip: a lone surrogate or a cut pair")
	}
	if got := len(utf16.Encode(rs)); got != host.Utf16Len(ctx) {
		t.Fatalf("Utf16Len %d disagrees with the real UTF-16 length %d", host.Utf16Len(ctx), got)
	}
}

// checkPreviews: each digest line's preview is the first line of its body, cut at 119 runes plus "…" when longer than
// 120, and the cut never splits a rune.
func checkPreviews(t *testing.T, ctx string, bodies []string) {
	t.Helper()
	for _, l := range strings.Split(ctx, "\n") {
		m := listedLine.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		seq, _ := strconv.Atoi(m[1])
		f := strings.Split(l, "\t") // #seq, from, size, preview, kind
		if len(f) < 5 {
			t.Fatalf("digest line %q has %d fields", l, len(f))
		}
		preview := f[3]
		if !utf8.ValidString(preview) {
			t.Fatalf("#%d preview is not valid UTF-8", seq)
		}
		first := []rune(bodies[seq-1]) // the bodies here are one line
		want := string(first)
		if len(first) > 120 {
			want = string(first[:119]) + "…"
		}
		if preview != want {
			t.Fatalf("#%d preview %q, want %q", seq, preview, want)
		}
	}
}

// Emoji, CJK and mixed text at and past Claude Code's cap. Each case spools n messages whose full rendering is exactly
// target UTF-16 units, runs the hook until the spool has nothing to show, and checks every run: never over 10,000
// units, never cut mid-rune or mid-pair, every message shown in full (drained) or listed in a digest that says to
// read it (listed, still unacked), or else not printed at all and still waiting; across the runs each message is
// printed exactly once. bytes is MASH_BATCH_BYTES: the default (16384) sends big UTF-8 bodies to a digest by size
// alone, and a large value leaves the UTF-16 count as the only reason to fall back from the full form.
func TestBoundaryUnicodeAtTheHookCap(t *testing.T) {
	type body func(want int) string
	bodyOf := map[string]body{
		"mixed": mixedBody,
	}
	for _, a := range alphabets {
		bodyOf[a.name] = func(want int) string { return unitsBody(a.runes, a.units, want) }
	}
	// targets straddle the hook's 9,500 budget, the 10,000 cap and a batch well past it.
	targets := []int{9400, 9499, 9500, 9501, 9600, 9999, 10000, 10001, 10500}
	for _, kind := range []string{"emoji", "cjk", "mixed"} {
		for _, n := range []int{1, 3} {
			for _, bb := range []string{"", "1048576"} {
				for _, target := range targets {
					name := fmt.Sprintf("%s/%dmsg/bytes=%s/%d", kind, n, bb, target)
					t.Run(name, func(t *testing.T) {
						extra := map[string]string{}
						if bb != "" {
							extra["MASH_BATCH_BYTES"] = bb
						}
						env := hookEnv(t, extra)
						sp := spoolOf(env)
						if err := sp.Init(); err != nil {
							t.Fatal(err)
						}
						// Calibrate: the frame and per-message header cost is the rendering of n one-unit bodies; the
						// rest is spread over the bodies so that the whole is exactly target units.
						calib := make([]string, n)
						for i := range calib {
							calib[i] = "x"
						}
						overhead := renderedUnits(t, calib) - n
						bodies := make([]string, n)
						for i := range bodies {
							share := (target - overhead) / n
							if i < (target-overhead)%n {
								share++
							}
							bodies[i] = bodyOf[kind](share)
						}
						if got := renderedUnits(t, bodies); got != target {
							t.Fatalf("calibration: the full rendering is %d units, wanted %d", got, target)
						}
						entries(t, sp, bodies, false)
						seen := map[int]int{} // seq -> runs that printed it
						for runs := 1; ; runs++ {
							if runs > n+2 {
								t.Fatalf("still output after %d runs for %d messages", runs, n)
							}
							raw, ctx := hookRaw(t, env)
							if ctx == "" {
								break
							}
							checkContext(t, raw, ctx)
							t.Logf("run %d: %d units, digest %v, shown %v", runs, host.Utf16Len(ctx), strings.Contains(ctx, ", digest, "), seqsShown(ctx))
							checkPreviews(t, ctx, bodies)
							for _, s := range seqsShown(ctx) {
								seen[s]++
							}
							for _, m := range listedLine.FindAllStringSubmatch(ctx, -1) {
								s, _ := strconv.Atoi(m[1])
								seen[s]++
							}
							for i, b := range bodies {
								seq, id := uint64(i+1), strconv.Itoa(i+1)
								full := strings.Contains(ctx, "\n--- #"+id+" ") || strings.HasPrefix(ctx, "--- #"+id+" ")
								listed := strings.Contains(ctx, "\n#"+id+"\tfrom ")
								switch {
								case full && listed:
									t.Fatalf("#%d is both shown in full and listed", seq)
								case full && (!strings.Contains(ctx, "\n> "+b) || !sp.IsDrained(spool.Ref{Seq: seq})):
									t.Fatalf("#%d is shown with a truncated body, or not drained", seq)
								case listed && (!sp.IsListed(spool.Ref{Seq: seq}) || sp.IsDrained(spool.Ref{Seq: seq}) || !sp.Has(spool.Ref{Seq: seq})):
									t.Fatalf("#%d is listed in a digest but not marked listed and unread", seq)
								case listed && !strings.Contains(ctx, "Read each with the read tool (#seq); they stay unread until you do."):
									t.Fatalf("a digest lists #%d without the read guidance:\n%.300s", seq, ctx)
								}
							}
						}
						for i := 1; i <= n; i++ {
							if seen[i] != 1 {
								t.Fatalf("#%d was printed %d times, want exactly once (seen %v)", i, seen[i], seen)
							}
							seq := uint64(i)
							if sp.IsDrained(spool.Ref{Seq: seq}) == sp.IsListed(spool.Ref{Seq: seq}) {
								t.Fatalf("#%d: drained %v listed %v: want exactly one", i, sp.IsDrained(spool.Ref{Seq: seq}), sp.IsListed(spool.Ref{Seq: seq}))
							}
						}
					})
				}
			}
		}
	}
}

// renderedUnits is the UTF-16 length of the full form of bodies as one batch (what Present builds first, before
// it falls back to a digest), with no byte limit. It does not touch a spool.
func renderedUnits(t *testing.T, bodies []string) int {
	t.Helper()
	var ms []render.Mail
	var ts []sap.Trust
	for i, b := range bodies {
		e := spool.Entry{Seq: uint64(i + 1), From: "b", Trust: "verified", At: time.Unix(0, 0), Body: []byte(b)}
		m := endpoint.MailOf(e)
		ms, ts = append(ms, m), append(ts, m.Trust)
	}
	text := endpoint.HookFrame("a", ts) + "\n" + render.HookBatch("a", "o", ms, 10, 1<<30)
	if strings.Contains(text, ", digest, ") {
		t.Fatalf("calibration rendered a digest: %.200s", text)
	}
	return host.Utf16Len(text)
}

// A burst past SpoolWakeCount with one urgent message behind it. The hook side: Present shows urgent entries first
// (each group in seq order), so the urgent message, the newest, is the first shown in the first run, ahead of #1; the
// runs after it take the fyi mail in seq order, BatchMsgs (10) at a time; and across the drain all 601 messages are
// shown exactly once and none is left. (The wake side is TestBoundaryBurstWakesOnce in internal/endpoint.)
func TestBoundaryBurstDrainShowsUrgentFirstAndEachOnce(t *testing.T) {
	const fyi = 600
	env := hookEnv(t, nil)
	sp := spoolOf(env)
	if err := sp.Init(); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= fyi+1; i++ {
		e := spool.Entry{Seq: uint64(i), From: "b", Trust: "verified", At: time.Now(), Body: []byte("m" + strconv.Itoa(i)), Kind: "fyi", Urgent: i == fyi+1}
		if i == fyi+1 {
			e.Kind = "task"
		}
		if err := sp.Put(e); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[int]int{}
	var first []int
	runs := 0
	for ; ; runs++ {
		if runs > 70 {
			t.Fatalf("still output after %d runs", runs)
		}
		ctx := runHook(t, env, "PostToolUse")
		if ctx == "" {
			break
		}
		shown := seqsShown(ctx)
		if host.Utf16Len(ctx) > 10000 || len(shown) == 0 || len(shown) > 10 || strings.Contains(ctx, ", digest, ") {
			t.Fatalf("run %d: %d messages, %d units:\n%.300s", runs, len(shown), host.Utf16Len(ctx), ctx)
		}
		if runs == 0 {
			first = shown
		}
		for _, s := range shown {
			seen[s]++
		}
	}
	// Present's order: urgent first, then seq order. The urgent message is #601 and leads the first run.
	want := []int{601, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	if fmt.Sprint(first) != fmt.Sprint(want) {
		t.Fatalf("first run shows %v, want %v (urgent first, then oldest first)", first, want)
	}
	if runs != 61 {
		t.Fatalf("%d runs, want 61 (601 messages, 10 per run)", runs)
	}
	for i := 1; i <= fyi+1; i++ {
		if seen[i] != 1 {
			t.Fatalf("#%d shown %d times, want once", i, seen[i])
		}
		if !sp.IsDrained(spool.Ref{Seq: uint64(i)}) {
			t.Fatalf("#%d shown but not drained", i)
		}
	}
	if w, _ := sp.Waiting(); len(w) != 0 {
		t.Fatalf("%d messages still waiting after the drain", len(w))
	}
}
