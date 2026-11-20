package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/ctlkeys"
	"github.com/bogworks/mash/internal/enroll"
	"github.com/bogworks/mash/internal/sap"
)

const endpointCreateUsage = `usage: mashctl endpoint create <address> [--out PATH] [--public-key PATH]
       [--dry-run] [--json]
  address            the endpoint, as owner.group.name
  --out PATH         write a join file for another machine and install
                     nothing here
  --public-key PATH  sign this public key and make no seed; needs --out. For
                     an endpoint that made its own key with mash key create
  --dry-run          print what would change, and change nothing
  --json             print the result as JSON
  With no option the key pair is made here and <address>.creds is written to
  the config folder.
Example:
  mashctl endpoint create acme.demo.bob
  mashctl endpoint create acme.demo.bob --out acme.demo.bob.join
`

var endpointCreateSpec = epSpec{
	name: "create", usage: endpointCreateUsage, flags: []string{"--dry-run"}, values: []string{"--out", "--public-key"},
	min: 1, max: 1, need: "the endpoint address (owner.group.name)", check: checkIssueArgs,
}

// checkIssueArgs is the check of create and reissue: the address, and --public-key only with --out.
func checkIssueArgs(in *epInput) error {
	if err := checkAddress(in); err != nil {
		return err
	}
	if _, ok := in.values["--public-key"]; ok {
		if _, ok := in.values["--out"]; !ok {
			return cliframe.Usagef("--public-key needs --out, the join file to write")
		}
	}
	return nil
}

// epRefusal is a refusal: a code for the code that decides what to do, and the sentence the person reads.
type epRefusal struct {
	Code   string
	Detail string
}

// epFail is a refusal (refused) or an error of one endpoint, by its code. Both exit 1.
type epFail struct {
	refused      bool
	code, detail string
	again        bool   // the run did part of its work: the same command finishes it
	hint         string // a command that helps, when there is one
}

// epErrOf is the error a command reports for a refusal or failure of addr. An address that is taken or deleted is
// worded the way the specification words it, whatever the library said.
func epErrOf(addr sap.Address, code, detail string) *cliframe.Error {
	detail = strings.TrimPrefix(strings.TrimPrefix(detail, "mashctl: "), "mash: ")
	if code == "no_account_key" {
		return cliframe.Failf("no account signing key").WithHint("mashctl init --help")
	}
	switch code {
	case enroll.CodeAddressTaken:
		return cliframe.Failf("%s already exists", addr)
	case enroll.CodeKeyRetired:
		return cliframe.Failf("%s was deleted and its address cannot be used again", addr)
	case "endpoint_missing":
		return cliframe.Failf("%s does not exist", addr).WithHint("mashctl endpoint create " + addr.String())
	}
	// A remedy that is a command goes to the hint line.
	for _, sep := range []string{"; make a new key with: ", "; make one with: "} {
		if i := strings.Index(detail, sep); i > 0 {
			return cliframe.Failf("%s", detail[:i]).WithHint(detail[i+len(sep):])
		}
	}
	switch code {
	case enroll.CodeKeyRevoked, enroll.CodePublicKeyIssued:
		return cliframe.Failf("%s", detail).WithHint("mash key create --out NAME.seed")
	}
	return cliframe.Failf("%s", detail)
}

func (r *epRefusal) err(addr sap.Address) error { return epErrOf(addr, r.Code, r.Detail) }
func (f *epFail) err(addr sap.Address) *cliframe.Error {
	e := epErrOf(addr, f.code, f.detail)
	if f.hint != "" {
		e = e.WithHint(f.hint)
	}
	return e
}

// epTarget is what create and reissue work out from the command line and the configuration before anything is read
// from the server or written.
type epTarget struct {
	addr    sap.Address
	cfg     config.Config
	base    joinBase
	join    bool   // --out: write a join file, install nothing here
	makeKey bool   // no --public-key: the key pair is made here
	pub     string // the public key --public-key names
	outPath string // the join file with --out, else the creds file in the config folder
	again   string // the command that finishes a run that did part of its work
}

// epLoadTarget reads the configuration and the options. Nothing is written and no server is asked.
func epLoadTarget(verb string, in *epInput, env func(string) string, lookup config.Lookup) (epTarget, error) {
	t := epTarget{addr: in.addr}
	cfg, err := loadCtlConfig(env, lookup, true)
	if err != nil {
		if errors.Is(err, config.ErrNoURL) {
			return t, cliframe.Failf("no server is configured").WithHint("mashctl endpoint " + verb + " " + in.addr.String() + " --server URL")
		}
		return t, failErr(err)
	}
	t.cfg = cfg
	if t.base, err = joinBaseOf(cfg); err != nil {
		if errors.Is(err, errCAFile) {
			return t, cliframe.Failf("%s", strings.TrimPrefix(err.Error(), errCAFile.Error()+": "))
		}
		return t, cliframe.Failf("%v", err).WithHint("mashctl endpoint " + verb + " " + in.addr.String() + " --server URL")
	}
	out := in.values["--out"]
	t.join = out != ""
	pk := in.values["--public-key"]
	t.makeKey = pk == ""
	t.again = "mashctl endpoint " + verb + " " + in.addr.String()
	if t.join {
		t.again += " --out " + out
		t.outPath = out
		// The folder of a join file, and of its .pending staging file, is one only the user can swap files in. A
		// folder that is missing is made later, 0700.
		dir := filepath.Dir(out)
		if _, serr := os.Lstat(dir); serr == nil {
			if derr := ctlkeys.CheckParentDir(dir); derr != nil {
				return t, cliframe.Failf("the folder %s is not usable: %v; nothing was changed", dir, derr)
			}
		}
	}
	if pk != "" {
		t.again += " --public-key " + pk
		var ref *epRefusal
		if t.pub, ref = epReadPublicKey(pk); ref != nil {
			return t, ref.err(in.addr)
		}
	}
	if !t.join {
		p, err := credsFileFor(cfg, in.addr)
		if err != nil {
			return t, cliframe.Failf("no config folder to write the credentials file in").WithHint("mashctl endpoint " + verb + " " + in.addr.String() + " --out " + in.addr.String() + ".join")
		}
		t.outPath = p
	}
	return t, nil
}

// epReadPublicKey reads a public key file; the content is never shown.
func epReadPublicKey(path string) (string, *epRefusal) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", &epRefusal{"public_key_unreadable", fmt.Sprintf("cannot read the public key file %s: %v", path, errors.Unwrap(err))}
	}
	k := strings.TrimSpace(string(b))
	if !nkeys.IsValidPublicUserKey(k) {
		return "", &epRefusal{enroll.CodePublicKeyInvalid, fmt.Sprintf("%s does not hold a NATS user public key (U, then 55 base32 characters); make one with: mash key create --out NAME.seed", path)}
	}
	return k, nil
}

// epResult is what a create or reissue did, for its output.
type epResult struct {
	planned bool
	userPub string
	acked   int
}

// createJSON is the JSON of create.
type createJSON struct {
	Address       string  `json:"address"`
	InstalledHere bool    `json:"installed_here"`
	Creds         *string `json:"creds"` // the credentials file installed here; null when a join file was written
	JoinFile      *string `json:"join_file"`
	DryRun        bool    `json:"dry_run,omitempty"`
}

// reissueJSON is the JSON of reissue.
type reissueJSON struct {
	Address  string  `json:"address"`
	JoinFile *string `json:"join_file"`
	DryRun   bool    `json:"dry_run,omitempty"`
}

// epPrint prints the result of create or reissue: the action line, the join file line, and with --verbose the
// label: value lines. Neither the seed nor a JWT is ever printed.
func epPrint(w io.Writer, verb string, t epTarget, res epResult, verbose, jsonOut bool) {
	name := t.addr.String()
	var join, creds *string
	if t.join {
		join = &t.outPath
	} else {
		creds = &t.outPath
	}
	if jsonOut {
		if verb == "create" {
			writeJSON(w, createJSON{Address: name, InstalledHere: !t.join, Creds: creds, JoinFile: join, DryRun: res.planned})
		} else {
			writeJSON(w, reissueJSON{Address: name, JoinFile: join, DryRun: res.planned})
		}
		return
	}
	switch {
	case res.planned && verb == "create":
		fmt.Fprintf(w, "Would create endpoint %s\n", name)
		return
	case res.planned:
		fmt.Fprintf(w, "Would reissue credentials for %s\n", name)
		return
	case verb == "create":
		fmt.Fprintf(w, "Created endpoint %s\n", name)
	default:
		fmt.Fprintf(w, "Reissued credentials for %s\n", name)
	}
	if t.join {
		fmt.Fprintf(w, "Join file: %s\n", t.outPath)
	}
	if !verbose {
		return
	}
	if verb == "reissue" {
		fmt.Fprintf(w, "servers acknowledged: %d\n", res.acked)
		return
	}
	fmt.Fprintf(w, "servers: %s\n", strings.Join(t.base.servers, ", "))
	if t.join {
		fmt.Fprintf(w, "join file: %s\n", t.outPath)
	} else {
		fmt.Fprintf(w, "credentials file: %s\n", t.outPath)
	}
	fmt.Fprintf(w, "public key: %s\n", res.userPub)
}

// runEndpointCreate is `mashctl endpoint create`.
func runEndpointCreate(args []string, g cliframe.Global, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	if helpAsked(args) {
		fmt.Fprint(stdout, endpointCreateUsage)
		return 0
	}
	in, err := parseEpInput(endpointCreateSpec, args)
	if err != nil {
		return cliframe.Report(stdout, stderr, err, jsonRequested(args, "--out", "--public-key"))
	}
	in.verbose = g.Detail
	t, res, err := epCreate(&in, env, lookup, stderr)
	if err != nil {
		return cliframe.Report(stdout, stderr, err, in.json)
	}
	epPrint(stdout, "create", t, res, g.Detail, in.json)
	return 0
}

// epCreate makes the endpoint, or with --dry-run checks that it could be made.
func epCreate(in *epInput, env func(string) string, lookup config.Lookup, stderr io.Writer) (epTarget, epResult, error) {
	addr, dry := in.addr, in.flags["--dry-run"]
	t, err := epLoadTarget("create", in, env, lookup)
	if err != nil {
		return t, epResult{}, err
	}
	cfg, outPath, pub := t.cfg, t.outPath, t.pub
	if err := admin.CheckProvision(cfg.NameRule, addr); err != nil {
		return t, epResult{}, cliframe.Failf("%s", strings.TrimPrefix(strings.TrimPrefix(err.Error(), "mash: "), "provision: "))
	}
	refuse := func(r *epRefusal) (epTarget, epResult, error) { return t, epResult{}, r.err(addr) }
	fail := func(code, detail string) (epTarget, epResult, error) {
		return t, epResult{}, epErrOf(addr, code, detail)
	}
	// partial is an error after the endpoint was recorded: what is left is done by running the same command again.
	partial := func(msg string) (epTarget, epResult, error) {
		return t, epResult{}, cliframe.Failf("%s", msg).WithHint(t.again)
	}

	// A same-host run killed after it staged its key finds the pending file: it is the key to finish with.
	pending := outPath + ".pending"
	var pendingKey string
	if t.makeKey {
		if _, lerr := os.Lstat(pending); lerr == nil {
			k, _, _, kerr := epStageKey(pending)
			if kerr != nil {
				return fail("write", fmt.Sprintf("cannot use the staged key %s: %v; nothing was changed", pending, kerr))
			}
			pendingKey = k
		}
	}
	// credsArePending is true when the output file already holds the staged key: the earlier run may have finished.
	credsArePending := false
	if pendingKey != "" {
		if _, lerr := os.Lstat(outPath); lerr == nil {
			have, cerr := epOutUser(outPath, addr, t.join)
			if cerr != nil || have != pendingKey {
				return refuse(&epRefusal{"creds_exists", fmt.Sprintf("%s already exists", outPath)})
			}
			credsArePending = true
		}
	} else if ref := epPathFree(outPath); ref != nil && t.join {
		return refuse(ref)
	}
	if t.makeKey {
		pub = pendingKey
	}
	keys, acct, err := epAccountOf(cfg, env)
	if err != nil {
		return refuse(&epRefusal{"no_account_key", err.Error()})
	}

	announceTarget(cfg, env, stderr, nil, in.verbose)
	adm, ctx, closeAdmin, err := ctl.OpenAdmin("mashctl", cfg)
	if err != nil {
		return fail("server", admin.RewordUnavailable(err.Error())+"; nothing was changed")
	}
	defer closeAdmin()
	servers := serverCount(cfg)

	// A creds file in the config folder for an address that is taken or deleted is that address's own: say so, rather
	// than that the file exists. This needs the server, so it is checked here and not before.
	if !t.join && pendingKey == "" {
		if _, lerr := os.Lstat(outPath); lerr == nil {
			burned, err := adm.IsBurned(ctx, addr)
			if err != nil {
				return fail("server", admin.RewordUnavailable(err.Error()))
			}
			if burned {
				return refuse(&epRefusal{enroll.CodeKeyRetired, ""})
			}
			switch _, _, err := adm.UserOf(ctx, addr); {
			case errors.Is(err, admin.ErrNoRecord):
				return refuse(epPathFree(outPath))
			case err != nil:
				return fail("server", admin.RewordUnavailable(err.Error()))
			}
			return refuse(&epRefusal{enroll.CodeAddressTaken, ""})
		}
	}

	// A supplied key that the account JWT revokes is never signed, whether or not an issue record says so (a JWT
	// issued after the revocation time would authenticate). This runs in a dry run too.
	if !t.makeKey {
		if f := epKeyRevokedFail(ctx, cfg, env, servers, pub); f != nil {
			return t, epResult{}, f.err(addr)
		}
	}
	// A staged key that the account JWT has revoked can never be used again: refuse, nothing was changed.
	if pendingKey != "" {
		revoked, rerr := keyRevoked(ctx, cfg, env, servers, pendingKey)
		if rerr != nil {
			return fail("revocation_unknown", fmt.Sprintf("cannot tell whether the staged key %s is revoked: %v; nothing was changed", pending, rerr))
		}
		if revoked {
			return t, epResult{}, cliframe.Failf("the staged key %s was revoked", pending).WithHint("rm " + pending)
		}
	}
	// dropPending removes the staged key this run read when the refusal says it can never be used for this address.
	dropPending := func(r *epRefusal) *epRefusal {
		if pendingKey != "" && !dry && (r.Code == enroll.CodeAddressTaken || r.Code == enroll.CodeKeyRetired) {
			os.Remove(pending)
		}
		return r
	}
	// The output file holds the staged key: that proves the earlier run finished only when the directory record names
	// the same key. This needs the server (accepted: a rerun that cannot reach it says so and changes nothing). No
	// record means the run was killed before the directory was written: the normal path completes it.
	if credsArePending && !dry {
		burned, err := adm.IsBurned(ctx, addr)
		if err != nil {
			return fail("server", admin.RewordUnavailable(err.Error()))
		}
		if burned {
			return refuse(dropPending(&epRefusal{enroll.CodeKeyRetired, ""}))
		}
		switch user, _, err := adm.UserOf(ctx, addr); {
		case errors.Is(err, admin.ErrNoRecord):
		case err != nil:
			return fail("server", admin.RewordUnavailable(err.Error()))
		case user != pendingKey:
			return refuse(dropPending(&epRefusal{enroll.CodeAddressTaken, ""}))
		default:
			os.Remove(pending)
			return t, epResult{userPub: pendingKey}, nil
		}
	}

	if dry {
		burned, err := adm.IsBurned(ctx, addr)
		if err != nil {
			return fail("server", admin.RewordUnavailable(err.Error()))
		}
		if burned {
			return refuse(&epRefusal{enroll.CodeKeyRetired, ""})
		}
		switch user, _, err := adm.UserOf(ctx, addr); {
		case errors.Is(err, admin.ErrNoRecord):
		case err != nil:
			return fail("server", admin.RewordUnavailable(err.Error()))
		case user != pub:
			return refuse(&epRefusal{enroll.CodeAddressTaken, ""})
		}
		if pub != "" {
			if ref, err := epIssuedRefusal(ctx, adm, addr, pub); err != nil {
				return fail("server", admin.RewordUnavailable(err.Error()))
			} else if ref != nil {
				return refuse(ref)
			}
		}
		return t, epResult{planned: true}, nil
	}

	deps := enroll.Deps{Keys: keys, Admin: adm, Account: acct}
	var seed []byte
	madePending := false
	if t.makeKey {
		// The key is made here and kept in the pending file before the directory is written, so a run killed at any
		// later point finds the same key when it runs again.
		if err := epPrepDir(filepath.Dir(outPath)); err != nil {
			return fail("write", fmt.Sprintf("the folder %s is not usable: %v; nothing was changed", filepath.Dir(outPath), err))
		}
		if pub, seed, madePending, err = epStageKey(pending); err != nil {
			return fail("write", fmt.Sprintf("cannot stage the new key in %s: %v; nothing was changed", pending, err))
		}
		defer clear(seed)
		if epAfterStage != nil {
			epAfterStage()
		}
	}
	res, err := enrollCreate(ctx, deps, enroll.Request{Addr: addr, PublicKey: pub, Meta: admin.Meta{Labels: map[string]string{}}})
	if err != nil {
		if r, ok := epRefusalOf(err); ok {
			if madePending {
				os.Remove(pending) // this call staged the key and the refusal changed nothing
			}
			return refuse(dropPending(r))
		}
		return partial(fmt.Sprintf("endpoint %s may be recorded but its credentials were not written: %s", addr, admin.RewordUnavailable(err.Error())))
	}

	var werr error
	switch {
	case credsArePending:
		// The output already holds this key (a run killed before the directory was written): nothing to write.
		os.Remove(pending)
	case t.makeKey && t.join:
		werr = epPublishStagedJoin(t.base, addr, res.JWT, seed, outPath, pending, true)
	case t.makeKey:
		werr = epPublishStaged(res.JWT, seed, outPath, pending, false, true)
	default:
		werr = writeJoinFile(outPath, t.base.withJWT(addr.String(), res.JWT))
	}
	if errors.Is(werr, fs.ErrExist) {
		return partial(fmt.Sprintf("%s appeared while the command ran; endpoint %s is recorded but %s was not written", outPath, addr, outPath))
	}
	if werr != nil {
		return partial(fmt.Sprintf("endpoint %s is recorded but %s was not written: %v", addr, outPath, werr))
	}
	return t, epResult{userPub: res.PublicKey}, nil
}

// epOutUser is the user public key of the credentials an earlier run wrote to path: the creds file, or with join the
// creds in the join file.
func epOutUser(path string, addr sap.Address, join bool) (string, error) {
	if !join {
		return epCredsUser(path, addr)
	}
	creds, err := readJoinCreds(path)
	if err != nil {
		return "", err
	}
	defer clear(creds)
	return epCredsUserOf(creds, path, addr)
}

// epPublishStagedJoin writes the join file for jwtText and seed and then, with removePending, removes the pending file.
// The join file is never written over an existing file.
func epPublishStagedJoin(base joinBase, addr sap.Address, jwtText string, seed []byte, path, pending string, removePending bool) error {
	if epAfterEnroll != nil {
		epAfterEnroll()
	}
	creds, err := jwt.FormatUserConfig(jwtText, seed)
	if err != nil {
		return errors.New("the creds could not be formatted")
	}
	defer clear(creds)
	if err := writeJoinFile(path, base.withCreds(addr.String(), creds)); err != nil {
		return err
	}
	if removePending {
		os.Remove(pending)
	}
	return nil
}

// epPathFree refuses an existing path (anything at all, a dangling link included).
func epPathFree(path string) *epRefusal {
	if _, err := os.Lstat(path); err == nil {
		return &epRefusal{"file_exists", fmt.Sprintf("%s already exists", path)}
	}
	return nil
}

// epTemp writes data to a new 0600 temp file in dir, named after base, and returns its path. On any failure the temp
// file is removed.
func epTemp(dir, base string, data []byte) (string, error) {
	f, err := os.CreateTemp(dir, "."+base+".tmp-*")
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	err = f.Chmod(0o600)
	if err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return "", err
	}
	return tmp, nil
}

// epWriteNew writes data to a new file, mode 0600, never over an existing file: a temp file in the same directory,
// then a hard link that fails with fs.ErrExist when path exists. A run killed part-way leaves no truncated path.
func epWriteNew(path string, data []byte) error {
	tmp, err := epTemp(filepath.Dir(path), filepath.Base(path), data)
	if err != nil {
		return err
	}
	defer os.Remove(tmp) // the temp this call made; path keeps its own link
	if err := os.Link(tmp, path); err != nil {
		return err
	}
	// The file was synced before the link; syncing the directory makes the link itself durable, so a staged seed
	// survives a crash before anything is revoked for it.
	return syncDir(filepath.Dir(path))
}

// epPrepDir makes the creds directory 0700 when it is missing and checks it is one only the user can swap files in.
func epPrepDir(dir string) error {
	if _, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	return ctlkeys.CheckParentDir(dir)
}

// epWriteCredsNew writes the creds file of a same-host endpoint, never over an existing file.
func epWriteCredsNew(path string, creds []byte) error {
	if err := epPrepDir(filepath.Dir(path)); err != nil {
		return err
	}
	created, err := ctlkeys.InstallFile(path, creds)
	if err != nil {
		if created {
			os.Remove(path)
		}
		return err
	}
	if !created {
		return fmt.Errorf("creds file %s exists: %w", path, fs.ErrExist)
	}
	return nil
}

// epReplaceCreds replaces the creds file at path: a 0600 temp file in the same directory, then a rename.
func epReplaceCreds(path string, creds []byte) error {
	dir := filepath.Dir(path)
	if err := epPrepDir(dir); err != nil {
		return err
	}
	tmp, err := epTemp(dir, filepath.Base(path), creds)
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// epDepsFor builds the enrollment dependencies from the key store.
func epAccountOf(cfg config.Config, env func(string) string) (*ctlkeys.Authority, string, error) {
	keys, err := openKeys(cfg, env)
	if err != nil {
		return nil, "", err
	}
	_, acct, err := keys.Account()
	if err != nil {
		return nil, "", err
	}
	return keys, acct, nil
}

// epRefusalOf maps an enroll refusal to its report; ok is false for any other error.
func epRefusalOf(err error) (*epRefusal, bool) {
	var r *enroll.Refusal
	if errors.As(err, &r) {
		return &epRefusal{r.Code, r.Msg}, true
	}
	return nil, false
}

// epIssuedRefusal is the refusal enroll.Create would make for publicKey from the issue record, or nil. It is the same
// read and the same order as the issue record write: a revoked key, then a key bound to another address.
func epIssuedRefusal(ctx context.Context, adm *admin.Admin, addr sap.Address, publicKey string) (*epRefusal, error) {
	rec, found, err := adm.IssuedByKey(ctx, publicKey)
	if err != nil || !found {
		return nil, err
	}
	switch {
	case rec.RevokedAt != "":
		return &epRefusal{enroll.CodeKeyRevoked, fmt.Sprintf("the public key %.8s... has been revoked; make a new key", publicKey)}, nil
	case rec.Address != addr.Flat():
		return &epRefusal{enroll.CodePublicKeyIssued, fmt.Sprintf("the public key %.8s... was already issued to another address", publicKey)}, nil
	}
	return nil, nil
}

// epKeyRevokedFail is nil when the account JWT does not revoke the user key pub. A revoked key is the refusal
// key_revoked: a JWT issued for it after the revocation time could authenticate, so it is never signed. A check that
// cannot be made is the error revocation_unknown.
func epKeyRevokedFail(ctx context.Context, cfg config.Config, env func(string) string, servers int, pub string) *epFail {
	revoked, err := keyRevoked(ctx, cfg, env, servers, pub)
	if err != nil {
		return &epFail{code: "revocation_unknown", detail: fmt.Sprintf("cannot tell whether the public key %.8s... is revoked: %v; nothing was changed", pub, err)}
	}
	if revoked {
		return &epFail{refused: true, code: enroll.CodeKeyRevoked, detail: fmt.Sprintf("the public key %.8s... was revoked; make a new key with: mash key create --out NAME.seed", pub)}
	}
	return nil
}

// epCredsUser is the user public key a creds file names, once the file is shown to be usable: it holds a user JWT
// whose name is the address and a seed whose public key is the JWT's subject. A JWT without its seed, or with another
// key's seed, is an error, never a key.
func epCredsUser(path string, addr sap.Address) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	defer clear(raw)
	return epCredsUserOf(raw, path, addr)
}

// epCredsUserOf is epCredsUser for creds text already read from path.
func epCredsUserOf(raw []byte, path string, addr sap.Address) (string, error) {
	tok, err := jwt.ParseDecoratedJWT(raw)
	if err != nil {
		return "", err
	}
	uc, err := jwt.DecodeUserClaims(tok)
	if err != nil {
		return "", err
	}
	if uc.Name != addr.String() {
		return "", fmt.Errorf("the JWT in %s is for %q, not %s", path, uc.Name, addr)
	}
	kp, err := jwt.ParseDecoratedUserNKey(raw)
	if err != nil {
		return "", fmt.Errorf("%s holds no usable seed: %w", path, err)
	}
	defer kp.Wipe()
	pub, err := kp.PublicKey()
	if err != nil || pub != uc.Subject {
		return "", fmt.Errorf("the seed in %s is not the key of its JWT", path)
	}
	return uc.Subject, nil
}

// enrollCreate and enrollReissue are the library calls the commands make. Tests replace them to inject a failure
// that is not a refusal; nothing else does.
var enrollCreate = enroll.Create
var enrollReissue = enroll.Reissue
