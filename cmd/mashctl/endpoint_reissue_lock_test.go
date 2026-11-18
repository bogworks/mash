package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/jwt/v2"

	"github.com/bogworks/mash/internal/ctlkeys"
)

// Two reissues of one endpoint from two config directories (two mashctl machines: their local lock directories
// differ) are serialised by the server's name lock, because the output file is written inside it. The first run to
// get the lock lingers inside it; the second must not move the record until the first run's file is written.
func TestEndpointReissueTwoMachinesSerialiseOnNameLock(t *testing.T) {
	h1 := newFleetEnv(t)
	calls := fakeRevoke(t, nil)
	fakeKeyRevoked(t, func(string) (bool, error) { return false, nil })
	a := ad("two")
	h1.seed(a)
	before, _ := h1.record(a)
	oldKey := before.User

	// the second machine: its own config and key directories, the same server and account
	dir2 := t.TempDir()
	if err := os.Chmod(dir2, 0o700); err != nil {
		t.Fatal(err)
	}
	h2 := &fleetEnv{t: t, e: h1.e, dir: dir2, env: map[string]string{}}
	for k, v := range h1.env {
		h2.env[k] = v
	}
	h2.env["HOME"] = t.TempDir()
	h2.env["MASH_CONFIG_DIR"] = dir2
	h2.env["MASH_KEYS_DIR"] = filepath.Join(dir2, "keys")
	if _, se, code := h2.run("init"); code != 0 {
		t.Fatalf("init: %q", se)
	}
	ka, err := ctlkeys.Open(h2.env["MASH_KEYS_DIR"])
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ka.ImportAccount(h1.e.AccountSeed); err != nil {
		t.Fatal(err)
	}

	out := t.TempDir()
	pub1, file1, _ := epKey(t, out, "k1")
	pub2, file2, _ := epKey(t, out, "k2")
	jwt1, jwt2 := filepath.Join(out, "one.jwt"), filepath.Join(out, "two.jwt")
	exists := func() int {
		n := 0
		for _, p := range []string{jwt1, jwt2} {
			if _, err := os.Lstat(p); err == nil {
				n++
			}
		}
		return n
	}

	var mu sync.Mutex
	var entered atomic.Int32
	var filesAtEntry []int
	epInsideLock = func() {
		n := entered.Add(1)
		mu.Lock()
		filesAtEntry = append(filesAtEntry, exists())
		mu.Unlock()
		if n == 1 {
			time.Sleep(300 * time.Millisecond)
		}
	}
	t.Cleanup(func() { epInsideLock = nil })

	type result struct {
		se   string
		code int
	}
	var res [2]result
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i, c := range []struct {
		h    *fleetEnv
		key  string
		jwtf string
	}{{h1, file1, jwt1}, {h2, file2, jwt2}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, se, code := c.h.run("endpoint", "reissue", a.String(), "--public-key", c.key, "--jwt-out", c.jwtf)
			res[i] = result{se, code}
		}()
	}
	close(start)
	wg.Wait()

	if len(filesAtEntry) != 2 || filesAtEntry[0] != 0 || filesAtEntry[1] != 1 {
		t.Fatalf("the second run entered the lock before the first run's file was written: files present at each entry %v; results %+v", filesAtEntry, res)
	}
	rec, _ := h1.record(a)
	keyOf := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			return ""
		}
		uc, err := jwt.DecodeUserClaims(strings.TrimSpace(string(b)))
		if err != nil {
			t.Errorf("%s: %v", p, err)
			return ""
		}
		return uc.Subject
	}
	if rec.User != pub1 && rec.User != pub2 {
		t.Fatalf("the record names neither key: %s", rec.User)
	}
	byKey := map[string]int{pub1: 0, pub2: 1}
	files := []string{jwt1, jwt2}
	keys := []string{pub1, pub2}
	last := byKey[rec.User] // the run that took the lock second holds the directory's key
	first := 1 - last
	for i, r := range res {
		if r.code != 0 {
			t.Fatalf("run %d did not exit 0: %d %q", i, r.code, r.se)
		}
	}
	if got := keyOf(files[first]); got != keys[first] {
		t.Errorf("the earlier run's JWT names %s, not its own key %s", got, keys[first])
	}
	if got := keyOf(files[last]); got != rec.User {
		t.Errorf("the later run's JWT names %s, the directory has %s", got, rec.User)
	}
	var revoked []string
	for _, c := range *calls {
		revoked = append(revoked, c.users...)
	}
	if len(revoked) != 2 || revoked[0] != oldKey || revoked[1] != keys[first] {
		t.Errorf("revoked %v, want the old key %s then the earlier run's key %s", revoked, oldKey, keys[first])
	}
}
