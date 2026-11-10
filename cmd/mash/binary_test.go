package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/bogworks/mash/internal/endpoint"
)

var (
	binOnce sync.Once
	binPath string
	binErr  error
	binDir  string
)

// mashBinary is the mash binary built once for the process-level tests (a hook killed mid-run, a real process chain).
func mashBinary(t *testing.T) string {
	t.Helper()
	binOnce.Do(func() {
		if binDir, binErr = os.MkdirTemp("", "mash-bin-"); binErr != nil {
			return
		}
		binPath = filepath.Join(binDir, "mash")
		if out, err := exec.Command("go", "build", "-o", binPath, ".").CombinedOutput(); err != nil {
			binErr = fmt.Errorf("go build: %v\n%s", err, out)
		}
	})
	if binErr != nil {
		t.Fatal(binErr)
	}
	return binPath
}

func TestMain(m *testing.M) {
	if os.Getenv("MASH_TEST_FAKE_CLAUDE") != "" { // re-run as the fake claude of the process-chain tests
		fakeClaudeMain()
		return
	}
	// In-process serves share a parent and a session id: without this each would take the lease from the one before as
	// an earlier serve of the same session (ADR 0082). A test that wants that calls asSession.
	endpoint.OurSession = func() endpoint.SessionIdent { return endpoint.SessionIdent{} }
	code := m.Run()
	if binDir != "" {
		os.RemoveAll(binDir)
	}
	os.Exit(code)
}
