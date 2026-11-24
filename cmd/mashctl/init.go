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
	"regexp"
	"slices"
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
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

// lookupWait bounds the account lookup of init (a variable so tests need not wait for the full time).
var lookupWait = 5 * time.Second

// sysCredsFile is the name the system account's creds file has in the config folder's server directory.
const sysCredsFile = "sys.creds"

// needConsumers is what one endpoint needs of the account's consumer limit: its inbox's durable consumer. Streams and
// disk follow the buckets init makes and the inbox cap (docs/nats-server.md, "Sizing"); limitWarnings derives them
// from store, with the limits of the configuration.
const needConsumers = 1

// limitWarnings are the warnings for an account whose JetStream limits are lim (a limit of 0 or less is no limit),
// with the inbox and bucket caps of l. One endpoint needs the buckets init makes plus its inbox stream, one consumer,
// and the buckets' caps plus the inbox cap of disk; each further endpoint adds a stream and an inbox cap.
func limitWarnings(lim jwt.JetStreamLimits, l store.Limits) []string {
	var warns []string
	needStreams := store.InitBuckets + 1
	needDisk := store.InitBucketsMaxBytes(l) + l.InboxMaxBytes
	if lim.Streams > 0 && lim.Streams < int64(needStreams) {
		warns = append(warns, fmt.Sprintf("the account allows %d streams; one endpoint needs %d, one more each", lim.Streams, needStreams))
	}
	if lim.Consumer > 0 && lim.Consumer < needConsumers {
		warns = append(warns, fmt.Sprintf("the account allows %d consumers; mash needs one per endpoint", lim.Consumer))
	}
	if lim.DiskStorage > 0 && lim.DiskStorage < needDisk {
		warns = append(warns, fmt.Sprintf("the account has %s of disk; one endpoint needs %s", config.FormatSize(lim.DiskStorage), config.FormatSize(needDisk)))
	}
	return warns
}

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
	setupKeys     = []string{"servers", "ca", "owner", "admin_creds", "system_creds", "operator_key", "account_key", "signing_key"}
	setupOptional = map[string]bool{"ca": true, "signing_key": true}
)

// setupFile is the setup file `mashctl init --config` reads: paths and addresses, never secrets. Every path in it is
// absolute here: a relative one was resolved against the folder of the file.
type setupFile struct {
	servers                                          []string
	ca, owner                                        string
	adminCreds, systemCreds, operatorKey, accountKey string
	signingKey                                       string // optional: the account SIGNING seed, for an operator with strict_signing_key_usage
}

// secretSeed matches an nkey seed: S, then O, A or U, then base32 letters, 20 characters or more in all.
var secretSeed = regexp.MustCompile(`^S[OAU][A-Z2-7]{18,}$`)

// looksLikeSecret reports whether a setup file value that should be a path is a key: an nkey seed or PEM text. It is
// checked before the value is used in any message, so no secret is ever echoed.
func looksLikeSecret(v string) bool {
	return secretSeed.MatchString(v) || strings.Contains(v, "-----BEGIN")
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
		// The name is not echoed: a value put where a key belongs could be a secret (#1065).
		return sf, cliframe.Failf("%s: %s not one of the setup keys (the name is not shown): the setup keys are %s",
			shown, count(len(unknown), "key")+" "+plural(len(unknown), "is", "are"), strings.Join(setupKeys, ", "))
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
		if looksLikeSecret(s) {
			return "", cliframe.Failf("%s: %s holds a key, not a path", shown, k)
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
	if sf.signingKey, err = file("signing_key"); err != nil {
		return sf, err
	}
	if sf.owner, err = text("owner"); err != nil {
		return sf, cliframe.Failf("%s: owner must be a name", shown)
	}
	if !config.ValidName(sf.owner) || len(sf.owner) > sap.MaxTokenLen {
		return sf, cliframe.Failf("%s: owner is not a valid name: use lower-case letters, digits and single hyphens, starting with a letter, up to 32 characters", shown)
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
	signKP           nkeys.KeyPair // the account signing key of signing_key; nil when it is not configured
	opPub, acctPub   string
	signPub          string
	haveSign         bool   // the key store already holds this very signing key
	storedSignPub    string // the public key of signing.nk in the key store when the setup file gives no signing_key
	sysCredsRaw      []byte
	trust            apply.Trust
	adminRaw         []byte
	serverDir        string
	sysDst           string
	cfgPath          string
	folder           string // the configuration folder as shown in a refusal: the home directory written as ~
	keyFolder        string // the key folder as shown in a refusal
	keyDir           string // the key folder as it is on disk
	haveOp, haveAcct bool   // the key store already holds this very key
	haveSys          bool   // sys.creds already in place with the same content
	haveCfg          bool   // config.json already holds these settings
	cfgNeedSignPub   bool   // config.json exists and lacks signing_key_pub: init completes it
	cfgSignPub       string // signing_key_pub of the existing config.json ("" when none)
}

// recordPub is the signing key's public key config.json records: the setup file's signing_key, else the key the key
// store already holds ("" when there is none).
func (st *initState) recordPub() string {
	if st.signPub != "" {
		return st.signPub
	}
	return st.storedSignPub
}

// initResult is what `mashctl init --json` prints.
type initResult struct {
	Server        []string `json:"server"`
	Owner         string   `json:"owner"`
	Changed       bool     `json:"changed"`
	DryRun        bool     `json:"dry_run,omitempty"`
	WouldApply    *bool    `json:"would_change,omitempty"`
	SigningKeyPub string   `json:"signing_key_pub,omitempty"`
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
	return cliframe.Report(stdout, stderr, doInit(in.values["--config"], in.flags["--dry-run"], jsonOut, g.Detail, env, lookup, stdout, stderr), jsonOut)
}

// step is one thing init would set up; any step means the run changes something.
type step struct{ label, value string }

func doInit(setupPath string, dry, jsonOut, detail bool, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) error {
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
	// init opens the store with no expectation: it compares the key store with config.json's signing_key_pub itself, in
	// checkStore, because it is the command that restores the files and completes the field.
	ks, err := ctlkeys.Open(keyDir, "")
	if err != nil {
		return failErr(err)
	}
	st := &initState{serverDir: filepath.Join(cfg.Dir, "server"), cfgPath: cfg.Find("config.json")}
	st.folder = shownFolder(cfg.Dir, env("HOME"))
	st.keyFolder = shownFolder(keyDir, env("HOME"))
	st.keyDir = keyDir
	st.sysDst = filepath.Join(st.serverDir, sysCredsFile)
	cfg.SysCreds = st.sysDst
	defer func() {
		if st.opKP != nil {
			st.opKP.Wipe()
		}
		if st.acctKP != nil {
			st.acctKP.Wipe()
		}
		if st.signKP != nil {
			st.signKP.Wipe()
		}
		clear(st.adminRaw)
	}()

	// (1) the files.
	if st.opKP, st.opPub, err = ctlkeys.ParseOperatorSeed(sf.operatorKey); err != nil {
		return seedFileFailure("operator_key", err, "it must be a 0600 file holding the operator key that signs the account (it starts with SO): its identity key when the server was set up with the nats command, or a signing key if your operator uses one")
	}
	if st.acctKP, st.acctPub, err = ctlkeys.ParseAccountSeed(sf.accountKey); err != nil {
		return seedFileFailure("account_key", err, "it must be a 0600 file holding the IDENTITY seed of the mash account (it starts with SA; its public key is the account JWT's subject)")
	}
	if sf.signingKey != "" {
		if st.signKP, st.signPub, err = ctlkeys.ParseSigningSeed(sf.signingKey); err != nil {
			return seedFileFailure("signing_key", err, "it must be a 0600 file holding the SIGNING seed of the mash account (it starts with SA): a signing key the account JWT lists, not the identity key")
		}
		if st.signPub == st.acctPub {
			return cliframe.Failf("signing_key is the account's identity key: give the identity key as account_key, and a separate signing key the account JWT lists as signing_key (nothing was changed)")
		}
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
	if cfg.SigningKeyPub != "" && !nkeys.IsValidPublicAccountKey(cfg.SigningKeyPub) {
		return cliframe.Failf("signing_key_pub in %s is not an account public key (%q): fix or restore it, or move config.json aside (nothing was changed)", st.cfgPath, cfg.SigningKeyPub)
	}
	st.cfgSignPub = cfg.SigningKeyPub
	if err := checkStore(ks, st); err != nil {
		return failErr(err)
	}
	if err := checkDestinations(st); err != nil {
		return failErr(err)
	}
	if err := checkConfigFile(st, cfg, sf); err != nil {
		return err
	}

	// (2) the server: operator mode, system creds, the resolver. Admin and system connections require TLS, so a URL
	// that is refused for that is refused before any connection is made.
	tlsReq, _, err := natsauth.RequireTLS(cfg.URL, cfg.SkipEncryption)
	if err != nil {
		return withPlainCode(cliframe.Failf("%v (nothing was changed)", err), err)
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

	if st.signPub != "" {
		if reason, advice := checkSigningKeyListed(claims, st.signPub); reason != "" {
			return cliframe.Failf("signing_key: its public key %s %s: %s (nothing was changed)", st.signPub, reason, advice)
		}
	}

	if st.signPub == "" && st.storedSignPub != "" {
		// The setup file names no signing_key, but the stored signing.nk keeps signing endpoint users.
		if reason, _ := checkSigningKeyListed(claims, st.storedSignPub); reason != "" {
			return cliframe.Failf("the key store's signing.nk (public key %s) keeps signing endpoint users and is no longer a plain listed signing key of the account JWT: it %s. Make the key a plain signing key in the account JWT again, or set up a new config folder and key folder (nothing was changed)", st.storedSignPub, reason)
		}
	}

	// (3) JetStream.
	lim, _, err := jsLimits(claims)
	if err != nil {
		return cliframe.Failf("%v (nothing was changed)", err)
	}
	warns = append(warns, limitWarnings(lim, cfg.Limits)...)
	if err := checkClusterListed(set, len(sf.servers), setupPath); err != nil {
		return err
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
	needSign := st.signPub != "" && !st.haveSign
	signing := st.signPub // the signing key that signs endpoint users, "" when the identity key does
	if signing == "" {
		signing = st.storedSignPub
	}
	if needOp {
		steps = append(steps, step{"operator key", filepath.Join(keyDir, "operator.nk")})
	}
	if needAcct {
		steps = append(steps, step{"account key", filepath.Join(keyDir, "account.nk")})
	}
	if needSign {
		steps = append(steps, step{"signing key", filepath.Join(keyDir, "signing.nk")})
	}
	if needSys {
		steps = append(steps, step{"system creds", st.sysDst})
	}
	if len(missing) > 0 {
		steps = append(steps, step{"storage", strings.Join(missing, ", ")})
	}
	if needCfg || st.cfgNeedSignPub {
		steps = append(steps, step{"config", st.cfgPath})
	}
	changed := len(steps) > 0

	if dry {
		return finishInit(stdout, jsonOut, detail, true, changed, sf, first, signing)
	}
	if needOp {
		if pub, _, err := ks.ImportOperator(sf.operatorKey); err != nil {
			return cliframe.Failf("%v\n  nothing else was written; running init again is safe", err)
		} else if pub != st.opPub {
			return cliframe.Failf("operator_key changed while init ran: the key imported is not the one that was checked (nothing else was written)")
		}
	}
	if needAcct {
		if pub, _, err := ks.ImportAccount(sf.accountKey); err != nil {
			return cliframe.Failf("%v\n  the operator key was imported; running init again is safe", err)
		} else if pub != st.acctPub {
			return cliframe.Failf("account_key changed while init ran: the key imported is not the one that was checked; running init again is safe")
		}
	}
	// A config.json that already exists gets signing_key_pub before the key is imported: an init that stops after the
	// import must not leave a key store whose signing key mashctl cannot tell from a lost one (stopped before the import,
	// the field with no files refuses and a rerun imports). A new config.json is written last, with everything else, so
	// a config.json that exists means init got that far.
	cfgWritten := false
	writeConfig := func() error {
		if needCfg {
			if err := ensureDir(filepath.Dir(st.cfgPath)); err != nil {
				return err
			}
			_, err := ctlkeys.InstallFile(st.cfgPath, configJSON(initSettings(cfg, sf, st)))
			return err
		}
		if st.cfgNeedSignPub {
			return addSigningPub(st.cfgPath, st.recordPub())
		}
		return nil
	}
	if st.recordPub() != "" && !needCfg && st.cfgNeedSignPub {
		if err := writeConfig(); err != nil {
			return failErr(err)
		}
		cfgWritten = true
	}
	if needSign {
		if pub, _, err := ks.ImportSigningChecked(sf.signingKey, st.signPub); err != nil {
			if errors.Is(err, ctlkeys.ErrSigningChanged) {
				return cliframe.Failf("%v (nothing was installed for it; running init again is safe)", err)
			}
			return cliframe.Failf("%v\n  the account key was imported; running init again is safe", err)
		} else if pub != st.signPub {
			return cliframe.Failf("signing_key changed while init ran: the key imported is not the one that was checked; running init again is safe")
		}
	}
	if needSys {
		if err := ensureDir(st.serverDir); err != nil {
			return failErr(err)
		}
		if err := installSysCreds(st); err != nil {
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
	if !cfgWritten {
		if err := writeConfig(); err != nil {
			return failErr(err)
		}
	}
	return finishInit(stdout, jsonOut, detail, false, changed, sf, first, signing)
}

// checkClusterListed refuses a setup file that lists too few servers for the cluster the ping saw: a revocation must be
// confirmed on every server, and one listed server cannot say what the others hold.
func checkClusterListed(set apply.ServerSet, listed int, setupPath string) error {
	if set.Cluster == "" && !set.Gateways {
		return nil
	}
	reported := max(set.Expected, len(set.Names))
	if listed >= max(reported, 2) {
		return nil
	}
	of := fmt.Sprintf("%d of %d servers", listed, reported)
	if reported < 2 {
		of = fmt.Sprintf("%d server but a cluster has more than one", listed)
	}
	return cliframe.Failf("the server is part of a cluster but the setup file lists %s", of).WithHint("mashctl init --config " + setupPath)
}

// installSysCreds puts the system creds in the server directory (which must be owned by the effective uid). A file that
// is already there is left alone and must hold the same bytes: the same content is a rerun, different content is a
// refusal.
func installSysCreds(st *initState) error {
	fi, err := os.Lstat(st.serverDir)
	if err != nil {
		return err
	}
	if err := ctlkeys.CheckOwned(fi, st.serverDir, "directory"); err != nil {
		return err
	}
	created, err := ctlkeys.InstallFile(st.sysDst, st.sysCredsRaw)
	if err != nil {
		return err
	}
	if created {
		return nil
	}
	have, err := ctlkeys.ReadOwnedFile(st.sysDst, "system creds")
	if err != nil {
		return err
	}
	defer clear(have)
	if !bytes.Equal(have, st.sysCredsRaw) {
		return differentSetup(st, "system creds")
	}
	return nil
}

// shownFolder is dir with the home directory written as ~; a folder outside the home directory is shown whole.
func shownFolder(dir, home string) string {
	if home != "" {
		if rel, err := filepath.Rel(home, dir); err == nil && (rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))) {
			return filepath.Join("~", rel)
		}
	}
	return dir
}

// seedFileFailure is the refusal of the seed file given as field: the file's own message, without its "mashctl:"
// prefix, and the second line saying what the file must hold only when its content is what is wrong (not its mode).
func seedFileFailure(field string, err error, must string) error {
	msg := strings.TrimPrefix(err.Error(), "mashctl: ")
	var ce *ctlkeys.SeedContentError
	if errors.As(err, &ce) {
		return cliframe.Failf("%s: %s\n  %s", field, msg, must)
	}
	return cliframe.Failf("%s: %s", field, msg)
}

// differentSigningKey is the refusal when the key folder holds a different signing key than signing_key gives.
func differentSigningKey(st *initState, recorded string) error {
	return cliframe.Failf("the key folder %s holds a different signing key (public key %s) than the setup file's signing_key; nothing was changed: give that key's seed as signing_key, or use a new key folder (set MASH_KEYS_DIR)", st.keyFolder, recorded).WithWrap().
		WithHint("MASH_KEYS_DIR=<a new folder> mashctl init --config <file>")
}

// shownSigningRefusal is a signing refusal of the key store as init prints it: the error:/hint: form, the key folder
// written with ~ for the home directory.
func shownSigningRefusal(err error, st *initState) error {
	var ce *cliframe.Error
	if !errors.As(err, &ce) || ce == nil {
		return err
	}
	c := *ce
	c.Msg = strings.Replace(c.Msg, st.keyDir, st.keyFolder, 1)
	return &c
}

// differentSetup is the refusal when the configuration folder holds a different what than the setup file gives: init
// never replaces what the folder holds.
func differentSetup(st *initState, what string) error {
	return cliframe.Failf("%s was set up with a different %s; nothing was changed", st.folder, what).WithWrap().
		WithHint("mashctl init --config <file> --config-dir <a new folder>")
}

// finishInit prints the one line of init or the JSON object.
func finishInit(stdout io.Writer, jsonOut, detail, dry, changed bool, sf setupFile, first, signPub string) error {
	shown := make([]string, len(sf.servers))
	for i, s := range sf.servers {
		shown[i] = maskURL(s)
	}
	if jsonOut {
		r := initResult{Server: shown, Owner: sf.owner, Changed: changed && !dry, SigningKeyPub: signPub}
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
		if detail && signPub != "" {
			// Its own line: the 56-character public key would take the first past 80 columns.
			fmt.Fprintln(stdout, "Signing key: "+signPub)
		}
	}
	if dry {
		fmt.Fprintln(stdout, "Nothing was changed.")
	}
	return nil
}

// initSettings are the settings config.json gets: the setup file's only. A one-off setting of this run (MASH_CA,
// MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION, MASH_TLS_*) or an old config.json's tls_cert is never written.
func initSettings(cfg config.Config, sf setupFile, st *initState) map[string]any {
	m := map[string]any{"servers": sf.servers, "owner": sf.owner, "admin_creds": sf.adminCreds, "system_creds": st.sysDst}
	if sf.ca != "" {
		m["ca"] = sf.ca // the setup file's own ca only: never an environment setting or an old config.json's
	}
	if p := st.recordPub(); p != "" {
		m["signing_key_pub"] = p // the PUBLIC key only: it tells a lost signing key from none ever configured
	}
	return m
}

// configJSON is the config.json text of the settings m, keys in a fixed order.
func configJSON(m map[string]any) []byte {
	var b bytes.Buffer
	b.WriteString("{\n")
	var parts []string
	for _, k := range []string{"servers", "ca", "owner", "admin_creds", "system_creds", "signing_key_pub", "tls_cert", "tls_key"} {
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
		return differentSetup(st, map[string]string{"servers": "server", "ca": "server", "owner": "owner", "admin_creds": "admin creds", "system_creds": "system creds"}[differ[0]])
	}
	var got string
	if rawv, present := have["signing_key_pub"]; present {
		if err := json.Unmarshal(rawv, &got); err != nil || (got != "" && !nkeys.IsValidPublicAccountKey(got)) {
			return cliframe.Failf("signing_key_pub in %s is not an account public key: fix or restore it, or move config.json aside (nothing was changed)", st.cfgPath)
		}
	}
	if p := st.recordPub(); p != "" {
		switch {
		case got != "" && got != p:
			return differentSetup(st, "signing key")
		case got == "":
			if fi, err := os.Lstat(st.cfgPath); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
				return cliframe.Failf("%s is a symbolic link: init will not rewrite it to add signing_key_pub. Point MASH_CONFIG_DIR at the real folder or replace the link with the file (nothing was changed)", st.cfgPath)
			}
			st.cfgNeedSignPub = true
		}
	}
	st.haveCfg = true
	return nil
}

// addSigningPub completes an existing config.json with signing_key_pub (the public key only). It keeps the file's bytes
// and layout: the one member is inserted before the closing brace, in the indentation of the last member. The file is
// then replaced by a 0600 temp file in the same folder renamed over it (a rename replaces whatever is at the path and
// never follows a link), and the folder is synced. The file is opened without following a link and checked on the open
// descriptor, so a link swapped in after the check is not read. A symbolic link is refused, a stored value that is not
// an account public key is an error, and a different stored key is never replaced.
func addSigningPub(path, pub string) error {
	if !nkeys.IsValidPublicAccountKey(pub) {
		return fmt.Errorf("signing key %q is not an account public key", pub)
	}
	notPlain := fmt.Errorf("%s is not a plain file (a symbolic link?): refusing to rewrite it. Replace it with the file itself", path)
	f, err := config.OpenNoFollow(path, os.O_RDONLY, 0)
	if err != nil {
		if config.IsSymlinkRefusal(err) {
			return notPlain
		}
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return notPlain
	}
	raw, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	var have map[string]json.RawMessage
	if err := json.Unmarshal(raw, &have); err != nil {
		return fmt.Errorf("%s is not valid JSON: refusing to replace it. Move it aside", path)
	}
	member, _ := json.Marshal(pub)
	var out []byte
	if rawv, present := have["signing_key_pub"]; present {
		var got string
		if err := json.Unmarshal(rawv, &got); err != nil || (got != "" && !nkeys.IsValidPublicAccountKey(got)) {
			return fmt.Errorf("signing_key_pub in %s is not an account public key: refusing to replace it. Fix or restore it", path)
		}
		if got == pub {
			return nil
		}
		if got != "" {
			return fmt.Errorf("%s records another signing key (%s): refusing to replace it", path, got)
		}
		// An empty field: the member exists, so the file is re-encoded (keys sorted, indent 2, & kept as it is).
		have["signing_key_pub"] = member
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(have); err != nil {
			return err
		}
		out = b.Bytes()
	} else {
		var err error
		if out, err = insertMember(raw, "signing_key_pub", member); err != nil {
			return err
		}
	}
	var check map[string]json.RawMessage
	if err := json.Unmarshal(out, &check); err != nil || !bytes.Equal(check["signing_key_pub"], member) || len(check) != len(have)+boolInt(!hasKey(have, "signing_key_pub")) {
		return fmt.Errorf("could not add signing_key_pub to %s safely: add it by hand", path)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config.json.tmp-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, werr := tmp.Write(out)
	if werr == nil {
		werr = tmp.Chmod(fi.Mode().Perm())
	}
	if werr == nil {
		werr = tmp.Sync()
	}
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return werr
	}
	// Another writer may have changed config.json since it was read: replace only the bytes that were read.
	if now, err := os.ReadFile(path); err != nil || !bytes.Equal(now, raw) {
		return fmt.Errorf("%s changed while init was adding signing_key_pub: nothing was written; run init again", path)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func hasKey(m map[string]json.RawMessage, k string) bool { _, ok := m[k]; return ok }

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// insertMember adds `"key": value` as the last member of the JSON object in raw, keeping every other byte.
func insertMember(raw []byte, key string, value []byte) ([]byte, error) {
	const ws = " \t\r\n"
	end := len(bytes.TrimRight(raw, ws))
	if end == 0 || raw[end-1] != '}' {
		return nil, errors.New("not a JSON object")
	}
	closeAt := end - 1
	core := bytes.TrimRight(raw[:closeAt], ws)
	gap := raw[len(core):closeAt] // the whitespace before the closing brace
	empty := len(core) > 0 && core[len(core)-1] == '{'
	member := fmt.Sprintf("%q: %s", key, value)
	var ins string
	switch {
	case empty && bytes.IndexByte(raw, '\n') >= 0:
		ins = "\n  " + member + "\n"
	case empty:
		ins = member
	case bytes.IndexByte(gap, '\n') >= 0:
		last := core[bytes.LastIndexByte(core, '\n')+1:]
		ins = "\n" + string(last[:len(last)-len(bytes.TrimLeft(last, " \t"))]) + member
	default:
		ins = " " + member
	}
	if !empty {
		ins = "," + ins
	}
	var out []byte
	out = append(out, core...)
	out = append(out, ins...)
	out = append(out, raw[len(core):]...)
	return out, nil
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

// checkSigningKeyListed refuses a signing key the account JWT does not list: nats-server would refuse every user it
// signs. It returns "" when the key is a plain listed signing key; else what is wrong with it, worded to follow "its
// public key X" (so the key is printed once, by the caller), and what the setup file's owner can do about it.
func checkSigningKeyListed(claims *jwt.AccountClaims, signPub string) (reason, advice string) {
	if claims.SigningKeys.Contains(signPub) {
		if sc, _ := claims.SigningKeys.GetScope(signPub); sc != nil {
			return "is a scoped signing key (its template would limit every endpoint)", "give a plain signing key"
		}
		return "", ""
	}
	return "is not one of the signing keys of the account JWT on the resolver, so users it signs would be refused",
		fmt.Sprintf("add it to the account's signing keys (nsc edit account --sk %s), then push the account (nsc push), or give the signing key the account lists", signPub)
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
	raw, err := ctlkeys.ReadOwnedFile(path, "creds file")
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
				return differentSetup(st, k.what+" key")
			}
			*k.have = true
		}
	}
	// config.json's signing_key_pub: a key it records that is not the one stored, or not the one given, is a different
	// signing key (the key store's own refusal comes first, as it names the stored key).
	cfgPub := st.cfgSignPub
	kp, pub, err := ks.Signing()
	rec, _ := ks.SigningRecord()
	switch {
	case errors.Is(err, ctlkeys.ErrNoSigningKey):
		// Both signing.nk and signing.pub are gone. config.json says whether a signing key was ever configured: then
		// only the same signing_key restores it (a different one is refused with the config.json comparison).
		if st.signPub == "" && st.cfgSignPub != "" {
			return cliframe.Failf("signing.nk and signing.pub are missing from %s", st.keyFolder).WithHint("mashctl init --config <file>")
		}
	case errors.Is(err, ctlkeys.ErrSigningKeyLost):
		// signing.nk is gone: init with the same signing_key restores it; anything else is refused.
		if st.signPub == "" {
			return shownSigningRefusal(err, st)
		}
		if rec != st.signPub || (cfgPub != "" && rec != cfgPub) {
			return differentSigningKey(st, rec)
		}
	case err != nil:
		return err
	default:
		kp.Wipe()
		if st.signPub != "" {
			if pub != st.signPub {
				return differentSigningKey(st, pub)
			}
			// A seed with no record is an init that stopped between the two writes: ImportSigning completes it.
			st.haveSign = rec == pub
		} else {
			if rec == "" {
				// signing.nk without its record and no signing_key to restore it: recording the key in config.json
				// would make every later command refuse, so init refuses now, before any write.
				return &ctlkeys.SigningPubMissingError{Pub: pub, Dir: st.keyFolder}
			}
			if cfgPub != "" && pub != cfgPub {
				return cliframe.Failf("the key folder %s holds signing key %s but config.json records signing_key_pub %s; nothing was changed: give the seed of the recorded key as signing_key, or use a new key folder (set MASH_KEYS_DIR)", st.keyFolder, pub, cfgPub).WithWrap().
					WithHint("MASH_KEYS_DIR=<a new folder> mashctl init --config <file>")
			}
			st.storedSignPub = pub
		}
	}
	if cfgPub != "" && st.signPub != "" && cfgPub != st.signPub {
		return differentSetup(st, "signing key")
	}
	return nil
}

// checkDestinations refuses files that would have to be replaced: no clobber.
func checkDestinations(st *initState) error {
	if b, err := os.ReadFile(st.sysDst); err == nil {
		if !bytes.Equal(b, st.sysCredsRaw) {
			return differentSetup(st, "system creds")
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
		return "", fmt.Errorf("admin_creds belong to another account (%s), not %s", uc.IssuerAccount, st.acctPub)
	}
	if uc.IssuerAccount == "" && uc.Issuer != st.acctPub {
		return "", fmt.Errorf("admin_creds belong to another account (%s), not %s", uc.Issuer, st.acctPub)
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
	tlsReq, _, err := natsauth.RequireTLS(url, cfg.SkipEncryption)
	if err != nil {
		return nil, err
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
		return lim, "", errors.New("JetStream is not enabled for the account: its JWT limits give it no file storage (disk 0), and mash keeps everything on file. Enable it first (for example `nsc edit account NAME --js-disk-storage 2G --js-streams 1024 --js-consumer 1024`, then push)")
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
