package config

import (
	"os"
	"path/filepath"
	"testing"
)

// A creds file that delete set aside (<address>.creds.deleted, .deleted.1) is not an installed endpoint.
func TestInstalledEndpointsIgnoresDeletedCreds(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"o.g.a.creds", "o.g.b.creds.deleted", "o.g.b.creds.deleted.1"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got := installedEndpoints(dir)
	if len(got) != 1 || got[0] != "o.g.a" {
		t.Errorf("installed endpoints: %v", got)
	}
}
