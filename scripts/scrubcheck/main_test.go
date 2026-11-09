package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

var testWords = []string{"herd", "lane", "coordinator", "rangehand", "homerange", "herdr", "theherd"}

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
		{"lead to it", nil},
		{"the shepherd walks", nil},
		{"a herdsman", nil},
		{"the lanes are full", []string{"lanes"}},
		{"Herds of them", []string{"herds"}},
		{"THEHERD", []string{"theherd"}},
		{"herdr and herdrs", []string{"herdr", "herdrs"}},
		{"herd-x and my_lane", []string{"herd", "lane"}},
		{"Lane, coordinator; rangehand/homerange", []string{"lane", "coordinator", "rangehand", "homerange"}},
		{"operator fleet", nil},
		{"lane2 lanex", nil},
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
	hs := scanText("f", []byte("ok\nxx lane herd\nlane\n"), testWords)
	if len(hs) != 3 || hs[0].line != 2 || hs[0].word != "lane" || hs[1].word != "herd" || hs[2].line != 3 {
		t.Fatalf("bad hits: %+v", hs)
	}
	if hs[0].col >= hs[1].col {
		t.Errorf("columns not increasing: %+v", hs)
	}
}

func TestBinaryPrintableStrings(t *testing.T) {
	data := append([]byte{0x7f, 'E', 'L', 'F', 0, 1, 2}, []byte("\x00\x01the lane runs\x00\x02ab herd\x00")...)
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

const wordsFile = "# list\n\nherd\nlane\ncoordinator\nrangehand\nhomerange\nherdr\ntheherd\n"

func runIn(t *testing.T, root string, extra ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := run(append([]string{"-root", root}, extra...), &out)
	return out.String(), err
}

func TestRunGitListingAndSelfSkip(t *testing.T) {
	root := gitRepo(t, map[string]string{
		"a.md":                          "one\nthe lane\n",
		"sub/b.txt":                     "herd herd\n",
		"clean.txt":                     "nothing here\n",
		"scripts/scrubcheck/words.txt":  wordsFile,
		"scripts/scrubcheck/fixture.md": "lane herd coordinator\n",
	})
	// an untracked file must not be scanned
	writeFile(t, root, "untracked.txt", "lane\n")
	out, err := runIn(t, root)
	if err == nil {
		t.Fatal("want error for hits")
	}
	want := "a.md:2:lane\nsub/b.txt:1:herd\nsub/b.txt:1:herd\n3 hits, 0 excepted, 0 exception problems\n"
	if out != want {
		t.Errorf("out:\n%q\nwant:\n%q", out, want)
	}
}

func TestRunClean(t *testing.T) {
	root := gitRepo(t, map[string]string{
		"a.md":                         "operator and fleet and shepherd\n",
		"scripts/scrubcheck/words.txt": wordsFile,
	})
	out, err := runIn(t, root)
	if err != nil {
		t.Fatalf("err %v\n%s", err, out)
	}
	if !strings.HasSuffix(out, "0 hits, 0 excepted, 0 exception problems\n") {
		t.Errorf("out %q", out)
	}
}

func exceptionsRoot(t *testing.T, tsv string) string {
	return gitRepo(t, map[string]string{
		"a.md":                              "the lane is here\nlane again\n",
		"scripts/scrubcheck/words.txt":      wordsFile,
		"scripts/scrubcheck/exceptions.tsv": tsv,
	})
}

func TestExceptionApplied(t *testing.T) {
	root := exceptionsRoot(t, "# c\na.md\tlane is here\tquoted on purpose\n")
	out, err := runIn(t, root)
	if err == nil {
		t.Fatal("other occurrence must still fail")
	}
	want := "a.md:2:lane\n2 hits, 1 excepted, 0 exception problems\n"
	if out != want {
		t.Errorf("out %q want %q", out, want)
	}
}

func TestExceptionsAllCoveredIsSuccess(t *testing.T) {
	root := exceptionsRoot(t, "a.md\tlane\twhy\n")
	// pattern "lane" is on both lines, so one entry covers both hits
	out, err := runIn(t, root)
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
		"a.md\tlane is\t",                // 2: no reason
		"a.md\tno such text\tbecause",    // 3: matches nothing
		"other.md\tlane\tbecause",        // 4: matches nothing
		"only two\tfields",               // 5: malformed
		"scripts/\tlane\twhole dir",      // 6: directory
		"a.md",                           // 7: malformed (one field)
		"*.md\tlane\twhole glob",         // 8: glob
		"docs/[ab].md\tlane\tglob class", // 9: glob
		"",
		"x?.md\tlane\tglob q", // 11: glob
	}, "\n") + "\n"
	root := exceptionsRoot(t, tsv)
	out, err := runIn(t, root)
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
	if strings.Contains(out, "a.md:1:lane") {
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
	root := gitRepo(t, map[string]string{"scripts/scrubcheck/words.txt": wordsFile})
	dir := t.TempDir()
	members := map[string]string{
		"pkg/README.md": "ok\nthe coordinator\n",
		"pkg/clean.txt": "fine\n",
		"pkg/bin":       "\x00\x01\x02 homerange here \x00",
	}
	tgz := filepath.Join(dir, "rel.tgz")
	zp := filepath.Join(dir, "rel.zip")
	bare := filepath.Join(dir, "mashbin")
	writeFile(t, dir, "rel.tgz", string(buildTarGz(t, members)))
	writeFile(t, dir, "rel.zip", string(buildZip(t, members)))
	writeFile(t, dir, "mashbin", "\x00\x00 the herd \x00")
	out, err := runIn(t, root, "-archive", tgz, "-archive", zp, "-archive", bare)
	if err == nil {
		t.Fatal("want hits")
	}
	for _, w := range []string{
		tgz + "!pkg/README.md:2:coordinator\n",
		tgz + "!pkg/bin:strings:homerange\n",
		zp + "!pkg/README.md:2:coordinator\n",
		zp + "!pkg/bin:strings:homerange\n",
		bare + ":strings:herd\n",
		"5 hits, 0 excepted, 0 exception problems\n",
	} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q in:\n%s", w, out)
		}
	}
	// exceptions name a member as archive!member
	writeFile(t, root, "scripts/scrubcheck/exceptions.tsv", tgz+"!pkg/README.md\tthe coordinator\tquoted\n")
	out, _ = runIn(t, root, "-archive", tgz)
	if strings.Contains(out, "README.md:") || !strings.Contains(out, "1 excepted") {
		t.Errorf("member exception not applied:\n%s", out)
	}
	// plain .tar
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: "m.txt", Mode: 0o644, Size: 4, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("lane")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "x.tar", buf.String())
	tarp := filepath.Join(dir, "x.tar")
	out, _ = runIn(t, root, "-archive", tarp, "-exceptions", filepath.Join(dir, "none.tsv"))
	if !strings.Contains(out, tarp+"!m.txt:1:lane\n") {
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

func TestCheckedInWords(t *testing.T) {
	ws, err := parseWords(repoFile(t, "scripts/scrubcheck/words.txt"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"herd", "lane", "coordinator", "rangehand", "homerange", "herdr", "theherd"}
	if !reflect.DeepEqual(ws, want) {
		t.Errorf("words %v want %v", ws, want)
	}
	for _, w := range ws {
		if w == "operator" || w == "fleet" {
			t.Errorf("%q must not be listed", w)
		}
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
		"a.md":                         "quoted herd; accidental coordinator\n",
		"scripts/scrubcheck/words.txt": wordsFile,
	})
	writeFile(t, root, "scripts/scrubcheck/exceptions.tsv", "a.md\tquoted herd\tquoted\n")
	out, err := runIn(t, root)
	if err == nil {
		t.Fatal("coordinator must still be reported")
	}
	want := "a.md:1:coordinator\n2 hits, 1 excepted, 0 exception problems\n"
	if out != want {
		t.Errorf("out %q want %q", out, want)
	}
	// a pattern that does not contain the word's span does not cover a word after it
	writeFile(t, root, "scripts/scrubcheck/exceptions.tsv", "a.md\tquoted \tnope\n")
	out, _ = runIn(t, root)
	for _, w := range []string{"a.md:1:herd\n", "a.md:1:coordinator\n", "exceptions.tsv:1: matches nothing\n", "2 hits, 0 excepted, 1 exception problems\n"} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q in:\n%s", w, out)
		}
	}
}

func TestExceptionCoversTwoOccurrencesInOnePattern(t *testing.T) {
	root := gitRepo(t, map[string]string{
		"a.md":                         "herd and herd\n",
		"scripts/scrubcheck/words.txt": wordsFile,
	})
	writeFile(t, root, "scripts/scrubcheck/exceptions.tsv", "a.md\therd and herd\tquoted\n")
	out, err := runIn(t, root)
	if err != nil || !strings.HasSuffix(out, "2 hits, 2 excepted, 0 exception problems\n") {
		t.Errorf("err %v out %q", err, out)
	}
}

func TestBinaryExceptionUsesRunOffset(t *testing.T) {
	root := gitRepo(t, map[string]string{
		"scripts/scrubcheck/words.txt": wordsFile,
		"b.bin":                        "\x00\x01xx quoted herd\x00\x02lane here\x00",
	})
	writeFile(t, root, "scripts/scrubcheck/exceptions.tsv", "b.bin\tquoted herd\tquoted\n")
	out, _ := runIn(t, root)
	if out != "b.bin:strings:lane\n2 hits, 1 excepted, 0 exception problems\n" {
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
		"real.txt":                     "ok\nthe lane\n",
		"scripts/scrubcheck/words.txt": wordsFile,
	}, map[string]string{"link.txt": "real.txt"})
	out, err := runIn(t, root)
	if err == nil {
		t.Fatal("want hits")
	}
	for _, w := range []string{"link.txt:2:lane\n", "real.txt:2:lane\n"} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q in:\n%s", w, out)
		}
	}
}

func TestDanglingSymlinkIsError(t *testing.T) {
	root := gitRepoLinks(t, map[string]string{
		"scripts/scrubcheck/words.txt": wordsFile,
	}, map[string]string{"dead.txt": "nowhere.txt"})
	_, err := runIn(t, root)
	var u errUsage
	if !errors.As(err, &u) || !strings.Contains(err.Error(), "dead.txt") {
		t.Errorf("want usage error naming dead.txt, got %v", err)
	}
}

func TestBinaryUTF8Letters(t *testing.T) {
	if hs := scanText("b", []byte("\x00lane\u00e9\x00"), testWords); len(hs) != 0 {
		t.Errorf("laneé must not hit: %+v", hs)
	}
	if hs := scanText("b", []byte("\x00lane \x00"), testWords); len(hs) != 1 {
		t.Errorf("lane must hit: %+v", hs)
	}
}
