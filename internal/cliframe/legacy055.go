package cliframe

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	"github.com/bogworks/mash/internal/config"
)

// ReleaseBase is where a release's files are published: the address the 0.5.5 migration hint is written from. The
// project's own releases are the default; a release build may set it with
// -ldflags "-X github.com/bogworks/mash/internal/cliframe.ReleaseBase=<base>" (a fork sets its own), and
// scripts/install.sh takes a fork's from MASH_REPO, which a binary does not see.
var ReleaseBase = "https://github.com/bogworks/mash/releases/latest/download"

// Legacy055 is the folder (as the tools name it, the home directory written as ~) when the configuration folder's
// config.json holds a mash 0.5.5 setting: the key url or tls_ca. Only the names of the keys are looked at, never their
// values. A folder that does not exist, a config.json that is not there, or one that is not a JSON object is not a
// 0.5.5 folder (the loader reports what is wrong with those).
func Legacy055(env func(string) string, home string) (string, bool) {
	dir := config.ConfigDir(env, home)
	if dir == "" {
		return "", false
	}
	raw, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		return "", false
	}
	var keys map[string]json.RawMessage
	if json.Unmarshal(raw, &keys) != nil {
		return "", false
	}
	_, hasURL := keys["url"]
	_, hasCA := keys["tls_ca"]
	if !hasURL && !hasCA {
		return "", false
	}
	return config.TildeIn(dir, home), true
}

// RefuseLegacy055 stops the command when the configuration folder was left by mash 0.5.5: one framed error, exit 1,
// nothing read from the old keys and nothing changed. refused is false when the folder is not a 0.5.5 folder.
func RefuseLegacy055(stdout, stderr io.Writer, env func(string) string, home string, jsonOut bool) (code int, refused bool) {
	folder, ok := Legacy055(env, home)
	if !ok {
		return 0, false
	}
	e := Failf("%s holds a mash 0.5.5 configuration", folder).WithHint(ReleaseBase + "/mash-migrate-0.5.5.sh")
	return Report(stdout, stderr, e, jsonOut), true
}
