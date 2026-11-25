// Package ctlkeys is the key store of mashctl: the account signing seed (and the operator and system keys of a server `mashctl init --local` stood up), kept
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

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/config"
)

var (
	// ErrNoOperatorKey is returned by Operator when operator.nk is absent.
	ErrNoOperatorKey = errors.New("mashctl: no operator key (operator.nk): it exists only for a server `mashctl init --local` stood up")
	// ErrNoSystemKey is returned by System when system.nk is absent.
	ErrNoSystemKey = errors.New("mashctl: no system account key (system.nk): it exists only for a server `mashctl init --local` stood up")
	// ErrNoAccountKey is returned by Account when the account identity key (account.nk) is absent.
	ErrNoAccountKey = errors.New("mashctl: no account identity key (account.nk): run `mashctl init`")
	// ErrNoSigningKey is returned by Signing when signing.nk is absent: the account has no separate signing key
	// configured, and endpoint users are signed by the account identity key.
	ErrNoSigningKey = errors.New("mashctl: no account signing key configured (signing.nk)")
	// ErrSigningKeyLost is returned by Signing when the store records a configured signing key (signing.pub) but
	// signing.nk is missing, unreadable or another key. The identity key must not stand in for it: a strict server
	// rejects what it signs.
	ErrSigningKeyLost = errors.New("mashctl: the account signing key was configured but signing.nk is missing, unreadable or not that key")
)

// Every signing refusal below is an ErrSigningKeyLost and renders through cliframe as `error: MSG` and `hint: HINT`
// (each has an As method that gives the *cliframe.Error, so cliframe.Report and the commands' own wrapping find it).
// Its Error text is the message alone. The long public keys it carries are fields for callers and tests, not text.

// signingFrame is the *cliframe.Error a signing refusal presents.
func signingFrame(msg, hint string) *cliframe.Error { return cliframe.Failf("%s", msg).WithHint(hint) }

// asFrame is the As method of every signing refusal.
func asFrame(target any, f *cliframe.Error) bool {
	if t, ok := target.(**cliframe.Error); ok {
		*t = f
		return true
	}
	return false
}

// SigningLostBothError is what Signing returns when config.json records a configured signing key (signing_key_pub)
// and the key store holds neither signing.nk nor signing.pub. It is an ErrSigningKeyLost: the identity key must not
// stand in for the signing key.
type SigningLostBothError struct{ Pub, Dir string }

func (e *SigningLostBothError) frame() *cliframe.Error {
	return signingFrame(fmt.Sprintf("signing.nk and signing.pub are missing from %s", e.Dir), "mashctl init --config <file>")
}
func (e *SigningLostBothError) Error() string { return e.frame().Msg }

// Is makes the error an ErrSigningKeyLost.
func (e *SigningLostBothError) Is(target error) bool { return target == ErrSigningKeyLost }

// As gives the error's cliframe form.
func (e *SigningLostBothError) As(target any) bool { return asFrame(target, e.frame()) }

// SigningPubMissingError is what Signing returns when signing.nk is in the key store and signing.pub is gone. Pub is
// the key signing.nk holds. It is an ErrSigningKeyLost.
type SigningPubMissingError struct{ Pub, Dir string }

func (e *SigningPubMissingError) frame() *cliframe.Error {
	return signingFrame(fmt.Sprintf("signing.pub is missing from %s", e.Dir), "mashctl init --config <file>")
}
func (e *SigningPubMissingError) Error() string { return e.frame().Msg }

// Is makes the error an ErrSigningKeyLost.
func (e *SigningPubMissingError) Is(target error) bool { return target == ErrSigningKeyLost }

// As gives the error's cliframe form.
func (e *SigningPubMissingError) As(target any) bool { return asFrame(target, e.frame()) }

// SigningNkMissingError is what Signing returns when signing.pub records a signing key and signing.nk is gone.
// Recorded is the key signing.pub records. It is an ErrSigningKeyLost.
type SigningNkMissingError struct{ Recorded, Dir string }

func (e *SigningNkMissingError) frame() *cliframe.Error {
	return signingFrame(fmt.Sprintf("signing.nk is missing from %s", e.Dir), "mashctl init --config <file>")
}
func (e *SigningNkMissingError) Error() string { return e.frame().Msg }

// Is makes the error an ErrSigningKeyLost.
func (e *SigningNkMissingError) Is(target error) bool { return target == ErrSigningKeyLost }

// As gives the error's cliframe form.
func (e *SigningNkMissingError) As(target any) bool { return asFrame(target, e.frame()) }

// SigningRecordMismatchError is what Signing returns when signing.pub records another key than config.json's
// signing_key_pub. It is an ErrSigningKeyLost.
type SigningRecordMismatchError struct{ Expected, Recorded, Dir string }

func (e *SigningRecordMismatchError) frame() *cliframe.Error {
	return signingFrame(fmt.Sprintf("signing.pub in %s does not match config.json", e.Dir), "mashctl doctor")
}
func (e *SigningRecordMismatchError) Error() string { return e.frame().Msg }

// Is makes the error an ErrSigningKeyLost.
func (e *SigningRecordMismatchError) Is(target error) bool { return target == ErrSigningKeyLost }

// As gives the error's cliframe form.
func (e *SigningRecordMismatchError) As(target any) bool { return asFrame(target, e.frame()) }

// SigningKeyMismatchError is what Signing returns when signing.nk holds another key than the one wanted. Want is the
// key signing.pub records, or, when signing.pub is gone, the one config.json records; WantFrom names which. It is an
// ErrSigningKeyLost.
type SigningKeyMismatchError struct{ Held, Want, WantFrom, Dir string }

func (e *SigningKeyMismatchError) frame() *cliframe.Error {
	return signingFrame(fmt.Sprintf("signing.nk in %s does not match signing.pub", e.Dir), "mashctl doctor")
}
func (e *SigningKeyMismatchError) Error() string { return e.frame().Msg }

// Is makes the error an ErrSigningKeyLost.
func (e *SigningKeyMismatchError) Is(target error) bool { return target == ErrSigningKeyLost }

// As gives the error's cliframe form.
func (e *SigningKeyMismatchError) As(target any) bool { return asFrame(target, e.frame()) }

// SigningUnreadableError is what Signing returns when signing.nk is there and cannot be read (its mode, owner or
// type is refused, or reading it failed). Err is the cause. It is an ErrSigningKeyLost.
type SigningUnreadableError struct {
	Dir string
	Err error
}

func (e *SigningUnreadableError) frame() *cliframe.Error {
	return signingFrame(fmt.Sprintf("signing.nk in %s cannot be read", e.Dir), "mashctl doctor")
}
func (e *SigningUnreadableError) Error() string { return e.frame().Msg }
func (e *SigningUnreadableError) Unwrap() error { return e.Err }

// Is makes the error an ErrSigningKeyLost.
func (e *SigningUnreadableError) Is(target error) bool { return target == ErrSigningKeyLost }

// As gives the error's cliframe form.
func (e *SigningUnreadableError) As(target any) bool { return asFrame(target, e.frame()) }

// SigningInvalidError is what Signing returns when signing.nk holds no valid signing key. Err is the cause. It is an
// ErrSigningKeyLost.
type SigningInvalidError struct {
	Dir string
	Err error
}

func (e *SigningInvalidError) frame() *cliframe.Error {
	return signingFrame(fmt.Sprintf("signing.nk in %s is not a valid key", e.Dir), "mashctl doctor")
}
func (e *SigningInvalidError) Error() string { return e.frame().Msg }
func (e *SigningInvalidError) Unwrap() error { return e.Err }

// Is makes the error an ErrSigningKeyLost.
func (e *SigningInvalidError) Is(target error) bool { return target == ErrSigningKeyLost }

// As gives the error's cliframe form.
func (e *SigningInvalidError) As(target any) bool { return asFrame(target, e.frame()) }

const (
	accountFile  = "account.nk"
	signingFile  = "signing.nk"
	signingRec   = "signing.pub"
	operatorFile = "operator.nk"
	systemFile   = "system.nk"
	groupsDir    = "groups"
	seedExt      = ".nk"
)

// kind is a type of key the store holds: how to make one and how to recognise its public key.
type kind struct {
	what   string
	field  string // the setup-file key that names this seed, "" when none does
	create func() (nkeys.KeyPair, error)
	valid  func(pub string) bool
}

var (
	accountKind  = kind{"account identity", "account_key", nkeys.CreateAccount, nkeys.IsValidPublicAccountKey}
	operatorKind = kind{"operator", "operator_key", nkeys.CreateOperator, nkeys.IsValidPublicOperatorKey}
	systemKind   = kind{"system account", "", nkeys.CreateAccount, nkeys.IsValidPublicAccountKey}
	signingKind  = kind{"account signing", "signing_key", nkeys.CreateAccount, nkeys.IsValidPublicAccountKey}
)

// Authority is the key store rooted at one directory.
type Authority struct {
	dir string
	mu  sync.Mutex
	// expectSigning is the public key config.json says was configured as signing_key ("" when it says none). It is
	// fixed at Open, so a caller cannot forget it.
	expectSigning string
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
//
// expectSigningPub is config.json's signing_key_pub, the public key of the signing key mashctl init was given ("" when
// the config records none). When it is set, Signing refuses, as ErrSigningKeyLost, unless signing.nk and signing.pub
// both hold exactly that key: the identity key never stands in for a signing key that was configured.
func Open(dir, expectSigningPub string) (*Authority, error) {
	if dir == "" {
		return nil, errors.New("mashctl: empty key directory")
	}
	if expectSigningPub != "" && !signingKind.valid(expectSigningPub) {
		return nil, fmt.Errorf("mashctl: signing_key_pub in config.json (%q) is not an account public key: fix it or restore it", expectSigningPub)
	}
	a := &Authority{dir: filepath.Clean(dir), expectSigning: expectSigningPub}
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
// effective uid, or is writable by other users or by its group (unless the group-writable directory is sticky): a place where a creds file may be created without another user
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
	if fi.Mode().Perm()&0o020 != 0 && fi.Mode()&fs.ModeSticky == 0 {
		return fmt.Errorf("directory %s has mode %04o: its group can write it and could replace the files in it. Run: chmod g-w %s", d, fi.Mode().Perm(), d)
	}
	if err := ownerErr(fi, d, "directory"); err != nil {
		return err
	}
	return nil
}

// CheckStrictParentDir is the rule for a directory that holds a file deciding who may be issued a credential (the
// requester registry, its lock file, the issue service's list set): a real directory (a symlink is refused) that the
// effective uid owns and that no group or other user can write (mode & 0022 == 0). Unlike CheckParentDir it refuses a
// group-writable directory too, because another member of the group could replace the file by rename. A missing
// directory is an error wrapping fs.ErrNotExist.
func CheckStrictParentDir(d string) error {
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
	if fi.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("directory %s has mode %04o: a group or other user can write it and could replace the files in it. Run: chmod go-w %s", d, fi.Mode().Perm(), d)
	}
	return ownerErr(fi, d, "directory")
}

// checkDirs checks the key dir; with create it makes it if missing (0700). A zones directory left by an older release
// is not read, checked or removed: its seeds are the operator's to keep or destroy (ADR 0091).
func (a *Authority) checkDirs(create bool) error { return a.checkDirsIn(create) }

// checkDirsIn checks the key dir and the sub directories subs (the key dir's own); with create it makes missing
// ones (0700).
func (a *Authority) checkDirsIn(create bool, subs ...string) error {
	for _, d := range append([]string{a.dir}, subs...) {
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
	bad := error(&SeedContentError{msg: fmt.Sprintf("mashctl: %s does not hold a valid %s seed", path, k.what)})
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
		if found := seedKindFound(seed); found != "" {
			want := "an account seed (SA)"
			if k.field == "operator_key" {
				want = "an operator seed (SO)"
			}
			who := "this file"
			if k.field != "" {
				who = k.field
			}
			bad = &SeedContentError{msg: fmt.Sprintf("%v: it holds %s; %s needs %s", bad, found, who, want)}
		}
		return nil, bad
	}
	return kp, nil
}

// SeedContentError is a seed file that was read but does not hold the seed it should: unlike a mode or ownership
// refusal, the file's content is what is wrong.
type SeedContentError struct{ msg string }

func (e *SeedContentError) Error() string { return e.msg }

// seedKindFound words the kind of a valid seed of another prefix ("an operator seed (SO)"), "" when seed is not one.
func seedKindFound(seed []byte) string {
	prefix, raw, err := nkeys.DecodeSeed(seed)
	if err != nil {
		return ""
	}
	clear(raw)
	switch prefix {
	case nkeys.PrefixByteOperator:
		return "an operator seed (SO)"
	case nkeys.PrefixByteAccount:
		return "an account seed (SA)"
	case nkeys.PrefixByteUser:
		return "a user seed (SU)"
	case nkeys.PrefixByteServer:
		return "a server seed (SN)"
	case nkeys.PrefixByteCluster:
		return "a cluster seed (SC)"
	}
	return ""
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

// Account returns the account identity key pair (account.nk) and its public key.
func (a *Authority) Account() (nkeys.KeyPair, string, error) {
	return a.getKind(accountFile, accountKind, ErrNoAccountKey)
}

// Signing returns the account SIGNING key pair (signing.nk) and its public key: the key that signs endpoint users
// when the operator requires signing keys (strict_signing_key_usage). It is ErrNoSigningKey when none was imported;
// Account is then the signer.
//
// When the store holds the record of a configured signing key (signing.pub, written by ImportSigning) and signing.nk
// is missing, unreadable or another key, it is ErrSigningKeyLost, never ErrNoSigningKey.
func (a *Authority) Signing() (nkeys.KeyPair, string, error) {
	kp, pub, err := a.getKind(signingFile, signingKind, ErrNoSigningKey)
	rec, rerr := a.SigningRecord()
	if rerr != nil {
		if kp != nil {
			kp.Wipe()
		}
		return nil, "", rerr
	}
	exp := a.expectSigning
	switch {
	case exp != "" && rec == "" && errors.Is(err, ErrNoSigningKey):
		return nil, "", &SigningLostBothError{Pub: exp, Dir: a.dir}
	case exp != "" && rec == "" && err == nil:
		// signing.nk is here and signing.pub is gone: the record is part of the configured state, so it is not trusted
		// until init (with the same signing_key) writes it again.
		kp.Wipe()
		if pub != exp {
			return nil, "", &SigningKeyMismatchError{Held: pub, Want: exp, WantFrom: "config.json (signing_key_pub)", Dir: a.dir}
		}
		return nil, "", &SigningPubMissingError{Pub: pub, Dir: a.dir}
	case exp != "" && rec != "" && rec != exp:
		if kp != nil {
			kp.Wipe()
		}
		return nil, "", &SigningRecordMismatchError{Expected: exp, Recorded: rec, Dir: a.dir}
	case (exp != "" || rec != "") && err != nil && !errors.Is(err, ErrNoSigningKey):
		var sc *SeedContentError
		if errors.As(err, &sc) {
			return nil, "", &SigningInvalidError{Dir: a.dir, Err: err}
		}
		return nil, "", &SigningUnreadableError{Dir: a.dir, Err: err}
	case rec == "":
		return kp, pub, err
	case err != nil:
		// signing.nk simply missing while signing.pub records the key.
		return nil, "", &SigningNkMissingError{Recorded: rec, Dir: a.dir}
	case pub != rec:
		kp.Wipe()
		return nil, "", &SigningKeyMismatchError{Held: pub, Want: rec, WantFrom: "signing.pub", Dir: a.dir}
	}
	return kp, pub, nil
}

// SigningRecord is the public key recorded in signing.pub by ImportSigning, "" when there is no record.
func (a *Authority) SigningRecord() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.signingRecordLocked()
}

func (a *Authority) signingRecordLocked() (string, error) {
	path := filepath.Join(a.dir, signingRec)
	f, err := config.OpenNoFollow(path, os.O_RDONLY, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("mashctl: read %s: %w", path, err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > 256 {
		return "", fmt.Errorf("mashctl: %s is not a signing key record: remove it or restore it", path)
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return "", fmt.Errorf("mashctl: read %s: %w", path, err)
	}
	pub := strings.TrimSpace(string(b))
	if !signingKind.valid(pub) {
		return "", fmt.Errorf("mashctl: %s does not hold an account public key: remove it or restore it", path)
	}
	return pub, nil
}

// ErrNoGroupKey is wrapped by GroupKey when the owner group has no key file and create is false.
var ErrNoGroupKey = errors.New("mashctl: no signing key for owner group")

// groupRE is one part of an owner group name "owner.group": lower-case letters, digits and hyphens.
var groupRE = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

// validGroup reports whether g is "owner.group" with both parts of that shape. The shape keeps path
// separators out of file names; the single dot cannot make a name that climbs.
func validGroup(g string) bool {
	r, o, ok := strings.Cut(g, ".")
	return ok && groupRE.MatchString(r) && groupRE.MatchString(o)
}

func (a *Authority) groupsPath() string { return filepath.Join(a.dir, groupsDir) }

// GroupKey returns the scoped signing key of owner group "owner.group", kept in groups/<owner>.<group>.nk under the same custody rules as the other
// keys; with create it generates and writes one if absent. It never overwrites.
func (a *Authority) GroupKey(group string, create bool) (nkeys.KeyPair, error) {
	if !validGroup(group) {
		return nil, fmt.Errorf("mashctl: invalid owner group %q: want two parts joined by a dot, each lower-case letters, digits and hyphens, up to 32 characters", group)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.checkDirsIn(create, a.groupsPath()); err != nil {
		return nil, err
	}
	kp, _, err := getOrCreate(filepath.Join(a.groupsPath(), group+seedExt), accountKind, create)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w %q", ErrNoGroupKey, group)
	}
	return kp, err
}

// Groups lists the owner groups that have a key file, sorted.
func (a *Authority) Groups() ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.checkDirsIn(false, a.groupsPath()); err != nil {
		return nil, err
	}
	ents, err := os.ReadDir(a.groupsPath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("mashctl: list owner groups: %w", err)
	}
	var out []string
	for _, e := range ents {
		if g, ok := strings.CutSuffix(e.Name(), seedExt); ok && validGroup(g) && !e.IsDir() {
			out = append(out, g)
		}
	}
	sort.Strings(out)
	return out, nil
}

// GroupPublics maps every owner group with a key file to the public key of that key. Each key pair is wiped after its
// public key is read; no seed leaves this function.
func (a *Authority) GroupPublics() (map[string]string, error) {
	groups, err := a.Groups()
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(groups))
	for _, g := range groups {
		kp, err := a.GroupKey(g, false)
		if err != nil {
			return nil, err
		}
		pub, err := kp.PublicKey()
		kp.Wipe()
		if err != nil {
			return nil, err
		}
		out[g] = pub
	}
	return out, nil
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

// ParseSigningSeed is ParseAccountSeed for the account signing seed (an account-prefix seed).
func ParseSigningSeed(path string) (nkeys.KeyPair, string, error) {
	return parseSeedFile(path, signingKind)
}

// ImportSigning is ImportAccount for the account signing seed (signing.nk): same custody, never overwritten.
//
// It also records the public key beside it (signing.pub, 0600, never replaced), so a later loss of signing.nk is told
// from "no signing key configured" (Signing). Importing the same key again is a no-op; a key that differs from the
// record is ErrDifferentKey and nothing changes.
func (a *Authority) ImportSigning(path string) (pub string, created bool, err error) {
	return a.ImportSigningChecked(path, "")
}

// ErrSigningChanged is ImportSigningChecked finding a seed whose public key is not the one the caller checked.
var ErrSigningChanged = errors.New("signing_key changed while init ran: the key to import is not the one that was checked")

// ImportSigningChecked is ImportSigning for a caller that validated the file earlier: the file is read once, and a seed
// whose public key is not wantPub (when wantPub is not "") is ErrSigningChanged before anything is written.
func (a *Authority) ImportSigningChecked(path, wantPub string) (pub string, created bool, err error) {
	nk, want, err := parseSeedFile(path, signingKind)
	if err != nil {
		return "", false, err
	}
	defer nk.Wipe()
	if wantPub != "" && want != wantPub {
		return "", false, ErrSigningChanged
	}
	differs := func(rec string) error {
		return fmt.Errorf("%w: %s records %s, not the %s key %s you gave; refusing to replace it (move it aside to adopt with another key)", ErrDifferentKey, filepath.Join(a.dir, signingRec), rec, signingKind.what, want)
	}
	a.mu.Lock()
	rec, rerr := a.signingRecordLocked()
	a.mu.Unlock()
	if rerr != nil {
		return "", false, rerr
	}
	if rec != "" && rec != want {
		return "", false, differs(rec)
	}
	pub, created, err = a.importParsed(nk, want, signingFile, signingKind)
	if err != nil {
		return "", false, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if rec == "" {
		if _, err := InstallFile(filepath.Join(a.dir, signingRec), []byte(pub+"\n")); err != nil {
			return "", false, err
		}
		got, err := a.signingRecordLocked()
		if err != nil {
			return "", false, err
		}
		if got != pub {
			return "", false, differs(got)
		}
	}
	return pub, created, nil
}

func (a *Authority) importKind(src, name string, k kind) (pub string, created bool, err error) {
	nk, pub, err := parseSeedFile(src, k)
	if err != nil {
		return "", false, err
	}
	defer nk.Wipe()
	return a.importParsed(nk, pub, name, k)
}

// importParsed installs an already parsed seed; the caller wipes nk.
func (a *Authority) importParsed(nk nkeys.KeyPair, pub, name string, k kind) (string, bool, error) {
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

// maxCredsFile bounds how much of a creds file ReadOwnedFile reads.
const maxCredsFile = 1 << 20

// ReadOwnedFile reads the credentials file at path under the key store's rules: it is opened once with O_NOFOLLOW (a
// symlink is refused), every check runs on that descriptor (a regular file, owned by the effective uid, no wider than
// mode 0600), and the bytes come from the same descriptor. what names the file in an error ("system_creds"). The error
// carries the path, never content.
func ReadOwnedFile(path, what string) ([]byte, error) {
	f, err := config.OpenNoFollow(path, os.O_RDONLY|config.OpenNonblock, 0)
	if err != nil {
		if config.IsSymlinkRefusal(err) {
			return nil, fmt.Errorf("%s %s is a symlink: refusing it (give the file itself)", what, path)
		}
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("read %s %s: %w", what, path, err)
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s %s is not a regular file", what, path)
	}
	if tooOpen(fi.Mode()) {
		return nil, fmt.Errorf("%s %s has mode %04o: it is readable by other users. Run: chmod 600 %s", what, path, fi.Mode().Perm(), path)
	}
	if err := ownerErr(fi, path, what); err != nil {
		return nil, err
	}
	if fi.Size() > maxCredsFile {
		return nil, fmt.Errorf("%s %s is too large to be a creds file", what, path)
	}
	return io.ReadAll(io.LimitReader(f, maxCredsFile))
}
