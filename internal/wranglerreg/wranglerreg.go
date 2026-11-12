// Package wranglerreg is the static wrangler registry: a file that says which wrangler public key may countersign an
// issue request for which host and owner group (REQ-013, ADR 0104).
//
// It is for test accounts only. How a real wrangler's key comes to be registered for a host and an owner group is not
// decided (DEC-002 leaves it open), so this registry refuses to serve any account that is not flagged as a test
// account in the file, and any account the file does not name. The flag in the file is a declaration and nothing
// more: the operator makes a second one, `mashctl issue serve --test-account`, and the service starts only when both
// agree. It holds public keys only; nothing in it is secret,
// but it decides who may have a credential signed, so it is written 0600 and read only if no one else can write it.
package wranglerreg

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nats-io/nkeys"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctlkeys"
)

// FileName is the registry's name inside the key store directory.
const FileName = "wranglers.json"

// maxFile bounds how much of the file is read.
const maxFile = 1 << 20

// ErrNoRegistry is wrapped by every refusal to serve from the static registry.
var ErrNoRegistry = errors.New("no wrangler registry for this account: the static registry is for test accounts only; real wrangler registration is not decided yet")

// Entry registers one wrangler public key for one host and owner group.
type Entry struct {
	Host        string `json:"host"`
	Group       string `json:"group"`
	WranglerKey string `json:"wrangler_key"`
}

// File is the registry file.
type File struct {
	Account     string  `json:"account"`
	TestAccount bool    `json:"test_account"`
	Entries     []Entry `json:"entries"`
}

// Path is the registry file inside keyDir.
func Path(keyDir string) string { return filepath.Join(keyDir, FileName) }

// Load reads the registry at path. A missing file wraps os.ErrNotExist. The file is opened once, without following a
// symlink, and checked on the descriptor: it must be a regular file owned by the effective uid that no other user can
// write. Its directory must be a real directory (not a symlink) that the effective uid owns and no other user can write,
// the checks the key store makes for the directory of a file that decides who may be issued a credential.
func Load(path string) (*File, error) {
	if err := ctlkeys.CheckParentDir(filepath.Dir(path)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		return nil, fmt.Errorf("wrangler registry %s: %w", path, err)
	}
	f, err := config.OpenNoFollow(path, os.O_RDONLY, 0)
	if err != nil {
		if config.IsSymlinkRefusal(err) {
			return nil, fmt.Errorf("wrangler registry %s is a symlink: refusing it", path)
		}
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("wrangler registry %s: %w", path, err)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("wrangler registry %s is not a regular file", path)
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return nil, fmt.Errorf("wrangler registry %s has mode %04o: others can write it. Run: chmod 600 %s", path, fi.Mode().Perm(), path)
	}
	if err := ctlkeys.CheckOwned(fi, path, "wrangler registry"); err != nil {
		return nil, err
	}
	if fi.Size() > maxFile {
		return nil, fmt.Errorf("wrangler registry %s is too large", path)
	}
	b, err := io.ReadAll(io.LimitReader(f, maxFile))
	if err != nil {
		return nil, fmt.Errorf("wrangler registry %s: %w", path, err)
	}
	var r File
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("wrangler registry %s: %w", path, err)
	}
	if !nkeys.IsValidPublicAccountKey(r.Account) {
		return nil, fmt.Errorf("wrangler registry %s: \"account\" is not an account public key", path)
	}
	for i, e := range r.Entries {
		if err := e.validate(); err != nil {
			return nil, fmt.Errorf("wrangler registry %s: entry %d: %w", path, i+1, err)
		}
	}
	return &r, nil
}

func (e Entry) validate() error {
	if e.Host == "" || strings.ContainsAny(e.Host, " \t\r\n\x00\n") {
		return errors.New("host is empty or has whitespace")
	}
	if r, o, ok := strings.Cut(e.Group, "."); !ok || r == "" || o == "" || strings.Contains(o, ".") {
		return fmt.Errorf("group %q is not range.owner", e.Group)
	}
	if !nkeys.IsValidPublicUserKey(e.WranglerKey) {
		return fmt.Errorf("wrangler_key %q is not a NATS user public key", e.WranglerKey)
	}
	return nil
}

// Save writes the registry to path: a 0600 temp file in the same directory, fsync, rename over the old file, sync of
// the directory. The directory must exist.
func (f *File) Save(path string) error {
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return err
	}
	tmp := path + ".tmp-" + hex.EncodeToString(rnd[:])
	w, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create %s: %w", tmp, err)
	}
	_, werr := w.Write(b)
	if werr == nil {
		werr = w.Sync()
	}
	if cerr := w.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(tmp, path)
	}
	if werr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write %s: %w", path, werr)
	}
	return ctlkeys.SyncDir(filepath.Dir(path))
}

// Add registers key for host and group. A second registration of the same triple is a no-op; the same host and group
// with another key adds a second key (a wrangler key may be rotated without a gap).
func (f *File) Add(host, group, key string) error {
	e := Entry{Host: host, Group: group, WranglerKey: key}
	if err := e.validate(); err != nil {
		return err
	}
	if !slices.Contains(f.Entries, e) {
		f.Entries = append(f.Entries, e)
	}
	return nil
}

// Remove drops every entry for host and group and reports how many it dropped.
func (f *File) Remove(host, group string) int {
	n := len(f.Entries)
	f.Entries = slices.DeleteFunc(f.Entries, func(e Entry) bool { return e.Host == host && e.Group == group })
	return n - len(f.Entries)
}

// Static is the WranglerRegistry the issue service uses on a test account. It reads the file again at every lookup, so
// `mashctl wrangler remove` takes effect at once and a file that has become unreadable, or no longer a test registry
// for the account, registers no one (fail closed).
type Static struct{ path, account string }

func openFile(path, account string) (*File, error) {
	f, err := Load(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil, fmt.Errorf("%w (%s does not exist)", ErrNoRegistry, path)
	case err != nil:
		return nil, fmt.Errorf("%w: %v", ErrNoRegistry, err)
	case f.Account != account:
		return nil, fmt.Errorf("%w (%s names account %s, not %s)", ErrNoRegistry, path, f.Account, account)
	case !f.TestAccount:
		return nil, fmt.Errorf("%w (%s is not flagged test_account)", ErrNoRegistry, path)
	}
	return f, nil
}

// OpenStatic checks the registry at path for account and refuses unless it exists, names account and is flagged as a
// test account. Every refusal wraps ErrNoRegistry.
func OpenStatic(path, account string) (*Static, error) {
	if _, err := openFile(path, account); err != nil {
		return nil, err
	}
	return &Static{path: path, account: account}, nil
}

// Lookup returns the wrangler public keys registered for exactly host and group; none when the file no longer passes
// the checks of OpenStatic.
func (s *Static) Lookup(host, group string) []string {
	f, err := openFile(s.path, s.account)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range f.Entries {
		if e.Host == host && e.Group == group {
			out = append(out, e.WranglerKey)
		}
	}
	return out
}
