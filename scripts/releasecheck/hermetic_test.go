package main

import (
	"fmt"
	"os"
	"testing"

	"github.com/bogworks/mash/internal/testenv"
)

func TestMain(m *testing.M) {
	cleanup, err := testenv.HermeticGit()
	if err != nil {
		fmt.Fprintln(os.Stderr, "hermetic git:", err)
		os.Exit(1)
	}
	code := m.Run()
	cleanup()
	os.Exit(code)
}

func TestGitIsHermetic(t *testing.T) {
	if got := os.Getenv("GIT_CONFIG_GLOBAL"); got != os.DevNull {
		t.Fatalf("GIT_CONFIG_GLOBAL = %q, want %q", got, os.DevNull)
	}
	out, _ := testenv.RunGit("config", "--global", "--list")
	if len(out) != 0 {
		t.Fatalf("global git config is not empty:\n%s", out)
	}
}
