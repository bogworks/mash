package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPublish_latestDownloadsRunAfterTheMarker(t *testing.T) {
	f := newFake()
	f.tagAt, f.release = "abc", relPublished
	if err := run(f); err != nil {
		t.Fatal(err)
	}
	var latest, dl = -1, -1
	for i, c := range f.calls {
		switch c {
		case "latest":
			latest = i
		case "latestdownloads":
			dl = i
		}
	}
	if latest < 0 || dl < latest {
		t.Errorf("calls %v: want latestdownloads after latest", f.calls)
	}
}

func TestPublish_latestDownloadsNotCheckedWithNotLatest(t *testing.T) {
	f := newFake()
	f.tagAt, f.release = "abc", relPublished
	f.downloadsErr = errors.New("would fail")
	if err := runPublish(Options{Version: "1.2.3", Wait: time.Minute, NotLatest: true}, f, "abc"); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.calls {
		if c == "latestdownloads" {
			t.Fatalf("checked latest-download with --not-latest: %v", f.calls)
		}
	}
}

func TestPublish_latestDownloadsFailureIsExit4AndStops(t *testing.T) {
	f := newFake()
	f.tagAt, f.release = "abc", relPublished
	f.downloadsErr = errors.New("asset mash-linux-amd64: latest-download is sha256:aa, the tagged release has sha256:bb")
	err := run(f)
	if err == nil || exitCode(err) != 4 || !strings.Contains(err.Error(), "mash-linux-amd64") || strings.Contains(err.Error(), "gh release edit") {
		t.Fatalf("got %v, exit %d", err, exitCode(err))
	}
	for _, c := range f.calls {
		if c == "assets" {
			t.Errorf("went on to the assets gate after the failure: %v", f.calls)
		}
	}
}

func TestPublish_latestDownloadsRerunOnResume(t *testing.T) {
	f := newFake()
	f.tagAt, f.release = "abc", relPublished
	f.downloadsErr = errors.New("lagging")
	if err := run(f); err == nil {
		t.Fatal("want a failure")
	}
	f.downloadsErr = nil
	if err := run(f); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, c := range f.calls {
		if c == "latestdownloads" {
			n++
		}
	}
	if n != 2 {
		t.Errorf("latestdownloads ran %d times, want 2: %v", n, f.calls)
	}
}

// ---- the real comparison, against a local https server and a fake gh ----

func sha(b string) string {
	s := sha256.Sum256([]byte(b))
	return "sha256:" + hex.EncodeToString(s[:])
}

// ghAssets is a gh that logs its arguments, answers `release view` with the assets (digests included unless noDigest)
// and `release download --pattern <name>` with the content.
func ghAssets(t *testing.T, assets map[string]string, noDigest bool) (Options, string) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "log")
	js := `{"assets":[`
	first := true
	for n, c := range assets {
		if !first {
			js += ","
		}
		first = false
		d := sha(c)
		if noDigest {
			d = ""
		}
		js += fmt.Sprintf(`{"name":%q,"digest":%q}`, n, d)
		if err := os.WriteFile(filepath.Join(dir, "c-"+n), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	js += "]}"
	if err := os.WriteFile(filepath.Join(dir, "view.json"), []byte(js), 0o644); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho \"$*\" >> " + log + "\ncase \"$1 $2\" in\n" +
		"\"release view\") cat " + filepath.Join(dir, "view.json") + " ;;\n" +
		"\"release download\") while [ $# -gt 0 ]; do [ \"$1\" = --pattern ] && cat " + dir + "/c-\"$2\"; shift; done ;;\n" +
		"esac\n"
	gh := filepath.Join(dir, "gh")
	if err := os.WriteFile(gh, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return Options{GH: gh, GHRepo: "bogworks/mash"}, log
}

// latestServer serves /bogworks/mash/releases/latest/download/<name> through a redirect, as GitHub does. served maps a
// name to its content; stale maps a name to the content served for the first staleFor requests (a lagging redirect).
func latestServer(t *testing.T, served, stale map[string]string, staleFor int) {
	t.Helper()
	var mu sync.Mutex
	hits := map[string]int{}
	mux := http.NewServeMux()
	mux.HandleFunc("/bogworks/mash/releases/latest/download/", func(w http.ResponseWriter, r *http.Request) {
		n := strings.TrimPrefix(r.URL.Path, "/bogworks/mash/releases/latest/download/")
		http.Redirect(w, r, "/objects/"+n, http.StatusFound)
	})
	mux.HandleFunc("/objects/", func(w http.ResponseWriter, r *http.Request) {
		n := strings.TrimPrefix(r.URL.Path, "/objects/")
		mu.Lock()
		hits[n]++
		h := hits[n]
		mu.Unlock()
		c, ok := served[n]
		if s, st := stale[n]; st && h <= staleFor {
			c, ok = s, true
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, c)
	})
	srv := httptest.NewTLSServer(mux)
	oldBase, oldClient := latestBase, latestClient
	latestBase, latestClient = srv.URL, srv.Client()
	t.Cleanup(func() { srv.Close(); latestBase, latestClient = oldBase, oldClient })
}

var releaseFiles = map[string]string{"mash-linux-amd64": "bin one", "mashctl-linux-amd64": "bin two", "SHA256SUMS": "sums", "mash.intoto.jsonl": "bundle"}

func TestVerifyLatestDownloads_matchPinsTheAttestation(t *testing.T) {
	o, log := ghAssets(t, releaseFiles, false)
	latestServer(t, releaseFiles, nil, 0)
	msg, err := verifyLatestDownloads(t.Context(), o, "v1.2.3", "0123456789abcdef0123", noSleep)
	if err != nil {
		t.Fatal(err)
	}
	if want := "latest-download: 4 assets of v1.2.3 match by digest; attestation pinned to 0123456789ab verified"; msg != want {
		t.Errorf("msg %q, want %q", msg, want)
	}
	b, _ := os.ReadFile(log)
	for _, w := range []string{"attestation verify", "SHA256SUMS", "--bundle", "mash.intoto.jsonl", "--signer-workflow bogworks/mash/.github/workflows/release.yml", "--source-ref refs/tags/v1.2.3", "--source-digest 0123456789abcdef0123"} {
		if !strings.Contains(string(b), w) {
			t.Errorf("gh log lacks %q:\n%s", w, b)
		}
	}
}

func TestVerifyLatestDownloads_digestFromDownloadWhenGitHubGivesNone(t *testing.T) {
	o, _ := ghAssets(t, releaseFiles, true)
	latestServer(t, releaseFiles, nil, 0)
	if _, err := verifyLatestDownloads(t.Context(), o, "v1.2.3", "abc", noSleep); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyLatestDownloads_changedAssetFailsNamingIt(t *testing.T) {
	o, log := ghAssets(t, releaseFiles, false)
	changed := map[string]string{}
	for k, v := range releaseFiles {
		changed[k] = v
	}
	changed["mashctl-linux-amd64"] = "tampered"
	latestServer(t, changed, nil, 0)
	sleeps := 0
	_, err := verifyLatestDownloads(t.Context(), o, "v1.2.3", "abc", func(time.Duration) { sleeps++ })
	if err == nil {
		t.Fatal("want a mismatch")
	}
	for _, w := range []string{"mashctl-linux-amd64", sha("tampered"), sha("bin two")} {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error lacks %q: %v", w, err)
		}
	}
	if sleeps != latestReads-1 {
		t.Errorf("%d sleeps, want %d", sleeps, latestReads-1)
	}
	if b, _ := os.ReadFile(log); strings.Contains(string(b), "attestation") {
		t.Errorf("attested after a mismatch:\n%s", b)
	}
}

func TestVerifyLatestDownloads_laggingRedirectIsRetried(t *testing.T) {
	o, _ := ghAssets(t, releaseFiles, false)
	latestServer(t, releaseFiles, map[string]string{"mash-linux-amd64": "the previous release"}, 2)
	sleeps := 0
	if _, err := verifyLatestDownloads(t.Context(), o, "v1.2.3", "abc", func(d time.Duration) {
		if d != latestInterval {
			t.Errorf("slept %v", d)
		}
		sleeps++
	}); err != nil {
		t.Fatal(err)
	}
	if sleeps != 2 {
		t.Errorf("%d sleeps, want 2", sleeps)
	}
}

func TestVerifyLatestDownloads_notKeylessSkipsAttestationAndSaysSo(t *testing.T) {
	files := map[string]string{"mash-linux-amd64": "bin one", "SHA256SUMS": "sums", "SHA256SUMS.sig": "sig"}
	o, log := ghAssets(t, files, false)
	latestServer(t, files, nil, 0)
	msg, err := verifyLatestDownloads(t.Context(), o, "v0.3.0", "abc", noSleep)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "3 assets of v0.3.0 match by digest") || !strings.Contains(msg, "no attestation was checked") {
		t.Errorf("msg %q", msg)
	}
	if b, _ := os.ReadFile(log); strings.Contains(string(b), "attestation") {
		t.Errorf("attested a release without a bundle:\n%s", b)
	}
}

func TestLatestGet_refusesAnHTTPRedirect(t *testing.T) {
	plain := httptest.NewServer(http.NotFoundHandler())
	defer plain.Close()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, plain.URL+"/x", http.StatusFound) })
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()
	oldBase, oldClient := latestBase, latestClient
	latestBase, latestClient = srv.URL, srv.Client()
	defer func() { latestBase, latestClient = oldBase, oldClient }()
	if _, err := latestGet(t.Context(), "bogworks/mash", "f"); err == nil || !strings.Contains(err.Error(), "https only") {
		t.Fatalf("got %v", err)
	}
}
