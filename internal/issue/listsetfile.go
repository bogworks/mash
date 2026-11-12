package issue

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/bogworks/mash/internal/listset"
)

// maxListSetFile bounds how much of a list set file is read.
const maxListSetFile = 8 << 20

// FileListSet is the ListSetInForce that reads a list set from a JSON file. DEC-004 leaves the list set's file format
// open, so the file is a JSON object shaped like listset.ListSet: {"Range": "...", "Switches": [{"Group", "Verb",
// "Switch"}], "Senders": [{"Sender", "Allow", "Deny"}]} (field names match without regard to case; an unknown field is
// refused). It is read and compiled again whenever its bytes change, and a file that cannot be read or does not compile
// is no list set in force (fail closed). The generation is 0 until the policy gate lands.
type FileListSet struct {
	Path string

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
	fh, err := os.Open(f.Path)
	if err != nil {
		return nil, 0, fmt.Errorf("list set: %w", err)
	}
	defer fh.Close()
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
