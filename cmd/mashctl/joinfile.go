package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/bogworks/mash/internal/config"
)

// joinFormat is the version of the join file: the value of its "mash_join" field.
const joinFormat = 1

// joinFile is the file `mashctl endpoint create --out` and `endpoint reissue --out` write for another machine, and
// `mash join` reads. It holds either Creds, the whole text of a NATS creds file (user JWT and seed), or UserJWT, the
// user JWT alone, for an endpoint that made its own key. It holds nothing else.
type joinFile struct {
	MashJoin int      `json:"mash_join"`
	Address  string   `json:"address"`
	Servers  []string `json:"servers"`
	CA       *string  `json:"ca"`
	Creds    string   `json:"creds,omitempty"`
	UserJWT  string   `json:"user_jwt,omitempty"`
}

// joinBase is what every join file of one run shares: the servers and the CA certificate the configuration names.
type joinBase struct {
	servers []string
	ca      *string
}

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
		pem := string(raw)
		b.ca = &pem
	}
	return b, nil
}

// withCreds is the join file of addr that holds the whole creds file text.
func (b joinBase) withCreds(addr string, creds []byte) joinFile {
	return joinFile{MashJoin: joinFormat, Address: addr, Servers: b.servers, CA: b.ca, Creds: string(creds)}
}

// withJWT is the join file of addr that holds the user JWT alone.
func (b joinBase) withJWT(addr, userJWT string) joinFile {
	return joinFile{MashJoin: joinFormat, Address: addr, Servers: b.servers, CA: b.ca, UserJWT: userJWT}
}

// encode is the file's bytes: JSON with a two-space indent and one trailing newline.
func (j joinFile) encode() ([]byte, error) {
	if j.Servers == nil {
		j.Servers = []string{}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(j); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// errJoinExists is returned by writeJoinFile for a path that is taken.
var errJoinExists = errors.New("the join file path is taken")

// writeJoinFile writes j to path, mode 0600, never over anything that exists there: a temp file made exclusively in the
// same folder, then a link to path that fails when path exists. A run killed part-way leaves no truncated file at path.
func writeJoinFile(path string, j joinFile) error {
	data, err := j.encode()
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
