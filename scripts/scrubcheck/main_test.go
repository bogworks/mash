package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

var testWords = []string{"alpha", "beta", "gamma", "delta", "epsilon", "alphar", "thealpha"}

func hitWords(hs []hit) []string {
	var out []string
	for _, h := range hs {
		out = append(out, h.word)
	}
	return out
}

func TestMatchWholeWord(t *testing.T) {
	cases := []struct {
		line string
		want []string
	}{
		{"for range x {}", nil},
		{"bet on it", nil},
		{"the shepalpha walks", nil},
		{"a alphasman", nil},
		{"the betas are full", []string{"betas"}},
		{"Alphas of them", []string{"alphas"}},
		{"THEALPHA", []string{"thealpha"}},
		{"alphar and alphars", []string{"alphar", "alphars"}},
		{"alpha-x and my_beta", []string{"alpha", "beta"}},
		{"Beta, gamma; delta/epsilon", []string{"beta", "gamma", "delta", "epsilon"}},
		{"operator fleet", nil},
		{"beta2 betax", nil},
		{"", nil},
	}
	for _, c := range cases {
		got := hitWords(scanText("f", []byte(c.line), testWords))
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: got %v want %v", c.line, got, c.want)
		}
	}
}

func TestScanTextPositions(t *testing.T) {
	hs := scanText("f", []byte("ok\nxx beta alpha\nbeta\n"), testWords)
	if len(hs) != 3 || hs[0].line != 2 || hs[0].word != "beta" || hs[1].word != "alpha" || hs[2].line != 3 {
		t.Fatalf("bad hits: %+v", hs)
	}
	if hs[0].col >= hs[1].col {
		t.Errorf("columns not increasing: %+v", hs)
	}
}

func TestBinaryPrintableStrings(t *testing.T) {
	data := append([]byte{0x7f, 'E', 'L', 'F', 0, 1, 2}, []byte("\x00\x01the beta runs\x00\x02ab alpha\x00")...)
	hs := scanText("bin", data, testWords)
	if len(hs) != 2 {
		t.Fatalf("hits: %+v", hs)
	}
	for _, h := range hs {
		if h.line != 0 || formatHit(h) != "bin:strings:"+h.word {
			t.Errorf("format: %q", formatHit(h))
		}
	}
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// gitRepo makes a real temporary git repository with every file tracked, so run goes through git ls-files.
func gitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	return gitRepoLinks(t, files, nil)
}

// gitRepoLinks is gitRepo plus symlinks (path -> target text), made before everything is added.
func gitRepoLinks(t *testing.T, files map[string]string, links map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	for rel, c := range files {
		writeFile(t, root, rel, c)
	}
	for rel, target := range links {
		if err := os.Symlink(target, filepath.Join(root, rel)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return root
}

// wordsFile is the neutral fixture word list: none of its words is on the real scrub list. alphar and thealpha stand in
// for a word that extends another listed word at its end or its start.
const wordsFile = "# list\n\nalpha\nbeta\ngamma\ndelta\nepsilon\nalphar\nthealpha\n"

// fixtureFile writes content as name in a fresh temp directory (outside any scanned repository) and returns its path.
func fixtureFile(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, name, content)
	return filepath.Join(dir, name)
}

// runIn runs the tool over root with the fixture word list and no exceptions file, unless extra gives -exceptions.
func runIn(t *testing.T, root string, extra ...string) (string, error) {
	t.Helper()
	args := []string{"-root", root, "-words", fixtureFile(t, "words.txt", wordsFile)}
	hasExc := false
	for _, a := range extra {
		hasExc = hasExc || a == "-exceptions"
	}
	if !hasExc {
		args = append(args, "-exceptions", filepath.Join(t.TempDir(), "none.tsv"))
	}
	var out bytes.Buffer
	err := run(append(args, extra...), &out)
	return out.String(), err
}

// runExc is runIn with an exceptions file holding tsv, named exceptions.tsv.
func runExc(t *testing.T, root, tsv string, extra ...string) (string, error) {
	t.Helper()
	return runIn(t, root, append([]string{"-exceptions", fixtureFile(t, "exceptions.tsv", tsv)}, extra...)...)
}

func TestRunGitListingScansOwnDirectory(t *testing.T) {
	root := gitRepo(t, map[string]string{
		"a.md":                          "one\nthe beta\n",
		"sub/b.txt":                     "alpha alpha\n",
		"clean.txt":                     "nothing here\n",
		"scripts/scrubcheck/fixture.md": "beta alpha gamma\n",
	})
	// an untracked file must not be scanned
	writeFile(t, root, "untracked.txt", "beta\n")
	out, err := runIn(t, root)
	if err == nil {
		t.Fatal("want error for hits")
	}
	// the tool's own directory is scanned like any other
	want := "a.md:2:beta\nscripts/scrubcheck/fixture.md:1:beta\nscripts/scrubcheck/fixture.md:1:alpha\nscripts/scrubcheck/fixture.md:1:gamma\n" +
		"sub/b.txt:1:alpha\nsub/b.txt:1:alpha\n6 hits, 0 excepted, 0 exception problems\n"
	if out != want {
		t.Errorf("out:\n%q\nwant:\n%q", out, want)
	}
}

func TestRunClean(t *testing.T) {
	root := gitRepo(t, map[string]string{
		"a.md": "operator and fleet and shepalpha\n",
	})
	out, err := runIn(t, root)
	if err != nil {
		t.Fatalf("err %v\n%s", err, out)
	}
	if !strings.HasSuffix(out, "0 hits, 0 excepted, 0 exception problems\n") {
		t.Errorf("out %q", out)
	}
}

func exceptionsRoot(t *testing.T) string {
	return gitRepo(t, map[string]string{"a.md": "the beta is here\nbeta again\n"})
}

func TestExceptionApplied(t *testing.T) {
	root := exceptionsRoot(t)
	tsv := "# c\na.md\tbeta is here\tquoted on purpose\n"
	out, err := runExc(t, root, tsv)
	if err == nil {
		t.Fatal("other occurrence must still fail")
	}
	want := "a.md:2:beta\n2 hits, 1 excepted, 0 exception problems\n"
	if out != want {
		t.Errorf("out %q want %q", out, want)
	}
}

func TestExceptionsAllCoveredIsSuccess(t *testing.T) {
	root := exceptionsRoot(t)
	tsv := "a.md\tbeta\twhy\n"
	// pattern "beta" is on both lines, so one entry covers both hits
	out, err := runExc(t, root, tsv)
	if err != nil {
		t.Fatalf("err %v\n%s", err, out)
	}
	if !strings.HasSuffix(out, "2 hits, 2 excepted, 0 exception problems\n") {
		t.Errorf("out %q", out)
	}
}

func TestExceptionProblems(t *testing.T) {
	tsv := strings.Join([]string{
		"# header",
		"a.md\tbeta is\t",                // 2: no reason
		"a.md\tno such text\tbecause",    // 3: matches nothing
		"other.md\tbeta\tbecause",        // 4: matches nothing
		"only two\tfields",               // 5: malformed
		"scripts/\tbeta\twhole dir",      // 6: directory
		"a.md",                           // 7: malformed (one field)
		"*.md\tbeta\twhole glob",         // 8: glob
		"docs/[ab].md\tbeta\tglob class", // 9: glob
		"",
		"x?.md\tbeta\tglob q", // 11: glob
	}, "\n") + "\n"
	root := exceptionsRoot(t)
	out, err := runExc(t, root, tsv)
	if err == nil {
		t.Fatal("want error")
	}
	for _, w := range []string{
		"exceptions.tsv:2: no reason\n",
		"exceptions.tsv:3: matches nothing\n",
		"exceptions.tsv:4: matches nothing\n",
		"exceptions.tsv:5: malformed entry",
		"exceptions.tsv:6: whole-file or whole-directory entry",
		"exceptions.tsv:7: malformed entry",
		"exceptions.tsv:8: whole-file or whole-directory entry",
		"exceptions.tsv:9: whole-file or whole-directory entry",
		"exceptions.tsv:11: whole-file or whole-directory entry",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q in:\n%s", w, out)
		}
	}
	if !strings.Contains(out, "9 exception problems") {
		t.Errorf("summary: %s", out)
	}
	// the no-reason entry still covers its hit
	if strings.Contains(out, "a.md:1:beta") {
		t.Errorf("no-reason entry should still cover its line:\n%s", out)
	}
}

func TestParseExceptions(t *testing.T) {
	es, ps := parseExceptions("x.md\tpat\treason here\n# c\n\n")
	if len(ps) != 0 || len(es) != 1 || es[0].path != "x.md" || es[0].pattern != "pat" || es[0].reason != "reason here" || es[0].line != 1 {
		t.Fatalf("%+v %+v", es, ps)
	}
}

func buildTarGz(t *testing.T, members map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, c := range members {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(c)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(c)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func buildZip(t *testing.T, members map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, c := range members {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(c)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestArchives(t *testing.T) {
	root := gitRepo(t, map[string]string{"README.md": "nothing\n"})
	dir := t.TempDir()
	members := map[string]string{
		"pkg/README.md": "ok\nthe gamma\n",
		"pkg/clean.txt": "fine\n",
		"pkg/bin":       "\x00\x01\x02 epsilon here \x00",
	}
	tgz := filepath.Join(dir, "rel.tgz")
	zp := filepath.Join(dir, "rel.zip")
	bare := filepath.Join(dir, "mashbin")
	writeFile(t, dir, "rel.tgz", string(buildTarGz(t, members)))
	writeFile(t, dir, "rel.zip", string(buildZip(t, members)))
	writeFile(t, dir, "mashbin", "\x00\x00 the alpha \x00")
	out, err := runIn(t, root, "-archive", tgz, "-archive", zp, "-archive", bare)
	if err == nil {
		t.Fatal("want hits")
	}
	for _, w := range []string{
		tgz + "!pkg/README.md:2:gamma\n",
		tgz + "!pkg/bin:strings:epsilon\n",
		zp + "!pkg/README.md:2:gamma\n",
		zp + "!pkg/bin:strings:epsilon\n",
		bare + ":strings:alpha\n",
		"5 hits, 0 excepted, 0 exception problems\n",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q in:\n%s", w, out)
		}
	}
	// exceptions name a member as archive!member
	tsv := tgz + "!pkg/README.md\tthe gamma\tquoted\n"
	out, _ = runExc(t, root, tsv, "-archive", tgz)
	if strings.Contains(out, "README.md:") || !strings.Contains(out, "1 excepted") {
		t.Errorf("member exception not applied:\n%s", out)
	}
	// plain .tar
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: "m.txt", Mode: 0o644, Size: 4, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("beta")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "x.tar", buf.String())
	tarp := filepath.Join(dir, "x.tar")
	out, _ = runIn(t, root, "-archive", tarp, "-exceptions", filepath.Join(dir, "none.tsv"))
	if !strings.Contains(out, tarp+"!m.txt:1:beta\n") {
		t.Errorf("tar: %s", out)
	}
}

func TestUsageErrors(t *testing.T) {
	var out bytes.Buffer
	if err := run([]string{"-bogus"}, &out); err == nil {
		t.Error("bad flag should error")
	}
	if err := run([]string{"-root", t.TempDir(), "-words", filepath.Join(t.TempDir(), "nope.txt")}, &out); err == nil {
		t.Error("missing words file should error")
	}
}

func repoFile(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// normalisedWords is the word list as the hash pins it: trimmed non-comment, non-blank lines, lower-cased, joined by \n.
func normalisedWords(content string) string {
	var ls []string
	for _, l := range strings.Split(content, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		ls = append(ls, strings.ToLower(l))
	}
	return strings.Join(ls, "\n")
}

// ruledWordsSHA256 is the SHA-256 of the normalised, ruled scrub list (seven words; "operator" and "fleet" are not on
// it). The test pins the list by hash so that this file does not itself contain the words.
const ruledWordsSHA256 = "dd69de23fe5da6d8f82b77f413ad40d1a26f142a8e00c968fbb2f8c54c9dcc3d"

func TestCheckedInWordsAreExactlyTheRuledList(t *testing.T) {
	sum := sha256.Sum256([]byte(normalisedWords(repoFile(t, "scripts/scrubcheck/words.txt"))))
	if got := hex.EncodeToString(sum[:]); got != ruledWordsSHA256 {
		t.Errorf("words.txt normalised SHA-256 %s, want the ruled list %s", got, ruledWordsSHA256)
	}
}

func TestCheckedInWordsParse(t *testing.T) {
	ws, err := parseWords(repoFile(t, "scripts/scrubcheck/words.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 7 {
		t.Errorf("want 7 words, got %d", len(ws))
	}
}

func TestDecodePattern(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain", "plain"},
		{`\x61lpha`, "alpha"},
		{`\x41lpha`, "Alpha"},
		{`\x4a\x4A`, "JJ"}, // either case of hex digit
		{`a\x20b`, "a b"},
		{`\x61lpha \x62eta`, "alpha beta"},
		{`\x6`, `\x6`},       // short escape is literal
		{`\x`, `\x`},         // no digits
		{`\xZZ`, `\xZZ`},     // not hex
		{`\x6Z`, `\x6Z`},     // second digit not hex
		{`\y61`, `\y61`},     // backslash not followed by x
		{`a\`, `a\`},         // trailing backslash
		{`\\x61`, `\a`},      // a literal backslash then a valid escape
		{`\X61`, `\X61`},     // capital X is not an escape
		{`end\x6`, `end\x6`}, // short at the very end
		{`x\x61`, "xa"},
	}
	for _, c := range cases {
		if got := decodePattern(c.in); got != c.want {
			t.Errorf("decodePattern(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseExceptionsDecodesPattern(t *testing.T) {
	es, ps := parseExceptions("a.md\t\\x61lpha\twhy\n")
	if len(ps) != 0 || len(es) != 1 || es[0].pattern != "alpha" {
		t.Fatalf("%+v %+v", es, ps)
	}
}

func TestEscapedExceptionMatchesIntendedHit(t *testing.T) {
	root := gitRepo(t, map[string]string{"a.md": "alpha here\nbeta here\n"})
	out, err := runExc(t, root, "a.md\t\\x61lpha\tescaped\n")
	if err == nil {
		t.Fatal("beta must still be reported")
	}
	if want := "a.md:2:beta\n2 hits, 1 excepted, 0 exception problems\n"; out != want {
		t.Errorf("out %q want %q", out, want)
	}
}

func TestEscapedExceptionThatMatchesNothingIsReported(t *testing.T) {
	root := gitRepo(t, map[string]string{"a.md": "alpha here\n"})
	out, _ := runExc(t, root, "a.md\t\\x7aeta\tstale\n")
	for _, w := range []string{"a.md:1:alpha\n", "exceptions.tsv:1: matches nothing\n", "1 hits, 0 excepted, 1 exception problems\n"} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q in:\n%s", w, out)
		}
	}
}

func TestEscapedExceptionsFileInTheScannedTreeIsClean(t *testing.T) {
	// the point of the escape: an exceptions file, tracked and scanned, whose entry names a listed word holds no hit
	tsv := "a.md\t\\x61lpha\tthe fixture\n"
	root := gitRepo(t, map[string]string{"a.md": "alpha here\n", "scripts/scrubcheck/exceptions.tsv": tsv})
	out, err := runIn(t, root, "-exceptions", filepath.Join(root, "scripts/scrubcheck/exceptions.tsv"))
	if err != nil || out != "1 hits, 1 excepted, 0 exception problems\n" {
		t.Errorf("err %v out %q", err, out)
	}
}

func TestCheckedInExceptionsParse(t *testing.T) {
	_, ps := parseExceptions(repoFile(t, "scripts/scrubcheck/exceptions.tsv"))
	if len(ps) != 0 {
		t.Errorf("problems: %v", ps)
	}
}

func TestExceptionCoversOnlyItsOwnSpan(t *testing.T) {
	root := gitRepo(t, map[string]string{
		"a.md": "quoted alpha; accidental gamma\n",
	})
	tsv := "a.md\tquoted alpha\tquoted\n"
	out, err := runExc(t, root, tsv)
	if err == nil {
		t.Fatal("gamma must still be reported")
	}
	want := "a.md:1:gamma\n2 hits, 1 excepted, 0 exception problems\n"
	if out != want {
		t.Errorf("out %q want %q", out, want)
	}
	// a pattern that does not contain the word's span does not cover a word after it
	tsv = "a.md\tquoted \tnope\n"
	out, _ = runExc(t, root, tsv)
	for _, w := range []string{"a.md:1:alpha\n", "a.md:1:gamma\n", "exceptions.tsv:1: matches nothing\n", "2 hits, 0 excepted, 1 exception problems\n"} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q in:\n%s", w, out)
		}
	}
}

func TestExceptionCoversTwoOccurrencesInOnePattern(t *testing.T) {
	root := gitRepo(t, map[string]string{
		"a.md": "alpha and alpha\n",
	})
	tsv := "a.md\talpha and alpha\tquoted\n"
	out, err := runExc(t, root, tsv)
	if err != nil || !strings.HasSuffix(out, "2 hits, 2 excepted, 0 exception problems\n") {
		t.Errorf("err %v out %q", err, out)
	}
}

func TestBinaryExceptionUsesRunOffset(t *testing.T) {
	root := gitRepo(t, map[string]string{
		"b.bin": "\x00\x01xx quoted alpha\x00\x02beta here\x00",
	})
	tsv := "b.bin\tquoted alpha\tquoted\n"
	out, _ := runExc(t, root, tsv)
	if out != "b.bin:strings:beta\n2 hits, 1 excepted, 0 exception problems\n" {
		t.Errorf("out %q", out)
	}
}

func TestStrayPositionalArgIsUsageError(t *testing.T) {
	var out bytes.Buffer
	err := run([]string{"stray", "-archive", "x.tgz"}, &out)
	var u errUsage
	if !errors.As(err, &u) || !strings.Contains(err.Error(), "stray") {
		t.Errorf("want usage error naming stray, got %v", err)
	}
}

func TestTrackedSymlinkFollowed(t *testing.T) {
	root := gitRepoLinks(t, map[string]string{
		"real.txt": "ok\nthe beta\n",
	}, map[string]string{"link.txt": "real.txt"})
	out, err := runIn(t, root)
	if err == nil {
		t.Fatal("want hits")
	}
	for _, w := range []string{"link.txt:2:beta\n", "real.txt:2:beta\n"} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q in:\n%s", w, out)
		}
	}
}

func TestDanglingSymlinkIsError(t *testing.T) {
	root := gitRepoLinks(t, map[string]string{}, map[string]string{"dead.txt": "nowhere.txt"})
	_, err := runIn(t, root)
	var u errUsage
	if !errors.As(err, &u) || !strings.Contains(err.Error(), "dead.txt") {
		t.Errorf("want usage error naming dead.txt, got %v", err)
	}
}

func TestBinaryUTF8Letters(t *testing.T) {
	if hs := scanText("b", []byte("\x00beta\u00e9\x00"), testWords); len(hs) != 0 {
		t.Errorf("betaé must not hit: %+v", hs)
	}
	if hs := scanText("b", []byte("\x00beta \x00"), testWords); len(hs) != 1 {
		t.Errorf("beta must hit: %+v", hs)
	}
}

func TestExceptionMayNotCoverTheExceptionsFile(t *testing.T) {
	const self = "scripts/scrubcheck/exceptions.tsv"
	tsv := self + "\talpha\thide the entry\n"
	root := gitRepo(t, map[string]string{self: tsv})
	// the flag names the file by a path that only cleans to the repo-relative one
	out, err := runIn(t, root, "-exceptions", filepath.Join(root, "scripts", "x", "..", "scrubcheck", "exceptions.tsv"))
	if err == nil {
		t.Fatal("want error")
	}
	want := self + ":1:alpha\n" +
		"exceptions.tsv:1: an exception may not cover the exceptions file itself; write the pattern with \\xHH escapes\n" +
		"1 hits, 0 excepted, 1 exception problems\n"
	if out != want {
		t.Errorf("out %q want %q", out, want)
	}
}

func TestParseWordsRejectsDuplicates(t *testing.T) {
	_, err := parseWords("alpha\nbeta\nALPHA\n")
	if err == nil || !strings.Contains(err.Error(), "alpha") || !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("err %v", err)
	}
	if _, err := parseWords("alpha\nbeta\n"); err != nil {
		t.Errorf("err %v", err)
	}
}
