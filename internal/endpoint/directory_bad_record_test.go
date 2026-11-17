package endpoint

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/store"
)

// One directory record that fails store.ParseRecord among good ones: the client lists the good ones, the load is not
// an error, and the bad one is reported by key (once for the same error), never silently.
func TestDirectoryListingSkipsBadRecordWithWarning(t *testing.T) {
	f := newFixture(t, "a", "b")
	ctx := context.Background()
	js, err := jetstream.New(f.e.Conn(t, f.e.AdminCreds))
	if err != nil {
		t.Fatal(err)
	}
	kv, err := js.KeyValue(ctx, store.DirectoryBucket)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := kv.Put(ctx, "junk_key", []byte(`{"owner":"x"}`)); err != nil {
		t.Fatal(err)
	}
	ep := f.leanClient(t, "a", ClientOptions{FullDirectory: true})
	if !hasName(ep.Who(), "a") || !hasName(ep.Who(), "b") || len(ep.Who()) != 2 {
		t.Fatalf("who %+v", ep.Who())
	}
	ep.mu.RLock()
	_, warned := ep.badSeen["junk_key"]
	ep.mu.RUnlock()
	if !warned {
		t.Fatal("no warning recorded for the bad record at open")
	}
	var buf bytes.Buffer
	ep.mu.Lock()
	ep.log = log.New(&buf, "", 0)
	ep.badSeen = nil
	ep.mu.Unlock()
	if err := ep.loadDirectory(ctx); err != nil {
		t.Fatalf("a bad record failed the load: %v", err)
	}
	if got := buf.String(); !strings.Contains(got, "skipping directory record junk_key") {
		t.Fatalf("warning %q does not name the key", got)
	}
	n := strings.Count(buf.String(), "junk_key")
	if err := ep.loadDirectory(ctx); err != nil {
		t.Fatal(err)
	}
	if strings.Count(buf.String(), "junk_key") != n {
		t.Fatalf("the same warning repeated: %q", buf.String())
	}
	if !hasName(ep.Who(), "a") || !hasName(ep.Who(), "b") || len(ep.Who()) != 2 {
		t.Fatalf("who after reload %+v", ep.Who())
	}
}
