//go:build unix

package ctlkeys

import "os"

// syncDir fsyncs the directory at path so a new entry in it survives power loss.
func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	serr := f.Sync()
	if cerr := f.Close(); serr == nil {
		serr = cerr
	}
	return serr
}
