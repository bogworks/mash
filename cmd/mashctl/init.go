package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/nats-io/nkeys"
	"gopkg.in/yaml.v3"

	madmin "github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/apply"
	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/ctlkeys"
	"github.com/bogworks/mash/internal/natsauth"
)

// lookupWait bounds the account lookup of init (a variable so tests need not wait for the full time).
var lookupWait = 5 * time.Second

// sysCredsFile is the name the system account's creds file has in the config folder's server directory.
const sysCredsFile = "sys.creds"

// What one endpoint needs of the account (docs/operations.md, "Account limits"): its inbox stream plus the two KV
// buckets, one consumer, and the default inbox cap (16 MiB) plus the two buckets' caps (16 MiB and 4 MiB).
const (
	needStreams   = 3
	needConsumers = 1
	needDisk      = (16 + 16 + 4) << 20
)

var initSpec = epSpec{
	name: "init", verb: "init", values: []string{"--config"}, flags: []string{"--dry-run"}, min: 0, max: 0,
	check: func(in *epInput) error {
		if in.values["--config"] == "" {
			return cliframe.Usagef("mashctl init needs --config <file>")
		}
		return nil
	},
}

// setupKeys are the keys of the setup file, in the order the file documents them; setupOptional are the ones it may
// leave out.
var (
	setupKeys     = []string{"servers", "ca", "owner", "admin_creds", "system_creds", "operator_key", "account_key"}
	setupOptional = map[string]bool{"ca": true}
)

// setupFile is the setup file `mashctl init --config` reads: paths and addresses, never secrets. Every path in it is
// absolute here: a relative one was resolved against the folder of the file.
type setupFile struct {
	servers                                          []string
	ca, owner                                        string
	adminCreds, systemCreds, operatorKey, accountKey string
}

// loadSetup reads and checks the setup file at path. Its errors name the file and the key, never a value.
func loadSetup(path, home string) (setupFile, error) {
	var sf setupFile
	raw, err := os.ReadFile(path)
	if err != nil {
		return sf, cliframe.Failf("cannot read %s", config.Clean(path))
	}
	shown := config.Clean(path)
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return sf, cliframe.Failf("%s: not a YAML file of keys: %s", shown, config.Clean(strings.TrimPrefix(err.Error(), "yaml: ")))
	}
	var unknown []string
	for k := range doc {
		if !slices.Contains(setupKeys, k) {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return sf, cliframe.Failf("%s: unknown key %s", shown, config.Clean(unknown[0]))
	}
	for _, k := range setupKeys {
		if _, ok := doc[k]; !ok && !setupOptional[k] {
			return sf, cliframe.Failf("%s: missing key %s", shown, k)
		}
	}
	base, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return sf, cliframe.Failf("%s: %v", shown, err)
	}
	text := func(k string) (string, error) {
		v, ok := doc[k]
		if !ok {
			return "", nil
		}
		s, isStr := v.(string)
		if !isStr || strings.TrimSpace(s) == "" {
			return "", cliframe.Failf("%s: %s must be a path", shown, k)
		}
		return strings.TrimSpace(s), nil
	}
	file := func(k string) (string, error) {
		s, err := text(k)
		if err != nil || s == "" {
			return "", err
		}
		s = config.ExpandHome(s, home)
		if !filepath.IsAbs(s) {
			s = filepath.Join(base, s)
		}
		return filepath.Clean(s), nil
	}
	if sf.ca, err = file("ca"); err != nil {
		return sf, err
	}
	if sf.adminCreds, err = file("admin_creds"); err != nil {
		return sf, err
	}
	if sf.systemCreds, err = file("system_creds"); err != nil {
		return sf, err
	}
	if sf.operatorKey, err = file("operator_key"); err != nil {
		return sf, err
	}
	if sf.accountKey, err = file("account_key"); err != nil {
		return sf, err
	}
	if sf.owner, err = text("owner"); err != nil {
		return sf, cliframe.Failf("%s: owner must be a name", shown)
	}
	if !config.ValidName(sf.owner) {
		return sf, cliframe.Failf("%s: owner is not a valid name: use lower-case letters, digits and single hyphens, starting with a letter, up to 48 characters", shown)
	}
	list, ok := doc["servers"].([]any)
	if !ok || len(list) == 0 {
		return sf, cliframe.Failf("%s: servers must be a list of server addresses", shown)
	}
	for _, v := range list {
		s, isStr := v.(string)
		if !isStr || strings.TrimSpace(s) == "" {
			return sf, cliframe.Failf("%s: servers must be a list of server addresses", shown)
		}
		s = strings.TrimSpace(s)
		if config.HasUserinfo(s) {
			return sf, cliframe.Failf("%s: servers must not hold credentials (write the address only, and give the credentials as files)", shown)
		}
		sf.servers = append(sf.servers, s)
	}
	return sf, nil
}

// initState is what the read-only checks learned and the write phase needs.
type initState struct {
	opKP, acctKP     nkeys.KeyPair
	opPub, acctPub   string
	sysCredsRaw      []byte
	trust            apply.Trust
	adminRaw         []byte
	serverDir        string
	sysDst           string
	cfgPath          string
	haveOp, haveAcct bool // the key store already holds this very key
	haveSys          bool // sys.creds already in place with the same content
	haveCfg          bool // config.json already holds these settings
}

// initResult is what `mashctl init --json` prints.
type initResult struct {
	Server     []string `json:"server"`
	Owner      string   `json:"owner"`
	Changed    bool     `json:"changed"`
	DryRun     bool     `json:"dry_run,omitempty"`
	WouldApply *bool    `json:"would_change,omitempty"`
}

// isUnreachable reports whether err is a server that did not answer, as opposed to one that answered and refused.
func isUnreachable(err error) bool {
	var ne net.Error
	return errors.Is(err, nats.ErrNoServers) || errors.Is(err, nats.ErrTimeout) || errors.Is(err, nats.ErrConnectionClosed) ||
		(errors.As(err, &ne) && ne.Timeout()) || strings.Contains(err.Error(), "connection refused") || strings.Contains(err.Error(), "no such host")
}

// runInit is `mashctl init --config FILE`: it checks the server, the keys and the credentials the setup file names,
// keeps what mashctl needs in its key store and config folder, and makes mash's storage on the server. It runs again
// without changing anything.
func runInit(args []string, g cliframe.Global, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	in, err := parseEpInput(initSpec, args)
	if err != nil {
		return cliframe.Report(stdout, stderr, err, jsonRequested(args, "--config"))
	}
	jsonOut := in.json || g.JSON
	return cliframe.Report(stdout, stderr, doInit(in.values["--config"], in.flags["--dry-run"], jsonOut, g.Verbose, env, lookup, stdout, stderr), jsonOut)
}

// step is one `label: value` line of --verbose.
type step struct{ label, value string }

func doInit(setupPath string, dry, jsonOut, verbose bool, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) error {
	sf, err := loadSetup(setupPath, env("HOME"))
	if err != nil {
		return err
	}
	cfg, err := loadConfig(env, lookup)
	if err != nil && !errors.Is(err, config.ErrNoConfigDir) {
		return failErr(err)
	}
	if cfg.Dir == "" {
		return cliframe.Failf("no config directory: set MASH_CONFIG_DIR, or HOME or XDG_CONFIG_HOME")
	}
	cfg.URL, cfg.Owner, cfg.AdminCreds = strings.Join(sf.servers, ","), sf.owner, sf.adminCreds
	if sf.ca != "" {
		if _, err := os.Stat(sf.ca); err != nil {
			return cliframe.Failf("ca: cannot read %s", config.Clean(sf.ca))
		}
		cfg.TLSCA = sf.ca
	}
	first := maskURL(sf.servers[0])
	keyDir, err := ctlkeys.DirFor(cfg, env)
	if err != nil {
		return failErr(err)
	}
	ks, err := ctlkeys.Open(keyDir)
	if err != nil {
		return failErr(err)
	}
	st := &initState{serverDir: filepath.Join(cfg.Dir, "server"), cfgPath: cfg.Find("config.json")}
	st.sysDst = filepath.Join(st.serverDir, sysCredsFile)
	cfg.SysCreds = st.sysDst
	defer func() {
		if st.opKP != nil {
			st.opKP.Wipe()
		}
		if st.acctKP != nil {
			st.acctKP.Wipe()
		}
		clear(st.adminRaw)
	}()

	// (1) the files.
	if st.opKP, st.opPub, err = ctlkeys.ParseOperatorSeed(sf.operatorKey); err != nil {
		return cliframe.Failf("operator_key: %v\n  it must be a 0600 file holding the operator SIGNING seed (it starts with SO)", err)
	}
	if st.acctKP, st.acctPub, err = ctlkeys.ParseAccountSeed(sf.accountKey); err != nil {
		return cliframe.Failf("account_key: %v\n  it must be a 0600 file holding the IDENTITY seed of the mash account (it starts with SA; its public key is the account JWT's subject)", err)
	}
	if st.sysCredsRaw, err = readUserCreds(sf.systemCreds); err != nil {
		return cliframe.Failf("system_creds: %v", err)
	}
	if st.adminRaw, err = readUserCreds(sf.adminCreds); err != nil {
		return cliframe.Failf("admin_creds: %v", err)
	}
	// init installs no operator.jwt: the accepted issuer is the operator key given
	if st.trust, err = apply.NewTrust(st.opPub, ""); err != nil {
		return cliframe.Failf("%v (nothing was changed)", err)
	}

	// The key store, the destination files and config.json must not conflict: checked now so nothing is written on a
	// refusal.
	if err := checkStore(ks, st); err != nil {
		return cliframe.Failf("%v", err)
	}
	if err := checkDestinations(st); err != nil {
		return cliframe.Failf("%v", err)
	}
	if err := checkConfigFile(st, cfg, sf); err != nil {
		return err
	}

	// (2) the server: operator mode, system creds, the resolver. Admin and system connections require TLS, so a URL
	// that is refused for that is refused before any connection is made.
	tlsReq, warning, err := natsauth.RequireTLS(cfg.URL, cfg.InsecurePlaintext)
	if err != nil {
		return cliframe.Failf("%v (nothing was changed)", err)
	}
	if warning != "" {
		fmt.Fprintln(os.Stderr, warning) // the process's stderr, where every admin connection puts it
	}
	unreachable := func() error {
		return cliframe.Failf("cannot reach %s", first).WithHint("mashctl doctor")
	}
	tlsOpts := natsauth.TLSOptions(cfg.TLSCA, cfg.TLSCert, cfg.TLSKey)
	if c, aerr := nats.Connect(cfg.URL, append(slices.Clone(tlsOpts), nats.Name("mashctl-init-probe"), nats.Timeout(connectTimeout), nats.MaxReconnects(0))...); aerr == nil {
		c.Close()
		return cliframe.Failf("%s accepts a connection without credentials: it does not look like an operator-mode server with a full resolver, so init has nothing to take over (nothing was changed)", first)
	} else if isUnreachable(aerr) {
		return unreachable()
	}
	nc, err := nats.Connect(cfg.URL, append(append(slices.Clone(tlsOpts), tlsReq...),
		natsauth.UserCredentialBytes(st.sysCredsRaw), nats.Name("mashctl-init"), nats.Timeout(connectTimeout), nats.MaxReconnects(0))...)
	if err != nil {
		if isUnreachable(err) {
			return unreachable()
		}
		return cliframe.Failf("connect %s with the system creds: %v (a refused connection can mean a TLS problem: check the server address and the ca key of the setup file; nothing was changed)", first, natsauth.TLSHint(err))
	}
	defer nc.Close()
	ctx, cancel := context.WithTimeout(context.Background(), applyDeadline)
	defer cancel()
	res := apply.NATS{Conn: nc}
	set, err := res.Servers(ctx)
	if err != nil {
		return cliframe.Failf("ping the servers: %v (nothing was changed)", config.Clean(err.Error()))
	}
	if len(set.Names) == 0 {
		return cliframe.Failf("the system account ping (%s) was answered by no server: this does not look like an operator-mode server with a full resolver, or system_creds are not system account user creds (nothing was changed)", "$SYS.REQ.SERVER.PING")
	}

	lctx, lcancel := context.WithTimeout(ctx, lookupWait)
	tok, err := res.Lookup(lctx, st.acctPub)
	lcancel()
	if err != nil || tok == "" {
		detail := "the resolver has no answer for it"
		if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, nats.ErrTimeout) {
			detail = config.Clean(err.Error())
		}
		return cliframe.Failf("account %s is not on the resolver (%s): init needs an account that already exists; it cannot create one. The server answers pings, so it is the account that is missing (or the resolver is not a full resolver). Check account_key (nothing was changed)", st.acctPub, detail)
	}
	claims, err := apply.VerifyAccountJWT(tok, st.acctPub, st.trust)
	if err != nil {
		return cliframe.Failf("refusing the resolver's JWT for %s: %v (nothing was changed)", st.acctPub, err)
	}
	warns, err := verifyAccount(claims, st.acctPub, time.Now().Unix())
	if err != nil {
		return cliframe.Failf("%v (nothing was changed)", err)
	}

	// (3) JetStream.
	lim, _, err := jsLimits(claims)
	if err != nil {
		return cliframe.Failf("%v (nothing was changed)", err)
	}
	if lim.Streams > 0 && lim.Streams < needStreams {
		warns = append(warns, fmt.Sprintf("the account allows %d streams; one endpoint needs %d (its inbox plus the two KV buckets), one more per endpoint", lim.Streams, needStreams))
	}
	if lim.Consumer > 0 && lim.Consumer < needConsumers {
		warns = append(warns, fmt.Sprintf("the account allows %d consumers; mash needs one per endpoint", lim.Consumer))
	}
	if lim.DiskStorage > 0 && lim.DiskStorage < needDisk {
		warns = append(warns, fmt.Sprintf("the account has %s of disk; one endpoint needs about %s (inbox cap plus the two buckets); see docs/operations.md, Account limits", config.FormatSize(lim.DiskStorage), config.FormatSize(needDisk)))
	}
	if set.Cluster != "" || set.Gateways {
		warns = append(warns, "the server is part of a cluster: list every server under `servers` in the setup file and run `mashctl init --config` again, so a revocation is confirmed on every server")
	}

	// (4) the admin creds must connect, and the server must answer for the account's JetStream, before anything is
	// written.
	if w, err := checkAdminCreds(cfg, cfg.URL, st); err != nil {
		return cliframe.Failf("admin_creds: %v (nothing was changed)", err)
	} else if w != "" {
		warns = append(warns, w)
	}
	if _, err := askJetStream(cfg, cfg.URL, st.adminRaw, lim.DiskStorage); err != nil {
		return cliframe.Failf("%v (nothing was changed)", err)
	}
	nc.Close()
	for _, w := range warns {
		fmt.Fprintf(stderr, "warning: %s\n", w)
	}

	adm, actx, closeAdmin, err := ctl.OpenAdmin("mashctl", cfg)
	if err != nil {
		return failErr(err)
	}
	defer closeAdmin()
	missing, err := adm.MissingBuckets(actx)
	if err != nil {
		return failErr(err)
	}

	var steps []step
	needOp, needAcct, needSys, needCfg := !st.haveOp, !st.haveAcct, !st.haveSys, !st.haveCfg
	if needOp {
		steps = append(steps, step{"operator key", filepath.Join(keyDir, "operator.nk")})
	}
	if needAcct {
		steps = append(steps, step{"account key", filepath.Join(keyDir, "account.nk")})
	}
	if needSys {
		steps = append(steps, step{"system creds", st.sysDst})
	}
	if len(missing) > 0 {
		steps = append(steps, step{"storage", strings.Join(missing, ", ")})
	}
	if needCfg {
		steps = append(steps, step{"config", st.cfgPath})
	}
	changed := len(steps) > 0

	if dry {
		return finishInit(stdout, jsonOut, verbose, true, changed, sf, first, steps)
	}
	if needOp {
		if _, _, err := ks.ImportOperator(sf.operatorKey); err != nil {
			return cliframe.Failf("%v\n  nothing else was written; running init again is safe", err)
		}
	}
	if needAcct {
		if _, _, err := ks.ImportAccount(sf.accountKey); err != nil {
			return cliframe.Failf("%v\n  the operator key was imported; running init again is safe", err)
		}
	}
	if needSys {
		if err := ensureDir(st.serverDir); err != nil {
			return failErr(err)
		}
		if _, err := ctlkeys.InstallFile(st.sysDst, st.sysCredsRaw); err != nil {
			return failErr(err)
		}
	}
	// Init also hardens and raises what exists (the lease bucket, the bucket caps), so it runs on every real run.
	if err := adm.Init(actx); err != nil {
		return failErr(err)
	}
	if err := ctl.RecordAdminCreds(actx, adm, sf.owner, st.adminRaw); err != nil {
		return cliframe.Failf("the admin user could not be recorded in the issue record (%v): run mashctl init again once the cause is fixed", config.Clean(err.Error()))
	}
	if needCfg {
		if err := ensureDir(filepath.Dir(st.cfgPath)); err != nil {
			return failErr(err)
		}
		if _, err := ctlkeys.InstallFile(st.cfgPath, configJSON(initSettings(cfg, sf, st))); err != nil {
			return failErr(err)
		}
	}
	return finishInit(stdout, jsonOut, verbose, false, changed, sf, first, steps)
}

// finishInit prints the one line of init (and, with verbose, the steps), or the JSON object.
func finishInit(stdout io.Writer, jsonOut, verbose, dry, changed bool, sf setupFile, first string, steps []step) error {
	shown := make([]string, len(sf.servers))
	for i, s := range sf.servers {
		shown[i] = maskURL(s)
	}
	if jsonOut {
		r := initResult{Server: shown, Owner: sf.owner, Changed: changed && !dry}
		if dry {
			r.DryRun = true
			r.WouldApply = &changed
		}
		writeJSON(stdout, r)
		return nil
	}
	switch {
	case !changed:
		fmt.Fprintf(stdout, "Already initialized on %s; nothing changed\n", first)
		return nil
	case dry:
		fmt.Fprintf(stdout, "Would initialize mash on %s for owner %s\n", first, sf.owner)
	default:
		fmt.Fprintf(stdout, "Initialized mash on %s for owner %s\n", first, sf.owner)
	}
	if verbose {
		for _, s := range steps {
			fmt.Fprintf(stdout, "%s: %s\n", s.label, config.Clean(s.value))
		}
	}
	if dry {
		fmt.Fprintln(stdout, "Nothing was changed.")
	}
	return nil
}

// initSettings are the settings config.json gets: the setup file's, and the connection settings the run itself used.
func initSettings(cfg config.Config, sf setupFile, st *initState) map[string]any {
	m := map[string]any{"servers": sf.servers, "owner": sf.owner, "admin_creds": sf.adminCreds, "system_creds": st.sysDst}
	if cfg.TLSCA != "" {
		m["ca"] = cfg.TLSCA
	}
	if cfg.InsecurePlaintext {
		m["insecure_plaintext"] = true
	}
	if cfg.TLSCert != "" && cfg.TLSKey != "" {
		m["tls_cert"], m["tls_key"] = cfg.TLSCert, cfg.TLSKey
	}
	return m
}

// configJSON is the config.json text of the settings m, keys in a fixed order.
func configJSON(m map[string]any) []byte {
	var b bytes.Buffer
	b.WriteString("{\n")
	var parts []string
	for _, k := range []string{"servers", "ca", "owner", "admin_creds", "system_creds", "insecure_plaintext", "tls_cert", "tls_key"} {
		if v, ok := m[k]; ok {
			enc, _ := json.Marshal(v)
			parts = append(parts, fmt.Sprintf("  %q: %s", k, enc))
		}
	}
	b.WriteString(strings.Join(parts, ",\n"))
	b.WriteString("\n}\n")
	return b.Bytes()
}

// checkConfigFile sets st.haveCfg when config.json already holds the settings of the setup file, and refuses when it
// holds different ones: init never replaces a config.json.
func checkConfigFile(st *initState, cfg config.Config, sf setupFile) error {
	raw, err := os.ReadFile(st.cfgPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return failErr(err)
	}
	var have map[string]json.RawMessage
	if err := json.Unmarshal(raw, &have); err != nil {
		return cliframe.Failf("%s is not valid JSON: refusing to replace it. Move it aside (nothing was changed)", st.cfgPath)
	}
	want := initSettings(cfg, sf, st)
	var differ []string
	for _, k := range []string{"servers", "ca", "owner", "admin_creds", "system_creds"} {
		w, ok := want[k]
		if !ok {
			continue
		}
		var got any
		if rawv, present := have[k]; present {
			_ = json.Unmarshal(rawv, &got)
		}
		if k == "servers" {
			if !sameServers(got, sf.servers) {
				differ = append(differ, k)
			}
			continue
		}
		if s, _ := got.(string); s != w.(string) {
			differ = append(differ, k)
		}
	}
	if len(differ) > 0 {
		return cliframe.Failf("%s already holds different settings (%s): init does not replace it. Move it aside, or make the setup file agree with it (nothing was changed)", st.cfgPath, strings.Join(differ, ", "))
	}
	st.haveCfg = true
	return nil
}

// sameServers reports whether a config.json "servers" value (a list, or one comma-separated string) is want.
func sameServers(got any, want []string) bool {
	var l []string
	switch v := got.(type) {
	case string:
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				l = append(l, p)
			}
		}
	case []any:
		for _, e := range v {
			s, _ := e.(string)
			l = append(l, s)
		}
	}
	return slices.Equal(l, want)
}

// verifyAccount checks the account JWT the resolver gave for acctPub: its subject is that key and it is inside its
// validity window.
func verifyAccount(claims *jwt.AccountClaims, acctPub string, now int64) (warns []string, err error) {
	if claims.Subject != acctPub {
		return nil, fmt.Errorf("the account JWT's subject is %s, not the account key's public key %s: account_key is not this account's identity key", claims.Subject, acctPub)
	}
	if claims.Expires != 0 && claims.Expires <= now {
		return nil, fmt.Errorf("the account JWT expired %s: a server will not accept its users", time.Unix(claims.Expires, 0).UTC().Format(time.RFC3339))
	}
	if claims.NotBefore != 0 && claims.NotBefore > now {
		return nil, fmt.Errorf("the account JWT is not valid before %s", time.Unix(claims.NotBefore, 0).UTC().Format(time.RFC3339))
	}
	return nil, nil
}

// readCreds reads a user creds file (0600, operator-mode JWT creds).
func readUserCreds(path string) ([]byte, error) {
	raw, err := config.ReadCredsFile(path)
	if err != nil {
		return nil, err
	}
	if err := natsauth.CheckCreds(path, raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// checkStore refuses when the key store holds a different operator or account key than the files given.
func checkStore(ks *ctlkeys.Authority, st *initState) error {
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
				return fmt.Errorf("the key store already holds a different %s key (%s, not %s): refusing to replace it. Use a key store of its own (MASH_KEYS_DIR), or move the old key aside if you mean to replace it (nothing was changed)", k.what, pub, k.want)
			}
			*k.have = true
		}
	}
	return nil
}

// checkDestinations refuses files that would have to be replaced: no clobber.
func checkDestinations(st *initState) error {
	if b, err := os.ReadFile(st.sysDst); err == nil {
		if !bytes.Equal(b, st.sysCredsRaw) {
			return fmt.Errorf("%s already holds different system creds: refusing to replace them. Move it aside, or point system_creds at it (nothing was changed)", st.sysDst)
		}
		st.haveSys = true
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
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
func checkAdminCreds(cfg config.Config, url string, st *initState) (string, error) {
	uc, err := userClaims(st.adminRaw)
	if err != nil {
		return "", err
	}
	warn := ""
	if uc.IssuerAccount != "" && uc.IssuerAccount != st.acctPub {
		return "", fmt.Errorf("the creds belong to account %s, not %s", uc.IssuerAccount, st.acctPub)
	}
	if uc.IssuerAccount == "" && uc.Issuer != st.acctPub {
		warn = fmt.Sprintf("the admin creds were signed by %s, not the account identity key; init could not tell they belong to this account, only that they connect", uc.Issuer)
	}
	if err := connectAdmin(cfg, url, st.adminRaw); err != nil {
		return "", fmt.Errorf("the creds cannot connect to %s: %v", url, err)
	}
	return warn, nil
}

// connectAdmin connects with admin creds (bytes; never printed) under the rules of every admin connection: TLS
// required unless the loopback opt-in applies. The error carries the server's text.
func connectAdmin(cfg config.Config, url string, raw []byte) error {
	c, err := dialAdmin(cfg, url, raw)
	if err != nil {
		return err
	}
	c.Close()
	return nil
}

// askJetStream asks the server, as an account user, for the account's JetStream info: the one proof that JetStream is
// enabled for the account. It fails with the likely cause when the server says it is not enabled.
func askJetStream(cfg config.Config, url string, raw []byte, limit int64) (string, error) {
	c, err := dialAdmin(cfg, url, raw)
	if err != nil {
		return "", err
	}
	defer c.Close()
	js, err := jetstream.New(c)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ai, err := js.AccountInfo(ctx)
	if err != nil {
		if errors.Is(err, nats.ErrNoResponders) {
			// Asked by this very check: no JetStream service answers for the account, which is how an account whose
			// JetStream the server could not enable looks too.
			return "", madmin.JetStreamNotEnabledHint(fmt.Errorf("%w (%v)", jetstream.ErrJetStreamNotEnabled, err), limit)
		}
		if madmin.IsJetStreamNotEnabled(err) {
			return "", madmin.JetStreamNotEnabledHint(err, limit)
		}
		return "", fmt.Errorf("ask the server for the account's JetStream info: %w", err)
	}
	return fmt.Sprintf("%d stream(s), %d consumer(s), %s of disk in use", ai.Streams, ai.Consumers, config.FormatSize(int64(ai.Store))), nil
}

func dialAdmin(cfg config.Config, url string, raw []byte) (*nats.Conn, error) {
	tlsReq, warning, err := natsauth.RequireTLS(url, cfg.InsecurePlaintext)
	if err != nil {
		return nil, err
	}
	if warning != "" {
		fmt.Fprintln(os.Stderr, warning)
	}
	c, err := nats.Connect(url, append(append(natsauth.TLSOptions(cfg.TLSCA, cfg.TLSCert, cfg.TLSKey), tlsReq...),
		natsauth.UserCredentialBytes(raw), nats.Name("mashctl-init-admin"), nats.Timeout(connectTimeout), nats.MaxReconnects(0))...)
	if err != nil {
		return nil, natsauth.TLSHint(err)
	}
	return c, nil
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
