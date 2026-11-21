package main

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/cmdcore/cmds"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/joinfile"
	"github.com/bogworks/mash/internal/sap"
)

// joinCAFile is the name the join file's certificate authority gets in the configuration folder, and what config.json's
// "ca" then says (a relative "ca" is read against the folder).
const joinCAFile = "ca.pem"

// maxJoinFile is the largest join file read: a creds file, a certificate and a few servers are a few kilobytes.
const maxJoinFile = 1 << 20

// joinSvc installs a join file into the configuration folder. It reads no endpoint and connects to nothing.
// The rename and remove the install steps and their rollback use; tests replace them to fail a step.
var (
	joinRename = os.Rename
	joinRemove = os.Remove
)

type joinSvc struct {
	env  func(string) string
	home string
}

// notJoinFile is the failure for a file that is not a join file this mash reads. It never carries any of the file.
func notJoinFile(path string) *cliframe.Error {
	return cliframe.Failf("%s is not a join file this mash can read", config.Clean(path))
}

// readJoinFile reads and parses the join file at path, and checks its address, its creds or JWT and its certificate.
func readJoinFile(path string) (joinfile.File, error) {
	var zero joinfile.File
	sf, err := openSecret(path)
	if err != nil {
		return zero, err
	}
	defer sf.close()
	if sf.f == nil {
		return zero, sf.refuse() // a directory, device or fifo: not a plain file
	}
	raw, err := sf.readAll(maxJoinFile)
	if err != nil {
		return zero, readFailure(path, err)
	}
	if len(raw) > maxJoinFile {
		clear(raw)
		return zero, notJoinFile(path)
	}
	defer clear(raw)
	j, err := joinfile.Parse(raw)
	if err != nil {
		if strings.Contains(err.Error(), "mash_join is") {
			return zero, cliframe.Failf("%s is a join file of a version this mash does not read (%s)", config.Clean(path), strings.TrimPrefix(err.Error(), joinfile.ErrNotJoinFile.Error()+": "))
		}
		return zero, notJoinFile(path)
	}
	// a join file that holds a seed is a secret file; one that holds only a user JWT is not
	if j.Creds != "" {
		if err := sf.refuse(); err != nil {
			return zero, err
		}
	}
	if _, err := sap.ParseAddress(j.Address); err != nil {
		return zero, notJoinFile(path)
	}
	if j.CA != nil {
		if _, _, err := certsIn([]byte(*j.CA)); err != nil {
			return zero, notJoinFile(path)
		}
	}
	for _, s := range j.Servers {
		if strings.TrimSpace(s) == "" || strings.ContainsAny(s, ", \t\r\n") {
			return zero, notJoinFile(path)
		}
	}
	return j, nil
}

// readSeed reads the seed file given with --key: a user seed, trimmed.
func readSeed(path string) ([]byte, nkeys.KeyPair, error) {
	sf, err := openSecret(path)
	if err != nil {
		return nil, nil, err
	}
	defer sf.close()
	if err := sf.refuse(); err != nil {
		return nil, nil, err
	}
	raw, err := sf.readAll(maxJoinFile)
	if err != nil {
		return nil, nil, readFailure(path, err)
	}
	defer clear(raw)
	seed := bytes.TrimSpace(raw)
	kp, err := nkeys.FromSeed(seed)
	if err != nil {
		clear(seed)
		return nil, nil, cliframe.Failf("%s does not hold a user seed", config.Clean(path))
	}
	pub, err := kp.PublicKey()
	if err != nil || !nkeys.IsValidPublicUserKey(pub) {
		clear(seed)
		return nil, nil, cliframe.Failf("%s does not hold a user seed", config.Clean(path))
	}
	return bytes.Clone(seed), kp, nil
}

// credsText is the creds file to install: the join file's own, or the NATS creds file made from its user JWT and the
// seed read from --key. A problem with the text is the join file's, a seed that does not belong is the seed's.
func credsText(j joinfile.File, req cmds.JoinRequest) ([]byte, error) {
	if j.Creds != "" {
		token, err := jwt.ParseDecoratedJWT([]byte(j.Creds))
		if err != nil {
			return nil, notJoinFile(req.File)
		}
		kp, err := jwt.ParseDecoratedUserNKey([]byte(j.Creds))
		if err != nil {
			return nil, notJoinFile(req.File)
		}
		pub, _ := kp.PublicKey()
		// the JWT's name is the endpoint it was issued for: it must be the address the join file says
		if c, err := jwt.DecodeUserClaims(token); err != nil || c.Subject != pub || c.Name != j.Address {
			return nil, notJoinFile(req.File)
		}
		text := j.Creds
		if !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		return []byte(text), nil
	}
	claims, err := jwt.DecodeUserClaims(j.UserJWT)
	if err != nil || claims.Name != j.Address {
		return nil, notJoinFile(req.File)
	}
	if req.Key == "" {
		return nil, cliframe.Usagef("this join file holds no seed; it was made for your own key").
			WithHint("mash join " + req.File + " --key <your seed file>")
	}
	seed, kp, err := readSeed(req.Key)
	if err != nil {
		return nil, err
	}
	defer clear(seed)
	if pub, _ := kp.PublicKey(); pub != claims.Subject {
		return nil, cliframe.Failf("%s is not the key this join file was made for", config.Clean(req.Key))
	}
	return jwt.FormatUserConfig(j.UserJWT, seed)
}

// serversIn is the servers a config.json value names: a JSON array of strings, or one string that is a comma list.
// stringIn is the JSON string raw holds, "" when it is absent or not a string.
func stringIn(raw json.RawMessage) string {
	var v string
	if json.Unmarshal(raw, &v) != nil {
		return ""
	}
	return v
}

func serversIn(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var one string
	if json.Unmarshal(raw, &one) == nil {
		var out []string
		for _, p := range strings.Split(one, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
		return out
	}
	var many []string
	if json.Unmarshal(raw, &many) == nil {
		var out []string
		for _, m := range many {
			for _, p := range strings.Split(m, ",") {
				if p = strings.TrimSpace(p); p != "" {
					out = append(out, p)
				}
			}
		}
		return out
	}
	return nil
}

func sameServers(a, b []string) bool {
	a, b = slices.Sorted(slices.Values(a)), slices.Sorted(slices.Values(b))
	return slices.Equal(a, b)
}

// shown is path as it is shown: a path under the home folder starts with ~.
// absPath is p made absolute (p itself when that cannot be done).
func absPath(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

func (s joinSvc) shown(path string) string {
	return config.TildeIn(path, strings.TrimRight(s.home, "/"))
}

// certsIn parses a PEM text that must hold certificates and nothing else: every block a CERTIFICATE (no headers) that
// x509 accepts, at least one, and only white space after the last. It returns the PEM re-encoded from the parsed blocks
// (what is written as ca.pem) and the certificates' DER bytes, sorted, which is what two authorities are compared by.
func certsIn(text []byte) (pemOut []byte, ders []string, err error) {
	rest := text
	for {
		// pem.Decode skips text before a block; only white space may come before one.
		if t := bytes.TrimLeft(rest, " \t\r\n"); len(t) != 0 && !bytes.HasPrefix(t, []byte("-----BEGIN ")) {
			return nil, nil, errors.New("not only certificates")
		}
		blk, r := pem.Decode(rest)
		if blk == nil {
			if len(bytes.TrimSpace(rest)) != 0 {
				return nil, nil, errors.New("not only certificates")
			}
			break
		}
		if blk.Type != "CERTIFICATE" || len(blk.Headers) != 0 {
			return nil, nil, errors.New("not only certificates")
		}
		if _, err := x509.ParseCertificate(blk.Bytes); err != nil {
			return nil, nil, err
		}
		pemOut = append(pemOut, pem.EncodeToMemory(blk)...)
		ders = append(ders, string(blk.Bytes))
		rest = r
	}
	if len(ders) == 0 {
		return nil, nil, errors.New("no certificate")
	}
	slices.Sort(ders)
	return pemOut, slices.Compact(ders), nil
}

// folderCA is the certificate authority the configuration folder trusts: the file config.json's "ca" names (a relative
// path is read against the folder, a ~ against the home), else ca.pem. have is false when the folder has none. ders is
// nil when the file is there but holds no certificates, which no join file's authority equals. A named file that is
// missing is an error when the join file brings a certificate authority (wantCA): there is nothing to compare with.
func (s joinSvc) folderCA(dir string, cfg map[string]json.RawMessage, wantCA bool) (have bool, ders []string, err error) {
	path := filepath.Join(dir, joinCAFile)
	named := stringIn(cfg["ca"])
	if named != "" {
		if path = config.ExpandHome(named, s.home); !filepath.IsAbs(path) {
			path = filepath.Join(dir, path)
		}
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		if named != "" && wantCA {
			return false, nil, cliframe.Failf("cannot read %s: it is missing", s.shown(path)).WithHint("mash doctor")
		}
		return false, nil, nil
	}
	if err != nil {
		return false, nil, cliframe.Failf("cannot read %s: %v", s.shown(path), errors.Unwrap(err))
	}
	_, ders, perr := certsIn(raw)
	if perr != nil {
		ders = nil
	}
	return true, ders, nil
}

// Join installs the join file into the configuration folder. Everything is checked before anything is written, under a
// lock on the folder; the files are written whole to temporary files and renamed or linked into place, the creds file
// first, then ca.pem, then config.json, so a run whose creds step fails has installed nothing, and a run that fails
// later removes what it made.
func (s joinSvc) Join(_ context.Context, req cmds.JoinRequest) (cmds.JoinOutcome, error) {
	j, err := readJoinFile(req.File)
	if err != nil {
		return cmds.JoinOutcome{}, err
	}
	creds, err := credsText(j, req)
	if err != nil {
		return cmds.JoinOutcome{}, err
	}
	defer clear(creds)
	if v := s.env("MASH_CONFIG_DIR"); v != "" && !filepath.IsAbs(v) {
		return cmds.JoinOutcome{}, cliframe.Usagef("--config-dir must be an absolute path: %s", config.Clean(v)).WithHint("mash join --help")
	}
	dir := config.ConfigDir(s.env, s.home)
	if dir == "" {
		return cmds.JoinOutcome{}, cliframe.Usagef("there is no configuration folder: HOME is not set").WithHint("mash join " + req.File + " --config-dir <folder>")
	}
	// one folder serves one mesh: a different server or certificate authority is refused, with or without --replace
	if !req.DryRun {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return cmds.JoinOutcome{}, cliframe.Failf("cannot make %s: %v", s.shown(dir), errors.Unwrap(err))
		}
		unlock, err := lockDir(dir)
		if err != nil {
			return cmds.JoinOutcome{}, cliframe.Failf("cannot lock %s: %v", s.shown(dir), errors.Unwrap(err))
		}
		defer unlock()
	}
	otherFolder := func(what string) error {
		return cliframe.Failf("%s was set up with a different %s;\n       nothing was changed", s.shown(dir), what).
			WithHint("mash join " + req.File + " --config-dir <another folder>")
	}

	// config.json: one folder serves one mesh and one certificate authority.
	cfgPath := filepath.Join(dir, "config.json")
	cfg := map[string]json.RawMessage{}
	cfgMode := os.FileMode(0o600)
	cfgRaw, err := os.ReadFile(cfgPath)
	switch {
	case err == nil:
		if fi, serr := os.Stat(cfgPath); serr == nil {
			cfgMode = fi.Mode().Perm()
		}
		if err := json.Unmarshal(cfgRaw, &cfg); err != nil || cfg == nil {
			return cmds.JoinOutcome{}, cliframe.Failf("%s is not a JSON object; nothing was written", s.shown(cfgPath)).WithHint("mash doctor")
		}
	case !errors.Is(err, fs.ErrNotExist):
		return cmds.JoinOutcome{}, cliframe.Failf("cannot read %s: %v", s.shown(cfgPath), errors.Unwrap(err))
	}
	have := serversIn(cfg["servers"])
	if len(have) > 0 && !sameServers(have, j.Servers) {
		return cmds.JoinOutcome{}, otherFolder("server")
	}

	// The certificate authority: the folder's, if it has one, must be this join file's, whatever --replace says.
	var caText []byte
	var joinDers []string
	if j.CA != nil {
		if caText, joinDers, err = certsIn([]byte(*j.CA)); err != nil {
			return cmds.JoinOutcome{}, notJoinFile(req.File)
		}
	}
	haveCA, folderDers, err := s.folderCA(dir, cfg, j.CA != nil)
	if err != nil {
		return cmds.JoinOutcome{}, err
	}
	if haveCA && (j.CA == nil || !slices.Equal(folderDers, joinDers)) {
		return cmds.JoinOutcome{}, otherFolder("ca")
	}
	if !haveCA && j.CA != nil && len(config.InstalledEndpoints(dir)) > 0 {
		// the folder has no certificate authority and already holds an endpoint: that endpoint trusts the system's
		// roots, and one that brings a ca would change what it trusts. The first join into an empty folder sets it.
		return cmds.JoinOutcome{}, otherFolder("ca")
	}

	address := j.Address
	credsPath := filepath.Join(dir, address+".creds")
	out := cmds.JoinOutcome{Endpoint: address, Servers: slices.Clone(j.Servers), Creds: s.shown(credsPath), CredsPath: absPath(credsPath)}
	_, lerr := os.Lstat(credsPath)
	credsExist := lerr == nil
	if credsExist && !req.Replace {
		return cmds.JoinOutcome{}, cliframe.Failf("%s is already installed here", address).WithHint("mash join " + req.File + " --replace")
	}

	// What else is written: --replace of installed creds replaces the creds and nothing else.
	caPath := filepath.Join(dir, joinCAFile)
	// An existing creds path that is not an installed endpoint (a symlink, a wrong mode) is replaced all the same, but
	// the join then writes the rest as a first join does: the new creds would be unusable without it.
	replaceCreds := credsExist
	replacing := credsExist && slices.Contains(config.InstalledEndpoints(dir), address)
	writeCA := j.CA != nil && !haveCA && !replacing
	newCfg := cloneConfig(cfg)
	if len(have) == 0 {
		b, _ := json.Marshal(j.Servers)
		newCfg["servers"] = b
	}
	if j.CA != nil && len(newCfg["ca"]) == 0 {
		b, _ := json.Marshal(joinCAFile)
		newCfg["ca"] = b
	}
	writeCfg := !replacing && (len(have) == 0 || len(cfg["ca"]) == 0 && j.CA != nil)

	if req.DryRun {
		return out, nil
	}

	// Write: temporary files first, then the creds, ca.pem and config.json into place, in that order.
	var temps []string
	defer func() {
		for _, t := range temps {
			os.Remove(t) // the temporary files this run made, by their recorded names
		}
	}()
	stage := func(data []byte, mode os.FileMode) (string, error) {
		f, err := os.CreateTemp(dir, ".join-*")
		if err != nil {
			return "", err
		}
		temps = append(temps, f.Name())
		if err := f.Chmod(mode); err != nil {
			f.Close()
			return "", err
		}
		if _, err := f.Write(data); err != nil {
			f.Close()
			return "", err
		}
		return f.Name(), f.Close()
	}
	fail := func(err error) (cmds.JoinOutcome, error) {
		reason := err
		var pe *fs.PathError
		var le *os.LinkError
		switch {
		case errors.As(err, &pe):
			reason = pe.Err
		case errors.As(err, &le):
			reason = le.Err
		}
		return cmds.JoinOutcome{}, cliframe.Failf("cannot write into %s: %v; nothing was installed", s.shown(dir), reason).WithHint("mash doctor")
	}
	var caTmp, cfgTmp string
	if writeCA {
		if caTmp, err = stage(caText, 0o644); err != nil {
			return fail(err)
		}
	}
	if writeCfg {
		b, merr := json.MarshalIndent(newCfg, "", "  ")
		if merr != nil {
			return fail(merr)
		}
		if cfgTmp, err = stage(append(b, '\n'), cfgMode); err != nil {
			return fail(err)
		}
	}
	credsTmp, err := stage(creds, 0o600)
	if err != nil {
		return fail(err)
	}

	// The creds first. A replaced creds file is kept under a temporary name until the run is whole, so a later failure
	// can put it back.
	var backup string
	if replaceCreds {
		backup = filepath.Join(dir, ".join-backup-"+filepath.Base(credsTmp))
		if err := os.Link(credsPath, backup); err != nil {
			return fail(err)
		}
		temps = append(temps, backup)
		err = os.Rename(credsTmp, credsPath)
	} else if err = os.Link(credsTmp, credsPath); errors.Is(err, fs.ErrExist) { // made since the check above
		return cmds.JoinOutcome{}, cliframe.Failf("%s is already installed here", address).WithHint("mash join " + req.File + " --replace")
	}
	if err != nil {
		return fail(err)
	}
	var made []string // what this run created, by exact path, removed if a later step fails
	// undo puts the folder back as it was and returns what it could not: the exact paths a rollback step failed on.
	undo := func() (left []string, kept string) {
		left, kept = joinUndo(made, backup, credsPath)
		if kept != "" && kept == backup {
			temps = slices.DeleteFunc(temps, func(t string) bool { return t == backup })
		}
		return left, kept
	}
	// rollback undoes the run after a failed step; if the undo itself fails it names what was left, by exact path.
	rollback := func(err error) (cmds.JoinOutcome, error) {
		left, kept := undo()
		if len(left) == 0 {
			return fail(err)
		}
		return cmds.JoinOutcome{}, s.undoFailure(left, kept)
	}
	if caTmp != "" {
		if err := joinRename(caTmp, caPath); err != nil {
			return rollback(err)
		}
		made = append(made, caPath)
	}
	if cfgTmp != "" {
		if err := joinRename(cfgTmp, cfgPath); err != nil {
			return rollback(err)
		}
	}
	return out, nil
}

func cloneConfig(m map[string]json.RawMessage) map[string]json.RawMessage {
	c := make(map[string]json.RawMessage, len(m)+2)
	for k, v := range m {
		c[k] = v
	}
	return c
}

// joinUndo removes what a join run made (by exact path) and puts a replaced creds file back from its backup. It returns
// the paths it could not clean up. When the backup cannot be restored the new creds stay in place and the old ones are
// kept: the backup is linked to <creds>.old (never over an existing file), and kept is that path; if that cannot be
// done the backup keeps its own name and kept is that.
func joinUndo(made []string, backup, credsPath string) (left []string, kept string) {
	for _, m := range made {
		if err := joinRemove(m); err != nil && !errors.Is(err, fs.ErrNotExist) {
			left = append(left, m)
		}
	}
	if backup == "" {
		if err := joinRemove(credsPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			left = append(left, credsPath)
		}
		return left, ""
	}
	if err := joinRename(backup, credsPath); err != nil {
		left = append(left, credsPath)
		kept = backup
		if old := credsPath + ".old"; os.Link(backup, old) == nil {
			os.Remove(backup)
			kept = old
		}
	}
	return left, kept
}

// undoFailure is the failure for a rollback that left paths behind: it names each by exact path and, when a replaced
// creds file could not be put back, where the old creds are kept.
func (s joinSvc) undoFailure(left []string, kept string) *cliframe.Error {
	shown := make([]string, len(left))
	for i, l := range left {
		shown[i] = s.shown(l)
	}
	note := ""
	if kept != "" {
		note = "; old creds kept at " + s.shown(kept)
	}
	return cliframe.Failf("could not finish and could not undo: %s left%s", strings.Join(shown, ", "), note).WithHint("mash doctor")
}
