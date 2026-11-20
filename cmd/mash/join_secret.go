package main

import (
	"errors"
	"io"
	"io/fs"
	"os"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/config"
)

// secretFile is an opened file that may hold a seed, with what was found out about it from the opened file itself
// (not from its name, so a swap between the check and the read changes nothing).
type secretFile struct {
	path    string
	f       *os.File // nil when the file is not a plain file
	plain   bool     // a plain file, owned by the current user
	private bool     // no group or other permission bit
}

// openSecret looks at path before it is read: Lstat, then open and Fstat the opened file. A symlink is followed only so
// a join file can be read to see whether it holds a seed, and is never plain. A directory, device or fifo is not opened.
func openSecret(path string) (*secretFile, error) {
	s := &secretFile{path: path}
	lst, err := os.Lstat(path)
	if err != nil {
		return nil, readFailure(path, err)
	}
	if lst.Mode()&os.ModeSymlink == 0 && !lst.Mode().IsRegular() {
		return s, nil
	}
	f, err := os.OpenFile(path, os.O_RDONLY|secretOpenFlags, 0)
	if err != nil {
		return nil, readFailure(path, err)
	}
	fst, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, readFailure(path, err)
	}
	if !fst.Mode().IsRegular() {
		f.Close()
		return s, nil
	}
	s.f = f
	s.plain = lst.Mode().IsRegular() && os.SameFile(lst, fst) && ownedByMe(fst)
	s.private = ownerOnly(fst)
	return s, nil
}

func (s *secretFile) close() {
	if s.f != nil {
		s.f.Close()
	}
}

// refuse is the refusal for a file that holds a seed, or nil when it may be used.
func (s *secretFile) refuse() error {
	p := config.Clean(s.path)
	if !s.plain {
		return cliframe.Failf("%s is not a plain file owned by you; nothing was installed", p).
			WithHint("copy it into a file of your own, then: chmod 600 <that file>")
	}
	if !s.private {
		return cliframe.Failf("%s can be read by others; a join file that holds a seed must be readable by you alone", p).
			WithHint("chmod 600 " + p)
	}
	return nil
}

// readAll reads the opened file, at most max+1 bytes.
func (s *secretFile) readAll(max int64) ([]byte, error) {
	if s.f == nil {
		return nil, readFailure(s.path, fs.ErrInvalid)
	}
	return io.ReadAll(io.LimitReader(s.f, max+1))
}

func readFailure(path string, err error) error {
	reason := err
	var pe *fs.PathError
	if errors.As(err, &pe) {
		reason = pe.Err
	}
	return cliframe.Failf("cannot read %s: %v", config.Clean(path), reason)
}
