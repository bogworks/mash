package main

import (
	"fmt"
	"io"

	"github.com/bogworks/mash/internal/config"
)

// maskURL hides the userinfo of a server URL, so it is never printed.
func maskURL(u string) string { return config.RedactURL(u) }

// announceTarget acts before a verb connects to a server or changes anything. It prints a warning to stderr when an
// environment variable overrode a different config.json value, and one more when the shell looks like it is inside an
// endpoint session whose own server that is. It only informs: it never refuses. --detail changes nothing here.
func announceTarget(cfg config.Config, env func(string) string, stderr io.Writer) {
	// The override warning names the endpoint session the shell may be inside, so it is given when an endpoint launch
	// token is set, or when MASH_ENDPOINT comes from the environment while config.json names servers (the shell's
	// endpoint is not the one config.json describes). A central host sets neither, and its MASH_SERVER list is meant.
	inSession := env("MASH_LAUNCH_TOKEN") != "" || (cfg.Source["endpoint"] == config.SourceKeys["endpoint"] && cfg.FileValues["servers"] != "")
	for _, key := range []string{"servers", "endpoint", "admin_creds", "ca"} {
		if !inSession {
			break
		}
		src, fileVal := cfg.Source[key], cfg.FileValues[key]
		if src != config.SourceKeys[key] || fileVal == "" {
			continue
		}
		endpoint := ""
		if cfg.Name != "" {
			endpoint = cfg.Owner + "." + cfg.Group + "." + cfg.Name
		}
		cur := map[string]string{"servers": cfg.URL, "endpoint": endpoint, "admin_creds": cfg.AdminCreds, "ca": cfg.TLSCA}[key]
		if cur == fileVal {
			continue
		}
		if key == "servers" {
			cur, fileVal = maskURL(cur), maskURL(fileVal)
		}
		fmt.Fprintf(stderr, "warning: %s (%s) overrides config.json's %s (%s); mashctl is acting on %s. These MASH_* variables belong to the endpoint session you may be inside; unset them to use config.json.\n",
			src, cur, key, fileVal, cur)
	}
	if cfg.Source["servers"] == "MASH_SERVER" && env("MASH_LAUNCH_TOKEN") != "" {
		fmt.Fprintf(stderr, "warning: MASH_LAUNCH_TOKEN is set, so this shell looks like it is inside a mash session, and %s (MASH_SERVER) is that session's own server. To act on another server, run outside the session or with `env -u MASH_SERVER ...` (and the other MASH_* variables) and config.json.\n", maskURL(cfg.URL))
	}
}
