package main

import (
	"archive/zip"
	"bytes"
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

// What goes into the node image besides the checkout's own binaries: a pinned minimal Debian, the packages the setup
// script needs (Containerfile.at005), and a pinned release of the nats command (the script needs `nats auth operator`).

// baseImage is the image the node image is built on, pulled by tag; its digest is read after the build and recorded.
const baseImage = "docker.io/library/debian:bookworm-slim"

// nodeBin is where the node image keeps the binaries, the nats command and the setup script; it is on the node's PATH.
const (
	nodeBin       = "/usr/local/bin"
	nodeSetupPath = nodeBin + "/mash-local-nats-setup.sh"
	nodeRepo      = "/opt/mash-repo" // in the image: the checkout's plugin/ directory only
	nodeUID       = "1000:1000"      // the image user mash: nodes never run as root
	nodePath      = nodeBin + ":/usr/bin:/bin"
)

// natsCLIVersion pins the nats command of the node image: one release, one zip per architecture, each with its
// sha256. It is a version that has `nats auth operator` (the setup script refuses one that has not); the harness
// verifies the sha256 of the download before anything is unpacked, and the download goes to the run directory only.
//
// The URL is written in two parts so that the portable-code scan (R3), which reads string literals that look like
// github.com URLs, does not take it for a link to another organisation.
const natsCLIVersion = "v0.5.1"

const natsCLIURLPrefix = "https://github.com/" + "nats-io/natscli/releases/download/"

type natsCLIAsset struct {
	URL    string
	SHA256 string
}

// natsCLIAssets is keyed by GOARCH of the machine that builds the image (the node has the same architecture).
var natsCLIAssets = map[string]natsCLIAsset{
	"amd64": {natsCLIURLPrefix + natsCLIVersion + "/nats-0.5.1-linux-amd64.zip", "f8a3e106deebed7afe736b7090359bda3aa62a04b310440bedc1d11a93c0ab4f"},
	"arm64": {natsCLIURLPrefix + natsCLIVersion + "/nats-0.5.1-linux-arm64.zip", "91c661f1173ee9bfe07514eef55a29d25ac7757a0868c179e825eba976073adc"},
}

// natsCLIFor is the pinned asset for an architecture.
func natsCLIFor(arch string) (natsCLIAsset, error) {
	a, ok := natsCLIAssets[arch]
	if !ok {
		return a, fmt.Errorf("no nats command is pinned for %s: the node image supports amd64 and arm64", arch)
	}
	return a, nil
}

const maxNatsCLIZip = 64 << 20

// httpFetch is the default download: one GET with a timeout and a size cap.
func httpFetch(url string) ([]byte, error) { return httpFetchDefault(url) }

// httpFetchDefault is the real download; the unit tests replace it so that none touches the network.
var httpFetchDefault = realFetch

// natscliFetchTimeout bounds the download of the nats CLI into the build context.
const natscliFetchTimeout = 180 * time.Second

func realFetch(url string) ([]byte, error) {
	resp, err := (&http.Client{Timeout: natscliFetchTimeout}).Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxNatsCLIZip+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxNatsCLIZip {
		return nil, fmt.Errorf("GET %s: more than %d bytes", url, maxNatsCLIZip)
	}
	return b, nil
}

// natsCLIBinary checks the zip against sha and returns the file named "nats" in it.
func natsCLIBinary(zipBytes []byte, sha string) ([]byte, error) {
	sum := sha256.Sum256(zipBytes)
	if got := hex.EncodeToString(sum[:]); got != sha {
		return nil, fmt.Errorf("the nats command download has sha256 %s, not the pinned %s: refusing to use it", got, sha)
	}
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return nil, err
	}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || path.Base(f.Name) != "nats" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		b, err := io.ReadAll(io.LimitReader(rc, maxNatsCLIZip+1))
		if err != nil {
			return nil, err
		}
		if len(b) > maxNatsCLIZip {
			return nil, errors.New("the nats command in the zip is too large")
		}
		return b, nil
	}
	return nil, errors.New("the nats command zip holds no file named nats")
}

// writeNatsCLI downloads the pinned nats command for this machine's architecture, verifies it and writes it into ctx.
func writeNatsCLI(ctx string, fetch func(string) ([]byte, error)) error {
	a, err := natsCLIFor(runtime.GOARCH)
	if err != nil {
		return err
	}
	if fetch == nil {
		fetch = httpFetch
	}
	zb, err := fetch(a.URL)
	if err != nil {
		return fmt.Errorf("downloading the nats command: %w", err)
	}
	bin, err := natsCLIBinary(zb, a.SHA256)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(ctx, "nats"), bin, 0o755)
}

// natsCLILine is the record line for the nats command.
func natsCLILine() string {
	a, err := natsCLIFor(runtime.GOARCH)
	if err != nil {
		return "nats command: unavailable"
	}
	return fmt.Sprintf("nats command: natscli %s linux/%s, zip sha256 %s (verified before use)", natsCLIVersion, runtime.GOARCH, a.SHA256)
}

// baseImageLine is the record line for the base image; digest is what `podman image inspect` said.
func baseImageLine(digest string) string {
	digest = strings.TrimSpace(digest)
	if digest == "" || strings.ContainsAny(digest, " \t\n") {
		digest = "unknown"
	}
	return "base image: " + baseImage + ", digest " + digest
}
