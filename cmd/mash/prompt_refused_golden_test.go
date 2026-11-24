package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/bogworks/mash/internal/config"
)

// cli.go prints a failed prompt with fmt.Fprintln(stderr, err) once framedIn has let it through unchanged. The goldens
// live beside the code that builds the line (internal/endpoint); this checks that the CLI adds nothing to it, so the
// stderr is exactly the golden (the "mash: " prefix is part of the error text).
func TestPromptRefusalReachesStderrUnchanged(t *testing.T) {
	for _, code := range []string{"400", "401", "403"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "internal", "endpoint", "testdata", "prompt_refused_"+code+".golden"))
		if err != nil {
			t.Fatal(err)
		}
		in := errors.New(string(bytes.TrimSuffix(b, []byte("\n"))))
		got := framedIn(config.Config{}, "o.dev.alice", in)
		if got != in {
			t.Fatalf("%s: framedIn changed the refusal: %v", code, got)
		}
		var stderr bytes.Buffer
		fmt.Fprintln(&stderr, got)
		if stderr.String() != string(b) {
			t.Errorf("%s: %q != %q", code, stderr.String(), b)
		}
	}
}
