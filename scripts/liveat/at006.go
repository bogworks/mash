package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
)

// The strict signing key scenario (issue #1239): an operator with strict_signing_key_usage refuses a user JWT signed by the
// account identity key, so mashctl signs endpoint users with the account SIGNING key (signing_key in the setup file) and
// names the account in issuer_account. The scenario is run on its own (`liveat at006`) and is not part of `all`: the
// setup script cannot make a strict operator, so the harness converts the script's server in place (strictFiles).

const (
	strictDir           = "strict"
	strictOperatorSeed  = strictDir + "/operator-signing.nk"
	strictAccountSeed   = strictDir + "/account-signing.nk"
	strictSetupYAML     = "mash-setup-strict.yaml"
	strictServerReadyBy = 20 * time.Second
)

// strictFiles are the files strictConvert wrote beside the script's: the setup file that gives mashctl init the signing key.
type strictFiles struct {
	Setup               string // the setup file for `mashctl init --config`
	OperatorSeed        string // the operator SIGNING seed that signs the account JWTs now (operator_key)
	AccountSigningSeed  string // the account signing seed (signing_key)
	AccountPub, SignPub string
}

// writeSeed writes kp's seed to path (0600) and returns the seed text, for the harness to search the output for.
func writeSeed(path string, kp nkeys.KeyPair) ([]byte, error) {
	seed, err := kp.Seed()
	if err != nil {
		return nil, fmt.Errorf("seed unavailable")
	}
	seed = append([]byte(nil), seed...)
	if err := os.WriteFile(path, append(append([]byte(nil), seed...), '\n'), 0o600); err != nil {
		return nil, err
	}
	return seed, nil
}

// signedUser makes a user of the account acct, signed by the signing key sk with issuer_account set, and returns its
// creds text.
func signedUser(name, acct string, sk nkeys.KeyPair) ([]byte, error) {
	ukp, err := nkeys.CreateUser()
	if err != nil {
		return nil, err
	}
	pub, err := ukp.PublicKey()
	if err != nil {
		return nil, err
	}
	seed, err := ukp.Seed()
	if err != nil {
		return nil, err
	}
	uc := jwt.NewUserClaims(pub)
	uc.Name = name
	uc.IssuerAccount = acct
	tok, err := uc.Encode(sk)
	if err != nil {
		return nil, err
	}
	return jwt.FormatUserConfig(tok, seed)
}

// strictConvert rewrites the script's server directory dir (stopped) into one whose operator has
// strict_signing_key_usage: an operator signing key signs the operator-declared accounts (the identity key may not),
// each account gets a signing key, and the admin and system users are signed by those. It overwrites operator.jwt,
// server.conf's preloaded account JWTs, the resolver's copies of them and the two creds files, which are the script's
// own files of this scenario's directory, and writes the new seeds and a setup file under dir/strict. secrets receives the
// seed texts. It returns where the files are. No seed is put in an error.
func strictConvert(dir string, port int, secrets map[string][]byte) (*strictFiles, error) {
	opKP, err := readSeedFile(filepath.Join(dir, fileOperatorNK))
	if err != nil {
		return nil, fmt.Errorf("operator seed: %w", err)
	}
	opPub, _ := opKP.PublicKey()
	opText, err := os.ReadFile(filepath.Join(dir, fileOperator))
	if err != nil {
		return nil, err
	}
	oc, err := jwt.DecodeOperatorClaims(strings.TrimSpace(string(opText)))
	if err != nil {
		return nil, fmt.Errorf("operator.jwt: %w", err)
	}
	if oc.Subject != opPub {
		return nil, fmt.Errorf("operator.jwt is for %s, not for the operator key of the server directory (%s)", oc.Subject, opPub)
	}
	acctKP, err := readSeedFile(filepath.Join(dir, fileAccountNK))
	if err != nil {
		return nil, fmt.Errorf("account seed: %w", err)
	}
	acctPub, _ := acctKP.PublicKey()

	if err := os.MkdirAll(filepath.Join(dir, strictDir), 0o700); err != nil {
		return nil, err
	}
	osk, _ := nkeys.CreateOperator()
	oskPub, _ := osk.PublicKey()
	ask, _ := nkeys.CreateAccount()
	askPub, _ := ask.PublicKey()
	ssk, _ := nkeys.CreateAccount()
	sskPub, _ := ssk.PublicKey()
	out := &strictFiles{
		Setup: filepath.Join(dir, strictSetupYAML), OperatorSeed: filepath.Join(dir, strictOperatorSeed),
		AccountSigningSeed: filepath.Join(dir, strictAccountSeed), AccountPub: acctPub, SignPub: askPub,
	}
	for label, w := range map[string]struct {
		path string
		kp   nkeys.KeyPair
	}{"strict operator signing seed": {out.OperatorSeed, osk}, "strict account signing seed": {out.AccountSigningSeed, ask}} {
		seed, err := writeSeed(w.path, w.kp)
		if err != nil {
			return nil, err
		}
		secrets[label] = seed
	}
	sysSeed := filepath.Join(dir, strictDir, "system-signing.nk")
	if seed, err := writeSeed(sysSeed, ssk); err != nil {
		return nil, err
	} else {
		secrets["strict system signing seed"] = seed
	}

	oc.StrictSigningKeyUsage = true
	oc.SigningKeys.Add(oskPub)
	otok, err := oc.Encode(opKP)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, fileOperator), []byte(otok+"\n"), 0o600); err != nil {
		return nil, err
	}

	conf, err := os.ReadFile(filepath.Join(dir, fileServerConf))
	if err != nil {
		return nil, err
	}
	cb := string(conf)
	sysM := regexp.MustCompile(`(?m)^system_account:\s*(A[A-Z2-7]+)\s*$`).FindStringSubmatch(cb)
	if sysM == nil {
		return nil, fmt.Errorf("server.conf names no system account")
	}
	sysPub := sysM[1]
	resign := func(pub, addKey string) error {
		m := regexp.MustCompile(`(?m)^\s+` + pub + `: "([^"]+)"$`).FindStringSubmatch(cb)
		if m == nil {
			return fmt.Errorf("server.conf preloads no JWT for %s", pub)
		}
		ac, err := jwt.DecodeAccountClaims(m[1])
		if err != nil {
			return err
		}
		ac.SigningKeys.Add(addKey)
		tok, err := ac.Encode(osk)
		if err != nil {
			return err
		}
		cb = strings.Replace(cb, m[1], tok, 1)
		// a full resolver keeps its own copy of an account; the preload does not replace one that is there
		if p := filepath.Join(dir, "resolver", pub+".jwt"); fileExists(p) {
			return os.WriteFile(p, []byte(tok), 0o600)
		}
		return nil
	}
	if err := resign(sysPub, sskPub); err != nil {
		return nil, err
	}
	if err := resign(acctPub, askPub); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, fileServerConf), []byte(cb), 0o600); err != nil {
		return nil, err
	}

	admin, err := signedUser("mashadmin", acctPub, ask)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, fileAdminCreds), admin, 0o600); err != nil {
		return nil, err
	}
	sys, err := signedUser("sys", sysPub, ssk)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, fileSysCreds), sys, 0o600); err != nil {
		return nil, err
	}
	registerKeyLines(secrets, "strict admin creds", admin)
	registerKeyLines(secrets, "strict system creds", sys)

	setup := fmt.Sprintf("servers: [tls://127.0.0.1:%d]\nca: %s\nowner: alice\nadmin_creds: %s\nsystem_creds: %s\noperator_key: %s\naccount_key: %s\nsigning_key: %s\n",
		port, filepath.Join(dir, fileCACert), filepath.Join(dir, fileAdminCreds), filepath.Join(dir, fileSysCreds),
		out.OperatorSeed, filepath.Join(dir, fileAccountNK), out.AccountSigningSeed)
	if err := os.WriteFile(out.Setup, []byte(setup), 0o600); err != nil {
		return nil, err
	}
	return out, nil
}

func readSeedFile(path string) (nkeys.KeyPair, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	kp, err := nkeys.FromSeed([]byte(strings.TrimSpace(string(b))))
	if err != nil {
		return nil, fmt.Errorf("%s holds no seed", path)
	}
	return kp, nil
}

// startStrictServer makes the script's server, stops it, converts its directory to a strict operator (strictConvert) and
// starts nats-server on the same configuration as a process of the run, which stops it by PID.
func (a *AT) startStrictServer(h Home) (*strictFiles, bool) {
	if !a.startScriptServer(h) {
		return nil, false
	}
	a.StopServer()
	sf, err := strictConvert(a.srvDir, a.R.Port, a.secrets)
	if !a.C.Check("the server directory is converted to an operator with strict_signing_key_usage", err == nil, redact(errText(err))) {
		return nil, false
	}
	p, err := a.R.StartProc("nats-server-strict", []string{"nats-server", "-c", filepath.Join(a.srvDir, fileServerConf)}, a.Env(h))
	if !a.C.Check("nats-server starts on the strict configuration", err == nil, errText(err)) {
		return nil, false
	}
	a.srvPID = p.PID
	a.R.record(p.PID, a.srvDir)
	ready := false
	for end := time.Now().Add(strictServerReadyBy); time.Now().Before(end); time.Sleep(200 * time.Millisecond) {
		if c, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(a.R.Port), time.Second); err == nil {
			c.Close()
			ready = true
			break
		}
	}
	if !a.C.Check("the strict server accepts connections", ready, "") {
		return nil, false
	}
	return sf, true
}

// AT006 is the strict operator: endpoint users are signed by the account signing key.
var AT006 = Scenario{
	ID: "at006",
	Scenario: "AT-006, strict_signing_key_usage (issue #1239): the setup script's server is converted to an operator that refuses users signed by the account identity key " +
		"(the operator signing key signs the accounts; the admin and system users are signed by account signing keys); mashctl init --config with signing_key; " +
		"mashctl endpoint create alice.dev.design (same machine), mash doctor as that endpoint (sign-in ok), mashctl endpoint reissue --out, mashctl endpoint delete; " +
		"the user JWT is signed by the signing key and names the account as issuer_account.",
	Body: func(a *AT) {
		h, err := a.Home("home")
		if !a.C.Check("a fresh scratch home is made", err == nil, "") {
			return
		}
		sf, ok := a.startStrictServer(h)
		if !ok {
			return
		}
		r := a.Ctl("init-strict", h, nil, "init", "--config", sf.Setup)
		if !a.C.Check("mashctl init --config with signing_key succeeds on the strict server", r.Code == 0, exitDetail(r)) {
			return
		}
		e, ok := a.provisionHere(h, "alice", "dev", "design")
		if !ok {
			return
		}
		uc, err := credsUserClaims(e.creds)
		a.C.Check("the created user JWT is signed by the account signing key and names the account as issuer_account",
			err == nil && uc.Issuer == sf.SignPub && uc.IssuerAccount == sf.AccountPub, fmt.Sprintf("%v", err))
		env := a.SessionEnv(h, "dev", "design")
		d := a.Mash("doctor-strict", h, env, "doctor")
		a.C.Check("mash doctor as the endpoint says sign-in ok on the strict server", d.Code == 0 && strings.Contains(d.Stdout, "sign-in   ok"), snip(grepLine(d.Text(), "sign-in")))

		join := filepath.Join(h.Dir, "reissue.join")
		k := a.Mash("key-create-reissue", h, a.Conn(), "key", "create", "--out", filepath.Join(h.Dir, "reissue.seed"))
		if !a.C.Check("a new key for the reissue", k.Code == 0, exitDetail(k)) {
			return
		}
		pub := pubOf(filepath.Join(h.Dir, "reissue.seed"))
		r = a.Ctl("endpoint-reissue-strict", h, a.Conn(), "endpoint", "reissue", "alice.dev.design", "--out", join, "--public-key", pub)
		a.C.Check("mashctl endpoint reissue keeps working: the revocation rewrites the account JWT with the signing key in it", r.Code == 0, exitDetail(r))
		r = a.Ctl("endpoint-delete-strict", h, a.Conn(), "endpoint", "delete", "alice.dev.design", "--yes")
		a.C.Check("mashctl endpoint delete succeeds on the strict server", r.Code == 0, exitDetail(r))
	},
}

// credsUserClaims reads the user claims of a creds file.
func credsUserClaims(path string) (*jwt.UserClaims, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	tok, err := jwt.ParseDecoratedJWT(b)
	if err != nil {
		return nil, err
	}
	return jwt.DecodeUserClaims(tok)
}
