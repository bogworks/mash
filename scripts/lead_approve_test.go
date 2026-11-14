package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A fake gh on PATH answers each call the script makes from FAKE_* variables and logs every call, so nothing here
// touches GitHub. It ignores --jq: the variables hold the value the real filter would print.
const fakeGH = `#!/usr/bin/env bash
echo "$*" >>"$FAKE_LOG"
case "$*" in
"repo view"*) echo bogworks/mash ;;
"api user"*) echo "$FAKE_LOGIN" ;;
"pr view"*headRefOid*)
	n=$(cat "$FAKE_LOG.heads" 2>/dev/null || echo 0); echo $((n+1)) >"$FAKE_LOG.heads"
	if [ "$n" -ge 1 ] && [ -n "${FAKE_HEAD_AFTER:-}" ]; then echo "$FAKE_HEAD_AFTER"; else echo "$FAKE_HEAD"; fi ;;
"pr view"*labels*) printf '%s' "${FAKE_LABELS:-}" ;;
"api repos/bogworks/mash/commits/"*/status\ --jq*) echo "${FAKE_CODEX:-success}" ;;
"api repos/bogworks/mash/commits/"*/check-runs\ --jq*) echo "${FAKE_RESET:-success}" ;;
"api --paginate repos/bogworks/mash/commits/"*/statuses*) printf '%s' "${FAKE_AUDIT:-}" ;;
"api -X POST repos/bogworks/mash/statuses/"*) ;;
*) echo "fake gh: unexpected call: $*" >&2; exit 99 ;;
esac
`

const sha1 = "1111111111111111111111111111111111111111"

// runLeadApprove runs scripts/lead-approve with the fake gh; env is extra KEY=VALUE pairs.
func runLeadApprove(t *testing.T, env []string, args ...string) (out string, calls string, code int) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(fakeGH), 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "calls.log")
	cmd := exec.Command("bash", "lead-approve")
	cmd.Args = append(cmd.Args, args...)
	cmd.Dir = "."
	cmd.Env = append([]string{"PATH=" + dir + ":" + os.Getenv("PATH"), "HOME=" + dir, "FAKE_LOG=" + log, "FAKE_HEAD=" + sha1}, env...)
	b, err := cmd.CombinedOutput()
	code = 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	c, _ := os.ReadFile(log)
	return string(b), string(c), code
}

func posted(calls string) bool { return strings.Contains(calls, "-X POST") }

func TestLeadApprove_refusesWithoutLeadLogin(t *testing.T) {
	out, calls, code := runLeadApprove(t, nil, "7")
	if code == 0 || !strings.Contains(out, "LEAD_LOGIN is not set") || posted(calls) {
		t.Errorf("code %d, out %q, calls %q: want a refusal naming LEAD_LOGIN and no post", code, out, calls)
	}
}

func TestLeadApprove_refusesOnWrongLogin(t *testing.T) {
	out, calls, code := runLeadApprove(t, []string{"LEAD_LOGIN=randybias", "FAKE_LOGIN=some-agent"}, "7")
	if code == 0 || !strings.Contains(out, "not LEAD_LOGIN") || posted(calls) {
		t.Errorf("code %d, out %q, calls %q: want a refusal and no post", code, out, calls)
	}
}

func TestLeadApprove_refusesWhenCodexReviewNotSuccess(t *testing.T) {
	out, calls, code := runLeadApprove(t, []string{"LEAD_LOGIN=randybias", "FAKE_LOGIN=randybias", "FAKE_CODEX=pending"}, "7")
	if code == 0 || !strings.Contains(out, "codex-review is 'pending'") || posted(calls) {
		t.Errorf("code %d, out %q, calls %q: want a refusal and no post", code, out, calls)
	}
}

func TestLeadApprove_refusesWhenLabelResetNotSuccess(t *testing.T) {
	out, calls, code := runLeadApprove(t, []string{"LEAD_LOGIN=randybias", "FAKE_LOGIN=randybias", "FAKE_RESET=none"}, "7")
	if code == 0 || !strings.Contains(out, "label-reset is 'none'") || posted(calls) {
		t.Errorf("code %d, out %q, calls %q: want a refusal and no post", code, out, calls)
	}
}

func TestLeadApprove_refusesOnHold(t *testing.T) {
	out, calls, code := runLeadApprove(t, []string{"LEAD_LOGIN=randybias", "FAKE_LOGIN=randybias", "FAKE_LABELS=docs\nhold\n"}, "7")
	if code == 0 || !strings.Contains(out, "hold label") || posted(calls) {
		t.Errorf("code %d, out %q, calls %q: want a refusal and no post", code, out, calls)
	}
}

func TestLeadApprove_postsTheStatusOnTheExactSHA(t *testing.T) {
	out, calls, code := runLeadApprove(t, []string{"LEAD_LOGIN=randybias", "FAKE_LOGIN=randybias", "FAKE_LABELS=docs\n"}, "7")
	if code != 0 {
		t.Fatalf("code %d, out %q", code, out)
	}
	for _, want := range []string{"-X POST repos/bogworks/mash/statuses/" + sha1, "state=success", "context=lead-review", "PR #7"} {
		if !strings.Contains(calls, want) {
			t.Errorf("calls lack %q:\n%s", want, calls)
		}
	}
}

func TestLeadApprove_warnsWhenTheHeadMoved(t *testing.T) {
	moved := strings.Repeat("2", 40)
	out, calls, code := runLeadApprove(t, []string{"LEAD_LOGIN=randybias", "FAKE_LOGIN=randybias", "FAKE_HEAD_AFTER=" + moved}, "7")
	if code == 0 || !strings.Contains(out, "WARNING") || !strings.Contains(out, moved) {
		t.Errorf("code %d, out %q: want a warning naming the new head and a non-zero exit", code, out)
	}
	if !strings.Contains(calls, "statuses/"+sha1) || strings.Contains(calls, "statuses/"+moved) {
		t.Errorf("the status must go on the reviewed SHA only:\n%s", calls)
	}
}

func TestLeadApprove_auditFlagsAForeignCreator(t *testing.T) {
	rows := "success\trandybias\t2026-11-14T10:00:00Z\nsuccess\tsome-agent\t2026-11-14T11:00:00Z\n"
	out, calls, code := runLeadApprove(t, []string{"LEAD_LOGIN=randybias", "FAKE_AUDIT=" + rows}, "--audit", "7")
	if code == 0 || !strings.Contains(out, "FOREIGN  success by some-agent") || !strings.Contains(out, "ok       success by randybias") {
		t.Errorf("code %d, out %q: want the foreign creator flagged and a non-zero exit", code, out)
	}
	if posted(calls) {
		t.Errorf("--audit must not post:\n%s", calls)
	}
	rows = "success\trandybias\t2026-11-14T10:00:00Z\n"
	if out, _, code = runLeadApprove(t, []string{"LEAD_LOGIN=randybias", "FAKE_AUDIT=" + rows}, "--audit", "7"); code != 0 {
		t.Errorf("only the lead's status: code %d, out %q, want 0", code, out)
	}
}
