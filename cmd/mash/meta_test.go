package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
)

func (w *world) admin(t *testing.T, args ...string) (string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	code := ctl.Run("mashctl", config.Config{InsecurePlaintext: true, URL: w.e.URL, Owner: w.e.Owner, AdminCreds: w.e.AdminCreds}, args, &out, &errb)
	return out.String() + errb.String(), code
}

// provision creates the directory record of name for user u through the admin API the endpoint verbs use.
func (w *world) provision(t *testing.T, name, u, note string, meta admin.Meta) error {
	t.Helper()
	adm, err := admin.New(w.e.Conn(t, w.e.AdminCreds), w.e.Owner)
	if err != nil {
		t.Fatal(err)
	}
	_, err = adm.Provision(context.Background(), tAddr(name), u, w.e.Account, note, meta)
	return err
}

// setMeta changes the metadata of name through the admin API the endpoint verbs use.
func (w *world) setMeta(t *testing.T, name string, u admin.MetaUpdate) {
	t.Helper()
	adm, err := admin.New(w.e.Conn(t, w.e.AdminCreds), w.e.Owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adm.SetMeta(context.Background(), tAddr(name), u); err != nil {
		t.Fatalf("set meta: %v", err)
	}
}

func TestWhoShowsProvisionedMeta(t *testing.T) {
	w := newWorld(t, "a")
	_, u := w.e.Mint(t, tAddr("c"))
	if err := w.provision(t, "c", u, "the note", admin.Meta{Role: "reviewer", Host: "laptop", Labels: map[string]string{"tier": "1", "team": "acme"}}); err != nil {
		t.Fatalf("provision: %v", err)
	}
	// Default who: address and state only.
	out, _ := w.cli(t, "a", "who")
	if !whoShows(out, tTo("c"), "offline") || strings.Contains(out, "reviewer") || strings.Contains(out, "the note") {
		t.Fatalf("default who: %q", out)
	}
	out, _ = w.cli(t, "a", "who", "--wide")
	for col, want := range map[string]string{"ROLE": "reviewer", "HOST": "laptop", "DESCRIPTION": "-"} {
		if got := whoCell(t, out, tTo("c"), col); got != want {
			t.Fatalf("who --wide %s of c = %q, want %q: %q", col, got, want, out)
		}
	}
	if got := whoCell(t, out, tTo("a"), "ROLE"); got != "-" {
		t.Fatalf("who --wide without metadata: %q", out)
	}

	// Only the given fields change; an empty value clears.
	role, host := "builder", ""
	w.setMeta(t, "c", admin.MetaUpdate{Role: &role, Host: &host, Labels: map[string]string{"tier": "", "env": "prod"}})
	out, _ = w.cli(t, "a", "who", "--wide")
	if r, h := whoCell(t, out, tTo("c"), "ROLE"), whoCell(t, out, tTo("c"), "HOST"); r != "builder" || h != "-" {
		t.Fatalf("after set meta: role %q host %q: %q", r, h, out)
	}
}

func TestWhoWideAndMCPWhoShowMeta(t *testing.T) {
	w := newWorld(t, "a", "b")
	if out, code := w.admin(t, "set-meta", tTo("a"), "--role", "chief", "--label", "team=h1", "--host", "elsewhere-box"); code != 0 {
		t.Fatalf("set-meta: %d %s", code, out)
	}
	s := startServe(t, w.env["a"])
	s.initialize(t)
	s.tools(t)
	eventually(t, 5*time.Second, "a online", func() bool { out, _ := w.cli(t, "b", "who"); return whoShows(out, tTo("a"), "online") })

	out, _ := w.cli(t, "b", "who", "--wide")
	if !whoShows(out, tTo("a"), "online") || whoCell(t, out, tTo("a"), "ROLE") != "chief" || whoCell(t, out, tTo("a"), "HOST") != "elsewhere-box" || strings.Contains(out, "team") {
		t.Fatalf("who --wide: %q", out)
	}
	// The MCP tool shows the same table.
	txt := resultText(s.call(t, 20, "who", `{"wide":true}`))
	if got := whoCell(t, txt, tTo("a"), "ROLE"); got != "chief" {
		t.Errorf("mcp who a: %q", txt)
	}
	if !whoShows(txt, tTo("b"), "offline") {
		t.Errorf("mcp who b: %q", txt)
	}
}
