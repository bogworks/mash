package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func fileSHA(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// sumsDigestOK reports whether s is a sha256 in hex.
func sumsDigestOK(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func sumBytes(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

var httpClient = &http.Client{Timeout: 5 * time.Minute}

// get downloads url (no credentials are sent) and returns the body.
func get(url string) ([]byte, error) {
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 512<<20))
}

// sumsLine is the digest the checksums file text gives for name ("<hex>  <name>" or "<hex> *<name>").
func sumsLine(text, name string) (string, bool) {
	for _, l := range strings.Split(text, "\n") {
		f := strings.Fields(l)
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name && len(f[0]) == 64 {
			return strings.ToLower(f[0]), true
		}
	}
	return "", false
}

// download fetches base/name, checks it against the digest sumsText gives for name, and returns the bytes.
func download(base, name, sumsText string) ([]byte, error) {
	want, ok := sumsLine(sumsText, name)
	if !ok {
		return nil, fmt.Errorf("the checksums file has no line for %s", name)
	}
	b, err := get(base + "/" + name)
	if err != nil {
		return nil, err
	}
	if got := sumBytes(b); got != want {
		return nil, fmt.Errorf("%s: sha256 %s, the checksums file says %s", name, got, want)
	}
	return b, nil
}

// extract returns the file named want (by base name) from a .tar.gz or .zip archive.
func extract(archive []byte, kind, want string) ([]byte, error) {
	switch kind {
	case "tgz":
		zr, err := gzip.NewReader(bytes.NewReader(archive))
		if err != nil {
			return nil, err
		}
		tr := tar.NewReader(zr)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			if h.Typeflag == tar.TypeReg && path.Base(h.Name) == want {
				return io.ReadAll(tr)
			}
		}
	case "zip":
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if !f.FileInfo().IsDir() && path.Base(f.Name) == want {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(rc)
			}
		}
	}
	return nil, fmt.Errorf("%s is not in the archive", want)
}

// Versions of the tools the proof needs, pinned: gh for the attested install, the local server and its client for the
// setup script. Each is downloaded from its project's release and checked against that release's checksums file.
const (
	ghVersion   = "2.102.0"
	natsServer  = "v2.15.1"
	natsCLIVers = "0.5.2"
)

type tool struct {
	name string
	url  string // directory of the release's files
	file string // the archive
	sums string // the release's checksums file
	kind string // "tgz" or "zip"
	exe  string // the executable inside it
}

func toolsFor(goos, goarch string) []tool {
	ghOS, ghKind := goos, "tgz"
	if goos == "darwin" {
		ghOS, ghKind = "macOS", "zip"
	}
	gh := "https://github.com/cli/cli/releases/download/v" + ghVersion
	ns := "https://github.com/nats-io/nats-server/releases/download/" + natsServer
	nc := "https://github.com/nats-io/natscli/releases/download/v" + natsCLIVers
	return []tool{
		{"gh", gh, fmt.Sprintf("gh_%s_%s_%s.%s", ghVersion, ghOS, goarch, map[string]string{"tgz": "tar.gz", "zip": "zip"}[ghKind]),
			fmt.Sprintf("gh_%s_checksums.txt", ghVersion), ghKind, "gh"},
		{"nats-server", ns, fmt.Sprintf("nats-server-%s-%s-%s.tar.gz", natsServer, goos, goarch), "SHA256SUMS", "tgz", "nats-server"},
		{"nats", nc, fmt.Sprintf("nats-%s-%s-%s.zip", natsCLIVers, goos, goarch), "SHA256SUMS", "zip", "nats"},
	}
}

// installTools downloads the tools into dir (which must be new), each alone in dir/<name>/ so that a PATH can name one
// without the others, and returns what each is, for the log.
func installTools(dir, goos, goarch string) ([]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	var notes []string
	for _, t := range toolsFor(goos, goarch) {
		sums, err := get(t.url + "/" + t.sums)
		if err != nil {
			return notes, err
		}
		arc, err := download(t.url, t.file, string(sums))
		if err != nil {
			return notes, err
		}
		exe, err := extract(arc, t.kind, t.exe)
		if err != nil {
			return notes, fmt.Errorf("%s: %w", t.file, err)
		}
		if err := os.MkdirAll(filepath.Join(dir, t.name), 0o755); err != nil {
			return notes, err
		}
		if err := os.WriteFile(filepath.Join(dir, t.name, t.name), exe, 0o755); err != nil {
			return notes, err
		}
		notes = append(notes, fmt.Sprintf("%s from %s/%s, archive sha256 %s (matches %s)", t.name, t.url, t.file, sumBytes(arc), t.sums))
	}
	return notes, nil
}

// onPath reports whether an executable of that name is in a directory of the PATH string.
func onPath(pathVar, name string) bool {
	for _, d := range strings.Split(pathVar, string(os.PathListSeparator)) {
		if d == "" {
			continue
		}
		if st, err := os.Stat(filepath.Join(d, name)); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return true
		}
	}
	return false
}

func platform() (string, string, error) {
	switch runtime.GOOS {
	case "linux", "darwin":
	default:
		return "", "", errors.New("unsupported OS " + runtime.GOOS)
	}
	return runtime.GOOS, runtime.GOARCH, nil
}
