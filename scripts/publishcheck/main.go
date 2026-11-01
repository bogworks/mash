// Command publishcheck is publish-check.yml's checker: "publishing must be deterministic". It reads a repository's v*
// tags and its releases (the JSON that gh api --paginate --slurp prints) and prints one line per violation of:
//
//	R1  every v* tag has a release: a published one, or a draft younger than 2 h (R3 flags a draft older than that);
//	    a release-candidate tag (vX.Y.Z-rcN) with no release at all is exempt
//	R2  published releases, release candidates excluded, were published in version order: by semver, published_at
//	    never goes down; each adjacent pair where a lower version came out after a higher one is reported
//	R3  no draft release, an rc draft included, is older than 2 h, measured from the draft's last upload (see
//	    draftTime)
//
//	go run ./scripts/publishcheck -repo <owner/name> -tags tags.json -releases releases.json [-now <RFC3339>]
//
// Exit 0: no violation. Exit 1: violations, one per line on stdout. Exit 2: usage or an unreadable input.
package main

import (
	"cmp"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// draftMaxAge is how long a draft may sit: strictly older is a violation, exactly this is not.
const draftMaxAge = 2 * time.Hour

type release struct {
	TagName     string    `json:"tag_name"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	CreatedAt   time.Time `json:"created_at"`
	PublishedAt time.Time `json:"published_at"` // zero for a draft (null)
	Assets      []struct {
		CreatedAt time.Time `json:"created_at"`
		UpdatedAt time.Time `json:"updated_at"`
	} `json:"assets"`
}

// draftTime is the time R3 measures a draft's age from: the latest of the release's created_at and every asset's
// created_at and updated_at. GitHub's release created_at is the tagged commit's date, not when the draft was made, so
// the asset upload times are the best signal the releases API gives of when work on a draft last happened. Its limit:
// a draft with no assets for an old commit has only created_at and can still read old.
func (r release) draftTime() time.Time {
	t := r.CreatedAt
	for _, a := range r.Assets {
		t = latest(t, latest(a.CreatedAt, a.UpdatedAt))
	}
	return t
}

func latest(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// version is a parsed vMAJOR.MINOR.PATCH[-rcN]; rc is 0 for a release (rc numbers start at 1).
type version struct{ major, minor, patch, rc int }

var versionRE = regexp.MustCompile(`^v(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-rc([1-9]\d*))?$`)

func parseVersion(tag string) (version, bool) {
	m := versionRE.FindStringSubmatch(tag)
	if m == nil {
		return version{}, false
	}
	var n [4]int
	for i := range n {
		if m[i+1] == "" {
			continue
		}
		v, err := strconv.Atoi(m[i+1])
		if err != nil {
			return version{}, false
		}
		n[i] = v
	}
	return version{n[0], n[1], n[2], n[3]}, true
}

func (v version) isRC() bool { return v.rc > 0 }

// compare orders by numbers; an rc sorts below its release (rc 0 means no rc, so it is ranked above every rc).
func compare(a, b version) int {
	rank := func(v version) int {
		if v.rc == 0 {
			return int(^uint(0) >> 1)
		}
		return v.rc
	}
	return cmp.Or(cmp.Compare(a.major, b.major), cmp.Compare(a.minor, b.minor), cmp.Compare(a.patch, b.patch), cmp.Compare(rank(a), rank(b)))
}

// readPages reads gh api --paginate --slurp's output: an array of pages, each an array of items.
func readPages[T any](path string) ([]T, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var pages [][]T
	if err := json.Unmarshal(b, &pages); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var all []T
	for _, p := range pages {
		all = append(all, p...)
	}
	return all, nil
}

// check returns the violations, one line each, in rule order and then version order.
func check(repo string, tags []string, releases []release, now time.Time) []string {
	var out []string
	slices.SortFunc(tags, func(a, b string) int {
		va, oka := parseVersion(a)
		vb, okb := parseVersion(b)
		if oka && okb {
			return compare(va, vb)
		}
		return cmp.Compare(a, b)
	})
	byTag := map[string][]release{}
	for _, r := range releases {
		byTag[r.TagName] = append(byTag[r.TagName], r)
	}

	// R1
	for _, tag := range tags {
		v, ok := parseVersion(tag)
		if !ok {
			out = append(out, fmt.Sprintf("R1 %s %s: not a vX.Y.Z[-rcN] tag", repo, tag))
			continue
		}
		rs := byTag[tag]
		switch {
		case len(rs) > 0: // a published release, or a draft in flight that R3 governs
		case v.isRC():
		default:
			out = append(out, fmt.Sprintf("R1 %s %s: tag has no release", repo, tag))
		}
	}

	// R2: published, not rc (by tag or by the prerelease flag), in version order.
	type pub struct {
		tag string
		v   version
		at  time.Time
	}
	var pubs []pub
	for _, r := range releases {
		v, ok := parseVersion(r.TagName)
		if r.Draft || r.Prerelease || !ok || v.isRC() {
			continue
		}
		pubs = append(pubs, pub{r.TagName, v, r.PublishedAt})
	}
	slices.SortFunc(pubs, func(a, b pub) int { return compare(a.v, b.v) })
	for i := 1; i < len(pubs); i++ {
		lo, hi := pubs[i-1], pubs[i]
		if lo.at.After(hi.at) {
			out = append(out, fmt.Sprintf("R2 %s %s: published %s, after %s (%s)", repo, lo.tag, lo.at.UTC().Format(time.RFC3339), hi.tag, hi.at.UTC().Format(time.RFC3339)))
		}
	}

	// R3
	var drafts []release
	for _, r := range releases {
		if r.Draft && now.Sub(r.draftTime()) > draftMaxAge {
			drafts = append(drafts, r)
		}
	}
	slices.SortFunc(drafts, func(a, b release) int { return a.draftTime().Compare(b.draftTime()) })
	for _, r := range drafts {
		at := r.draftTime()
		out = append(out, fmt.Sprintf("R3 %s %s: draft release last touched %s, %s old (limit %s)", repo, r.TagName, at.UTC().Format(time.RFC3339), now.Sub(at).Truncate(time.Minute), draftMaxAge))
	}
	return out
}

// run is main without the process exit: the exit code, violations on stdout, errors on stderr.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("publishcheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	repo := fs.String("repo", "", "the repository, owner/name, for the messages")
	tagsFile := fs.String("tags", "", "gh api --paginate --slurp repos/R/git/matching-refs/tags/v output")
	relFile := fs.String("releases", "", "gh api --paginate --slurp repos/R/releases output")
	nowFlag := fs.String("now", "", "the time to measure draft age against, RFC3339 (default: now)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *tagsFile == "" || *relFile == "" {
		fmt.Fprintln(stderr, "usage: publishcheck -repo <owner/name> -tags tags.json -releases releases.json [-now RFC3339]")
		return 2
	}
	now := time.Now()
	if *nowFlag != "" {
		var err error
		if now, err = time.Parse(time.RFC3339, *nowFlag); err != nil {
			fmt.Fprintln(stderr, "publishcheck: -now:", err)
			return 2
		}
	}
	refs, err := readPages[struct {
		Ref string `json:"ref"`
	}](*tagsFile)
	if err != nil {
		fmt.Fprintln(stderr, "publishcheck:", err)
		return 2
	}
	releases, err := readPages[release](*relFile)
	if err != nil {
		fmt.Fprintln(stderr, "publishcheck:", err)
		return 2
	}
	var tags []string
	for _, r := range refs {
		tags = append(tags, strings.TrimPrefix(r.Ref, "refs/tags/"))
	}
	lines := check(*repo, tags, releases, now)
	for _, l := range lines {
		fmt.Fprintln(stdout, l)
	}
	if len(lines) > 0 {
		return 1
	}
	return 0
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
