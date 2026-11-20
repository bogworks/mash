package endpoint

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nats-io/nats.go"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/natsauth"
)

// CheckServer says whether a server of cfg.URL answers, without signing in: it connects with no credentials under the
// same TLS rules as an endpoint (a plain URL needs insecure_plaintext, the CA and client certificate apply). A server
// that answers and refuses the missing credentials has answered, so that is no error; so is a connection that is made.
// What it returns is the failure to reach a server, redacted. It opens nothing it leaves open.
func CheckServer(ctx context.Context, cfg config.Config) error {
	tlsReq, err := EndpointTLS(cfg)
	if err != nil {
		return err
	}
	opts := append(append(natsauth.TLSOptions(cfg.TLSCA, cfg.TLSCert, cfg.TLSKey), tlsReq...),
		nats.Name("mash doctor"), nats.NoReconnect())
	nc, err := connect(ctx, cfg.URL, opts...)
	if err == nil {
		nc.Close()
		return nil
	}
	if isAuthFailure(err) {
		return nil
	}
	return fmt.Errorf("mash: connect %s: %w", config.RedactURL(cfg.URL), config.RedactErr(natsauth.TLSHint(err), cfg.URL))
}

// RefusedFile is the file, in the endpoint's state directory, that holds the number of unverified messages the
// endpoint refused: a whole number in decimal. The endpoint writes it when it refuses one; nothing writes it yet.
const RefusedFile = "refused-unverified"

// RefusedCount is the number of unverified messages refused for the endpoint cfg configures: what RefusedFile holds,
// and 0 when it is absent, empty or not a whole number (doctor never fails over advice).
func RefusedCount(cfg config.Config) int {
	d, err := SpoolDir(cfg)
	if err != nil {
		return 0
	}
	b, err := os.ReadFile(filepath.Join(d, RefusedFile))
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || n < 0 {
		return 0
	}
	return n
}
