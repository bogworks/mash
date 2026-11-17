// Package keycred makes a user key pair and builds a credentials file from a seed and a user JWT. It reads and writes
// files only: it needs no server, no config file and no account key.
package keycred

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nats-io/jwt/v2"
	"github.com/nats-io/nkeys"
)

// Outcomes of a run.
const (
	Created = "created"
	Planned = "planned"
	Refused = "refused"
	Errored = "error"
)

// Refusal codes.
const (
	CodeFileExists  = "file_exists"
	CodeKeyMismatch = "key_mismatch"
	CodeBadSeed     = "bad_seed"
	CodeBadJWT      = "bad_jwt"
	CodeOutIsInput  = "out_is_input"
)

// Refusal says why a run did nothing.
type Refusal struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

// ErrNeedOut is returned by CreateCredentials when no output path was given and the JWT has no name to build one from.
var ErrNeedOut = errors.New("the user JWT has no name, so there is no default file name; give --out")

// KeyOptions are the inputs of CreateKey.
type KeyOptions struct {
	Out    string // seed file
	PubOut string // public key file; empty means derived from Out
	DryRun bool
	// Gen makes the key pair; nil means nkeys.CreateUser.
	Gen func() (nkeys.KeyPair, error)
}

// KeyResult is what CreateKey did. The seed is never part of it.
type KeyResult struct {
	Outcome    string
	PublicKey  string
	SeedPath   string
	PublicPath string
	Refusal    *Refusal
}

// PublicPathFor is the public key file for a seed file: a trailing .seed becomes .pub, else .pub is appended.
func PublicPathFor(seed string) string {
	if s, ok := strings.CutSuffix(seed, ".seed"); ok && s != "" && !strings.HasSuffix(s, "/") {
		return s + ".pub"
	}
	return seed + ".pub"
}

func exists(p string) (bool, error) {
	_, err := os.Lstat(p)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

// CreateKey makes a user key pair and writes the seed (0600) and the public key. Both targets are checked first; if
// either exists nothing is written and the result is a refusal. If the public file cannot be written, the seed file
// this call made is removed.
func CreateKey(o KeyOptions) (KeyResult, error) {
	if o.Out == "" {
		return KeyResult{}, errors.New("no seed file given")
	}
	pub := o.PubOut
	if pub == "" {
		pub = PublicPathFor(o.Out)
	}
	if filepath.Clean(pub) == filepath.Clean(o.Out) {
		return KeyResult{}, errors.New("the seed file and the public file are the same path")
	}
	res := KeyResult{SeedPath: o.Out, PublicPath: pub}
	for _, p := range []string{o.Out, pub} {
		ok, err := exists(p)
		if err != nil {
			return res, err
		}
		if ok {
			res.Outcome = Refused
			res.Refusal = &Refusal{CodeFileExists, p + " exists; mash does not overwrite it"}
			return res, nil
		}
	}
	gen := o.Gen
	if gen == nil {
		gen = func() (nkeys.KeyPair, error) { return nkeys.CreateUser() }
	}
	kp, err := gen()
	if err != nil {
		return res, err
	}
	pk, err := kp.PublicKey()
	if err != nil {
		return res, err
	}
	res.PublicKey = pk
	if o.DryRun {
		res.Outcome = Planned
		return res, nil
	}
	seed, err := kp.Seed()
	if err != nil {
		return res, err
	}
	if err := writeExcl(o.Out, append(seed, '\n'), 0o600); err != nil {
		return refuseExists(res, o.Out, err)
	}
	if err := writeExcl(pub, []byte(pk+"\n"), 0o644); err != nil {
		os.Remove(o.Out) // the seed this run created
		return refuseExists(res, pub, err)
	}
	res.Outcome = Created
	return res, nil
}

// refuseExists turns a lost race on O_EXCL into a refusal; any other error stays an error.
func refuseExists(res KeyResult, p string, err error) (KeyResult, error) {
	if errors.Is(err, os.ErrExist) {
		res.Outcome = Refused
		res.Refusal = &Refusal{CodeFileExists, p + " exists; mash does not overwrite it"}
		return res, nil
	}
	return res, err
}

func writeExcl(p string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(p)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(p)
		return err
	}
	return nil
}

// CredOptions are the inputs of CreateCredentials.
type CredOptions struct {
	Seed      string // seed file
	JWT       string // user JWT file
	Out       string // creds file; empty means ConfigDir/<name>.creds
	ConfigDir string
	Force     bool
	DryRun    bool
}

// CredResult is what CreateCredentials did. Neither the seed nor the JWT is part of it.
type CredResult struct {
	Outcome   string
	PublicKey string
	CredsPath string
	Name      string
	Refusal   *Refusal
}

// CreateCredentials checks that the JWT names the seed's public key and writes the creds file (0600, by rename).
func CreateCredentials(o CredOptions) (CredResult, error) {
	var res CredResult
	seedRaw, err := os.ReadFile(o.Seed)
	if err != nil {
		return res, err
	}
	jwtRaw, err := os.ReadFile(o.JWT)
	if err != nil {
		return res, err
	}
	seed := []byte(strings.TrimSpace(string(seedRaw)))
	token := strings.TrimSpace(string(jwtRaw))
	kp, err := nkeys.FromSeed(seed)
	if err != nil {
		return refuse(res, CodeBadSeed, o.Seed+" does not hold an nkey seed"), nil
	}
	pub, err := kp.PublicKey()
	if err != nil || !nkeys.IsValidPublicUserKey(pub) {
		return refuse(res, CodeBadSeed, o.Seed+" is not a user key seed"), nil
	}
	res.PublicKey = pub
	claims, err := jwt.DecodeUserClaims(token)
	if err != nil {
		return refuse(res, CodeBadJWT, o.JWT+" is not a user JWT"), nil
	}
	res.Name = claims.Name
	if claims.Subject != pub {
		return refuse(res, CodeKeyMismatch, fmt.Sprintf("the JWT is for %s, the seed is for %s", claims.Subject, pub)), nil
	}
	out := o.Out
	if out == "" {
		if claims.Name == "" || strings.ContainsAny(claims.Name, `/\`) || claims.Name == "." || claims.Name == ".." {
			return res, ErrNeedOut
		}
		if o.ConfigDir == "" {
			return res, errors.New("no config directory; give --out")
		}
		out = filepath.Join(o.ConfigDir, claims.Name+".creds")
	}
	res.CredsPath = out
	// --force must never replace an input: a seed lost this way cannot be recovered.
	if outFi, serr := os.Stat(out); serr == nil {
		for _, in := range []string{o.Seed, o.JWT} {
			if inFi, ierr := os.Stat(in); ierr == nil && os.SameFile(outFi, inFi) {
				return refuse(res, CodeOutIsInput, out+" is the same file as the input "+in+"; choose another --out"), nil
			}
		}
	}
	ok, err := exists(out)
	if err != nil {
		return res, err
	}
	if ok && !o.Force {
		return refuse(res, CodeFileExists, out+" exists; pass --force to replace it"), nil
	}
	if o.DryRun {
		res.Outcome = Planned
		return res, nil
	}
	creds, err := jwt.FormatUserConfig(token, seed)
	if err != nil {
		return res, err
	}
	dir := filepath.Dir(out)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return res, err
	}
	tmp, err := os.CreateTemp(dir, ".creds-*")
	if err != nil {
		return res, err
	}
	tmpName := tmp.Name()
	fail := func(e error) (CredResult, error) {
		tmp.Close()
		os.Remove(tmpName) // the temp file this call made
		return res, e
	}
	if err := tmp.Chmod(0o600); err != nil {
		return fail(err)
	}
	if _, err := tmp.Write(creds); err != nil {
		return fail(err)
	}
	if err := tmp.Close(); err != nil {
		return fail(err)
	}
	if o.Force {
		if err := os.Rename(tmpName, out); err != nil {
			return fail(err)
		}
	} else {
		// A link fails when out exists, so a file made since the check above is not replaced.
		err := os.Link(tmpName, out)
		os.Remove(tmpName) // the temp file this call made
		if errors.Is(err, os.ErrExist) {
			return refuse(res, CodeFileExists, out+" exists; pass --force to replace it"), nil
		}
		if err != nil {
			return res, err
		}
	}
	res.Outcome = Created
	return res, nil
}

func refuse(res CredResult, code, detail string) CredResult {
	res.Outcome = Refused
	res.Refusal = &Refusal{code, detail}
	return res
}
