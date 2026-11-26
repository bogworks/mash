package main

import (
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctlkeys"
	"github.com/bogworks/mash/internal/natsauth"
)

// connectTimeout bounds the connection to the server; applyDeadline bounds a whole apply.
const connectTimeout = 5 * time.Second

// applyDeadline is a variable so tests can shorten it.
var applyDeadline = 25 * time.Second

// sysCredsPath is config.json "system_creds" (the flag and environment parameters are kept for the callers and
// are not read). With none set it refuses and points at `mashctl init --config`.
func sysCredsPath(_ string, _ func(string) string, cfg config.Config) (string, error) {
	if cfg.SysCreds != "" {
		return cfg.SysCreds, nil
	}
	return "", cliframe.Failf("no system account credentials").WithHint("mashctl init --config <file>")
}

// connectSys connects with the system account credentials (config.json "system_creds").
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
	tlsReq, _, err := natsauth.RequireTLS(cfg.URL, cfg.SkipEncryption)
	if err != nil {
		return nil, fmt.Errorf("connect %s with the system account credentials: %v", config.RedactURL(cfg.URL), config.RedactErr(err, cfg.URL))
	}
	opts := append(append(natsauth.TLSOptions(cfg.TLSCA, cfg.TLSCert, cfg.TLSKey), tlsReq...),
		natsauth.UserCredentialBytes(raw), nats.Name("mashctl-sys"), nats.Timeout(connectTimeout), nats.MaxReconnects(0))
	nc, err := nats.Connect(cfg.URL, opts...)
	if err != nil {
		return nil, fmt.Errorf("connect %s with the system account credentials: %v", config.RedactURL(cfg.URL), config.RedactErr(natsauth.NoServersHint(natsauth.TLSHint(err)), cfg.URL))
	}
	return nc, nil
}

// loadCtlConfig is config.Load for the verbs that need the owner; offline verbs also pass ErrNoURL.
func loadCtlConfig(env func(string) string, lookup config.Lookup, needURL bool) (config.Config, error) {
	cfg, err := loadConfig(env, lookup)
	if err != nil && (needURL || !errors.Is(err, config.ErrNoURL)) {
		return cfg, err
	}
	return cfg, nil
}

// openKeys opens the key store the config names.
func openKeys(cfg config.Config, env func(string) string) (*ctlkeys.Authority, error) {
	dir, err := ctlkeys.DirFor(cfg, env)
	if err != nil {
		return nil, err
	}
	return ctlkeys.Open(dir, cfg.SigningKeyPub)
}

// credsUserKey is the user public key (the JWT's subject) of the creds file at p; no seed is kept.
func credsUserKey(p string) (string, error) {
	uc, err := credsUserClaims(p)
	if err != nil {
		return "", err
	}
	return uc.Subject, nil
}

// credsAccountKey is the public key of the account a creds file's user belongs to: the account the user JWT names as
// its issuing account (a signing key issues on behalf of it), else its issuer.
func credsAccountKey(p string) (string, error) {
	uc, err := credsUserClaims(p)
	if err != nil {
		return "", err
	}
	if uc.IssuerAccount != "" {
		return uc.IssuerAccount, nil
	}
	return uc.Issuer, nil
}

func credsUserClaims(p string) (*jwt.UserClaims, error) {
	raw, err := config.ReadCredsFile(p)
	if err != nil {
		return nil, err
	}
	defer clear(raw)
	token, err := jwt.ParseDecoratedJWT(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	uc, err := jwt.DecodeUserClaims(token)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	return uc, nil
}

// readCreds reads the creds file at path, refusing one other users can read.
func readCreds(path string) ([]byte, error) { return config.ReadCredsFile(path) }
