package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/config"
)

// lockedRevoke replaces the revocation seams with ones that are safe for concurrent reissues.
func lockedRevoke(t *testing.T) {
	t.Helper()
	var mu sync.Mutex
	prev := revokeUsers
	revokeUsers = func(context.Context, config.Config, func(string) string, int, []string, io.Writer, func() time.Time) error {
		mu.Lock()
		defer mu.Unlock()
		return nil
	}
	t.Cleanup(func() { revokeUsers = prev })
	prevK := keyRevoked
	keyRevoked = func(context.Context, config.Config, func(string) string, int, string) (bool, error) {
		return false, nil
	}
	t.Cleanup(func() { keyRevoked = prevK })
}

// Two reissues of one name at once end with the creds file's key equal to the record's user, never mismatched.
func TestConcurrentReissueKeepsFileAndRecordTogether(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	h.seedWith("a", extraTag) // narrowing: each reissue rotates
	lockedRevoke(t)

	const n = 3
	var wg sync.WaitGroup
	codes := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var out, errb strings.Builder
			codes[i] = runEnv([]string{"reissue", "a"}, func(k string) string { return h.env[k] }, &out, &errb)
		}()
	}
	wg.Wait()
	ok := 0
	for _, c := range codes {
		if c == 0 {
			ok++
		}
	}
	if ok == 0 {
		t.Fatalf("no reissue succeeded: %v", codes)
	}
	_, filePub := h.credsClaims("a")
	recs := h.records()
	if len(recs) != 1 || recs[0] != "a//"+filePub {
		t.Fatalf("creds key %s, records %v: file and record disagree", filePub, recs)
	}
}

// The name lock is held while the creds file is replaced: another SetUser of the same name cannot get in.
func TestReissueHoldsNameLockDuringFileRewrite(t *testing.T) {
	h := newAddEnv(t, addPolicy)
	h.seed("a") // up to date: the file is rewritten
	lockedRevoke(t)

	adm, err := admin.New(h.e.Conn(t, h.e.AdminCreds), h.e.Owner)
	if err != nil {
		t.Fatal(err)
	}
	_, pub := h.credsClaims("a")
	var inner error
	called := false
	prev := writeCreds
	writeCreds = func(path string, creds []byte) error {
		called = true
		cctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
		defer cancel()
		_, inner = adm.SetUser(cctx, "a", h.accountPub(), pub, pub)
		return prev(path, creds)
	}
	t.Cleanup(func() { writeCreds = prev })

	out, errb, code := h.run("reissue", "a")
	if code != 0 || !called {
		t.Fatalf("%d %v %q %q", code, called, out, errb)
	}
	if inner == nil || !errors.Is(inner, context.DeadlineExceeded) {
		t.Fatalf("a SetUser during the file rewrite was not held off by the lock: %v", inner)
	}
}
