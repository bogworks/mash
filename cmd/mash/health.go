package main

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/bogworks/mash/internal/cmdcore/cmds"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/natsauth"
	"github.com/bogworks/mash/internal/spool"
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
	// scanning says, in a session, whether it is checking the stored mail before it delivers any (TO-14). nil at a shell.
	scanning func() bool
	// scanSkipped says, in a session, which state directory made it skip the stored-mail scan ("" when it did not). nil at a shell.
	scanSkipped func() string
	// lease says, in a session, whether it holds its endpoint now: "" when it does (or at a shell, where it is nil),
	// else cmds.LeaseOther, LeaseLost or LeaseFailed with the cause of the last.
	lease func() (state, cause string)
	// hooks says, in a session, whether the agent host's hooks have delivered mail in it: endpoint.HookRan, HookNotRun,
	// HookSuspected, HookDegraded or HookRefused. nil at a shell.
	hooks func() string
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
	c.Code = cmds.CodeConfigUnreadable
	var several *config.SeveralEndpointsError
	switch {
	case errors.As(h.cfgErr, &several):
		c.Detail = redactText(strings.TrimPrefix(h.cfgErr.Error(), "mash: "))
		c.Code = cmds.CodeEndpointsAmbiguous
	case errors.Is(h.cfgErr, config.ErrGroupMissing):
		c.Detail = "the endpoint address has no group: " + config.CodeGroupMissing
	case h.cfgErr != nil && !errors.Is(h.cfgErr, config.ErrNoURL) && !errors.Is(h.cfgErr, config.ErrNoOwner):
		c.Detail = redactText(strings.TrimPrefix(h.cfgErr.Error(), "mash: "))
	case h.cfg.Name == "":
		// only a session reaches here: at a shell Health stops before the rows with "no endpoint is installed here"
		c.Detail = cmds.ReasonNoSessionEndpoint
		c.Code = cmds.CodeNoEndpoint
	case func() bool { _, err := h.cfg.Identity(); return err != nil }():
		_, err := h.cfg.Identity()
		c.Detail = redactText(strings.TrimPrefix(err.Error(), "mash: "))
	case h.cfg.URL == "":
		c.Detail = "no server is set"
	default:
		c.State, c.Code = cmds.CheckOK, ""
	}
	c.Reason = c.Detail
	return c
}

// setAsideDeleted is whether p's creds file was set aside by mashctl endpoint delete: <p>.deleted is a regular file.
func setAsideDeleted(p string) bool {
	fi, err := os.Lstat(p + ".deleted")
	return err == nil && fi.Mode().IsRegular()
}

// joinFix is the join hint fix when join can mend the creds file: join writes only <config-dir>/<address>.creds, so
// with a custom creds path (MASH_CREDS or "creds": Load fills Creds with the default when none is set) no join command
// repairs it and there is no hint.
func (h *healthSvc) joinFix(fix string) string {
	if p := credsPath(h.cfg); p == "" || p != h.cfg.Find(h.cfg.CredsName()+".creds") {
		return ""
	}
	return fix
}

// credsCheck is the creds row: the file exists, is a regular file only this user can read, and holds a user JWT and
// its seed. It reads the file to check its shape and prints none of it.
func (h *healthSvc) credsCheck() cmds.HealthCheck {
	c := cmds.HealthCheck{Name: cmds.CheckCreds, State: cmds.CheckFail, Code: cmds.CodeCredsMissing}
	p := credsPath(h.cfg)
	if p == "" {
		c.Detail = "no creds file is chosen"
		c.Reason = c.Detail
		return c
	}
	raw, err := config.ReadCredsFile(p)
	switch {
	case errors.Is(err, fs.ErrNotExist) && setAsideDeleted(p):
		// mashctl endpoint delete set the file aside: the endpoint is gone, and a new creds file would not bring it back
		c.Detail, c.Fix, c.Code = cmds.ReasonEndpointDeleted, cmds.JoinFix, cmds.CodeEndpointDeleted
	case errors.Is(err, fs.ErrNotExist):
		// the row names the file with ~; status keeps its longer sentence
		c.Detail, c.Fix = config.Tilde(p)+" is missing", h.joinFix(cmds.JoinFix)
		c.Reason = "the creds file is missing: " + p
	case err != nil && strings.Contains(err.Error(), "readable by other users"):
		c.Detail = config.Tilde(p) + " can be read by others"
		c.Reason = "the creds file can be read by other users: " + p
	case err != nil && strings.Contains(err.Error(), "not a regular file"):
		c.Detail = "the creds file is not a regular file: " + p
	case err != nil:
		c.Detail = "the creds file cannot be read: " + p
	default:
		if cerr := natsauth.CheckCreds(p, raw); cerr != nil {
			c.Detail = config.Tilde(p) + " is not a creds file"
			c.Reason = "the creds file is not a creds file: " + p
			c.Fix = h.joinFix(cmds.JoinReplaceFix)
		} else {
			c.State, c.Code = cmds.CheckOK, ""
		}
	}
	if c.Reason == "" {
		c.Reason = c.Detail
	}
	return c
}

// serverCheck is the server row: the address is allowed (tls, or plain with MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1) and a server answers.
func (h *healthSvc) serverCheck(ctx context.Context) cmds.HealthCheck {
	c := cmds.HealthCheck{Name: cmds.CheckServer, State: cmds.CheckFail}
	url := config.RedactURL(h.cfg.URL)
	if _, err := endpoint.EndpointTLS(h.cfg); err != nil {
		// the row is one short line; status keeps the whole refusal as its reason
		c.Detail = "the server address is not tls://: " + url
		c.Code = cmds.CodeServerNoTLS
		c.Reason = redactText(strings.TrimPrefix(err.Error(), "mash: "))
		return c
	}
	octx, cancel := context.WithTimeout(ctx, openFor(h.cfg))
	defer cancel()
	if err := endpoint.CheckServer(octx, h.cfg); err != nil {
		c.Detail, c.Code = "the server did not answer: "+url, cmds.CodeServerUnreachable
		switch {
		case errors.Is(err, endpoint.ErrServerCertUntrusted):
			c.Detail = "the server's certificate is not trusted: " + url
			// no hint: join cannot change the CA of a folder that has an endpoint
			c.Code = cmds.CodeServerUntrusted
		case errors.Is(err, endpoint.ErrServerNotTLS):
			c.Detail = "the server did not offer TLS: " + url
			c.Code = cmds.CodeServerNoTLS
		case errors.Is(err, endpoint.ErrServerRefusedClientCert):
			c.Detail = "the server refused the client certificate: " + url
			c.Code = cmds.CodeCredsRefused
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
func (h *healthSvc) signInCheck(ctx context.Context) (c cmds.HealthCheck, online bool, refused *endpoint.RefusedCounts) {
	c = cmds.HealthCheck{Name: cmds.CheckSignIn, State: cmds.CheckFail}
	octx, cancel := context.WithTimeout(ctx, openFor(h.cfg))
	ep, err := endpoint.OpenClientWith(octx, h.cfg, endpoint.ClientOptions{Lease: true})
	cancel()
	if err != nil {
		switch msg := err.Error(); {
		case endpoint.IsAuthFailure(err):
			c.Detail, c.Reason = "the server refused these credentials", "the server refused its credentials"
			c.Fix, c.Code = h.joinFix(cmds.JoinReplaceFix), cmds.CodeCredsRefused
		case transient(err) || strings.Contains(msg, "no servers available"):
			c.Detail = "the server did not answer: " + config.RedactURL(h.cfg.URL)
			c.Reason, c.Code = c.Detail, cmds.CodeServerUnreachable
		case errors.Is(err, endpoint.ErrCredsMismatch):
			c.Detail = "the creds file belongs to another endpoint than " + h.cfg.DisplayName()
			c.Fix, c.Code = h.joinFix(cmds.JoinReplaceFix), cmds.CodeCredsRefused
		case strings.Contains(msg, "the server refused the client certificate"):
			c.Detail = "the server refused the client certificate: " + config.RedactURL(h.cfg.URL)
			c.Code = cmds.CodeCredsRefused
		case errors.Is(err, endpoint.ErrNotProvisioned):
			c.Detail, c.Fix, c.Code = cmds.ReasonEndpointDeleted, cmds.JoinFix, cmds.CodeEndpointDeleted
		default:
			c.Detail, c.Code = redactText(strings.TrimPrefix(msg, "mash: ")), cmds.CodeUnknown
		}
		if c.Reason == "" {
			c.Reason = c.Detail
		}
		return c, false, nil
	}
	defer ep.Close()
	lctx, lcancel := context.WithTimeout(ctx, callFor(h.cfg))
	_, online = ep.OwnLease(lctx)
	lcancel()
	// The refused count is of what the stream still holds: the one read of its range, while the endpoint is open.
	rctx, rcancel := context.WithTimeout(ctx, callFor(h.cfg))
	if rc, err := ep.RefusedCounts(rctx); err == nil {
		refused = &rc
	}
	rcancel()
	c.State = cmds.CheckOK
	return c, online, refused
}

// refusedCounts is what the refused row and status --detail count: the refused messages the stream still holds when it
// could be read (rc), else everything the two lists on this machine record (shown with the server down).
func (h *healthSvc) refusedCounts(rc *endpoint.RefusedCounts) cmds.RefusedCounts {
	if rc == nil {
		d := endpoint.RefusedCountsOnDisk(h.cfg)
		rc = &d
	}
	return cmds.RefusedCounts{Total: rc.Total, ByClass: rc.ByClass}
}

// refusedCheck is the refused row (TO-5): how many messages were refused, whatever the reason. Its state is ok whatever it
// counts: a refused message cannot be mended, so it never changes doctor's verdict or exit status (CHG-14).
func refusedCheck(rc cmds.RefusedCounts) cmds.HealthCheck {
	return cmds.HealthCheck{Name: cmds.CheckRefused, State: cmds.CheckOK, Detail: cmds.RefusedDetail(rc.Total)}
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
	if h.mailHeld != nil && h.mailHeld() {
		detail := "cannot write its mail folder"
		if dir, err := endpoint.SpoolDir(h.cfg); err == nil {
			detail = cmds.CannotWrite + config.Tilde(dir)
		}
		return cmds.HealthCheck{Name: cmds.CheckDelivery, State: cmds.CheckFail, Detail: detail}
	}
	if h.scanning != nil && h.scanning() {
		return cmds.HealthCheck{Name: cmds.CheckDelivery, State: cmds.CheckFail, Detail: cmds.ReasonStoredScan}
	}
	if h.hooks != nil {
		switch h.hooks() {
		case endpoint.HookNotRun:
			return cmds.HealthCheck{Name: cmds.CheckDelivery, State: cmds.CheckWarn, Detail: cmds.ReasonHooksNotRun}
		case endpoint.HookSuspected, endpoint.HookDegraded, endpoint.HookRefused:
			return cmds.HealthCheck{Name: cmds.CheckDelivery, State: cmds.CheckFail, Detail: cmds.ReasonHooksFailing, Fix: cmds.HooksFailingFix}
		}
	}
	return cmds.HealthCheck{Name: cmds.CheckDelivery, State: cmds.CheckOK}
}

// sessionOnDisk is what status says at a shell about the session on this machine, from what its serve recorded in the
// spool: nil when there is none (no session, or its serve is gone).
func (h *healthSvc) sessionOnDisk() *cmds.SessionState {
	dir, err := endpoint.SpoolDir(h.cfg)
	if err != nil {
		return nil
	}
	hooks, storage, ok := endpoint.SessionOnDisk(spool.Spool{Dir: dir})
	if !ok {
		return nil
	}
	return &cmds.SessionState{HoldsEndpoint: true, Hooks: hooks, Storage: storage}
}

// Health runs the checks in order: config, creds, server, sign-in, delivery, refused, and always returns all six rows. A
// row that an earlier failure means it cannot run is skipped: a failed config skips all five after it; sign-in needs
// creds and server; delivery needs the server and sign-in, and a session (at a shell it is skipped, unless the state folder cannot be created and mail waits for the spool: MASH_SPOOL_REQUIRED or MASH_SPOOL_FROM_START); refused needs nothing but the configuration. The creds row and the server row do not wait on each other (the server is probed
// with no credentials). At a shell with no endpoint chosen and none installed it returns cmds.ErrNoEndpointInstalled.
func (h *healthSvc) Health(ctx context.Context) (cmds.HealthReport, error) {
	var se *config.SeveralEndpointsError
	if errors.As(h.cfgErr, &se) && !h.session { // a session reports it as the config row's failure
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
	var stored *endpoint.RefusedCounts
	switch {
	case creds.State == cmds.CheckFail:
		rep.Checks = append(rep.Checks, skipped(cmds.CheckSignIn, "creds"))
	case server.State == cmds.CheckFail:
		rep.Checks = append(rep.Checks, skipped(cmds.CheckSignIn, "server"))
	default:
		sign, online, rc := h.signInCheck(ctx)
		stored = rc
		rep.Checks = append(rep.Checks, sign)
		rep.Online = online
		signedIn = sign.State == cmds.CheckOK
	}
	shellDir, shellErr := endpoint.SpoolDir(h.cfg)
	// A shell has no record of its own and cannot see the session's host, so it asks the system, and only where the
	// settings it can read (MASH_SPOOL_REQUIRED, MASH_SPOOL_FROM_START; the plugin sets the latter) mean mail is held
	// without the spool: otherwise mail stays on the channel and is still delivered.
	folderBroken := !h.session && shellErr == nil && (h.cfg.SpoolRequired || h.cfg.SpoolFromStart) && endpoint.SpoolFolderUnwritable(shellDir)
	// With neither setting visible, the shell cannot tell whether the session holds mail without the spool (the plugin sets
	// MASH_SPOOL_FROM_START in the session), so it warns instead of failing; status is unaffected.
	folderWarn := !h.session && shellErr == nil && !(h.cfg.SpoolRequired || h.cfg.SpoolFromStart) && endpoint.SpoolFolderUnwritable(shellDir)
	switch {
	case server.State == cmds.CheckFail:
		rep.Checks = append(rep.Checks, skipped(cmds.CheckDelivery, "server"))
	case !signedIn:
		rep.Checks = append(rep.Checks, cmds.HealthCheck{Name: cmds.CheckDelivery, State: cmds.CheckSkip, Detail: "needs sign-in"})
	case folderBroken: // the one shell case with a delivery row: no session could store mail here, and mail waits for the spool
		rep.Checks = append(rep.Checks, cmds.HealthCheck{Name: cmds.CheckDelivery, State: cmds.CheckFail, Detail: cmds.CannotWrite + config.Tilde(shellDir)})
	case folderWarn:
		rep.Checks = append(rep.Checks, cmds.HealthCheck{Name: cmds.CheckDelivery, State: cmds.CheckWarn, Detail: cmds.CannotWrite + config.Tilde(shellDir) + "; plugin sessions get no mail until fixed"})
	case !h.session: // a shell has no session to deliver to
		rep.Checks = append(rep.Checks, cmds.HealthCheck{Name: cmds.CheckDelivery, State: cmds.CheckSkip, Detail: "checked only inside a Claude Code session"})
	default:
		rep.Checks = append(rep.Checks, h.deliveryCheck())
	}
	rep.Refused = h.refusedCounts(stored) // the lists are kept on this machine: they show with the server down
	rep.Checks = append(rep.Checks, refusedCheck(rep.Refused))
	rep.MailHeld = h.mailHeld != nil && h.mailHeld()
	rep.StoredScan = h.scanning != nil && h.scanning()
	if h.scanSkipped != nil {
		rep.StoredSkipDir = config.Tilde(h.scanSkipped())
	}
	if !h.session { // a shell on the session's machine reads what its serve recorded, as the tool would say it, whatever the sign-in said
		if rep.Session = h.sessionOnDisk(); rep.Session != nil && rep.Session.Storage == cmds.StorageFailing {
			rep.MailHeld = true
		}
		if folderBroken {
			rep.MailHeld = true
		}
		if dir, err := endpoint.SpoolDir(h.cfg); err == nil && endpoint.ScanOnDisk(spool.Spool{Dir: dir}) {
			rep.StoredScan = true
		}
		if dir, err := endpoint.SpoolDir(h.cfg); err == nil {
			rep.StoredSkipDir = config.Tilde(endpoint.ScanSkipOnDisk(spool.Spool{Dir: dir}))
		}
	}
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
