package main

import (
	"bytes"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/joinfile"
)

// joinFormat is the version of the join file: the value of its "mash_join" field.
const joinFormat = joinfile.Format

// joinFile is the file `mashctl endpoint create --out` and `endpoint reissue --out` write for another machine, and
// `mash join` reads: one definition, shared in internal/joinfile.
type joinFile = joinfile.File

// joinBase is what every join file of one run shares: the servers and the CA certificate the configuration names.
type joinBase struct {
	servers []string
	ca      *string
}

// errCAFile marks a CA file that is not certificates only.
var errCAFile = errors.New("ca file")

// joinBaseOf reads the servers and the CA certificate (PEM text, from the configured CA file) from the configuration.
func joinBaseOf(cfg config.Config) (joinBase, error) {
	var b joinBase
	for _, s := range strings.Split(cfg.URL, ",") {
		if s = strings.TrimSpace(s); s != "" {
			b.servers = append(b.servers, s)
		}
	}
	if len(b.servers) == 0 {
		return b, errors.New("no server is configured")
	}
	if cfg.TLSCA != "" {
		raw, err := os.ReadFile(cfg.TLSCA)
		if err != nil {
			return b, fmt.Errorf("cannot read the CA file %s: %v", cfg.TLSCA, errors.Unwrap(err))
		}
		defer clear(raw)
		certs, ok := onlyCertificates(raw)
		if !ok {
			return b, fmt.Errorf("%w: %s must hold only certificates", errCAFile, cfg.TLSCA)
		}
		b.ca = &certs
	}
	return b, nil
}

// onlyCertificates re-encodes the CERTIFICATE blocks of PEM text. It reports false for any other block (a private key
// in the same file is never copied), for data that is not PEM, and for text with no certificate.
func onlyCertificates(raw []byte) (string, bool) {
	var out []byte
	blocks := 0
	rest := raw
	for {
		var blk *pem.Block
		blk, rest = pem.Decode(rest)
		if blk == nil {
			break
		}
		if blk.Type != "CERTIFICATE" || len(blk.Headers) != 0 {
			return "", false
		}
		out = append(out, pem.EncodeToMemory(blk)...)
		blocks++
	}
	// A block that does not decode is skipped by pem.Decode; refuse it rather than drop it.
	if blocks == 0 || bytes.Count(raw, []byte("-----BEGIN ")) != blocks || len(bytes.TrimSpace(rest)) != 0 {
		return "", false
	}
	return string(out), true
}

// withCreds is the join file of addr that holds the whole creds file text.
func (b joinBase) withCreds(addr string, creds []byte) joinFile {
	return joinFile{MashJoin: joinFormat, Address: addr, Servers: b.servers, CA: b.ca, Creds: string(creds)}
}

// withJWT is the join file of addr that holds the user JWT alone.
func (b joinBase) withJWT(addr, userJWT string) joinFile {
	return joinFile{MashJoin: joinFormat, Address: addr, Servers: b.servers, CA: b.ca, UserJWT: userJWT}
}

// errJoinExists is returned by writeJoinFile for a path that is taken.
var errJoinExists = errors.New("the join file path is taken")

// writeJoinFile writes j to path, mode 0600, never over anything that exists there: a temp file made exclusively in the
// same folder, then a link to path that fails when path exists. A run killed part-way leaves no truncated file at path.
func writeJoinFile(path string, j joinFile) error {
	data, err := j.Encode()
	if err != nil {
		return err
	}
	defer clear(data)
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("%w: %w", errJoinExists, fs.ErrExist)
	}
	return epWriteNew(path, data)
}

// readJoinCreds is the creds text a join file at path holds.
func readJoinCreds(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	defer clear(raw)
	var j joinFile
	if err := json.Unmarshal(raw, &j); err != nil {
		return nil, fmt.Errorf("%s is not a join file", path)
	}
	if j.Creds == "" {
		return nil, fmt.Errorf("%s holds no creds", path)
	}
	return []byte(j.Creds), nil
}
