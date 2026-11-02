package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/bootstrap"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctlkeys"
	"github.com/bogworks/mash/internal/natsauth"
)

// lookupWait bounds the account lookup of adopt (a variable so tests need not wait for the full time).
var lookupWait = 5 * time.Second

// What one endpoint needs of the account (docs/operations.md, "Account limits"): its inbox stream plus the two KV
// buckets, one consumer, and the default inbox cap (16 MiB) plus the two buckets' caps (16 MiB and 4 MiB).
const (
	needStreams   = 3
	needConsumers = 1
	needDisk      = (16 + 16 + 4) << 20
)

func adoptUsage() string {
	return "usage: mashctl adopt --server URL [--account NAME] --operator-key FILE --account-key FILE --sys-creds FILE [--admin-creds FILE | --mint-admin] [--owner O] [--dry-run]\n"
}

type adoptOpts struct {
	server, account, owner            string
	operatorKey, accountKey, sysCreds string
	adminCreds                        string
	mintAdmin, dryRun                 bool
}

func parseAdoptArgs(args []string) (adoptOpts, error) {
	var o adoptOpts
	str := map[string]*string{
		"--server": &o.server, "--account": &o.account, "--owner": &o.owner, "--operator-key": &o.operatorKey,
		"--account-key": &o.accountKey, "--sys-creds": &o.sysCreds, "--admin-creds": &o.adminCreds,
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--mint-admin":
			o.mintAdmin = true
			continue
		case "--dry-run":
			o.dryRun = true
			continue
		}
		name, val, hasEq := strings.Cut(a, "=")
		dst, ok := str[name]
		if !ok {
			return o, fmt.Errorf("unknown argument %q", a)
		}
		if !hasEq {
			if i+1 >= len(args) {
				return o, fmt.Errorf("%s needs a value", name)
			}
			i++
			val = args[i]
		}
		if val == "" {
			return o, fmt.Errorf("%s needs a value", name)
		}
		*dst = val
	}
	return o, nil
}

// adoptState is what the read-only checks learned and the write phase needs.
type adoptState struct {
	opKP, acctKP     nkeys.KeyPair
	opPub, acctPub   string
	sysCredsRaw      []byte
	trust            apply.Trust
	adminCredsAbs    string
	adminRaw         []byte
	serverDir        string
	sysDst, adminDst string
	cfgPath, polPath string
	haveOp, haveAcct bool // the key store already holds this very key
	haveSys          bool // sys.creds already in place with the same content
	haveAdmin        bool // mashadmin.creds already in place (kept)
}

// runAdopt is `mashctl adopt`: the counterpart of `init --local` for a server you already run.
func runAdopt(args []string, env func(string) string, stdout, stderr io.Writer) int {
	const pfx = "mashctl: adopt: "
	o, err := parseAdoptArgs(args)
	if err != nil {
		fmt.Fprintf(stderr, "%s%v\n%s", pfx, err, adoptUsage())
		return 2
	}
	var missing []string
	need := func(v, flag, why string) {
		if v == "" {
			missing = append(missing, flag+" ("+why+")")
		}
	}
	need(o.server, "--server", "the NATS URL of the server you run")
	need(o.operatorKey, "--operator-key", "the operator signing seed: mashctl cannot create authority on a server it did not set up, so adopt needs yours")
	need(o.accountKey, "--account-key", "the identity seed of the account mash uses: mashctl cannot create an account on a server it did not set up")
	need(o.sysCreds, "--sys-creds", "system account user creds: they prove access to the resolver and are how `mashctl policy apply` pushes")
	if len(missing) > 0 {
		fmt.Fprintf(stderr, "%smissing: %s\nadopt takes over a server you already run in operator mode; it cannot stand one up or reconfigure one from a URL (use `mashctl init --local` for a new local server)\n%s", pfx, strings.Join(missing, "; "), adoptUsage())
		return 2
	}
	if o.mintAdmin && o.adminCreds != "" {
		fmt.Fprintf(stderr, "%s--admin-creds and --mint-admin are alternatives: pass one\n%s", pfx, adoptUsage())
		return 2
	}
	fail := func(format string, a ...any) int { fmt.Fprintf(stderr, pfx+format+"\n", a...); return 1 }

	cfg, err := config.Load(env, env("HOME"))
	if err != nil && !errors.Is(err, config.ErrNoConfigDir) {
		fmt.Fprintln(stderr, err)
		return 1
	}
	owner := o.owner
	if owner == "" {
		owner = cfg.Owner
	}
	if owner == "" {
		return fail("no owner: pass --owner O (or set \"owner\" in config.json or MASH_OWNER)")
	}
	if !config.ValidName(owner) {
		return fail("owner %q is not a valid name: use lower-case letters, digits and single hyphens, starting with a letter, up to 48 characters", owner)
	}
	account := o.account
	if account == "" {
		account = owner
	}
	if cfg.Dir == "" {
		return fail("no config directory: set MASH_CONFIG_DIR, or HOME or XDG_CONFIG_HOME")
	}
	keyDir, err := ctlkeys.DirFor(cfg, env)
	if err != nil {
		return fail("%v", err)
	}
	ks, err := ctlkeys.Open(keyDir)
	if err != nil {
		return fail("%v", err)
	}

	st := &adoptState{serverDir: filepath.Join(cfg.Dir, "server"), cfgPath: cfg.Find("config.json"), polPath: cfg.Find("policy.yaml")}
	st.sysDst = filepath.Join(st.serverDir, bootstrap.SysCreds)
	st.adminDst = filepath.Join(st.serverDir, bootstrap.AdminCreds)
	defer func() {
		if st.opKP != nil {
			st.opKP.Wipe()
		}
		if st.acctKP != nil {
			st.acctKP.Wipe()
		}
	}()

	fmt.Fprintf(stdout, "mashctl adopt: checking %s as owner %q, account %q (read-only: nothing is written until every check passes)\n", o.server, owner, account)

	// (1) the files.
	if st.opKP, st.opPub, err = ctlkeys.ParseOperatorSeed(o.operatorKey); err != nil {
		return fail("--operator-key: %v\n  it must be a 0600 file holding the operator SIGNING seed (it starts with SO)", err)
	}
	if st.acctKP, st.acctPub, err = ctlkeys.ParseAccountSeed(o.accountKey); err != nil {
		return fail("--account-key: %v\n  it must be a 0600 file holding the IDENTITY seed of the mash account (it starts with SA; its public key is the account JWT's subject)", err)
	}
	if st.sysCredsRaw, err = readUserCreds(o.sysCreds); err != nil {
		return fail("--sys-creds: %v", err)
	}
	if o.adminCreds != "" {
		if st.adminCredsAbs, err = filepath.Abs(o.adminCreds); err != nil {
			return fail("--admin-creds: %v", err)
		}
		if st.adminRaw, err = readUserCreds(o.adminCreds); err != nil {
			return fail("--admin-creds: %v", err)
		}
	}
	// adopt installs no operator.jwt: the accepted issuer is the operator key given
	if st.trust, err = apply.NewTrust(st.opPub, ""); err != nil {
		return fail("%v (nothing was changed)", err)
	}
	fmt.Fprintf(stdout, "  1. files          ok: operator key %s, account key %s, system creds (modes 0600, seeds not printed)\n", st.opPub, st.acctPub)

	// The key store and the destination files must not conflict: checked now so nothing is written on a refusal.
	if err := checkStore(ks, st); err != nil {
		return fail("%v", err)
	}
	if err := checkDestinations(o, st); err != nil {
		return fail("%v", err)
	}

	// (2) the server: operator mode, system creds, the resolver. Admin and system connections require TLS, so a URL
	// that is refused for that is refused before any connection is made.
	tlsReq, err := natsauth.RequireTLS(o.server, cfg.InsecureLoopback)
	if err != nil {
		return fail("%v (nothing was changed)", err)
	}
	if c, aerr := nats.Connect(o.server, append(natsauth.TLSOptions(cfg.TLSCA, cfg.TLSCert, cfg.TLSKey), nats.Name("mashctl-adopt-probe"), nats.Timeout(connectTimeout), nats.MaxReconnects(0))...); aerr == nil {
		c.Close()
		return fail("%s accepts a connection without credentials: it does not look like an operator-mode server with a full resolver, so adopt has nothing to take over (nothing was changed)", o.server)
	}
	nc, err := nats.Connect(o.server, append(append(natsauth.TLSOptions(cfg.TLSCA, cfg.TLSCert, cfg.TLSKey), tlsReq...),
		natsauth.UserCredentialBytes(st.sysCredsRaw), nats.Name("mashctl-adopt"), nats.Timeout(connectTimeout), nats.MaxReconnects(0))...)
	if err != nil {
		return fail("connect %s with the system creds: %v (a refused connection can mean the server is unreachable or a TLS problem: check the URL, the port and the tls_ca, tls_cert and tls_key settings; nothing was changed)", o.server, natsauth.TLSHint(err))
	}
	defer nc.Close()
	ctx, cancel := context.WithTimeout(context.Background(), applyDeadline)
	defer cancel()
	res := apply.NATS{Conn: nc}
	set, err := res.Servers(ctx)
	if err != nil {
		return fail("ping the servers: %v (nothing was changed)", err)
	}
	if len(set.Names) == 0 {
		return fail("the system account ping (%s) was answered by no server: this does not look like an operator-mode server with a full resolver, or --sys-creds are not system account user creds (nothing was changed)", "$SYS.REQ.SERVER.PING")
	}
	fmt.Fprintf(stdout, "  2. server         ok: refused a connection without credentials (operator mode); the system creds connect\n")

	lctx, lcancel := context.WithTimeout(ctx, lookupWait)
	tok, err := res.Lookup(lctx, st.acctPub)
	lcancel()
	if err != nil || tok == "" {
		detail := "the resolver has no answer for it"
		if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, nats.ErrTimeout) {
			detail = err.Error()
		}
		return fail("account %s is not on the resolver (%s): adopt needs an account that already exists; it cannot create one. The server answers pings, so it is the account that is missing (or the resolver is not a full resolver). Check --account-key (nothing was changed)", st.acctPub, detail)
	}
	claims, err := apply.VerifyAccountJWT(tok, st.acctPub, st.trust)
	if err != nil {
		return fail("refusing the resolver's JWT for %s: %v (nothing was changed)", st.acctPub, err)
	}
	warns, err := verifyAccount(claims, st.acctPub, account, time.Now().Unix())
	if err != nil {
		return fail("%v (nothing was changed)", err)
	}
	fmt.Fprintf(stdout, "  2. account        ok: %q found on the resolver, subject = the account key, not expired. Not visible from here: whether the operator has revoked it\n", claims.Name)

	// (3) JetStream.
	lim, tier, err := jsLimits(claims)
	if err != nil {
		return fail("%v (nothing was changed)", err)
	}
	fmt.Fprintf(stdout, "  3. jetstream      ok: enabled for the account%s: disk %s, streams %s, consumers %s\n", tier, limitText(lim.DiskStorage, true), limitText(lim.Streams, false), limitText(lim.Consumer, false))
	if lim.Streams > 0 && lim.Streams < needStreams {
		warns = append(warns, fmt.Sprintf("the account allows %d streams; one endpoint needs %d (its inbox plus the two KV buckets), one more per endpoint", lim.Streams, needStreams))
	}
	if lim.Consumer > 0 && lim.Consumer < needConsumers {
		warns = append(warns, fmt.Sprintf("the account allows %d consumers; mash needs one per endpoint", lim.Consumer))
	}
	if lim.DiskStorage > 0 && lim.DiskStorage < needDisk {
		warns = append(warns, fmt.Sprintf("the account has %s of disk; one endpoint needs about %s (inbox cap plus the two buckets); see docs/operations.md, Account limits", config.FormatSize(lim.DiskStorage), config.FormatSize(needDisk)))
	}

	// (4) the transport.
	shape := "standalone"
	if set.Cluster != "" {
		shape = fmt.Sprintf("cluster %q", set.Cluster)
	}
	fmt.Fprintf(stdout, "  4. system user    ok: the ping was answered (%s, %d server(s) replied) and the claims lookup worked. The claims update (push) permission is not exercised here: `mashctl policy apply` is the first to use it\n", shape, len(set.Names))
	if set.Cluster != "" || set.Gateways {
		warns = append(warns, "the server is part of a cluster: pass --servers N to `mashctl policy apply` so it can confirm every server took the update")
	}

	// (5) can the operator key sign for this account?
	if claims.Issuer == st.opPub {
		fmt.Fprintf(stdout, "  5. operator key   ok: it issued the account JWT (issuer %s), whose signature is valid\n", claims.Issuer)
	} else {
		fmt.Fprintf(stdout, "  5. operator key   ok: the account JWT's valid signature is by %s, a signing key the operator JWT declares (the operator key %s is declared too)\n", claims.Issuer, st.opPub)
	}

	if st.adminRaw != nil {
		if w, err := checkAdminCreds(cfg, o.server, st); err != nil {
			return fail("--admin-creds: %v (nothing was changed)", err)
		} else if w != "" {
			warns = append(warns, w)
		}
	}
	for _, w := range warns {
		fmt.Fprintf(stdout, "  warning: %s\n", w)
	}
	nc.Close()

	// The write phase.
	verb := map[bool]string{true: "would ", false: ""}[o.dryRun]
	fmt.Fprintf(stdout, "\nAll checks passed.")
	if o.dryRun {
		fmt.Fprintf(stdout, " --dry-run: nothing is written. This is what adopt would do:")
	}
	fmt.Fprintln(stdout)
	step := func(done string, a ...any) { fmt.Fprintf(stdout, "  "+done+"\n", a...) }

	if o.dryRun {
		step("%simport the operator key %s into %s (operator.nk, 0600)%s", verb, st.opPub, keyDir, already(st.haveOp))
		step("%simport the account key %s into %s (account.nk, 0600)%s", verb, st.acctPub, keyDir, already(st.haveAcct))
		step("%scopy the system creds to %s (0600)%s", verb, st.sysDst, already(st.haveSys))
	} else {
		if _, created, err := ks.ImportOperator(o.operatorKey); err != nil {
			return fail("%v\n  nothing else was written; running adopt again is safe", err)
		} else {
			step("operator key %s %s in %s", st.opPub, map[bool]string{true: "imported", false: "already stored"}[created], keyDir)
		}
		if _, created, err := ks.ImportAccount(o.accountKey); err != nil {
			return fail("%v\n  the operator key was imported; running adopt again is safe", err)
		} else {
			step("account key %s %s in %s", st.acctPub, map[bool]string{true: "imported", false: "already stored"}[created], keyDir)
		}
		if err := ensureDir(st.serverDir); err != nil {
			return fail("%v", err)
		}
		if created, err := ctlkeys.InstallFile(st.sysDst, st.sysCredsRaw); err != nil {
			return fail("%v", err)
		} else {
			step("system creds %s at %s", map[bool]string{true: "copied", false: "already there"}[created], st.sysDst)
		}
	}

	adminPath := ""
	switch {
	case o.adminCreds != "":
		adminPath = st.adminCredsAbs
		step("%srecord the admin creds path %s (the file is not copied)", verb, adminPath)
	case o.mintAdmin && st.haveAdmin:
		adminPath = st.adminDst
		step("%skeep the existing %s (it is valid creds for this account; not minted again)", verb, st.adminDst)
	case o.mintAdmin:
		adminPath = st.adminDst
		if o.dryRun {
			step("would mint a mashadmin user in the account, signed by the account key, and write %s (0600)", st.adminDst)
			break
		}
		raw, err := bootstrap.MintAdminCreds(st.acctKP)
		if err != nil {
			return fail("%v", err)
		}
		created, err := ctlkeys.InstallFile(st.adminDst, raw)
		clear(raw)
		if err != nil {
			return fail("%v", err)
		}
		if !created {
			return fail("%s appeared while adopt ran: left as it is; run adopt again", st.adminDst)
		}
		step("minted the mashadmin user and wrote %s (0600)", st.adminDst)
	}

	settings := map[string]string{"url": o.server, "owner": owner}
	if adminPath != "" {
		settings["admin_creds"] = adminPath
	}
	if cfg.InsecureLoopback {
		settings["insecure_loopback"] = "true"
	}
	if cfg.TLSCA != "" {
		settings["tls_ca"] = cfg.TLSCA
	}
	if cfg.TLSCert != "" && cfg.TLSKey != "" {
		settings["tls_cert"], settings["tls_key"] = cfg.TLSCert, cfg.TLSKey
	}
	if config.Absent(st.cfgPath) {
		step("%swrite %s with %s", verb, st.cfgPath, settingsText(settings))
		if !o.dryRun {
			if err := ensureDir(filepath.Dir(st.cfgPath)); err != nil {
				return fail("%v", err)
			}
			if _, err := ctlkeys.InstallFile(st.cfgPath, configJSON(settings)); err != nil {
				return fail("%v", err)
			}
		}
	} else {
		step("%s exists and is left as it is; make sure it has: %s", st.cfgPath, settingsText(settings))
	}
	if config.Absent(st.polPath) {
		step("%swrite %s (open: default allow)", verb, st.polPath)
		if !o.dryRun {
			if err := ensureDir(filepath.Dir(st.polPath)); err != nil {
				return fail("%v", err)
			}
			if _, err := ctlkeys.InstallFile(st.polPath, []byte(bootstrap.OpenPolicy)); err != nil {
				return fail("%v", err)
			}
		}
	} else {
		step("%s exists, left as it is", st.polPath)
	}

	fmt.Fprintf(stdout, "\nNext:\n  1. mashctl init                                  the directory and lease buckets (needs the admin creds)\n  2. mashctl add NAME ...                          declare an endpoint and mint its creds\n  3. mashctl policy apply                          push the policy's zone keys to the account\n")
	if adminPath == "" {
		fmt.Fprintf(stdout, "  no admin creds were given: `mashctl init` and `add` need them. Pass --admin-creds FILE or --mint-admin, or set \"admin_creds\" in config.json\n")
	}
	fmt.Fprintf(stdout, "\nWhat adopt does not do: it changes nothing on the server. It pushes no account update, creates no users on the server (the optional mashadmin exists only as a local creds file) and no buckets. The cutover of a live account, and revoking old credentials, is a separate step you run yourself.\n")
	return 0
}

// verifyAccount checks the account JWT the resolver gave for acctPub: its subject is that key, it is inside its
// validity window and its name is the one the operator named. A JWT without a name only warns.
func verifyAccount(claims *jwt.AccountClaims, acctPub, account string, now int64) (warns []string, err error) {
	if claims.Subject != acctPub {
		return nil, fmt.Errorf("the account JWT's subject is %s, not the account key's public key %s: --account-key is not this account's identity key", claims.Subject, acctPub)
	}
	if claims.Expires != 0 && claims.Expires <= now {
		return nil, fmt.Errorf("the account JWT expired %s: a server will not accept its users", time.Unix(claims.Expires, 0).UTC().Format(time.RFC3339))
	}
	if claims.NotBefore != 0 && claims.NotBefore > now {
		return nil, fmt.Errorf("the account JWT is not valid before %s", time.Unix(claims.NotBefore, 0).UTC().Format(time.RFC3339))
	}
	switch {
	case claims.Name == "":
		warns = append(warns, fmt.Sprintf("the account JWT has no name, so --account %q was not checked against it", account))
	case claims.Name != account:
		return nil, fmt.Errorf("the account JWT is named %q, not %q: pass --account %s if that is the account (--account defaults to the owner)", claims.Name, account, claims.Name)
	}
	return warns, nil
}

func already(have bool) string {
	if have {
		return " (already stored: nothing to do)"
	}
	return ""
}

func settingsText(m map[string]string) string {
	var parts []string
	for _, k := range []string{"url", "owner", "admin_creds", "insecure_loopback", "tls_ca", "tls_cert", "tls_key"} {
		if v, ok := m[k]; ok {
			parts = append(parts, fmt.Sprintf("%q: %s", k, jsonValue(k, v)))
		}
	}
	return strings.Join(parts, ", ")
}

func configJSON(m map[string]string) []byte {
	var b bytes.Buffer
	b.WriteString("{\n")
	var parts []string
	for _, k := range []string{"url", "owner", "admin_creds", "insecure_loopback", "tls_ca", "tls_cert", "tls_key"} {
		if v, ok := m[k]; ok {
			parts = append(parts, fmt.Sprintf("  %q: %s", k, jsonValue(k, v)))
		}
	}
	b.WriteString(strings.Join(parts, ",\n"))
	b.WriteString("\n}\n")
	return b.Bytes()
}

// readCreds reads a user creds file (0600, operator-mode JWT creds).
func readUserCreds(path string) ([]byte, error) {
	if err := config.CheckCredsMode(path); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := natsauth.CheckCreds(path, raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// checkStore refuses when the key store holds a different operator or account key than the files given.
func checkStore(ks *ctlkeys.Authority, st *adoptState) error {
	for _, k := range []struct {
		what string
		get  func() (nkeys.KeyPair, string, error)
		want string
		have *bool
		none error
	}{
		{"operator", ks.Operator, st.opPub, &st.haveOp, ctlkeys.ErrNoOperatorKey},
		{"account", ks.Account, st.acctPub, &st.haveAcct, ctlkeys.ErrNoAccountKey},
	} {
		kp, pub, err := k.get()
		switch {
		case errors.Is(err, k.none):
		case err != nil:
			return err
		default:
			kp.Wipe()
			if pub != k.want {
				return fmt.Errorf("the key store already holds a different %s key (%s, not %s): refusing to replace it. Adopt with the key store of its own (MASH_KEYS_DIR), or move the old key aside if you mean to replace it (nothing was changed)", k.what, pub, k.want)
			}
			*k.have = true
		}
	}
	return nil
}

// checkDestinations refuses files that would have to be replaced: no clobber.
func checkDestinations(o adoptOpts, st *adoptState) error {
	if b, err := os.ReadFile(st.sysDst); err == nil {
		if !bytes.Equal(b, st.sysCredsRaw) {
			return fmt.Errorf("%s already holds different system creds: refusing to replace them. Move it aside, or use it (--sys-creds %s) (nothing was changed)", st.sysDst, st.sysDst)
		}
		st.haveSys = true
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if o.mintAdmin {
		if b, err := os.ReadFile(st.adminDst); err == nil {
			if err := config.CheckCredsMode(st.adminDst); err != nil {
				return err
			}
			uc, err := userClaims(b)
			if err != nil || !(uc.IssuerAccount == st.acctPub || uc.Issuer == st.acctPub) {
				return fmt.Errorf("%s exists but is not valid creds for this account: refusing to replace it. Move it aside to mint a new one (nothing was changed)", st.adminDst)
			}
			st.haveAdmin = true
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	for _, d := range []string{st.serverDir} {
		if fi, err := os.Lstat(d); err == nil && (fi.Mode()&fs.ModeSymlink != 0 || !fi.IsDir() || fi.Mode().Perm()&0o077 != 0) {
			return fmt.Errorf("%s is not a private directory (a plain directory, mode 0700): it holds credentials (nothing was changed)", d)
		}
	}
	return nil
}

func userClaims(raw []byte) (*jwt.UserClaims, error) {
	tok, err := nkeys.ParseDecoratedJWT(raw)
	if err != nil {
		return nil, err
	}
	return jwt.DecodeUserClaims(tok)
}

// checkAdminCreds connects with the admin creds: they must work against this server. A user of another account is
// refused; one signed by a signing key (not the identity key) draws a warning only.
func checkAdminCreds(cfg config.Config, url string, st *adoptState) (string, error) {
	uc, err := userClaims(st.adminRaw)
	if err != nil {
		return "", err
	}
	warn := ""
	if uc.IssuerAccount != "" && uc.IssuerAccount != st.acctPub {
		return "", fmt.Errorf("the creds belong to account %s, not %s", uc.IssuerAccount, st.acctPub)
	}
	if uc.IssuerAccount == "" && uc.Issuer != st.acctPub {
		warn = fmt.Sprintf("the admin creds were signed by %s, not the account identity key; adopt could not tell they belong to this account, only that they connect", uc.Issuer)
	}
	tlsReq, err := natsauth.RequireTLS(url, cfg.InsecureLoopback)
	if err != nil {
		return "", err
	}
	c, err := nats.Connect(url, append(append(natsauth.TLSOptions(cfg.TLSCA, cfg.TLSCert, cfg.TLSKey), tlsReq...),
		natsauth.UserCredentialBytes(st.adminRaw), nats.Name("mashctl-adopt-admin"), nats.Timeout(connectTimeout), nats.MaxReconnects(0))...)
	if err != nil {
		return "", fmt.Errorf("the creds cannot connect to %s: %v", url, natsauth.TLSHint(err))
	}
	c.Close()
	return warn, nil
}

// jsLimits is the JetStream limits of the account (the R1 tier when the account uses tiered limits). It errors when
// the account has no file storage: mash keeps everything on file.
func jsLimits(c *jwt.AccountClaims) (jwt.JetStreamLimits, string, error) {
	lim, tier := c.Limits.JetStreamLimits, ""
	if lim == (jwt.JetStreamLimits{}) && len(c.Limits.JetStreamTieredLimits) > 0 {
		r1, ok := c.Limits.JetStreamTieredLimits["R1"]
		if !ok {
			return lim, "", errors.New("the account has tiered JetStream limits but no R1 tier, which is what mash's streams use")
		}
		lim, tier = r1, " (tier R1)"
	}
	if lim.DiskStorage == 0 {
		return lim, "", errors.New("JetStream is not enabled for the account: its JWT limits give it no file storage (disk 0), and mash keeps everything on file. Enable it first (for example `nsc edit account NAME --js-disk-storage 2G --js-streams 64 --js-consumer 128`, then push)")
	}
	return lim, tier, nil
}

func limitText(n int64, bytes bool) string {
	switch {
	case n < 0:
		return "unlimited"
	case n == 0:
		return "0 (no limit set)"
	case bytes:
		return config.FormatSize(n)
	}
	return strconv.FormatInt(n, 10)
}

func ensureDir(d string) error {
	if fi, err := os.Lstat(d); err == nil {
		if fi.Mode()&fs.ModeSymlink != 0 || !fi.IsDir() {
			return fmt.Errorf("%s is not a plain directory", d)
		}
		return nil
	}
	if err := os.MkdirAll(d, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", d, err)
	}
	return nil
}

// jsonValue is v as a config.json value: insecure_loopback is a boolean, everything else a string.
func jsonValue(key, v string) string {
	if key == "insecure_loopback" {
		return v
	}
	return strconv.Quote(v)
}
