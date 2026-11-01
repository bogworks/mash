package release

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
