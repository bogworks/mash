package admin_test

import (
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/admin"
)

// An owner that is not a valid token is refused before the connection is used, and the error does not print it (#1308).
func TestNewRefusesABadOwnerWithoutPrintingIt(t *testing.T) {
	for _, owner := range []string{"Bad Owner", strings.Repeat("a", 33), ""} {
		_, err := admin.New(nil, owner)
		if err == nil {
			t.Fatalf("owner %q was accepted", owner)
		}
		if owner != "" && strings.Contains(err.Error(), owner) {
			t.Errorf("the error prints the owner: %v", err)
		}
		if !strings.Contains(err.Error(), "the value is not shown") {
			t.Errorf("unexpected error: %v", err)
		}
	}
}
