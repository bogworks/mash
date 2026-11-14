package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/mash/internal/policyapply"
	"github.com/bogworks/mash/internal/policyintake"
	"github.com/bogworks/mash/internal/policyserve"
)

const policyServeUsage = `usage: mashctl policy serve --range RANGE [--servers N] [--sys-creds FILE]
  --range     the range this service applies list sets for; the policy bucket is named by MASH_POLICY_BUCKET, in which
              {range} is replaced by it
  --servers   the number of servers in the deployment (or MASH_SERVERS); every apply reads the limits and the account
              JWT from every one and refuses when any does not answer
  (one process serves the one account the key store and the admin credentials belong to; it refuses to start unless
  that account is adopted)
`

// policyServeContext is the context the service runs under; tests replace it. The default ends on SIGINT or SIGTERM.
var policyServeContext = func() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// policyServeReady is called once the service is watching for list sets; a test seam.
var policyServeReady = func() {}

// policyServeProvisioner is a test seam: the provisioner of new addresses. Production leaves it nil, which is the
// fail-closed provisioner (a change that adds an address is refused).
var policyServeProvisioner policyapply.Provisioner

// policyServeSetup bounds what the service does before it starts watching.
const policyServeSetup = 30 * time.Second

// runPolicyServe is `mashctl policy serve`: the intake of list sets for one adopted account.
func runPolicyServe(args []string, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	var rng, serversFlag, sysCreds string
	if err := flagArgs(args, map[string]*string{"--range": &rng, "--servers": &serversFlag, "--sys-creds": &sysCreds}); err != nil {
		fmt.Fprintf(stderr, "mashctl: policy serve: %v\n%s", err, policyServeUsage)
		return 2
	}
	if rng == "" {
		fmt.Fprintf(stderr, "mashctl: policy serve: --range is required\n%s", policyServeUsage)
		return 2
	}
	servers := 0
	if sv, src := serversFlag, "--servers"; sv != "" || env("MASH_SERVERS") != "" {
		if sv == "" {
			sv, src = env("MASH_SERVERS"), "MASH_SERVERS"
		}
		var aerr error
		if servers, aerr = strconv.Atoi(strings.TrimSpace(sv)); aerr != nil || servers < 1 {
			fmt.Fprintf(stderr, "mashctl: policy serve: %s must be a whole number of servers, 1 or more, got %q\n%s", src, sv, policyServeUsage)
			return 2
		}
	}
	bucket, err := policyapply.PolicyBucket(env(policyapply.PolicyBucketEnv), rng)
	if err != nil {
		fmt.Fprintf(stderr, "mashctl: policy serve: %v\n%s", err, policyServeUsage)
		return 2
	}
	cfg, err := loadCtlConfig(env, lookup, true)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "mashctl: policy serve: %s\n", fmt.Sprintf(format, a...))
		return 1
	}
	keys, err := openKeys(cfg, env)
	if err != nil {
		return fail("%v", err)
	}
	account, _, err := keys.HeldKeys()
	if err != nil {
		return fail("%v", err)
	}
	adminKey, err := ctl.AdminUserKey(cfg)
	if err != nil {
		return fail("%v", err)
	}
	if adminAcct, err := credsAccountKey(cfg.AdminCreds); err != nil {
		return fail("admin credentials: %s", config.Clean(err.Error()))
	} else if adminAcct != account {
		return fail("the admin credentials belong to another account than the key store's account key")
	}
	trust, err := apply.LoadTrust(keys)
	if err != nil {
		return fail("%v", err)
	}
	announceTarget(cfg, env, stderr, &sysTarget{sysCreds})
	sys, err := connectSys(cfg, sysCreds, env)
	if err != nil {
		return fail("%v", err)
	}
	defer sys.Close()
	adm, setupCtx, closeAdmin, err := ctl.OpenAdmin("mashctl", cfg)
	if err != nil {
		return fail("%v", err)
	}
	defer closeAdmin()

	ctx, cancel := policyServeContext()
	defer cancel()
	setupCtx, setupCancel := context.WithTimeout(setupCtx, policyServeSetup)
	defer setupCancel()
	marker, found, err := adm.Adoption(setupCtx)
	switch {
	case err != nil:
		return fail("cannot tell whether this account is adopted: %v", err)
	case !found:
		return fail("this account is not adopted: run `mashctl policy adopt --submitter KEY` first; the service applies list sets only to an adopted account")
	case marker.Account != "" && marker.Account != account:
		return fail("the adoption marker names another account than the key store's account key")
	}
	js, err := jetstream.New(adm.Conn())
	if err != nil {
		return fail("%v", err)
	}
	ledger, err := policyapply.NewKVLedger(setupCtx, js, account, policyapply.KVLedgerOptions{Bucket: bucket, Holder: "mashctl-policy-serve"})
	if err != nil {
		return fail("%v", err)
	}
	kv, err := js.KeyValue(setupCtx, bucket)
	if err != nil {
		return fail("policy bucket %s: %v", bucket, err)
	}

	logger := slog.New(slog.NewTextHandler(stderr, nil))
	scheme, host := serverAddress(cfg.URL)
	app := &policyserve.Applier{
		Account: account, Range: rng, Ledger: ledger, Sys: sys, Keys: keys, Trust: trust, Record: policyapply.NewAdminIssueRecord(adm),
		Servers: servers, Scheme: scheme, Host: host, Log: logger, Provisioner: policyServeProvisioner,
		Load: func(ctx context.Context, u string) (*nats.Conn, error) {
			opts := append(natsauth.TLSOptions(cfg.TLSCA, cfg.TLSCert, cfg.TLSKey), natsauth.UserCredentials(cfg.AdminCreds),
				nats.Name("mashctl-policy-serve"), nats.Timeout(connectTimeout), nats.MaxReconnects(0))
			return nats.Connect(u, opts...)
		},
	}
	intake := &policyintake.Intake{
		Account: account, Range: rng, KV: kv,
		Marker: policyintake.AdminMarker{Admin: adm}, Lock: policyintake.AdminLock{Admin: adm},
		Decode: policyserve.Decode, InForce: policyserve.InForce(ledger),
		Apply: app, Report: policyserve.Reporter{Ledger: ledger},
		HeldKeys: policyserve.HeldKeys(keys.HeldKeys, keys.GroupPublics, adminKey),
		Log:      slog.NewLogLogger(logger.Handler(), slog.LevelInfo),
	}
	logger.Info("policy service listening", "key", policyintake.KeyPrefix+account, "bucket", bucket, "range", rng, "servers", servers)
	policyServeReady()
	if err := intake.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return fail("%v", err)
	}
	logger.Info("policy service stopped")
	return 0
}

// serverAddress returns the scheme and host of the server URL: where a server that reports no host of its own is
// reached.
func serverAddress(raw string) (scheme, host string) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "nats", "127.0.0.1"
	}
	return u.Scheme, u.Hostname()
}
