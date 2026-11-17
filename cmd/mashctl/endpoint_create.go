package main

import (
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
  --public-key FILE  sign the public key the endpoint made (mash key create); the JWT is written to --jwt-out
  --make-key         make the key on this host and write <config dir>/<address>.creds (mode 0600)
  exit codes: 0 done or planned, 1 refused, 2 usage, 6 error
Example:
  mashctl endpoint create alice/dev/design --public-key design.pub
`

// endpointSchema is the schema field of the JSON of the endpoint verbs.
const endpointSchema = "mashctl-endpoint@1"

type epEndpoint struct {
	Owner       string            `json:"owner"`
	Group       string            `json:"group"`
	Instance    string            `json:"instance"`
	Address     string            `json:"address"`
	Key         string            `json:"key"`
	Labels      map[string]string `json:"labels"`
	Caps        []string          `json:"caps"`
	Description string            `json:"description"`
	Note        string            `json:"note"`
}

type epFile struct {
	Path    string `json:"path"`
	Written bool   `json:"written"`
}

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

// epDoc is the JSON document of create and reissue. No field holds a seed or a JWT.
type epDoc struct {
	Schema        string     `json:"schema"`
	Outcome       string     `json:"outcome"`
	Endpoint      epEndpoint `json:"endpoint"`
	JWT           *epFile    `json:"jwt,omitempty"`
	Creds         *epFile    `json:"creds,omitempty"`
	UserPublicKey string     `json:"user_public_key,omitempty"`
	NextSteps     []epStep   `json:"next_steps"`
	Refusal       *epRefusal `json:"refusal"`
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

func epEndpointOf(a sap.Address) epEndpoint {
	return epEndpoint{Owner: a.Owner, Group: a.Group, Instance: a.Instance, Address: a.String(), Key: a.Flat(),
		Labels: map[string]string{}, Caps: []string{}}
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

// epWriteNew writes data to a new file, mode 0600, never over an existing file.
func epWriteNew(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(path)
		return err
	}
	return nil
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
		return fmt.Errorf("creds file %s exists", path)
	}
	return nil
}

// epReplaceCreds replaces the creds file at path: a 0600 temp file in the same directory, then a rename.
func epReplaceCreds(path string, creds []byte) error {
	dir := filepath.Dir(path)
	if err := epPrepDir(dir); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".creds-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if err := f.Chmod(0o600); err == nil {
		_, err = f.Write(creds)
		if err == nil {
			err = f.Sync()
		}
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	} else {
		f.Close()
	}
	if err != nil {
		os.Remove(tmp)
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
	usage := func(msg string) int {
		fmt.Fprintf(stderr, "mashctl: endpoint create: %s\n%s", msg, endpointCreateUsage)
		return 2
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
	var addr sap.Address
	doc := epDoc{Schema: endpointSchema, Outcome: "refused"}
	refuse := func(code int, ref *epRefusal) int {
		doc.Refusal = ref
		if code == 2 {
			doc.Outcome = "error"
		}
		if doc.Endpoint.Address == "" {
			doc.Endpoint = epEndpointOf(addr)
		}
		return epReport("create", doc, nil, code, stdout, stderr, c.jsonOut)
	}

	// Naming (design section 2.1).
	var sOwner, sGroup, sInstance string
	if positional != "" {
		toks := strings.Split(strings.ReplaceAll(positional, ".", "/"), "/")
		switch len(toks) {
		case 3:
			sOwner, sGroup, sInstance = toks[0], toks[1], toks[2]
		case 1:
			sInstance = toks[0]
		case 2:
			return refuse(2, &epRefusal{"address_ambiguous", "two tokens are ambiguous; write alice/dev/design or --group dev design"})
		default:
			return refuse(2, &epRefusal{"name_invalid", "an address has three tokens: owner/group/instance"})
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
		group = env("MASH_GROUP")
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
		return refuse(2, &epRefusal{"name_invalid", err.Error()})
	}
	doc.Endpoint = epEndpointOf(addr)
	if err := admin.CheckProvision(cfg.NameRule, addr); err != nil {
		return refuse(2, &epRefusal{"name_rule", err.Error()})
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
	doc.Endpoint.Labels, doc.Endpoint.Description, doc.Endpoint.Note = labels, describe, note
	if caps != nil {
		doc.Endpoint.Caps = caps
	}
	if cfgErr != nil {
		fmt.Fprintln(stderr, cfgErr)
		doc.Outcome = "error"
		doc.Refusal = &epRefusal{"config", "the configuration did not load; fix it and run the same command again"}
		return epReport("create", doc, nil, 6, stdout, io.Discard, c.jsonOut)
	}

	// Everything that does not write: the key file, the output path, the account key.
	var pub, outPath string
	var outFile *epFile
	if c.makeKey {
		p, err := credsFileFor(cfg, addr)
		if err != nil {
			return refuse(1, &epRefusal{"no_config_dir", "no config directory to write the creds file in; set MASH_CONFIG_DIR"})
		}
		outPath = p
		doc.Creds = &epFile{Path: p}
		outFile = doc.Creds
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
		doc.JWT = &epFile{Path: display}
		outFile = doc.JWT
	}
	if ref := epPathFree(outPath); ref != nil {
		return refuse(1, ref)
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

	// next steps, shown on both the planned and the created result.
	var step epStep
	if c.makeKey {
		step = epNextSession(addr)
	} else {
		step = epNextCredential(addr, outFile.Path)
	}
	doc.NextSteps = []epStep{step}
	nextLine := "next: on the endpoint's machine, run: " + step.Detail
	if c.makeKey {
		nextLine = "next: start a session as this endpoint: " + step.Detail
	}
	where := func(w io.Writer) {
		fmt.Fprintf(w, "  address  %s\n", addr)
		if c.makeKey {
			fmt.Fprintf(w, "  creds    %s\n", outFile.Path)
		} else {
			fmt.Fprintf(w, "  jwt      %s\n", outFile.Path)
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
		case c.makeKey || user != pub:
			return refuse(1, &epRefusal{enroll.CodeAddressTaken, fmt.Sprintf("endpoint %s already exists; mashctl endpoint show %s shows it, mashctl endpoint reissue %s gives it a new key", addr, epShortcut(addr), epShortcut(addr))})
		}
		doc.Outcome = "planned"
		return epReport("create", doc, func(w io.Writer) {
			fmt.Fprintf(w, "would create endpoint %s\n", epShortcut(addr))
			where(w)
		}, 0, stdout, stderr, c.jsonOut)
	}

	deps := enroll.Deps{Keys: keys, Admin: adm, Account: acct}
	res, err := enroll.Create(ctx, deps, enroll.Request{Addr: addr, PublicKey: pub, Note: note,
		Meta: admin.Meta{Labels: labels, Description: describe, Caps: caps}})
	if err != nil {
		if r, ok := epRefusalOf(err); ok {
			return refuse(1, r)
		}
		return epErr(doc, "create", "enroll", admin.RewordUnavailable(err.Error())+"; run the same command again to finish it", c.jsonOut, stdout, stderr)
	}
	defer clear(res.Seed)
	doc.UserPublicKey = res.PublicKey

	var werr error
	if c.makeKey {
		creds, ferr := jwt.FormatUserConfig(res.JWT, res.Seed)
		if ferr != nil {
			werr = errors.New("the creds could not be formatted")
		} else {
			werr = epWriteCredsNew(outPath, creds)
			clear(creds)
		}
	} else {
		werr = epWriteNew(outPath, []byte(res.JWT+"\n"))
	}
	if werr != nil {
		finish := "run the same command again to finish it (it signs again and writes the JWT)"
		if c.makeKey {
			finish = fmt.Sprintf("the key made for it is lost; run: mashctl endpoint reissue %s --make-key", epShortcut(addr))
		}
		return epErr(doc, "create", "write", fmt.Sprintf("endpoint %s is recorded but the file %s was not written (%v); %s", addr, outPath, werr, finish), c.jsonOut, stdout, stderr)
	}
	outFile.Written = true
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
