// Package ctlkeys is the key store of mashctl: the account signing seed and the per-zone scoped signing seeds, kept
// as 0600 files in a 0700 directory (ADR 0052). Seed text is never put in an error or a log line.
package ctlkeys

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
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
	// ErrNoAccountKey is returned by Account when account.nk is absent.
	ErrNoAccountKey = errors.New("mashctl: no account signing key: run `mashctl init`")
)

const (
	accountFile = "account.nk"
	zonesDir    = "zones"
	seedExt     = ".nk"
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

// checkDir refuses a symlinked, non-directory or group/other-accessible dir. exists is false for a missing one.
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
	return true, nil
}

func (a *Authority) zonesPath() string { return filepath.Join(a.dir, zonesDir) }

// checkDirs checks the key dir and the zones dir; with create it makes missing ones (0700).
func (a *Authority) checkDirs(create bool) error {
	for _, d := range []string{a.dir, a.zonesPath()} {
		ok, err := checkDir(d)
		if err != nil {
			return err
		}
		if ok || !create {
			continue
		}
		if err := os.MkdirAll(d, 0o700); err != nil {
			return fmt.Errorf("mashctl: create %s: %w", d, err)
		}
		if _, err := checkDir(d); err != nil {
			return err
		}
	}
	return nil
}

// readSeed reads and verifies the seed file at path. A missing file returns an error wrapping fs.ErrNotExist.
func readSeed(path string) (nkeys.KeyPair, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		return nil, fmt.Errorf("mashctl: seed file %s is a symlink: refusing it", path)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("mashctl: seed file %s is not a regular file", path)
	}
	if tooOpen(fi.Mode()) {
		return nil, fmt.Errorf("mashctl: seed file %s has mode %04o: it is readable by other users. Run: chmod 600 %s", path, fi.Mode().Perm(), path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("mashctl: read seed file %s: %w", path, err)
	}
	seed := []byte(strings.TrimSpace(string(b)))
	clear(b)
	defer clear(seed)
	bad := fmt.Errorf("mashctl: %s does not hold a valid account signing seed", path)
	kp, err := nkeys.FromSeed(seed)
	if err != nil {
		return nil, bad
	}
	pub, err := kp.PublicKey()
	if err != nil || !nkeys.IsValidPublicAccountKey(pub) {
		kp.Wipe()
		return nil, bad
	}
	return kp, nil
}

// createSeed makes a new account-type key and installs it at path without clobbering. If the file appears first
// (another process), that file is read and returned instead.
func createSeed(path string) (nkeys.KeyPair, bool, error) {
	nk, err := nkeys.CreateAccount()
	if err != nil {
		return nil, false, fmt.Errorf("mashctl: generate key: %w", err)
	}
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
			kp, err := readSeed(path)
			return kp, false, err
		}
		return nil, false, fmt.Errorf("mashctl: install %s: %w", path, err)
	}
	return nk, true, nil
}

// getOrCreate reads path, creating it first when create is set and it is absent.
func getOrCreate(path string, create bool) (nkeys.KeyPair, bool, error) {
	kp, err := readSeed(path)
	if err == nil {
		return kp, false, nil
	}
	if !errors.Is(err, fs.ErrNotExist) || !create {
		return nil, false, err
	}
	return createSeed(path)
}

// InitAccount creates the account signing key if absent. It never overwrites an existing seed.
func (a *Authority) InitAccount() (pub string, created bool, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.checkDirs(true); err != nil {
		return "", false, err
	}
	kp, created, err := getOrCreate(filepath.Join(a.dir, accountFile), true)
	if err != nil {
		return "", false, err
	}
	defer kp.Wipe()
	pub, err = kp.PublicKey()
	return pub, created, err
}

// Account returns the account signing key pair and its public key.
func (a *Authority) Account() (nkeys.KeyPair, string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, err := checkDir(a.dir); err != nil {
		return nil, "", err
	}
	kp, err := readSeed(filepath.Join(a.dir, accountFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, "", ErrNoAccountKey
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
	kp, _, err := getOrCreate(filepath.Join(a.zonesPath(), zone+seedExt), create)
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
