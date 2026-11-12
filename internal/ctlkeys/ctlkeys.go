// Package ctlkeys is the key store of mashctl: the account signing seed and the per-zone scoped signing seeds, kept
// as 0600 files in a 0700 directory (ADR 0052). Seed text is never put in an error or a log line.
package ctlkeys

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/config"
)

var (
	// ErrNoZoneKey is wrapped by ZoneKey when the zone has no key file and create is false.
	ErrNoZoneKey = errors.New("mashctl: no signing key for zone")
	// ErrNoOperatorKey is returned by Operator when operator.nk is absent.
	ErrNoOperatorKey = errors.New("mashctl: no operator key (operator.nk): it exists only for a server `mashctl init --local` stood up")
	// ErrNoSystemKey is returned by System when system.nk is absent.
	ErrNoSystemKey = errors.New("mashctl: no system account key (system.nk): it exists only for a server `mashctl init --local` stood up")
	// ErrNoAccountKey is returned by Account when account.nk is absent.
	ErrNoAccountKey = errors.New("mashctl: no account signing key: run `mashctl init`")
)

const (
	accountFile  = "account.nk"
	operatorFile = "operator.nk"
	systemFile   = "system.nk"
	zonesDir     = "zones"
	groupsDir    = "groups"
	seedExt      = ".nk"
)

// kind is a type of key the store holds: how to make one and how to recognise its public key.
type kind struct {
	what   string
	create func() (nkeys.KeyPair, error)
	valid  func(pub string) bool
}

var (
	accountKind  = kind{"account signing", nkeys.CreateAccount, nkeys.IsValidPublicAccountKey}
	operatorKind = kind{"operator", nkeys.CreateOperator, nkeys.IsValidPublicOperatorKey}
	systemKind   = kind{"system account", nkeys.CreateAccount, nkeys.IsValidPublicAccountKey}
)

// zoneRE is the shape of a zone name, identical to sap.ValidZone (zone-in-subject, ADR 0054; not on main yet): swap it for that
// when it lands. The shape also keeps path separators and dots out of file names.
var zoneRE = regexp.MustCompile(`^[a-z0-9-]+$`)

func validZone(z string) bool { return len(z) >= 1 && len(z) <= 32 && zoneRE.MatchString(z) }

// Authority is the key store rooted at one directory.
type Authority struct {
	dir string
	mu  sync.Mutex
}

// DirFor is the key directory: $MASH_KEYS_DIR when absolute (a relative one is an error), else <cfg.Dir>/keys.
func DirFor(cfg config.Config, env func(string) string) (string, error) {
	if d := env("MASH_KEYS_DIR"); d != "" {
		if !filepath.IsAbs(d) {
			return "", fmt.Errorf("mashctl: MASH_KEYS_DIR=%q is not an absolute path", d)
		}
		return filepath.Clean(d), nil
	}
	if cfg.Dir == "" {
		return "", errors.New("mashctl: cannot find a key directory: set MASH_KEYS_DIR, or HOME or XDG_CONFIG_HOME")
	}
	return filepath.Join(cfg.Dir, "keys"), nil
}

// Open opens the key store at dir. It creates nothing; a missing dir is an empty store.
func Open(dir string) (*Authority, error) {
	if dir == "" {
		return nil, errors.New("mashctl: empty key directory")
	}
	a := &Authority{dir: filepath.Clean(dir)}
	if _, err := checkDir(a.dir); err != nil {
		return nil, err
	}
	return a, nil
}

// checkDir refuses a symlinked, non-directory, group/other-accessible or foreign-owned dir. exists is false for a
// missing one. Only the final component is checked: a key directory path commonly runs through a legitimately
// symlinked ancestor (a home directory on another volume, /home -> /var/home), so refusing every symlinked
// ancestor would break real installs; the final directory is the one whose mode and owner decide who can reach the
// keys, and an ancestor another user controls cannot get past that directory's 0700 mode and owner.
func checkDir(d string) (exists bool, err error) {
	fi, err := os.Lstat(d)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("mashctl: key directory %s: %w", d, err)
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		return true, fmt.Errorf("mashctl: key directory %s is a symlink: refusing it", d)
	}
	if !fi.IsDir() {
		return true, fmt.Errorf("mashctl: key directory %s is not a directory", d)
	}
	if tooOpen(fi.Mode()) {
		return true, fmt.Errorf("mashctl: key directory %s has mode %04o: it is accessible to other users. Run: chmod 700 %s", d, fi.Mode().Perm(), d)
	}
	if err := ownerErr(fi, d, "key directory"); err != nil {
		return true, err
	}
	return true, nil
}

// geteuid is the effective uid files and directories must belong to; tests replace it.
var geteuid = os.Geteuid

// ownerErr refuses a file or directory that the effective uid does not own. It is a no-op where the platform
// reports no owner.
func ownerErr(fi fs.FileInfo, path, what string) error {
	if uid, ok := config.FileUID(fi); ok && int(uid) != geteuid() {
		return fmt.Errorf("mashctl: %s %s is owned by uid %d, not the effective uid %d: refusing it", what, path, uid, geteuid())
	}
	return nil
}

// CheckOwned refuses a file or directory (described by fi, named path, called what in the error) that the effective
// uid does not own: the owner check the key store applies to its own files, for a caller that holds a file of the same
// custody. It is a no-op where the platform reports no owner.
func CheckOwned(fi fs.FileInfo, path, what string) error { return ownerErr(fi, path, what) }

// CheckParentDir refuses a directory that is not a real directory (a symlink is refused), is not owned by the
// effective uid, or is writable by other users (group-writable is allowed: under the usual user-private-group umask 002 a config directory is 0775 and the group is the owner alone): a place where a creds file may be created without another user
// being able to swap or replace it. A missing directory is an error wrapping fs.ErrNotExist.
func CheckParentDir(d string) error {
	fi, err := os.Lstat(d)
	if err != nil {
		return err
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("directory %s is a symlink: refusing it", d)
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", d)
	}
	if fi.Mode().Perm()&0o002 != 0 {
		return fmt.Errorf("directory %s has mode %04o: it is writable by every user. Run: chmod o-w %s", d, fi.Mode().Perm(), d)
	}
	if err := ownerErr(fi, d, "directory"); err != nil {
		return err
	}
	return nil
}

func (a *Authority) zonesPath() string { return filepath.Join(a.dir, zonesDir) }

// checkDirs checks the key dir and the zones dir; with create it makes missing ones (0700).
func (a *Authority) checkDirs(create bool) error { return a.checkDirsIn(a.zonesPath(), create) }

// checkDirsIn checks the key dir and the sub directory sub (one of the key dir's own); with create it makes missing
// ones (0700).
func (a *Authority) checkDirsIn(sub string, create bool) error {
	for _, d := range []string{a.dir, sub} {
		ok, err := checkDir(d)
		if err != nil {
			return err
		}
		if ok || !create {
			continue
		}
		// Remember which ancestors are missing so each new one can be made durable.
		var made []string
		for p := d; ; p = filepath.Dir(p) {
			if _, err := os.Lstat(p); err == nil || filepath.Dir(p) == p {
				break
			}
			made = append(made, p)
		}
		if err := os.MkdirAll(d, 0o700); err != nil {
			return fmt.Errorf("mashctl: create %s: %w", d, err)
		}
		for _, m := range made {
			for _, p := range []string{m, filepath.Dir(m)} {
				if err := syncDirHook(p); err != nil {
					return fmt.Errorf("mashctl: sync %s: %w", p, err)
				}
			}
		}
		if _, err := checkDir(d); err != nil {
			return err
		}
	}
	return nil
}

// syncDirHook is syncDir, replaceable by tests to inject failures.
var syncDirHook = syncDir

// maxSeedFile bounds how much of a seed file is read: a seed is about 58 bytes.
const maxSeedFile = 4096

// readSeed reads and verifies the seed file at path. A missing file returns an error wrapping fs.ErrNotExist. The
// file is opened once with O_NOFOLLOW and every check runs on that descriptor, which is then read: there is no
// window between checking the path and reading it.
func readSeed(path string, k kind) (nkeys.KeyPair, error) {
	f, err := config.OpenNoFollow(path, os.O_RDONLY, 0)
	if err != nil {
		if config.IsSymlinkRefusal(err) {
			return nil, fmt.Errorf("mashctl: seed file %s is a symlink: refusing it", path)
		}
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("mashctl: read seed file %s: %w", path, err)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("mashctl: seed file %s is not a regular file", path)
	}
	if tooOpen(fi.Mode()) {
		return nil, fmt.Errorf("mashctl: seed file %s has mode %04o: it is readable by other users. Run: chmod 600 %s", path, fi.Mode().Perm(), path)
	}
	if err := ownerErr(fi, path, "seed file"); err != nil {
		return nil, err
	}
	bad := fmt.Errorf("mashctl: %s does not hold a valid %s seed", path, k.what)
	if fi.Size() > maxSeedFile {
		return nil, bad
	}
	// One buffer, sized up front, so no grown copy of the seed is left behind; it is wiped whole on return.
	buf := make([]byte, fi.Size()+1)
	defer clear(buf)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("mashctl: read seed file %s: %w", path, err)
	}
	seed := bytes.TrimSpace(buf[:n])
	kp, err := nkeys.FromSeed(seed)
	if err != nil {
		return nil, bad
	}
	pub, err := kp.PublicKey()
	if err != nil || !k.valid(pub) {
		kp.Wipe()
		return nil, bad
	}
	return kp, nil
}

// createSeed makes a new key and installs it at path without clobbering (see installSeed).
func createSeed(path string, k kind) (nkeys.KeyPair, bool, error) {
	nk, err := k.create()
	if err != nil {
		return nil, false, fmt.Errorf("mashctl: generate key: %w", err)
	}
	return installSeed(path, k, nk)
}

// installSeed writes nk's seed to path with the custody rules of createSeed: a 0600 temp file in the same directory
// (O_EXCL), fsync, a hard link that cannot replace an existing file, then a sync of the directory. If the file appears
// first, that file is read and returned with created false (the caller compares it with nk).
func installSeed(path string, k kind, nk nkeys.KeyPair) (nkeys.KeyPair, bool, error) {
	raw, err := nk.Seed()
	if err != nil {
		return nil, false, errors.New("mashctl: generate key: seed unavailable")
	}
	// Seed returns the key's own buffer; copy it so wiping the copy leaves nk usable.
	seed := append(make([]byte, 0, len(raw)+1), raw...)
	defer clear(seed)
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return nil, false, err
	}
	tmp := path + ".tmp-" + hex.EncodeToString(rnd[:])
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, false, fmt.Errorf("mashctl: create %s: %w", tmp, err)
	}
	defer os.Remove(tmp)
	_, werr := f.Write(append(seed, '\n'))
	if werr == nil {
		werr = f.Sync()
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return nil, false, fmt.Errorf("mashctl: write %s: %w", tmp, werr)
	}
	if err := os.Link(tmp, path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			nk.Wipe()
			kp, err := readSeed(path, k)
			return kp, false, err
		}
		return nil, false, fmt.Errorf("mashctl: install %s: %w", path, err)
	}
	// Drop the temp name, then make the new entry durable before reporting success.
	_ = os.Remove(tmp)
	if err := syncDirHook(filepath.Dir(path)); err != nil {
		nk.Wipe()
		return nil, false, fmt.Errorf("mashctl: sync %s: %w", filepath.Dir(path), err)
	}
	return nk, true, nil
}

// getOrCreate reads path, creating it first when create is set and it is absent.
func getOrCreate(path string, k kind, create bool) (nkeys.KeyPair, bool, error) {
	kp, err := readSeed(path, k)
	if err == nil {
		return kp, false, nil
	}
	if !errors.Is(err, fs.ErrNotExist) || !create {
		return nil, false, err
	}
	return createSeed(path, k)
}

// initKind creates the key file name of kind k if absent (never overwriting) and reports its public key.
func (a *Authority) initKind(name string, k kind) (pub string, created bool, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.checkDirs(true); err != nil {
		return "", false, err
	}
	kp, created, err := getOrCreate(filepath.Join(a.dir, name), k, true)
	if err != nil {
		return "", false, err
	}
	defer kp.Wipe()
	pub, err = kp.PublicKey()
	return pub, created, err
}

// getKind reads the key file name of kind k; missing is returned when the file is absent.
func (a *Authority) getKind(name string, k kind, missing error) (nkeys.KeyPair, string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, err := checkDir(a.dir); err != nil {
		return nil, "", err
	}
	kp, err := readSeed(filepath.Join(a.dir, name), k)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", missing
	}
	if err != nil {
		return nil, "", err
	}
	pub, err := kp.PublicKey()
	if err != nil {
		kp.Wipe()
		return nil, "", err
	}
	return kp, pub, nil
}

// InitOperator creates the operator key (operator.nk) if absent; used by `mashctl init --local`. It never overwrites.
func (a *Authority) InitOperator() (pub string, created bool, err error) {
	return a.initKind(operatorFile, operatorKind)
}

// Operator returns the operator key pair and its public key.
func (a *Authority) Operator() (nkeys.KeyPair, string, error) {
	return a.getKind(operatorFile, operatorKind, ErrNoOperatorKey)
}

// InitSystem creates the system account's identity key (system.nk) if absent. It never overwrites.
func (a *Authority) InitSystem() (pub string, created bool, err error) {
	return a.initKind(systemFile, systemKind)
}

// System returns the system account's identity key pair and its public key.
func (a *Authority) System() (nkeys.KeyPair, string, error) {
	return a.getKind(systemFile, systemKind, ErrNoSystemKey)
}

// InitAccount creates the account signing key if absent. It never overwrites an existing seed.
func (a *Authority) InitAccount() (pub string, created bool, err error) {
	return a.initKind(accountFile, accountKind)
}

// Account returns the account signing key pair and its public key.
func (a *Authority) Account() (nkeys.KeyPair, string, error) {
	return a.getKind(accountFile, accountKind, ErrNoAccountKey)
}

// ZoneKey returns zone's scoped signing key; with create it generates and writes one if absent. It never overwrites.
func (a *Authority) ZoneKey(zone string, create bool) (nkeys.KeyPair, error) {
	if !validZone(zone) {
		return nil, fmt.Errorf("mashctl: invalid zone name %q: use lower-case letters, digits and hyphens, starting with a letter, up to 32 characters", zone)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.checkDirs(create); err != nil {
		return nil, err
	}
	kp, _, err := getOrCreate(filepath.Join(a.zonesPath(), zone+seedExt), accountKind, create)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w %q", ErrNoZoneKey, zone)
	}
	return kp, err
}

// ZonePublic is the public key of an existing zone key.
func (a *Authority) ZonePublic(zone string) (string, error) {
	kp, err := a.ZoneKey(zone, false)
	if err != nil {
		return "", err
	}
	defer kp.Wipe()
	return kp.PublicKey()
}

// Zones lists the zones that have a key file, sorted.
func (a *Authority) Zones() ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.checkDirs(false); err != nil {
		return nil, err
	}
	ents, err := os.ReadDir(a.zonesPath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("mashctl: list zones: %w", err)
	}
	var out []string
	for _, e := range ents {
		if z, ok := strings.CutSuffix(e.Name(), seedExt); ok && validZone(z) && !e.IsDir() {
			out = append(out, z)
		}
	}
	sort.Strings(out)
	return out, nil
}

// ErrNoGroupKey is wrapped by GroupKey when the owner group has no key file and create is false.
var ErrNoGroupKey = errors.New("mashctl: no signing key for owner group")

// groupRE is one part of an owner group name "range.owner": the shape of a zone name.
var groupRE = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

// validGroup reports whether g is "range.owner" with both parts of a zone name's shape. The shape keeps path
// separators out of file names; the single dot cannot make a name that climbs.
func validGroup(g string) bool {
	r, o, ok := strings.Cut(g, ".")
	return ok && groupRE.MatchString(r) && groupRE.MatchString(o)
}

func (a *Authority) groupsPath() string { return filepath.Join(a.dir, groupsDir) }

// GroupKey returns the scoped signing key of owner group "range.owner", kept in groups/<range>.<owner>.nk beside the
// zones directory under the same custody rules; with create it generates and writes one if absent. It never
// overwrites. Zone keys (ZoneKey) are the policy file's; group keys are the list set's.
func (a *Authority) GroupKey(group string, create bool) (nkeys.KeyPair, error) {
	if !validGroup(group) {
		return nil, fmt.Errorf("mashctl: invalid owner group %q: want range.owner, each part lower-case letters, digits and hyphens, up to 32 characters", group)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.checkDirsIn(a.groupsPath(), create); err != nil {
		return nil, err
	}
	kp, _, err := getOrCreate(filepath.Join(a.groupsPath(), group+seedExt), accountKind, create)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w %q", ErrNoGroupKey, group)
	}
	return kp, err
}

// ErrDifferentKey is wrapped by ImportOperator and ImportAccount when the store already holds another key.
var ErrDifferentKey = errors.New("mashctl: the key store already holds a different key")

// ParseOperatorSeed reads the operator seed file at path (0600, a regular file, an operator-prefix seed) and returns
// the key pair and its public key. The seed is never put in an error.
func ParseOperatorSeed(path string) (nkeys.KeyPair, string, error) {
	return parseSeedFile(path, operatorKind)
}

// ParseAccountSeed is ParseOperatorSeed for an account identity seed (account prefix).
func ParseAccountSeed(path string) (nkeys.KeyPair, string, error) {
	return parseSeedFile(path, accountKind)
}

func parseSeedFile(path string, k kind) (nkeys.KeyPair, string, error) {
	kp, err := readSeed(path, k)
	if err != nil {
		return nil, "", err
	}
	pub, err := kp.PublicKey()
	if err != nil {
		kp.Wipe()
		return nil, "", err
	}
	return kp, pub, nil
}

// ImportOperator copies the operator seed file at path into the store as operator.nk (0600, no clobber, fsync). The
// same key already stored is not an error (created is false); a different one is ErrDifferentKey and nothing changes.
func (a *Authority) ImportOperator(path string) (pub string, created bool, err error) {
	return a.importKind(path, operatorFile, operatorKind)
}

// ImportAccount is ImportOperator for the account identity seed (account.nk).
func (a *Authority) ImportAccount(path string) (pub string, created bool, err error) {
	return a.importKind(path, accountFile, accountKind)
}

func (a *Authority) importKind(src, name string, k kind) (pub string, created bool, err error) {
	nk, pub, err := parseSeedFile(src, k)
	if err != nil {
		return "", false, err
	}
	defer nk.Wipe()
	a.mu.Lock()
	defer a.mu.Unlock()
	dst := filepath.Join(a.dir, name)
	same := func(have nkeys.KeyPair) error {
		defer have.Wipe()
		hp, err := have.PublicKey()
		if err != nil {
			return err
		}
		if hp != pub {
			return fmt.Errorf("%w: %s holds %s, not the %s key %s you gave; refusing to replace it (move it aside to adopt with another key)", ErrDifferentKey, dst, hp, k.what, pub)
		}
		return nil
	}
	if have, err := readSeed(dst, k); err == nil {
		return pub, false, same(have)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", false, err
	}
	if err := a.checkDirs(true); err != nil {
		return "", false, err
	}
	have, created, err := installSeed(dst, k, nk)
	if err != nil {
		return "", false, err
	}
	if !created {
		return pub, false, same(have)
	}
	have.Wipe()
	return pub, true, nil
}

// InstallFile writes data to path with the custody rules of the key files: a 0600 temp file in the same directory
// (O_EXCL), fsync, a hard link that cannot replace an existing file, a sync of the directory. An existing path is left
// untouched and reported with created false; the caller compares its content. The directory must exist.
func InstallFile(path string, data []byte) (created bool, err error) {
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return false, err
	}
	tmp := path + ".tmp-" + hex.EncodeToString(rnd[:])
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return false, fmt.Errorf("mashctl: create %s: %w", tmp, err)
	}
	defer os.Remove(tmp)
	_, werr := f.Write(data)
	if werr == nil {
		werr = f.Sync()
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return false, fmt.Errorf("mashctl: write %s: %w", tmp, werr)
	}
	if err := os.Link(tmp, path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return false, nil
		}
		return false, fmt.Errorf("mashctl: install %s: %w", path, err)
	}
	_ = os.Remove(tmp)
	if err := syncDirHook(filepath.Dir(path)); err != nil {
		return true, fmt.Errorf("mashctl: sync %s: %w", filepath.Dir(path), err)
	}
	return true, nil
}

// SyncDir fsyncs the directory at path so a new or renamed entry in it survives power loss.
func SyncDir(path string) error { return syncDirHook(path) }

// SetSyncDirHook replaces the directory sync that InstallFile and SyncDir use and returns the function that
// restores it. It exists so tests in other packages can inject a failure.
func SetSyncDirHook(h func(string) error) (restore func()) {
	old := syncDirHook
	syncDirHook = h
	return func() { syncDirHook = old }
}

// OperatorJWTFile is the name, in the key directory, of the copy of the operator JWT that tells mashctl which keys
// the operator accepts as issuers of an account JWT.
const OperatorJWTFile = "operator.jwt"

// ErrDifferentOperatorJWT is wrapped by InstallOperatorJWT when the key directory already holds another operator JWT.
var ErrDifferentOperatorJWT = errors.New("mashctl: the key directory already holds a different operator.jwt")

// InstallOperatorJWT stores token as <keydir>/operator.jwt (0600, never replacing a file). With a file already in
// place, created is false and the error says so when its content differs. The token is not validated here.
func (a *Authority) InstallOperatorJWT(token string) (created bool, err error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return false, errors.New("mashctl: empty operator JWT")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.checkDirs(true); err != nil {
		return false, err
	}
	path := filepath.Join(a.dir, OperatorJWTFile)
	created, err = InstallFile(path, []byte(token+"\n"))
	if err != nil || created {
		return created, err
	}
	have, rerr := os.ReadFile(path)
	if rerr != nil {
		return false, rerr
	}
	if strings.TrimSpace(string(have)) != token {
		return false, fmt.Errorf("%w (%s): refusing to replace it", ErrDifferentOperatorJWT, path)
	}
	return false, nil
}

// OperatorJWT returns the operator JWT stored in the key directory, or "" when there is none.
func (a *Authority) OperatorJWT() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	path := filepath.Join(a.dir, OperatorJWTFile)
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("mashctl: read %s: %w", path, err)
	}
	return strings.TrimSpace(string(b)), nil
}
