// Package keycred makes a user key pair. It writes files only: it needs no server, no config file and no account key.
package keycred

import (
	"errors"
	"os"
	"path/filepath"

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
	CodeFileExists = "file_exists"
)

// Refusal says why a run did nothing.
type Refusal struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

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
	Existing   string // the path that was already there, when Refusal is file_exists
}

// PublicPathFor is the public key file for a seed file: the seed's whole path with .pub added (bob.seed.pub).
func PublicPathFor(seed string) string { return seed + ".pub" }

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

// CreateKey makes a user key pair and writes the seed (0600) and the public key, creating a missing parent directory
// (0700). Both targets are checked first; if either exists nothing is written and the result is a refusal. The public
// file is linked first, then the seed; if the seed cannot be linked, the public file this call linked is removed.
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
			res.Existing = p
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
	for _, p := range []string{o.Out, pub} {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return res, err
		}
	}
	// Each file is written whole to a temp file next to its target, then published by link, so a run that is killed
	// leaves no empty or partial seed or public file under a final name.
	seedTmp, err := writeTemp(o.Out, ".seed-*", append(seed, '\n'), 0o600)
	if err != nil {
		return res, err
	}
	defer os.Remove(seedTmp) // the temp file this call made
	pubTmp, err := writeTemp(pub, ".pub-*", []byte(pk+"\n"), 0o644)
	if err != nil {
		return res, err
	}
	defer os.Remove(pubTmp) // the temp file this call made
	if err := os.Link(pubTmp, pub); err != nil {
		return refuseExists(res, pub, err)
	}
	if err := os.Link(seedTmp, o.Out); err != nil {
		os.Remove(pub) // the public file this call linked
		return refuseExists(res, o.Out, err)
	}
	res.Outcome = Created
	return res, nil
}

// refuseExists turns a lost race on a link into a refusal; any other error stays an error.
func refuseExists(res KeyResult, p string, err error) (KeyResult, error) {
	if errors.Is(err, os.ErrExist) {
		res.Outcome = Refused
		res.Existing = p
		res.Refusal = &Refusal{CodeFileExists, p + " exists; mash does not overwrite it"}
		return res, nil
	}
	return res, err
}

// writeTemp writes data to a new temp file in the directory of target and returns its name. On error the temp file
// this call made is removed.
func writeTemp(target, pattern string, data []byte, mode os.FileMode) (string, error) {
	f, err := os.CreateTemp(filepath.Dir(target), pattern)
	if err != nil {
		return "", err
	}
	name := f.Name()
	if err := f.Chmod(mode); err == nil {
		_, err = f.Write(data)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err == nil {
			return name, nil
		}
	} else {
		f.Close()
	}
	os.Remove(name)
	return "", err
}
