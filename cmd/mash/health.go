package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"github.com/bogworks/mash/internal/cmdcore/cmds"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/natsauth"
)

// healthSvc is cmds.Health: the checks behind status and doctor, one implementation for the command line and the MCP
// tools. It reads the configuration it was given and connects as the endpoint to see for itself; it never takes the
// endpoint's lease, writes nothing and prints no secret. session says the configuration was loaded for a session
// (mash serve), which never picks an endpoint for itself.
type healthSvc struct {
	cfg     config.Config
	cfgErr  error
	session bool
	// mailHeld says, in a session, whether it holds mail because the folder it requires cannot be written. nil at a shell.
	mailHeld func() bool
	// lease says, in a session, whether it holds its endpoint now: "" when it does (or at a shell, where it is nil),
	// else cmds.LeaseOther, LeaseLost or LeaseFailed with the cause of the last.
	lease func() (state, cause string)
}

// leaseNow is the session's lease state, "" when it holds the endpoint or there is no session.
func (h *healthSvc) leaseNow() (state, cause string) {
	if !h.session || h.lease == nil {
		return "", ""
	}
	return h.lease()
}

// credsPath is the creds file the endpoint signs in with: MASH_CREDS or "creds", else the endpoint's default; "" with no name.
func credsPath(cfg config.Config) string {
	switch {
	case cfg.Creds != "":
		return cfg.Creds
	case cfg.Name != "" && cfg.Dir != "":
		return cfg.Find(cfg.CredsName() + ".creds")
	}
	return ""
}

// urlSecretWarning is the warning for a server address that carries a password or token, which mash never uses: it
// signs in with operator-mode JWT credentials only. "" when no address does. The secret is not printed.
func urlSecretWarning(cfg config.Config) string {
	for _, e := range strings.Split(cfg.URL, ",") {
		_, rest, ok := strings.Cut(strings.TrimSpace(e), "://")
		if !ok {
			rest = strings.TrimSpace(e)
		}
		rest, _, _ = strings.Cut(rest, "/")
		if strings.Contains(rest, "@") {
			return "the server address carries a password or token, which mash does not use: it signs in with the creds file"
		}
	}
	return ""
}

// configCheck is the config row: whether the configuration is readable and names an endpoint and a server.
func (h *healthSvc) configCheck() cmds.HealthCheck {
	c := cmds.HealthCheck{Name: cmds.CheckConfig, State: cmds.CheckFail}
	file := "config.json"
	if h.cfg.Dir != "" {
		file = filepath.Join(h.cfg.Dir, "config.json")
	}
	switch {
	case errors.Is(h.cfgErr, config.ErrGroupMissing):
		c.Detail = "the endpoint address has no group: " + config.CodeGroupMissing
		c.Fix = "set MASH_ENDPOINT=<owner.group.name>"
	case h.cfgErr != nil && !errors.Is(h.cfgErr, config.ErrNoURL) && !errors.Is(h.cfgErr, config.ErrNoOwner):
		c.Detail = redactText(strings.TrimPrefix(h.cfgErr.Error(), "mash: "))
		if strings.Contains(c.Detail, "MASH_ENDPOINT") {
			c.Fix = "set MASH_ENDPOINT=<owner.group.name>"
		}
	case h.cfg.Name == "":
		// only a session reaches here: at a shell Health stops before the rows with "no endpoint is installed here"
		c.Detail, c.Fix = cmds.ReasonNoSessionEndpoint, cmds.SessionEndpointFix
	case func() bool { _, err := h.cfg.Identity(); return err != nil }():
		_, err := h.cfg.Identity()
		c.Detail = redactText(strings.TrimPrefix(err.Error(), "mash: "))
		c.Fix = "set MASH_ENDPOINT=<owner.group.name>"
	case h.cfg.URL == "":
		c.Detail = "no server is set"
		c.Fix = `set "servers" in ` + file + " or MASH_SERVER"
	default:
		c.State = cmds.CheckOK
	}
	c.Reason = c.Detail
	return c
}

// credsCheck is the creds row: the file exists, is a regular file only this user can read, and holds a user JWT and
// its seed. It reads the file to check its shape and prints none of it.
func (h *healthSvc) credsCheck() cmds.HealthCheck {
	c := cmds.HealthCheck{Name: cmds.CheckCreds, State: cmds.CheckFail}
	addr := h.cfg.DisplayName()
	p := credsPath(h.cfg)
	if p == "" {
		c.Detail = "no creds file is chosen"
		c.Reason = c.Detail
		return c
	}
	raw, err := config.ReadCredsFile(p)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		c.Detail = "the creds file is missing: " + p
		c.Fix = "mashctl endpoint reissue " + addr
	case err != nil && strings.Contains(err.Error(), "readable by other users"):
		c.Detail = "the creds file can be read by other users: " + p
		c.Fix = "chmod 600 " + p
	case err != nil && strings.Contains(err.Error(), "not a regular file"):
		c.Detail = "the creds file is not a regular file: " + p
	case err != nil:
		c.Detail = "the creds file cannot be read: " + p
	default:
		if cerr := natsauth.CheckCreds(p, raw); cerr != nil {
			c.Detail = "the creds file is not a creds file: " + p
			c.Fix = "mashctl endpoint reissue " + addr
		} else {
			c.State = cmds.CheckOK
		}
	}
	c.Reason = c.Detail
	return c
}

// serverCheck is the server row: the address is allowed (tls, or plain with MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1) and a server answers.
func (h *healthSvc) serverCheck(ctx context.Context) cmds.HealthCheck {
	c := cmds.HealthCheck{Name: cmds.CheckServer, State: cmds.CheckFail}
	url := config.RedactURL(h.cfg.URL)
	if _, err := endpoint.EndpointTLS(h.cfg); err != nil {
		c.Detail = redactText(strings.TrimPrefix(err.Error(), "mash: "))
		c.Fix = `use a tls:// server address (MASH_SERVER or "servers" in config.json)`
		c.Reason = c.Detail
		return c
	}
	octx, cancel := context.WithTimeout(ctx, openFor(h.cfg))
	defer cancel()
	if err := endpoint.CheckServer(octx, h.cfg); err != nil {
		c.Detail = "the server did not answer: " + url
		switch {
		case errors.Is(err, endpoint.ErrServerCertUntrusted):
			c.Detail = "the server's certificate is not trusted: " + url
			c.Fix = `set "ca" or MASH_CA to the PEM file of the CA that signed it`
		case errors.Is(err, endpoint.ErrServerNotTLS):
			c.Detail = "the server did not offer TLS: " + url
			c.Fix = `use the server's plain address with MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1 set, or ask whoever runs your mesh for its tls:// address`
		case errors.Is(err, endpoint.ErrServerRefusedClientCert):
			c.Detail = "the server refused the client certificate: " + url
			c.Fix = "set tls_cert and tls_key, or MASH_TLS_CERT and MASH_TLS_KEY, to a certificate the server's CA signed"
		}
		c.Reason = c.Detail
		return c
	}
	c.State, c.Detail = cmds.CheckOK, url
	if endpoint.PlainURL(h.cfg) {
		c.State, c.Detail = cmds.CheckWarn, url+" is plain text: MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION is set"
	} else if w := urlSecretWarning(h.cfg); w != "" {
		c.State, c.Detail = cmds.CheckWarn, w
	}
	return c
}

// signInCheck is the sign-in row: the server accepts this endpoint's credentials and knows the endpoint. It also says
// whether an agent session holds the endpoint open now (its lease).
func (h *healthSvc) signInCheck(ctx context.Context) (c cmds.HealthCheck, online bool) {
	c = cmds.HealthCheck{Name: cmds.CheckSignIn, State: cmds.CheckFail}
	addr := h.cfg.DisplayName()
	octx, cancel := context.WithTimeout(ctx, openFor(h.cfg))
	ep, err := endpoint.OpenClientWith(octx, h.cfg, endpoint.ClientOptions{Lease: true})
	cancel()
	if err != nil {
		switch msg := err.Error(); {
		case endpoint.IsAuthFailure(err):
			c.Detail, c.Reason = "the server refused these credentials", "the server refused its credentials"
			c.Fix = "mashctl endpoint reissue " + addr
		case transient(err) || strings.Contains(msg, "no servers available"):
			c.Detail = "the server did not answer: " + config.RedactURL(h.cfg.URL)
			c.Reason = c.Detail
		case strings.Contains(msg, "do not match endpoint"):
			c.Detail = "the creds file belongs to another endpoint than " + addr
			c.Fix = "mashctl endpoint reissue " + addr
		default:
			c.Detail = redactText(strings.TrimPrefix(msg, "mash: "))
		}
		if c.Reason == "" {
			c.Reason = c.Detail
		}
		return c, false
	}
	defer ep.Close()
	lctx, lcancel := context.WithTimeout(ctx, callFor(h.cfg))
	_, online = ep.OwnLease(lctx)
	lcancel()
	c.State = cmds.CheckOK
	return c, online
}

// refusedCheck is the refused row: how many unverified messages the endpoint refused.
func (h *healthSvc) refusedCheck() cmds.HealthCheck {
	n := endpoint.RefusedCount(h.cfg)
	switch n {
	case 0:
		return cmds.HealthCheck{Name: cmds.CheckRefused, State: cmds.CheckOK, Detail: "no unverified mail was refused"}
	case 1:
		return cmds.HealthCheck{Name: cmds.CheckRefused, State: cmds.CheckWarn, Detail: "1 unverified message was refused"}
	}
	return cmds.HealthCheck{Name: cmds.CheckRefused, State: cmds.CheckWarn, Detail: fmt.Sprintf("%d unverified messages were refused", n)}
}

// skipped is a row that could not be run: needs says which earlier row it waits on.
func skipped(name, needs string) cmds.HealthCheck {
	return cmds.HealthCheck{Name: name, State: cmds.CheckSkip, Detail: "needs the " + needs}
}

// deliveryCheck is the delivery row, run only inside a session once sign-in worked: whether mail that arrives can be
// stored on this machine. It reads the session's own state (the same source status uses) and writes nothing. A held
// session fails with the folder, shown with ~ for the home directory, after a colon.
func (h *healthSvc) deliveryCheck() cmds.HealthCheck {
	if lease, cause := h.leaseNow(); lease != "" {
		d := map[string]string{cmds.LeaseOther: cmds.ReasonAnotherSession, cmds.LeaseLost: cmds.ReasonLostToNewer}[lease]
		if lease == cmds.LeaseFailed {
			d = cmds.ReasonCouldNotTake
			if cause != "" {
				d += ": " + cause
			}
		}
		return cmds.HealthCheck{Name: cmds.CheckDelivery, State: cmds.CheckFail, Detail: d}
	}
	if h.mailHeld == nil || !h.mailHeld() {
		return cmds.HealthCheck{Name: cmds.CheckDelivery, State: cmds.CheckOK}
	}
	detail := cmds.ReasonMailHeld
	if dir, err := endpoint.SpoolDir(h.cfg); err == nil {
		detail += ": " + config.Tilde(dir)
	}
	return cmds.HealthCheck{Name: cmds.CheckDelivery, State: cmds.CheckFail, Detail: detail}
}

// Health runs the checks in order: config, creds, server, sign-in, delivery, refused, and always returns all six rows. A
// row that an earlier failure means it cannot run is skipped: a failed config skips all five after it; sign-in needs
// creds and server; delivery needs the server and sign-in, and a session (at a shell it is always skipped); refused needs nothing but the configuration. The creds row and the server row do not wait on each other (the server is probed
// with no credentials). At a shell with no endpoint chosen and none installed it returns cmds.ErrNoEndpointInstalled.
func (h *healthSvc) Health(ctx context.Context) (cmds.HealthReport, error) {
	var se *config.SeveralEndpointsError
	if errors.As(h.cfgErr, &se) {
		return cmds.HealthReport{}, h.cfgErr
	}
	if !h.session && h.cfgErr == nil && h.cfg.Name == "" {
		return cmds.HealthReport{}, cmds.ErrNoEndpointInstalled
	}
	rep := cmds.HealthReport{}
	if h.cfg.Name != "" {
		rep.Endpoint = h.cfg.DisplayName()
	}
	cfgc := h.configCheck()
	rep.Checks = append(rep.Checks, cfgc)
	if cfgc.State == cmds.CheckFail {
		for _, n := range []string{cmds.CheckCreds, cmds.CheckServer, cmds.CheckSignIn, cmds.CheckDelivery, cmds.CheckRefused} {
			rep.Checks = append(rep.Checks, skipped(n, "config"))
		}
		return rep, nil
	}
	parent := ctx
	if d := cliDeadline(h.cfg); d > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d)
		defer cancel()
	}
	creds := h.credsCheck()
	server := h.serverCheck(ctx)
	rep.Checks = append(rep.Checks, creds, server)
	signedIn := false
	switch {
	case creds.State == cmds.CheckFail:
		rep.Checks = append(rep.Checks, skipped(cmds.CheckSignIn, "creds"))
	case server.State == cmds.CheckFail:
		rep.Checks = append(rep.Checks, skipped(cmds.CheckSignIn, "server"))
	default:
		sign, online := h.signInCheck(ctx)
		rep.Checks = append(rep.Checks, sign)
		rep.Online = online
		signedIn = sign.State == cmds.CheckOK
	}
	switch {
	case server.State == cmds.CheckFail:
		rep.Checks = append(rep.Checks, skipped(cmds.CheckDelivery, "server"))
	case !signedIn:
		rep.Checks = append(rep.Checks, cmds.HealthCheck{Name: cmds.CheckDelivery, State: cmds.CheckSkip, Detail: "needs sign-in"})
	case !h.session: // a shell has no session to deliver to
		rep.Checks = append(rep.Checks, cmds.HealthCheck{Name: cmds.CheckDelivery, State: cmds.CheckSkip, Detail: "needs a session"})
	default:
		rep.Checks = append(rep.Checks, h.deliveryCheck())
	}
	rep.Checks = append(rep.Checks, h.refusedCheck()) // the count is kept on this machine: it shows with the server down
	rep.MailHeld = h.mailHeld != nil && h.mailHeld()
	if rep.Lease, rep.LeaseCause = h.leaseNow(); rep.Lease != "" {
		rep.Online = false // this session does not hold the endpoint: its mail does not reach it
	}
	if err := parent.Err(); err != nil { // the caller gave up (an MCP call was cancelled): there is no report to give
		return cmds.HealthReport{}, err
	}
	return rep, nil
}

// openFor is the bound on one connection of a check: open_timeout, or its default when the configuration has none.
func openFor(cfg config.Config) time.Duration {
	if cfg.OpenTimeout > 0 {
		return cfg.OpenTimeout
	}
	return config.DefaultOpenTimeout
}

func callFor(cfg config.Config) time.Duration {
	if cfg.CallTimeout > 0 {
		return cfg.CallTimeout
	}
	return config.DefaultCallTimeout
}
