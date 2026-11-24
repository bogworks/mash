package main

// #1081 (REQ-007): when `mashctl endpoint delete` cancelled the endpoint's key and a later step failed, it prints two
// lines: the address the user typed with a cause in plain words, then the command to run again. Each cause is driven
// here against the embedded server (the conformance test runs the same drivers against the specification's cases).

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/store"
)

// partialCauses are the ten causes of the specification, in its order: the letter, the cause words, and a driver that
// sets a world up so that exactly that step fails after the key was cancelled, then runs the delete.
var partialCauses = []struct {
	letter, cause string
	drive         func(t *testing.T, w *confWorld) confRan
}{
	{"a", "its lock was lost", func(t *testing.T, w *confWorld) confRan {
		// the lock of the name disappears while the key is being cancelled
		return partialInHook(t, w, func() {
			kv := partialKV(t, w, store.LeaseBucket)
			if err := kv.Delete(w.ctx, "limits-lock.acme_demo_bob"); err != nil {
				t.Fatal(err)
			}
		})
	}},
	{"b", "its record could not be read", func(t *testing.T, w *confWorld) confRan {
		// the record is replaced by something unreadable meanwhile, so the guarded removal reads it again
		return partialInHook(t, w, func() {
			if _, err := partialKV(t, w, store.DirectoryBucket).Put(w.ctx, "acme_demo_bob", []byte("not json")); err != nil {
				t.Fatal(err)
			}
		})
	}},
	{"c", "its record changed meanwhile", func(t *testing.T, w *confWorld) confRan {
		// the record vanishes meanwhile
		return partialInHook(t, w, func() {
			if err := partialKV(t, w, store.DirectoryBucket).Delete(w.ctx, "acme_demo_bob"); err != nil {
				t.Fatal(err)
			}
		})
	}},
	{"d", "record removal not confirmed", func(t *testing.T, w *confWorld) confRan {
		// the directory refuses writes, so the purge does not report success
		return partialInHook(t, w, func() { partialSeal(t, w, "KV_"+store.DirectoryBucket) })
	}},
	{"e", "its name was not freed", func(t *testing.T, w *confWorld) confRan {
		// the retired-name bucket refuses writes, so the pending marker cannot be made final
		return partialInHook(t, w, func() { partialSeal(t, w, "KV_"+store.RetiredBucket) })
	}},
	{"f", "its address was not closed", func(t *testing.T, w *confWorld) confRan {
		return partialInHook(t, w, func() { partialSeal(t, w, store.LeaseStreamName) })
	}},
	{"g", "its inbox is still there", func(t *testing.T, w *confWorld) confRan {
		// a server never refuses to delete a stream that exists: the delete is made to fail by the admin package's seam
		admin.InboxDeleteFault = func() error {
			return errors.New("nats: API error: code=500 err_code=10049 description=stream delete failed")
		}
		t.Cleanup(func() { admin.InboxDeleteFault = nil })
		return w.run("endpoint", "delete", "acme.demo.bob", "--yes")
	}},
	{"h", "an earlier removal is stuck", func(t *testing.T, w *confWorld) confRan {
		// an earlier removal purged the record and stopped with its marker pending; finishing it fails
		if err := partialKV(t, w, store.DirectoryBucket).Purge(w.ctx, "acme_demo_bob"); err != nil {
			t.Fatal(err)
		}
		m := store.Retired{Owner: "acme", Group: "demo", Instance: "bob", RetiredAt: time.Now().UTC().Format(time.RFC3339), State: store.RetiredPending}
		val := []byte(`{"owner":"acme","group":"demo","instance":"bob","retired_at":"` + m.RetiredAt + `","state":"pending"}`)
		if _, err := partialKV(t, w, store.RetiredBucket).Create(w.ctx, "acme_demo_bob", val); err != nil {
			t.Fatal(err)
		}
		partialSeal(t, w, store.LeaseStreamName)
		return w.run("endpoint", "delete", "acme.demo.bob", "--yes")
	}},
	{"i", "its creds file was not moved", func(t *testing.T, w *confWorld) confRan {
		// the place to move the file to is taken, a hundred and one times over
		p := filepath.Join(w.a.env["MASH_CONFIG_DIR"], "acme.demo.bob.creds")
		for i := 0; i <= 100; i++ {
			d := p + ".deleted"
			if i > 0 {
				d += fmt.Sprintf(".%d", i)
			}
			if err := os.WriteFile(d, []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return w.run("endpoint", "delete", "acme.demo.bob", "--yes")
	}},
	{"j", "its creds path is not a file", func(t *testing.T, w *confWorld) confRan {
		// the creds path is a link: left alone
		p := filepath.Join(w.a.env["MASH_CONFIG_DIR"], "acme.demo.bob.creds")
		real := p + ".real"
		if err := os.Rename(p, real); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(real, p); err != nil {
			t.Fatal(err)
		}
		return w.run("endpoint", "delete", "acme.demo.bob", "--yes")
	}},
}

func partialKV(t *testing.T, w *confWorld, bucket string) jetstream.KeyValue {
	t.Helper()
	kv, err := w.js.KeyValue(w.ctx, bucket)
	if err != nil {
		t.Fatal(err)
	}
	return kv
}

func partialSeal(t *testing.T, w *confWorld, stream string) {
	t.Helper()
	s, err := w.js.Stream(w.ctx, stream)
	if err != nil {
		t.Fatal(err)
	}
	cfg := s.CachedInfo().Config
	cfg.Sealed = true
	if _, err := w.js.UpdateStream(w.ctx, cfg); err != nil {
		t.Fatal(err)
	}
}

// partialInHook runs the delete with hook called inside the guarded removal, under the name's lock, before the key is cancelled.
func partialInHook(t *testing.T, w *confWorld, hook func()) confRan {
	t.Helper()
	beforeRetireRevoke = hook
	t.Cleanup(func() { beforeRetireRevoke = nil })
	return w.run("endpoint", "delete", "acme.demo.bob", "--yes")
}

func TestPartialCauseLines(t *testing.T) {
	for _, pc := range partialCauses {
		t.Run(pc.letter, func(t *testing.T) {
			w := newConfWorld(t, true)
			w.do("endpoint", "create", "acme.demo.bob")
			r := pc.drive(t, w)
			want := "error: acme.demo.bob is partly deleted: " + pc.cause + "\nhint: mashctl endpoint delete acme.demo.bob\n"
			if r.code != 1 || r.out != "" || !strings.HasSuffix(r.errs, want) {
				t.Fatalf("exit %d, stdout %q, stderr %q; want it to end with %q", r.code, r.out, r.errs, want)
			}
			partialNoLeaks(t, r)
		})
	}
}

var partialLeakRE = regexp.MustCompile(`PARTIAL|acme_demo_bob|\b[A-Z0-9]{8}\b|nats|API error`)

// partialNoLeaks: the output holds no internal word, the flat name, a key id or the server's own words.
func partialNoLeaks(t *testing.T, r confRan) {
	t.Helper()
	// the lines before the error (the server announcement) may name the server; only the error lines are checked
	i := strings.Index(r.errs, "error: ")
	if i < 0 {
		t.Fatalf("no error line in %q", r.errs)
	}
	tail := r.errs[i:]
	if strings.Count(tail, "\n") != 2 {
		t.Errorf("want exactly two lines, got %q", tail)
	}
	if m := partialLeakRE.FindString(tail); m != "" {
		t.Errorf("the error carries %q: %q", m, tail)
	}
	for _, l := range strings.Split(strings.TrimSuffix(tail, "\n"), "\n") {
		if len(l) > 80 {
			t.Errorf("line over 80 columns: %q", l)
		}
	}
}
