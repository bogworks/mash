package main

import (
	"fmt"
	"io"

	"github.com/bogworks/mash/internal/config"
)

// announceSink, when set, receives the target lines instead of stderr: the tests that compare a verb's whole stderr set
// it to io.Discard (see TestMain); production leaves it nil.
var announceSink io.Writer

// sysTarget asks announceTarget to name the system account credentials too: the --sys-creds value, if any.
type sysTarget struct{ flagVal string }

// maskURL hides the userinfo of a server URL, so it is never printed.
func maskURL(u string) string { return config.RedactURL(u) }

// sourceText is where the setting key came from, as a person reads it: the environment variable, the path of
// config.json, or "default"/"unset".
func sourceText(cfg config.Config, key string) string {
	switch s := cfg.Source[key]; {
	case s == "config.json" && cfg.File != "":
		return cfg.File
	case s == "":
		return "unset"
	default:
		return s
	}
}

// announceTarget prints, to stderr and before a verb connects to a server or changes anything, which server mashctl is
// about to act on and where each setting came from; then a warning when an environment variable overrode a different
// config.json value, and one more when the shell looks like it is inside an endpoint session whose own server that is.
// It only informs: it never refuses, and it never prints the contents of a credentials file. sys, when set, adds the
// system account credentials' path. Interim until the redesign gives the target an explicit flag or context (#183).
func announceTarget(cfg config.Config, env func(string) string, stderr io.Writer, sys *sysTarget) {
	if announceSink != nil {
		stderr = announceSink
	}
	line := "mashctl: target server " + maskURL(cfg.URL) + " (from " + sourceText(cfg, "url") + ")"
	if cfg.URL == "" {
		line = "mashctl: target server unset"
	}
	if cfg.Owner != "" {
		line += ", owner " + cfg.Owner + " (from " + sourceText(cfg, "owner") + ")"
	} else {
		line += ", owner unset"
	}
	if cfg.AdminCreds != "" {
		line += ", admin creds " + cfg.AdminCreds + " (from " + sourceText(cfg, "admin_creds") + ")"
	} else {
		line += ", admin creds unset"
	}
	if sys != nil {
		if p, err := sysCredsPath(sys.flagVal, env, cfg); err == nil {
			src := "the default location"
			switch {
			case sys.flagVal != "":
				src = "--sys-creds"
			case env("MASH_SYS_CREDS") != "":
				src = "MASH_SYS_CREDS"
			}
			line += ", system creds " + p + " (from " + src + ")"
		}
	}
	fmt.Fprintln(stderr, line)

	for _, key := range []string{"url", "owner", "admin_creds", "tls_ca"} {
		src, fileVal := cfg.Source[key], cfg.FileValues[key]
		if src != config.SourceKeys[key] || fileVal == "" {
			continue
		}
		cur := map[string]string{"url": cfg.URL, "owner": cfg.Owner, "admin_creds": cfg.AdminCreds, "tls_ca": cfg.TLSCA}[key]
		if cur == fileVal {
			continue
		}
		if key == "url" {
			cur, fileVal = maskURL(cur), maskURL(fileVal)
		}
		fmt.Fprintf(stderr, "warning: %s (%s) overrides config.json's %s (%s); mashctl is acting on %s. These MASH_* variables belong to the endpoint session you may be inside; unset them to use config.json.\n",
			src, cur, key, fileVal, cur)
	}
	if cfg.Source["url"] == "MASH_URL" && (env("MASH_NAME") != "" || env("MASH_LAUNCH_TOKEN") != "") {
		fmt.Fprintf(stderr, "warning: MASH_NAME or MASH_LAUNCH_TOKEN is set, so this shell looks like it is inside a mash session, and %s (MASH_URL) is that session's own server. To act on another server, run outside the session or with `env -u MASH_URL ...` (and the other MASH_* variables) and config.json.\n", maskURL(cfg.URL))
	}
}

// announcesVerb reports whether the verb of the common admin path (ctl.Run) connects to a server: every one but
// permissions, `limits --show` and `init --local`, which are offline.
func announcesVerb(args []string) bool {
	switch args[0] {
	case "permissions":
		return false
	case "limits":
		return !(len(args) == 2 && args[1] == "--show")
	case "init":
		for _, a := range args[1:] {
			if a == "--local" {
				return false
			}
		}
	}
	return true
}
