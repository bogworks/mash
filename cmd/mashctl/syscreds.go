package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/bootstrap"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/natsauth"
)

// connectTimeout bounds the connection to the server; applyDeadline bounds a whole apply.
const (
	connectTimeout = 5 * time.Second
	applyDeadline  = 25 * time.Second
)

// sysCredsPath is --sys-creds, else MASH_SYS_CREDS, else the sys.creds that `mashctl init --local` wrote next to the
// admin credentials (or in the default server directory under the config directory).
func sysCredsPath(flagVal string, env func(string) string, cfg config.Config) (string, error) {
	switch {
	case flagVal != "":
		return flagVal, nil
	case env("MASH_SYS_CREDS") != "":
		return env("MASH_SYS_CREDS"), nil
	}
	var cands []string
	if cfg.AdminCreds != "" {
		cands = append(cands, filepath.Join(filepath.Dir(cfg.AdminCreds), bootstrap.SysCreds))
	}
	if cfg.Dir != "" {
		cands = append(cands, filepath.Join(cfg.Dir, "server", bootstrap.SysCreds))
	}
	for _, c := range cands {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", errors.New("no system account credentials: pass --sys-creds FILE or set MASH_SYS_CREDS (a server `mashctl init --local` stood up has them in <server dir>/sys.creds)")
}

// connectSys connects with the system account credentials (flagVal, MASH_SYS_CREDS or the init server's).
func connectSys(cfg config.Config, flagVal string, env func(string) string) (*nats.Conn, error) {
	credsPath, err := sysCredsPath(flagVal, env, cfg)
	if err != nil {
		return nil, err
	}
	// The same mode check as admin and endpoint credentials: a file other users can read is refused, before it is used.
	raw, err := config.ReadCredsFile(credsPath)
	if err != nil {
		return nil, fmt.Errorf("system account credentials: %v", err)
	}
	if err := natsauth.CheckCreds(credsPath, raw); err != nil {
		return nil, fmt.Errorf("system account credentials: %v", err)
	}
	tlsReq, err := natsauth.RequireTLS(cfg.URL, cfg.InsecureLoopback)
	if err != nil {
		return nil, fmt.Errorf("connect %s with the system account credentials: %v", config.RedactURL(cfg.URL), config.RedactErr(err, cfg.URL))
	}
	opts := append(append(natsauth.TLSOptions(cfg.TLSCA, cfg.TLSCert, cfg.TLSKey), tlsReq...),
		natsauth.UserCredentialBytes(raw), nats.Name("mashctl-sys"), nats.Timeout(connectTimeout), nats.MaxReconnects(0))
	nc, err := nats.Connect(cfg.URL, opts...)
	if err != nil {
		return nil, fmt.Errorf("connect %s with the system account credentials: %v", config.RedactURL(cfg.URL), config.RedactErr(natsauth.TLSHint(err), cfg.URL))
	}
	return nc, nil
}
