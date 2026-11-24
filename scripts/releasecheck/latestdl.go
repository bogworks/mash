package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bogworks/mash/internal/release"
)

// latestBase is the host of the latest-download addresses; latestClient fetches them. Tests point both at a server.
var (
	latestBase                = "https://github.com"
	latestClient *http.Client = &http.Client{Timeout: 10 * time.Minute}
)

// latestGet fetches one latest-download address, following redirects over https only.
func latestGet(ctx context.Context, repo, name string) (io.ReadCloser, error) {
	u := latestBase + "/" + repo + "/releases/latest/download/" + url.PathEscape(name)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	c := *latestClient
	c.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if r.URL.Scheme != "https" {
			return fmt.Errorf("refusing a redirect to %s: https only", r.URL.Redacted())
		}
		if len(via) >= 10 {
			return errors.New("too many redirects")
		}
		return nil
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("%s answered %s", u, resp.Status)
	}
	return resp.Body, nil
}

// taggedDigests is the tagged release's assets, name -> "sha256:<hex>": the digest GitHub reports, else the digest of
// the downloaded asset.
func taggedDigests(o Options, tag string) (map[string]string, error) {
	b, err := gh(o, "release", "view", tag, "--repo", o.GHRepo, "--json", "assets")
	if err != nil {
		return nil, op(err)
	}
	var resp struct {
		Assets []struct {
			Name   string `json:"name"`
			Digest string `json:"digest"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(b, &resp); err != nil {
		return nil, op(fmt.Errorf("could not read the release's assets: %w", err))
	}
	if len(resp.Assets) == 0 {
		return nil, op(fmt.Errorf("%s has no assets to compare", tag))
	}
	have := map[string]string{}
	for _, a := range resp.Assets {
		d := a.Digest
		if d == "" {
			data, err := gh(o, "release", "download", tag, "--repo", o.GHRepo, "--pattern", a.Name, "--output", "-")
			if err != nil {
				return nil, op(err)
			}
			sum := sha256.Sum256(data)
			d = "sha256:" + hex.EncodeToString(sum[:])
		}
		have[a.Name] = strings.ToLower(d)
	}
	return have, nil
}

// fetchLatest downloads every named asset from latest-download into dir, returning name -> digest.
func fetchLatest(ctx context.Context, o Options, names []string, dir string) (map[string]string, error) {
	got := map[string]string{}
	for _, n := range names {
		body, err := latestGet(ctx, o.GHRepo, n)
		if err != nil {
			return nil, fmt.Errorf("latest-download of %s: %w", n, err)
		}
		f, err := os.Create(filepath.Join(dir, n))
		if err != nil {
			body.Close()
			return nil, op(err)
		}
		h := sha256.New()
		_, err = io.Copy(io.MultiWriter(h, f), body)
		body.Close()
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("latest-download of %s: %w", n, err)
		}
		got[n] = "sha256:" + hex.EncodeToString(h.Sum(nil))
	}
	return got, nil
}

// compareDigests names the first asset (in name order) whose latest-download digest differs from the tagged one.
func compareDigests(tagged, latest map[string]string) error {
	names := make([]string, 0, len(tagged))
	for n := range tagged {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if latest[n] != tagged[n] {
			return fmt.Errorf("asset %s: latest-download is %s, the tagged release has %s", n, latest[n], tagged[n])
		}
	}
	return nil
}

// verifyLatestDownloads holds each latest-download file of the release to the tagged release's asset by digest, then,
// for a keyless release (one that carries mash.intoto.jsonl), verifies the attestation of the downloaded SHA256SUMS
// with the downloaded bundle, pinned to the tag and its commit. The redirect can lag the marker, so a mismatch or a
// failed fetch is retried like the marker (latestReads, latestInterval). It returns the line to print.
func verifyLatestDownloads(ctx context.Context, o Options, tag, commit string, sleep func(time.Duration)) (string, error) {
	tagged, err := taggedDigests(o, tag)
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(tagged))
	for n := range tagged {
		names = append(names, n)
	}
	sort.Strings(names)
	dir, err := os.MkdirTemp("", "release-check-latest-")
	if err != nil {
		return "", op(err)
	}
	defer os.RemoveAll(dir)
	var last error
	matched := false
	for i := 0; i < latestReads && !matched; i++ {
		if i > 0 {
			sleep(latestInterval)
		}
		latest, err := fetchLatest(ctx, o, names, dir)
		if err == nil {
			err = compareDigests(tagged, latest)
		}
		if err == nil {
			matched = true
		}
		last = err
	}
	if !matched {
		return "", op(last)
	}
	short := commit
	if len(short) > 12 {
		short = short[:12]
	}
	if _, keyless := tagged[release.ProvenanceName]; !keyless {
		return fmt.Sprintf("latest-download: %d assets of %s match by digest; no %s, so no attestation was checked", len(names), tag, release.ProvenanceName), nil
	}
	if _, ok := tagged[release.SumsName]; !ok {
		return "", fmt.Errorf("%s carries %s but no %s to attest", tag, release.ProvenanceName, release.SumsName)
	}
	args := []string{"attestation", "verify", filepath.Join(dir, release.SumsName), "--bundle", filepath.Join(dir, release.ProvenanceName)}
	if _, err := gh(o, append(args, attestFlags(o, tag, commit)...)...); err != nil {
		return "", fmt.Errorf("latest-download %s has no attestation verifying for %s at %s: %w", release.SumsName, tag, short, err)
	}
	return fmt.Sprintf("latest-download: %d assets of %s match by digest; attestation pinned to %s verified", len(names), tag, short), nil
}
