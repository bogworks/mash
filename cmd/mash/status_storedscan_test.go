package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/cmdcore"
	"github.com/bogworks/mash/internal/cmdcore/cmds"
)

// TO-14 and CHG-16: while the first start of the switched-on build checks the stored mail, `mash status` is not ready in
// main's form, with its own reason, exit 1 and the hint mash doctor; doctor's delivery row has the same words and no fix.
// When the scan is over the same session is ready. Needs a server (the checks sign in): run in a container.
func TestStatusAndDoctorSayStoredMailIsBeingChecked(t *testing.T) {
	w := tlsWorld(t, "a")
	scanning := true
	h := &healthSvc{cfg: w.cfgOf(t, "a"), session: true, scanning: func() bool { return scanning }}
	env := func(context.Context) (*cmdcore.Env, error) { return &cmdcore.Env{Svc: h}, nil }
	status := func() (int, string, string) {
		var o, e bytes.Buffer
		code := cmdcore.CLI(cmds.Status, env)(nil, strings.NewReader(""), &o, &e, cliframe.Global{})
		return code, o.String(), e.String()
	}
	code, out, errs := status()
	if code != 1 || !strings.Contains(out, ": not ready: mail is waiting while stored mail is checked\n") || !strings.Contains(errs, "hint: mash doctor") {
		t.Fatalf("status during the scan: exit %d\nstdout: %q\nstderr: %q", code, out, errs)
	}
	if rc := sessionDetail(t, &healthSvc{cfg: w.cfgOf(t, "a"), scanning: func() bool { return true }}); rc != "unknown" {
		t.Fatalf("reason_code %q: the reason says what was seen, with the specification's list unchanged", rc)
	}
	dcode, dout, _ := doctorOf(h, false)
	if dcode != 1 || !strings.Contains(dout, "delivery  FAIL  mail is waiting while stored mail is checked\n") {
		t.Fatalf("doctor during the scan: exit %d\n%s", dcode, dout)
	}
	dcode, dout, _ = doctorOf(h, true)
	if dcode != 1 || !strings.Contains(dout, `"fix": null`) && !strings.Contains(dout, `"fix":null`) {
		t.Fatalf("doctor --json during the scan: exit %d\n%s", dcode, dout)
	}
	scanning = false
	if code, out, _ := status(); code != 0 || strings.Contains(out, "not ready") {
		t.Fatalf("status after the scan: exit %d %q", code, out)
	}
}
