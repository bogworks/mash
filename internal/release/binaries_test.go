package release

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// maxTrackedFileSize is the largest tracked file allowed without an entry in
// largeFileAllowlist.
const maxTrackedFileSize = 256 * 1024

// largeFileAllowlist maps a repo-relative path to the reason it may exceed
// maxTrackedFileSize. Additions need a reason.
var largeFileAllowlist = map[string]string{}

// binaryAssetExts are extensions of binary assets that may contain NUL bytes.
var binaryAssetExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".ico": true,
	".pdf": true, ".woff": true, ".woff2": true,
}

var executableMagics = []struct {
	name  string
	magic []byte
}{
	{"ELF", []byte("\x7fELF")},
	{"Mach-O", []byte{0xFE, 0xED, 0xFA, 0xCE}},
	{"Mach-O", []byte{0xFE, 0xED, 0xFA, 0xCF}},
	{"Mach-O", []byte{0xCE, 0xFA, 0xED, 0xFE}},
	{"Mach-O", []byte{0xCF, 0xFA, 0xED, 0xFE}},
	{"Mach-O fat", []byte{0xCA, 0xFE, 0xBA, 0xBE}},
	{"Windows PE", []byte("MZ")},
}

func TestRepo_noCommittedBinariesOrLargeFiles(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	out, err := exec.Command("git", "-C", "../..", "ls-files", "-z").Output()
	if err != nil {
		t.Skipf("not a git checkout: %v", err)
	}
	var bad []string
	for _, rel := range strings.Split(string(out), "\x00") {
		if rel == "" {
			continue
		}
		path := filepath.Join("../..", rel)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if _, ok := largeFileAllowlist[rel]; !ok && info.Size() > maxTrackedFileSize {
			bad = append(bad, fmt.Sprintf("%s: %d bytes exceeds %d", rel, info.Size(), maxTrackedFileSize))
		}
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		head := make([]byte, 8192)
		n, _ := f.Read(head)
		f.Close()
		head = head[:n]
		for _, m := range executableMagics {
			if bytes.HasPrefix(head, m.magic) {
				bad = append(bad, fmt.Sprintf("%s: executable format (%s)", rel, m.name))
				break
			}
		}
		if bytes.IndexByte(head, 0) >= 0 && !binaryAssetExts[strings.ToLower(filepath.Ext(rel))] {
			bad = append(bad, fmt.Sprintf("%s: binary content (NUL byte in first 8 KiB)", rel))
		}
	}
	if len(bad) > 0 {
		t.Errorf("committed binaries or large files (build outputs belong in .gitignore):\n  %s", strings.Join(bad, "\n  "))
	}
}
