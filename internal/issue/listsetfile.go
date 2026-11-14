package issue

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctlkeys"
	"github.com/bogworks/mash/internal/listset"
)

// maxListSetFile bounds how much of a list set file is read.
const maxListSetFile = 8 << 20

// FileListSet is the ListSetInForce that reads a list set from a JSON file. DEC-004 leaves the list set's file format
// open, so the file is a JSON object shaped like listset.ListSet: {"Range": "...", "Switches": [{"Group", "Verb",
// "Switch"}], "Senders": [{"Sender", "Allow", "Deny"}]} (field names match without regard to case; an unknown field is
// refused). It is read and compiled again whenever its bytes change, and a file that cannot be read or does not compile
// is no list set in force (fail closed). The generation is 0 until the policy gate lands.
//
// The list set decides who may be issued a credential, so every read applies custody checks: the file is opened
// without following a symlink and must be a regular file owned by the effective uid that no group or other user can
// write (mode & 0022 == 0); its directory must be a real directory the effective uid owns that no group or other user
// can write. Reading by others is allowed: the list set is not secret. The checks run on every read, so a file that is
// swapped or loosened after start is refused rather than served from the table compiled earlier.
type FileListSet struct {
	Path string

	// owned checks the owner of the file; nil means ctlkeys.CheckOwned. Tests replace it.
	owned func(fs.FileInfo, string, string) error

	mu       sync.Mutex
	last     []byte
	compiled *listset.Compiled
}

// ParseListSet decodes the JSON form of a list set.
func ParseListSet(data []byte) (listset.ListSet, error) {
	var ls listset.ListSet
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&ls); err != nil {
		return ls, fmt.Errorf("list set: %w", err)
	}
	if dec.More() {
		return ls, fmt.Errorf("list set: more than one JSON value")
	}
	return ls, nil
}

// InForce implements ListSetInForce.
func (f *FileListSet) InForce(_ context.Context) (*listset.Compiled, int, error) {
	if err := ctlkeys.CheckStrictParentDir(filepath.Dir(f.Path)); err != nil {
		return nil, 0, fmt.Errorf("list set %s: %w", f.Path, err)
	}
	fh, err := config.OpenNoFollow(f.Path, os.O_RDONLY, 0)
	if err != nil {
		if config.IsSymlinkRefusal(err) {
			return nil, 0, fmt.Errorf("list set %s is a symlink: refusing it", f.Path)
		}
		return nil, 0, fmt.Errorf("list set: %w", err)
	}
	defer fh.Close()
	fi, err := fh.Stat()
	if err != nil {
		return nil, 0, fmt.Errorf("list set %s: %w", f.Path, err)
	}
	if !fi.Mode().IsRegular() {
		return nil, 0, fmt.Errorf("list set %s is not a regular file", f.Path)
	}
	if fi.Mode().Perm()&0o022 != 0 {
		return nil, 0, fmt.Errorf("list set %s has mode %04o: a group or other user can write it. Run: chmod go-w %s", f.Path, fi.Mode().Perm(), f.Path)
	}
	owned := f.owned
	if owned == nil {
		owned = ctlkeys.CheckOwned
	}
	if err := owned(fi, f.Path, "list set"); err != nil {
		return nil, 0, err
	}
	b, err := io.ReadAll(io.LimitReader(fh, maxListSetFile+1))
	if err != nil {
		return nil, 0, fmt.Errorf("list set: %w", err)
	}
	if len(b) > maxListSetFile {
		return nil, 0, fmt.Errorf("list set: %s is too large", f.Path)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.compiled != nil && bytes.Equal(b, f.last) {
		return f.compiled, 0, nil
	}
	ls, err := ParseListSet(b)
	if err != nil {
		return nil, 0, err
	}
	c, err := listset.Compile(ls)
	if err != nil {
		return nil, 0, err
	}
	f.last, f.compiled = b, c
	return c, 0, nil
}
