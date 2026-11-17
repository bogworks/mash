package main

import (
	"context"
	"strings"
	"testing"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/store"
	"github.com/bogworks/mash/internal/testenv"
)

// list with one directory record that fails store.ParseRecord among good ones: the good ones are listed on stdout,
// the bad one is named by key on stderr, and the exit status is 0.
func TestListSkipsBadRecordWithWarning(t *testing.T) {
	e := testenv.Start(t)
	if _, errb, code := ctlRun(t, e, "init"); code != 0 {
		t.Fatalf("init: %d %q", code, errb)
	}
	_, u := e.Mint(t, ad("c"))
	if out, errb, code := ctlRun(t, e, "provision", "o.g.c", "--user", u, "--account", e.Account, "--role", "r"); code != 0 {
		t.Fatalf("provision: %d %q %q", code, out, errb)
	}
	js, err := jetstream.New(e.Conn(t, e.AdminCreds))
	if err != nil {
		t.Fatal(err)
	}
	kv, err := js.KeyValue(context.Background(), store.DirectoryBucket)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kv.Put(context.Background(), "junk_key", []byte(`{"owner":"x"}`)); err != nil {
		t.Fatal(err)
	}
	out, errb, code := ctlRun(t, e, "list")
	if code != 0 {
		t.Fatalf("list exit %d: %q %q", code, out, errb)
	}
	if !strings.HasPrefix(out, "o.g.c\t"+u) || strings.Contains(out, "junk") {
		t.Fatalf("stdout %q", out)
	}
	if !strings.Contains(errb, "warning: skipping directory record junk_key") {
		t.Fatalf("stderr %q does not name the bad key", errb)
	}
}
