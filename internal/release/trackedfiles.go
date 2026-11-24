package release

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// maxTrackedFileSize is the largest tracked file allowed without an entry in
// largeFileAllowlist.
const maxTrackedFileSize = 256 * 1024

// largeFileAllowlist maps a repo-relative path to the reason it may exceed
// maxTrackedFileSize. Additions need a reason.
var largeFileAllowlist = map[string]string{
	"docs/demo/mash-demo.mp4": "demo video in the README, Randy 2026-11-22 (#1244)",
}

// binaryAssetExts are extensions of binary assets that may contain NUL bytes.
var binaryAssetExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".ico": true,
	".pdf": true, ".woff": true, ".woff2": true, ".mp4": true,
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

// CheckTrackedFiles lists the files git tracks under root (git ls-files) and
// returns one message per problem: a file over maxTrackedFileSize that is not
// in largeFileAllowlist, a file that starts with an executable magic number,
// or a file with a NUL byte in its first 8 KiB whose extension is not a binary
// asset. It returns an error when git cannot list root's files (git missing,
// or root is not a git checkout).
func CheckTrackedFiles(root string) ([]string, error) {
	out, err := exec.Command("git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files in %s: %w", root, err)
	}
	var bad []string
	for _, rel := range strings.Split(string(out), "\x00") {
		if rel == "" {
			continue
		}
		path := filepath.Join(root, rel)
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
	return bad, nil
}
