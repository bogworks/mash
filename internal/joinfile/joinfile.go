// Package joinfile is the join file: the one text file whoever runs a mesh hands to a machine that is to join it.
// `mashctl endpoint create --out` writes it and `mash join` reads it, so both share this one definition (spec/mash.json,
// the command join, "THE JOIN FILE").
package joinfile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// Format is the version of the join file: the value of its "mash_join" field.
const Format = 1

// File holds either Creds, the whole text of a NATS creds file (user JWT and seed), or UserJWT, the user JWT alone, for
// an endpoint that made its own key. It holds nothing else.
type File struct {
	MashJoin int      `json:"mash_join"`
	Address  string   `json:"address"`
	Servers  []string `json:"servers"`
	CA       *string  `json:"ca"`
	Creds    string   `json:"creds,omitempty"`
	UserJWT  string   `json:"user_jwt,omitempty"`
}

// Encode is the file's bytes: JSON with a two-space indent and one trailing newline.
func (j File) Encode() ([]byte, error) {
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

// ErrNotJoinFile is what Parse returns for bytes that are not a join file of a format it reads. Its text never holds
// any of the bytes: a join file holds a seed.
var ErrNotJoinFile = errors.New("not a join file")

// Parse reads a join file. Unknown fields are refused, and so is a file with neither or both of creds and user_jwt, no
// address, or no server. It does not check the address, the creds or the JWT.
func Parse(raw []byte) (File, error) {
	var j File
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&j); err != nil {
		return File{}, ErrNotJoinFile
	}
	if dec.More() {
		return File{}, ErrNotJoinFile
	}
	if j.MashJoin != Format {
		return File{}, fmt.Errorf("%w: mash_join is %d, this mash reads %d", ErrNotJoinFile, j.MashJoin, Format)
	}
	if j.Address == "" || len(j.Servers) == 0 || (j.Creds == "") == (j.UserJWT == "") {
		return File{}, ErrNotJoinFile
	}
	return j, nil
}
