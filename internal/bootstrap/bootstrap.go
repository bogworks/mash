// Package bootstrap stands up the configuration of a local operator-mode NATS server with JetStream for one
// developer (`mashctl init --local`, ADR 0052): keys through the ctlkeys store, the operator, system account, MASH
// account and two user JWTs, a server.conf that preloads them, an open policy file and a config.json. Build computes
// every file's content and creates the keys; Plan.Write puts the files down without clobbering anything.
package bootstrap

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctlkeys"
	"github.com/bogworks/mash/internal/policy"
)

// File names inside the server directory.
const (
	ServerConf  = "server.conf"
	OperatorJWT = "operator.jwt"
	AdminCreds  = "mashadmin.creds"
	SysCreds    = "sys.creds"
	ServerLog   = "server.log"
	ServerPID   = "server.pid"

	resolverDir  = "resolver"
	jetstreamDir = "jetstream"

	// DefaultPort is the client port of the local server.
	DefaultPort = 4222

	adminUserName = "mashadmin"
	sysUserName   = "sys"
	serverName    = "mash-local"
)

// Account limits for JetStream, the defaults docs/operations.md describes (2G of disk, 64 streams, 128 consumers).
// No memory store: mash stores everything on file.
const (
	jsDisk      = 2 << 30
	jsStreams   = 64
	jsConsumers = 128
)

// OpenPolicy is the policy file written when none exists: everything allowed, which is what the account permissions
// give today.
const OpenPolicy = `# The policy file: who may mail whom. See docs/policy-file.md.
# Everything is open until you add rules; mashctl compiles this file into NATS permissions.
default: allow
`

// Params says what to build.
type Params struct {
	Dir        string // the server directory (absolute)
	Owner      string
	Port       int
	PolicyPath string // where an open policy file goes if none exists; "" skips it
	ConfigPath string // where config.json goes if none exists; "" skips it
}

// File is one file to create.
type File struct {
	Path string
	Data []byte
}

// Plan is everything Build decided: the files to create, with the config.json and policy outcomes.
type Plan struct {
	Params
	Files    []File // the server directory's files
	Dirs     []string
	Policy   *File // nil when a policy file exists or no path was given
	Config   *File // nil when a config.json exists or no path was given
	URL      string
	Creds    string // the admin creds path
	Account  string // public keys, safe to print
	Operator string
	System   string
}

func (p Params) path(n string) string { return filepath.Join(p.Dir, n) }

// ConfPath is the server.conf path.
func (p Params) ConfPath() string { return p.path(ServerConf) }

// AdminCredsPath is the mashadmin creds path.
func (p Params) AdminCredsPath() string { return p.path(AdminCreds) }

// LogPath is the server log path used by Start.
func (p Params) LogPath() string { return p.path(ServerLog) }

// PIDPath is the pid file path used by Start.
func (p Params) PIDPath() string { return p.path(ServerPID) }

// Preflight refuses when dir holds any file that Build would create (or Start would write). A missing dir is fine.
func Preflight(dir string) error {
	fi, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("mashctl: %s: %w", dir, err)
	}
	if fi.Mode()&fs.ModeSymlink != 0 || !fi.IsDir() {
		return fmt.Errorf("mashctl: %s is not a plain directory", dir)
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("mashctl: %s has mode %04o: it holds credentials. Run: chmod 700 %s", dir, fi.Mode().Perm(), dir)
	}
	var have []string
	for _, n := range []string{ServerConf, OperatorJWT, AdminCreds, SysCreds, ServerLog, ServerPID, resolverDir, jetstreamDir} {
		if _, err := os.Lstat(filepath.Join(dir, n)); !errors.Is(err, fs.ErrNotExist) {
			have = append(have, n)
		}
	}
	if len(have) > 0 {
		return fmt.Errorf("mashctl: %s already holds %s: refusing to overwrite. Remove them (or choose another --dir) to start over; nothing was changed", dir, strings.Join(have, ", "))
	}
	return nil
}

// Build creates the keys (operator, system, account) in ks if absent and computes every file. It writes nothing
// but those keys; call Plan.Write for the rest. It refuses before touching anything when Preflight does.
func Build(p Params, ks *ctlkeys.Authority) (*Plan, error) {
	if !filepath.IsAbs(p.Dir) {
		return nil, fmt.Errorf("mashctl: server directory %q is not an absolute path", p.Dir)
	}
	if strings.ContainsAny(p.Dir, "\r\n\x00") {
		return nil, errors.New("mashctl: server directory has a control character in its name")
	}
	if !config.ValidName(p.Owner) {
		return nil, fmt.Errorf("mashctl: owner %q is not a valid name: use lower-case letters, digits and single hyphens, starting with a letter, up to 48 characters", p.Owner)
	}
	if p.Port < 1 || p.Port > 65535 {
		return nil, fmt.Errorf("mashctl: port %d is out of range", p.Port)
	}
	if err := Preflight(p.Dir); err != nil {
		return nil, err
	}
	if _, _, err := ks.InitOperator(); err != nil {
		return nil, err
	}
	if _, _, err := ks.InitSystem(); err != nil {
		return nil, err
	}
	if _, _, err := ks.InitAccount(); err != nil {
		return nil, err
	}
	opKP, opPub, err := ks.Operator()
	if err != nil {
		return nil, err
	}
	defer opKP.Wipe()
	sysKP, sysPub, err := ks.System()
	if err != nil {
		return nil, err
	}
	defer sysKP.Wipe()
	acctKP, acctPub, err := ks.Account()
	if err != nil {
		return nil, err
	}
	defer acctKP.Wipe()

	oc := jwt.NewOperatorClaims(opPub)
	oc.Name = "mash-local-operator"
	oc.SystemAccount = sysPub
	opJWT, err := oc.Encode(opKP)
	if err != nil {
		return nil, fmt.Errorf("mashctl: operator jwt: %w", err)
	}
	sc := jwt.NewAccountClaims(sysPub)
	sc.Name = "SYS"
	sysJWT, err := sc.Encode(opKP)
	if err != nil {
		return nil, fmt.Errorf("mashctl: system account jwt: %w", err)
	}
	ac := jwt.NewAccountClaims(acctPub)
	ac.Name = "MASH"
	ac.Limits.JetStreamLimits = jwt.JetStreamLimits{DiskStorage: jsDisk, Streams: jsStreams, Consumer: jsConsumers}
	acctJWT, err := ac.Encode(opKP)
	if err != nil {
		return nil, fmt.Errorf("mashctl: account jwt: %w", err)
	}
	// mashadmin has full access within the account, as docs/operations.md describes: it creates and deletes streams.
	adminCreds, err := userCreds(adminUserName, acctKP)
	if err != nil {
		return nil, err
	}
	sysCreds, err := userCreds(sysUserName, sysKP)
	if err != nil {
		return nil, err
	}

	pl := &Plan{Params: p, URL: "nats://127.0.0.1:" + strconv.Itoa(p.Port), Creds: p.AdminCredsPath(), Account: acctPub, Operator: opPub, System: sysPub}
	pl.Dirs = []string{p.Dir, p.path(resolverDir), p.path(jetstreamDir)}
	pl.Files = []File{
		{p.path(OperatorJWT), []byte(opJWT + "\n")},
		{p.path(AdminCreds), adminCreds},
		{p.path(SysCreds), sysCreds},
		{p.ConfPath(), []byte(serverConfFor(p, sysPub, sysJWT, acctPub, acctJWT))},
	}
	if p.PolicyPath != "" && config.Absent(p.PolicyPath) {
		pf, err := policy.Parse([]byte(OpenPolicy))
		if err != nil {
			return nil, fmt.Errorf("mashctl: built-in policy does not parse: %w", err)
		}
		if r := pf.Check(); len(r.Errors) > 0 || len(r.Warnings) > 0 {
			return nil, errors.New("mashctl: built-in policy does not check clean")
		}
		pl.Policy = &File{p.PolicyPath, []byte(OpenPolicy)}
	}
	if p.ConfigPath != "" && config.Absent(p.ConfigPath) {
		pl.Config = &File{p.ConfigPath, []byte(fmt.Sprintf("{\n  \"url\": %s,\n  \"owner\": %s,\n  \"admin_creds\": %s\n}\n",
			strconv.Quote(pl.URL), strconv.Quote(p.Owner), strconv.Quote(pl.Creds)))}
	}
	return pl, nil
}

// userCreds mints a user signed by signer (an account identity key) and returns its .creds bytes.
func userCreds(name string, signer nkeys.KeyPair) ([]byte, error) {
	ukp, err := nkeys.CreateUser()
	if err != nil {
		return nil, err
	}
	defer ukp.Wipe()
	pub, err := ukp.PublicKey()
	if err != nil {
		return nil, err
	}
	raw, err := ukp.Seed()
	if err != nil {
		return nil, errors.New("mashctl: user seed unavailable")
	}
	seed := append([]byte(nil), raw...)
	defer clear(seed)
	uc := jwt.NewUserClaims(pub)
	uc.Name = name
	tok, err := uc.Encode(signer)
	if err != nil {
		return nil, fmt.Errorf("mashctl: %s user jwt: %w", name, err)
	}
	return jwt.FormatUserConfig(tok, seed)
}

// confQuote quotes s as a nats-server config string.
func confQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// serverConfFor is server.conf: loopback only, no TLS, operator mode with a full resolver preloaded with the two
// accounts, JetStream on file storage.
func serverConfFor(p Params, sysPub, sysJWT, acctPub, acctJWT string) string {
	var b strings.Builder
	b.WriteString("# Written by `mashctl init --local`. A local development server: it listens on loopback only and has no TLS.\n")
	fmt.Fprintf(&b, "server_name: %s\n", serverName)
	fmt.Fprintf(&b, "listen: 127.0.0.1:%d\n", p.Port)
	fmt.Fprintf(&b, "operator: %s\n", confQuote(p.path(OperatorJWT)))
	fmt.Fprintf(&b, "system_account: %s\n", sysPub)
	fmt.Fprintf(&b, "resolver: {\n  type: full\n  dir: %s\n  allow_delete: false\n}\n", confQuote(p.path(resolverDir)))
	fmt.Fprintf(&b, "resolver_preload: {\n  %s: %s\n  %s: %s\n}\n", sysPub, confQuote(sysJWT), acctPub, confQuote(acctJWT))
	fmt.Fprintf(&b, "jetstream: {\n  store_dir: %s\n}\n", confQuote(p.path(jetstreamDir)))
	return b.String()
}

// Write creates the directories (0700) and files (0600) of the plan, never replacing one. If a step fails, what
// this call created is removed again (the keys stay).
func (pl *Plan) Write() (err error) {
	var madeFiles, madeDirs []string
	defer func() {
		if err == nil {
			return
		}
		for _, f := range madeFiles {
			_ = os.Remove(f)
		}
		for i := len(madeDirs) - 1; i >= 0; i-- {
			_ = os.Remove(madeDirs[i])
		}
	}()
	mkdir := func(d string) error {
		if _, serr := os.Lstat(d); serr == nil {
			return nil
		}
		if mkerr := os.MkdirAll(d, 0o700); mkerr != nil {
			return fmt.Errorf("mashctl: create %s: %w", d, mkerr)
		}
		madeDirs = append(madeDirs, d)
		return nil
	}
	for _, d := range pl.Dirs {
		if err := mkdir(d); err != nil {
			return err
		}
	}
	files := append([]File(nil), pl.Files...)
	if pl.Policy != nil {
		files = append(files, *pl.Policy)
	}
	if pl.Config != nil {
		files = append(files, *pl.Config)
	}
	for _, f := range files {
		if err := mkdir(filepath.Dir(f.Path)); err != nil {
			return err
		}
		if err := createFile(f.Path, f.Data); err != nil {
			return err
		}
		madeFiles = append(madeFiles, f.Path)
	}
	return nil
}

// createFile writes data to a new 0600 file, failing if it exists.
func createFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("mashctl: create %s: %w", path, err)
	}
	_, werr := f.Write(data)
	if werr == nil {
		werr = f.Sync()
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(path)
		return fmt.Errorf("mashctl: write %s: %w", path, werr)
	}
	return nil
}
