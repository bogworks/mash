package main

import (
	"context"
	"encoding/json"
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
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/ctlkeys"
	"github.com/bogworks/mash/internal/enroll"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/store"
)

const endpointCreateUsage = `usage: mashctl endpoint create [ADDRESS] [--owner O] [--group G] [--name N] (--public-key FILE | --make-key)
                               [--jwt-out FILE] [--label k=v]... [--tool] [--note N] [--describe T] [--cap C]...
                               [--servers N] [--sys-creds FILE] [--dry-run] [--json]
  ADDRESS is alice/dev/design, or one token (the instance) with --group. There is no default group.
  --public-key FILE  sign the public key the endpoint made (mash key create); the JWT is written to --jwt-out.
                     A key the account JWT revokes is refused (key_revoked); a check that cannot be made is an error
                     (revocation_unknown), and nothing is changed
  --make-key         make the key on this host and write <config dir>/<address>.creds (mode 0600); the key is kept in
                     <creds file>.pending until the creds file is written, so the same command run again after a kill
                     finishes with the same key. A staged key the account revokes is refused (staged_key_revoked); when
                     the address is taken or retired the file is removed, since that key can never be used for it
  A group cannot be changed later: make a new endpoint and delete the old one. The environment does not give a group.
  exit codes: 0 done or planned, 1 refused, 2 usage, 6 error
Example:
  mashctl endpoint create alice/dev/design --public-key design.pub
`

// endpointSchema is the schema field of the JSON of the endpoint verbs.
const endpointSchema = "mashctl-endpoint@1"

type epStep struct {
	Sender  string `json:"sender"`
	Step    string `json:"step"`
	Action  string `json:"action"`
	Address string `json:"address"`
	Purpose string `json:"purpose"`
	Detail  string `json:"detail"`
}

type epRefusal struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

// epDoc is the JSON document of create and reissue. The fields of the contract (REQ-003) are at the top level. No field
// holds a seed or a JWT.
type epDoc struct {
	Schema        string `json:"schema"`
	Outcome       string `json:"outcome"`
	Owner         string `json:"owner"`
	Group         string `json:"group"`
	Instance      string `json:"instance"`
	Address       string `json:"address"`
	Key           string `json:"key"`
	JWTPath       string `json:"jwt_path,omitempty"`
	JWTWritten    *bool  `json:"jwt_written,omitempty"`
	CredsPath     string `json:"creds_path,omitempty"`
	CredsWritten  *bool  `json:"creds_written,omitempty"`
	UserPublicKey string `json:"user_public_key,omitempty"`
	// ServersAcknowledged is set by reissue only: create does not change the account JWT, so it has no such field.
	ServersAcknowledged *int              `json:"servers_acknowledged,omitempty"`
	Labels              map[string]string `json:"labels"`
	Caps                []string          `json:"caps"`
	Description         string            `json:"description"`
	Note                string            `json:"note"`
	NextSteps           []epStep          `json:"next_steps"`
	Refusal             *epRefusal        `json:"refusal"`
}

// epDocFor is the document of an endpoint at addr (a zero addr leaves the names empty).
func epDocFor(addr sap.Address) epDoc {
	d := epDoc{Schema: endpointSchema, Outcome: "refused", Labels: map[string]string{}, Caps: []string{}}
	d.setAddr(addr)
	return d
}

func (d *epDoc) setAddr(a sap.Address) {
	if a.Instance == "" {
		return
	}
	d.Owner, d.Group, d.Instance, d.Address, d.Key = a.Owner, a.Group, a.Instance, a.String(), a.Flat()
}

// epOutput is the file a create or reissue writes: the JWT file, or with --make-key the creds file.
type epOutput struct {
	doc   *epDoc
	creds bool
}

func (o epOutput) set(path string) {
	f := false
	if o.creds {
		o.doc.CredsPath, o.doc.CredsWritten = path, &f
	} else {
		o.doc.JWTPath, o.doc.JWTWritten = path, &f
	}
}

func (o epOutput) path() string {
	if o.creds {
		return o.doc.CredsPath
	}
	return o.doc.JWTPath
}

func (o epOutput) written() {
	t := true
	if o.creds {
		o.doc.CredsWritten = &t
	} else {
		o.doc.JWTWritten = &t
	}
}

// epCommon are the flags create and reissue share.
type epCommon struct {
	publicKey, jwtOut        string
	makeKey, dryRun, jsonOut bool
}

// take reads one shared flag; handled is false for a flag it does not know.
func (c *epCommon) take(flag string, get func() (string, error)) (handled bool, err error) {
	switch flag {
	case "--make-key":
		c.makeKey = true
	case "--dry-run":
		c.dryRun = true
	case "--json":
		c.jsonOut = true
	case "--public-key", "--jwt-out":
		v, err := get()
		if err != nil {
			return true, err
		}
		if flag == "--public-key" {
			c.publicKey = v
		} else {
			c.jwtOut = v
		}
	default:
		return false, nil
	}
	return true, nil
}

func splitFlag(args []string, i int) (flag, inline string, hasInline bool) {
	a := args[i]
	if !strings.HasPrefix(a, "--") {
		return a, "", false
	}
	return strings.Cut(a, "=")
}

// epReport prints the result and returns the exit code. A refusal or error also goes to stderr as a sentence.
func epReport(verb string, doc epDoc, text func(io.Writer), code int, stdout, stderr io.Writer, jsonOut bool) int {
	if doc.Refusal != nil {
		doc.NextSteps = nil
		if doc.Refusal.Code == enroll.CodePublicKeyIssued {
			// Each key is used at one address; the remedy is a new key for this endpoint.
			doc.Refusal.Detail += "; each key is used at one address: make a new key for this endpoint with: mash key create --out " + doc.Instance + ".seed"
			doc.NextSteps = []epStep{{Sender: "endpoint", Step: "key", Action: "make a new key for this endpoint on its machine",
				Address: doc.Address, Purpose: "use", Detail: "mash key create --out " + doc.Instance + ".seed"}}
		}
		if doc.Refusal.Code == enroll.CodeKeyRevoked {
			doc.NextSteps = []epStep{{Sender: "endpoint", Step: "key", Action: "make a new key for this endpoint on its machine",
				Address: doc.Address, Purpose: "use", Detail: "mash key create --out " + doc.Instance + ".seed"}}
		}
		if doc.Refusal.Code == "key_unchanged" {
			doc.NextSteps = []epStep{{Sender: "endpoint", Step: "key", Action: "make a new key for this endpoint on its machine",
				Address: doc.Address, Purpose: "use", Detail: "mash key create --out NAME.seed"}}
		}
	}
	if doc.NextSteps == nil {
		doc.NextSteps = []epStep{}
	}
	if doc.Refusal != nil {
		fmt.Fprintf(stderr, "mashctl: endpoint %s: %s (%s): %s\n", verb, doc.Outcome, doc.Refusal.Code, doc.Refusal.Detail)
	}
	if jsonOut {
		b, _ := json.MarshalIndent(doc, "", "  ")
		fmt.Fprintf(stdout, "%s\n", b)
	} else if text != nil {
		text(stdout)
	}
	return code
}

// epValueFlags are the flags of create and reissue that take a value, for the scan that finds --json before parsing.
var epValueFlags = []string{"--owner", "--group", "--name", "--note", "--describe", "--cap", "--label", "--public-key", "--jwt-out", "--servers", "--sys-creds"}

// jsonRequested reports whether args hold a bare --json, so a usage error can be answered in JSON. The value after a
// flag that takes one is skipped.
func jsonRequested(args []string, valueFlags ...string) bool {
	for i := 0; i < len(args); i++ {
		if args[i] == "--json" {
			return true
		}
		for _, f := range valueFlags {
			if args[i] == f {
				i++
				break
			}
		}
	}
	return false
}

// epUsage reports a usage error: the sentence and the usage text on stderr, and with jsonOut the document on stdout.
func epUsage(verb, usageText string, doc epDoc, code, msg string, jsonOut bool, stdout, stderr io.Writer) int {
	fmt.Fprintf(stderr, "mashctl: endpoint %s: %s\n%s", verb, msg, usageText)
	if jsonOut {
		doc.Outcome = "error"
		doc.Refusal = &epRefusal{code, msg}
		doc.NextSteps = []epStep{}
		writeJSON(stdout, doc)
	}
	return 2
}

func epShortcut(a sap.Address) string { return a.Owner + "/" + a.Group + "/" + a.Instance }

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

// epPathFree refuses an existing path (anything at all, a dangling link included).
func epPathFree(path string) *epRefusal {
	if _, err := os.Lstat(path); err == nil {
		return &epRefusal{"file_exists", fmt.Sprintf("%s already exists and is never overwritten; name another file with --jwt-out or remove it", path)}
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
		return nil, "", fmt.Errorf("%v (run mashctl init first)", err)
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

// epNextCredential is the next step for a signed JWT on the endpoint's machine.
func epNextCredential(a sap.Address, jwtPath string) epStep {
	seed := a.Instance + ".seed"
	return epStep{Sender: "endpoint", Step: "credential", Action: "make the creds file on the endpoint's machine",
		Address: a.String(), Purpose: "use",
		Detail: fmt.Sprintf("mash credential create --seed %s --jwt %s", seed, filepath.Base(jwtPath))}
}

func epNextSession(a sap.Address) epStep {
	return epStep{Sender: "endpoint", Step: "session", Action: "start a session as this endpoint",
		Address: a.String(), Purpose: "use",
		Detail: fmt.Sprintf("MASH_OWNER=%s MASH_GROUP=%s MASH_NAME=%s", a.Owner, a.Group, a.Instance)}
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

// runEndpointCreate is `mashctl endpoint create`.
func runEndpointCreate(args []string, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprint(stdout, endpointCreateUsage)
		return 0
	}
	var (
		c                  epCommon
		positional         string
		owner, group, name string
		note, describe     string
		tool               bool
		caps               []string
		labels             = map[string]string{}
	)
	c.jsonOut = jsonRequested(args, epValueFlags...)
	var addr sap.Address
	doc := epDocFor(addr)
	usageCode := "usage"
	usage := func(msg string) int {
		code := usageCode
		usageCode = "usage"
		return epUsage("create", endpointCreateUsage, doc, code, msg, c.jsonOut, stdout, stderr)
	}
	for i := 0; i < len(args); i++ {
		flag, inline, hasInline := splitFlag(args, i)
		get := func() (string, error) { return flagValue(args, &i, flag, inline, hasInline) }
		if ok, err := c.take(flag, get); ok {
			if err != nil {
				return usage(err.Error())
			}
			continue
		}
		var v string
		var err error
		switch flag {
		case "--tool":
			tool = true
		case "--owner", "--group", "--name", "--note", "--describe", "--cap", "--label":
			if v, err = get(); err != nil {
				return usage(err.Error())
			}
			switch flag {
			case "--owner":
				owner = v
			case "--group":
				group = v
			case "--name":
				name = v
			case "--note":
				note = v
			case "--describe":
				describe = v
			case "--cap":
				caps = append(caps, v)
			case "--label":
				k, lv, ok := strings.Cut(v, "=")
				if !ok || k == "" || lv == "" {
					return usage("--label wants k=v (the value is not shown)")
				}
				if _, dup := labels[k]; dup {
					return usage(fmt.Sprintf("label %q given twice", k))
				}
				labels[k] = lv
			}
		default:
			if strings.HasPrefix(flag, "-") {
				usageCode = "unknown_flag"
				return usage("unknown " + config.RejectedArg(flag))
			}
			if positional != "" {
				return usage("one endpoint address only")
			}
			positional = flag
		}
	}

	cfg, cfgErr := loadCtlConfig(env, lookup, true)
	if cfgErr != nil {
		cfg, _ = loadCtlConfig(env, lookup, false)
	}

	// refuse reports a usage-level refusal (exit 2) or a refusal (exit 1) with the endpoint as far as it is known.
	refuse := func(code int, ref *epRefusal) int {
		doc.Refusal = ref
		if code == 2 {
			doc.Outcome = "error"
		}
		return epReport("create", doc, nil, code, stdout, stderr, c.jsonOut)
	}

	// Naming (design section 2.1).
	var sOwner, sGroup, sInstance string
	if positional != "" {
		toks := strings.Split(strings.ReplaceAll(positional, ".", "/"), "/")
		switch len(toks) {
		case 3:
			if toks[0] == "" || toks[1] == "" || toks[2] == "" {
				return refuse(1, &epRefusal{"name_invalid", "an address has three tokens, none empty: owner/group/instance"})
			}
			sOwner, sGroup, sInstance = toks[0], toks[1], toks[2]
		case 1:
			sInstance = toks[0]
		case 2:
			return refuse(2, &epRefusal{"address_ambiguous", "two tokens are ambiguous; write alice/dev/design or --group dev design"})
		default:
			return refuse(1, &epRefusal{"name_invalid", "an address has three tokens: owner/group/instance"})
		}
	}
	pick := func(what, shortcut, flagv string) (string, *epRefusal) {
		if shortcut != "" && flagv != "" && shortcut != flagv {
			return "", &epRefusal{"naming_disagrees", fmt.Sprintf("the address and --%s name different %s values; give one", what, what)}
		}
		if shortcut != "" {
			return shortcut, nil
		}
		return flagv, nil
	}
	var ref *epRefusal
	if owner, ref = pick("owner", sOwner, owner); ref != nil {
		return refuse(2, ref)
	}
	if group, ref = pick("group", sGroup, group); ref != nil {
		return refuse(2, ref)
	}
	if name, ref = pick("name", sInstance, name); ref != nil {
		return refuse(2, ref)
	}
	if group == "" {
		return refuse(2, &epRefusal{"group_missing", "an endpoint needs a group; write --group dev or alice/dev/design"})
	}
	if owner == "" {
		owner = cfg.Owner
	}
	if owner == "" {
		return refuse(2, &epRefusal{"owner_missing", `no owner: write --owner, alice/dev/design, or set MASH_OWNER (config.json "owner")`})
	}
	if name == "" {
		return refuse(2, &epRefusal{"name_missing", "no endpoint name: write alice/dev/design or --name design"})
	}
	var err error
	if addr, err = sap.NewAddress(owner, group, name); err != nil {
		return refuse(1, &epRefusal{"name_invalid", err.Error()})
	}
	doc.setAddr(addr)
	if err := admin.CheckProvision(cfg.NameRule, addr); err != nil {
		return refuse(1, &epRefusal{"name_rule", err.Error()})
	}
	switch {
	case c.makeKey && c.publicKey != "":
		return refuse(2, &epRefusal{"key_both", "give one of --public-key FILE and --make-key, not both"})
	case !c.makeKey && c.publicKey == "":
		return refuse(2, &epRefusal{"key_missing", "an endpoint needs a key: write --public-key FILE (the endpoint's public key, from mash key create) or --make-key (make the key on this host)"})
	}
	if tool {
		if old, ok := labels["kind"]; ok && old != "tool" {
			return usage(fmt.Sprintf("label kind=%s conflicts with --tool", old))
		}
		labels["kind"] = "tool"
	}
	if err := store.ValidLabels(labels); err != nil {
		return refuse(2, &epRefusal{"labels_invalid", err.Error()})
	}
	doc.Labels, doc.Description, doc.Note = labels, describe, note
	if caps != nil {
		doc.Caps = caps
	}
	if cfgErr != nil {
		fmt.Fprintln(stderr, cfgErr)
		doc.Outcome = "error"
		doc.Refusal = &epRefusal{"config", "the configuration did not load; fix it and run the same command again"}
		return epReport("create", doc, nil, 6, stdout, io.Discard, c.jsonOut)
	}

	// Everything that does not write: the key file, the output path, the account key.
	var pub, outPath string
	out := epOutput{doc: &doc, creds: c.makeKey}
	if c.makeKey {
		p, err := credsFileFor(cfg, addr)
		if err != nil {
			return refuse(1, &epRefusal{"no_config_dir", "no config directory to write the creds file in; set MASH_CONFIG_DIR"})
		}
		outPath = p
		out.set(p)
	} else {
		if pub, ref = epReadPublicKey(c.publicKey); ref != nil {
			return refuse(1, ref)
		}
		outPath = c.jwtOut
		display := c.jwtOut
		if outPath == "" {
			outPath = addr.String() + ".jwt"
			display = "./" + outPath
		}
		out.set(display)
	}
	// A same-host run killed after it staged its key finds the pending file: it is the key to finish with.
	pending := outPath + ".pending"
	var pendingKey string
	if c.makeKey {
		if _, lerr := os.Lstat(pending); lerr == nil {
			k, _, _, kerr := epStageKey(pending)
			if kerr != nil {
				return epErr(doc, "create", "write", fmt.Sprintf("cannot use the staged key %s: %v; nothing was changed", pending, kerr), c.jsonOut, stdout, stderr)
			}
			pendingKey = k
		}
	}
	// credsArePending is true when the creds file already holds the staged key: the earlier run may have finished.
	credsArePending := false
	if pendingKey != "" {
		if _, lerr := os.Lstat(outPath); lerr == nil {
			have, cerr := epCredsUser(outPath, addr)
			if cerr != nil || have != pendingKey {
				return refuse(1, &epRefusal{"creds_exists", fmt.Sprintf("%s already exists for another key and is never overwritten; move it aside and run the same command again", outPath)})
			}
			credsArePending = true
		}
	} else if ref := epPathFree(outPath); ref != nil {
		return refuse(1, ref)
	}
	if c.makeKey {
		pub = pendingKey
	}
	keys, acct, err := epAccountOf(cfg, env)
	if err != nil {
		return refuse(1, &epRefusal{"no_account_key", err.Error()})
	}

	announceTarget(cfg, env, stderr, nil)
	adm, ctx, closeAdmin, err := ctl.OpenAdmin("mashctl", cfg)
	if err != nil {
		return epErr(doc, "create", "server", admin.RewordUnavailable(err.Error())+"; nothing was changed, run the same command again once the server is reachable", c.jsonOut, stdout, stderr)
	}
	defer closeAdmin()

	// A supplied key that the account JWT revokes is never signed, whether or not an issue record says so (a JWT
	// issued after the revocation time would authenticate). This runs in a dry run too.
	if !c.makeKey {
		servers, serr := parseServers(env)
		if serr != nil {
			return refuse(2, &epRefusal{"servers_invalid", serr.Error()})
		}
		if f := epKeyRevokedFail(ctx, cfg, env, servers, pub); f != nil {
			if f.refused {
				return refuse(1, &epRefusal{f.code, f.detail})
			}
			return epErr(doc, "create", f.code, f.detail, c.jsonOut, stdout, stderr)
		}
	}
	// A staged key that the account JWT has revoked can never be used again: refuse, nothing was changed.
	if pendingKey != "" {
		servers, serr := parseServers(env)
		if serr != nil {
			return refuse(2, &epRefusal{"servers_invalid", serr.Error()})
		}
		revoked, rerr := keyRevoked(ctx, cfg, env, servers, pendingKey)
		if rerr != nil {
			return epErr(doc, "create", "revocation_unknown", fmt.Sprintf("cannot tell whether the staged key %s is revoked: %v; nothing was changed", pending, rerr), c.jsonOut, stdout, stderr)
		}
		if revoked {
			return refuse(1, &epRefusal{"staged_key_revoked", fmt.Sprintf("the staged key %s was revoked; remove %s and run the command again", pending, pending)})
		}
	}
	// dropPending removes the staged key this run read when the refusal says it can never be used for this address.
	dropPending := func(r *epRefusal) *epRefusal {
		if pendingKey != "" && !c.dryRun && (r.Code == enroll.CodeAddressTaken || r.Code == enroll.CodeKeyRetired) {
			os.Remove(pending)
		}
		return r
	}
	// The creds file holds the staged key: that proves the earlier run finished only when the directory record names
	// the same key. This needs the server (accepted: a rerun that cannot reach it says so and changes nothing). No
	// record means the run was killed before the directory was written: the normal path completes it.
	if credsArePending && !c.dryRun {
		burned, err := adm.IsBurned(ctx, addr)
		if err != nil {
			return epErr(doc, "create", "server", admin.RewordUnavailable(err.Error()), c.jsonOut, stdout, stderr)
		}
		if burned {
			return refuse(1, dropPending(&epRefusal{enroll.CodeKeyRetired, fmt.Sprintf("endpoint %s was retired and its address is never reassigned", addr)}))
		}
		switch user, _, err := adm.UserOf(ctx, addr); {
		case errors.Is(err, admin.ErrNoRecord):
		case err != nil:
			return epErr(doc, "create", "server", admin.RewordUnavailable(err.Error()), c.jsonOut, stdout, stderr)
		case user != pendingKey:
			return refuse(1, dropPending(&epRefusal{enroll.CodeAddressTaken, fmt.Sprintf("endpoint %s already exists; mashctl endpoint show %s shows it, mashctl endpoint reissue %s gives it a new key", addr, epShortcut(addr), epShortcut(addr))}))
		default:
			os.Remove(pending)
			doc.UserPublicKey = pendingKey
			out.written()
			doc.Outcome = "created"
			doc.NextSteps = []epStep{epNextSession(addr)}
			return epReport("create", doc, func(w io.Writer) {
				fmt.Fprintf(w, "created endpoint %s\n  address  %s\n  creds    %s\nnext: start a session as this endpoint: %s\n", epShortcut(addr), addr, outPath, doc.NextSteps[0].Detail)
			}, 0, stdout, stderr, c.jsonOut)
		}
	}

	// next steps, shown on both the planned and the created result.
	var step epStep
	if c.makeKey {
		step = epNextSession(addr)
	} else {
		step = epNextCredential(addr, out.path())
	}
	doc.NextSteps = []epStep{step}
	nextLine := "next: on the endpoint's machine, run: " + step.Detail
	if c.makeKey {
		nextLine = "next: start a session as this endpoint: " + step.Detail
	}
	where := func(w io.Writer) {
		fmt.Fprintf(w, "  address  %s\n", addr)
		if c.makeKey {
			fmt.Fprintf(w, "  creds    %s\n", out.path())
		} else {
			fmt.Fprintf(w, "  jwt      %s\n", out.path())
		}
		fmt.Fprintln(w, nextLine)
	}

	if c.dryRun {
		burned, err := adm.IsBurned(ctx, addr)
		if err != nil {
			return epErr(doc, "create", "server", admin.RewordUnavailable(err.Error()), c.jsonOut, stdout, stderr)
		}
		if burned {
			return refuse(1, &epRefusal{enroll.CodeKeyRetired, fmt.Sprintf("endpoint %s was retired and its address is never reassigned", addr)})
		}
		switch user, _, err := adm.UserOf(ctx, addr); {
		case errors.Is(err, admin.ErrNoRecord):
		case err != nil:
			return epErr(doc, "create", "server", admin.RewordUnavailable(err.Error()), c.jsonOut, stdout, stderr)
		case user != pub:
			return refuse(1, &epRefusal{enroll.CodeAddressTaken, fmt.Sprintf("endpoint %s already exists; mashctl endpoint show %s shows it, mashctl endpoint reissue %s gives it a new key", addr, epShortcut(addr), epShortcut(addr))})
		}
		if pub != "" {
			if ref, err := epIssuedRefusal(ctx, adm, addr, pub); err != nil {
				return epErr(doc, "create", "server", admin.RewordUnavailable(err.Error()), c.jsonOut, stdout, stderr)
			} else if ref != nil {
				return refuse(1, ref)
			}
		}
		doc.Outcome = "planned"
		return epReport("create", doc, func(w io.Writer) {
			fmt.Fprintf(w, "would create endpoint %s\n", epShortcut(addr))
			where(w)
		}, 0, stdout, stderr, c.jsonOut)
	}

	deps := enroll.Deps{Keys: keys, Admin: adm, Account: acct}
	var seed []byte
	madePending := false
	if c.makeKey {
		// The key is made here and kept in the pending file before the directory is written, so a run killed at any
		// later point finds the same key when it runs again.
		if err := epPrepDir(filepath.Dir(outPath)); err != nil {
			return epErr(doc, "create", "write", fmt.Sprintf("the creds directory is not usable: %v; nothing was changed", err), c.jsonOut, stdout, stderr)
		}
		if pub, seed, madePending, err = epStageKey(pending); err != nil {
			return epErr(doc, "create", "write", fmt.Sprintf("cannot stage the new key in %s: %v; nothing was changed", pending, err), c.jsonOut, stdout, stderr)
		}
		defer clear(seed)
		if epAfterStage != nil {
			epAfterStage()
		}
	}
	res, err := enrollCreate(ctx, deps, enroll.Request{Addr: addr, PublicKey: pub, Note: note,
		Meta: admin.Meta{Labels: labels, Description: describe, Caps: caps}})
	if err != nil {
		if r, ok := epRefusalOf(err); ok {
			if madePending {
				os.Remove(pending) // this call staged the key and the refusal changed nothing
			}
			return refuse(1, dropPending(r))
		}
		return epErr(doc, "create", "server", admin.RewordUnavailable(err.Error())+"; run the same command again to finish it", c.jsonOut, stdout, stderr)
	}
	doc.UserPublicKey = res.PublicKey

	var werr error
	if credsArePending {
		// The creds file already holds this key (a run killed before the directory was written): nothing to write.
		os.Remove(pending)
	} else if c.makeKey {
		werr = epPublishStaged(res.JWT, seed, outPath, pending, false)
	} else {
		werr = epWriteNew(outPath, []byte(res.JWT+"\n"))
	}
	if errors.Is(werr, fs.ErrExist) {
		return refuse(1, &epRefusal{"file_exists", fmt.Sprintf("%s appeared while the command ran and is never overwritten; endpoint %s is recorded: run the same command again with another --jwt-out", outPath, addr)})
	}
	if werr != nil {
		finish := "run the same command again to finish it (it signs again and writes the JWT)"
		if c.makeKey {
			finish = fmt.Sprintf("the key made for it is kept in %s; run the same command again to finish it", pending)
		}
		return epErr(doc, "create", "write", fmt.Sprintf("endpoint %s is recorded but the file %s was not written (%v); %s", addr, outPath, werr, finish), c.jsonOut, stdout, stderr)
	}
	out.written()
	doc.Outcome = "created"
	return epReport("create", doc, func(w io.Writer) {
		fmt.Fprintf(w, "created endpoint %s\n", epShortcut(addr))
		where(w)
	}, 0, stdout, stderr, c.jsonOut)
}

// epErr reports an error outcome (exit 6).
func epErr(doc epDoc, verb, code, detail string, jsonOut bool, stdout, stderr io.Writer) int {
	doc.Outcome = "error"
	doc.Refusal = &epRefusal{code, detail}
	return epReport(verb, doc, nil, 6, stdout, stderr, jsonOut)
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

// enrollCreate and enrollReissueThen are the library calls the commands make. Tests replace them to inject a failure
// that is not a refusal; nothing else does.
var enrollCreate = enroll.Create
var enrollReissueThen = enroll.ReissueThen
