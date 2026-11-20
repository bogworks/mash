package scripts

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// install.sh is run against a fake release directory through a file:// base, with a PATH that holds only the tools
// it needs (so a gh on the host never leaks in), HOME and the install folder in a temp dir. Nothing is downloaded.

const (
	fakeMashVersion = "mash v0.5.6 (3f9a41c)"
	fakeCtlVersion  = "mashctl 0.5.6"
)

func installTools(t *testing.T, extra map[string]string) string {
	t.Helper()
	bin := t.TempDir()
	for _, tool := range []string{"bash", "curl", "awk", "sha256sum", "mktemp", "tr", "head", "ls", "dirname", "mkdir", "cp", "chmod", "mv", "rm", "rmdir", "cat", "uname"} {
		if _, ok := extra[tool]; ok {
			continue
		}
		p, err := exec.LookPath(tool)
		if err != nil {
			if tool == "sha256sum" { // macOS has shasum instead
				if p, err = exec.LookPath("shasum"); err == nil {
					tool = "shasum"
				}
			}
			if err != nil {
				t.Skipf("%s not found", tool)
			}
		}
		if err := os.Symlink(p, filepath.Join(bin, tool)); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range extra {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return bin
}

// tempHome is a fresh home folder with mode 0755 (t.TempDir follows the umask, which may leave it group-writable).
func tempHome(t *testing.T) string {
	t.Helper()
	h := t.TempDir()
	if err := os.Chmod(h, 0o755); err != nil {
		t.Fatal(err)
	}
	return h
}

func hostPlatform(t *testing.T) (string, string) {
	t.Helper()
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("install.sh supports linux and darwin")
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skip("install.sh supports amd64 and arm64")
	}
	return runtime.GOOS, runtime.GOARCH
}

// fakeRelease makes a release directory: fake mash and mashctl for the host, a real SHA256SUMS, and a bundle file.
func fakeRelease(t *testing.T) (dir, mashName, ctlName string) {
	t.Helper()
	goos, goarch := hostPlatform(t)
	dir = t.TempDir()
	mashName = "mash-" + goos + "-" + goarch
	ctlName = "mashctl-" + goos + "-" + goarch
	files := map[string]string{
		mashName: "#!/bin/sh\n[ \"$1\" = version ] && echo '" + fakeMashVersion + "'\nexit 0\n",
		ctlName:  "#!/bin/sh\n[ \"$1\" = version ] && echo '" + fakeCtlVersion + "'\nexit 0\n",
	}
	var sums strings.Builder
	for _, n := range []string{mashName, ctlName} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(files[n]), 0o644); err != nil {
			t.Fatal(err)
		}
		h := sha256.Sum256([]byte(files[n]))
		fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(h[:]), n)
	}
	writeFile(t, filepath.Join(dir, "SHA256SUMS"), sums.String())
	writeFile(t, filepath.Join(dir, "mash.intoto.jsonl"), "{}\n")
	return dir, mashName, ctlName
}

func writeFile(t *testing.T, p, body string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

type installRun struct {
	out  string
	err  error
	home string
	dest string
}

// runInstall runs the script (path, or "" for scripts/install.sh) with the given tool dir as PATH.
func runInstall(t *testing.T, release, toolPath string, env ...string) installRun {
	t.Helper()
	home := tempHome(t)
	dest := filepath.Join(home, "bin")
	return runInstallIn(t, release, toolPath, home, dest, "install.sh", env...)
}

func runInstallIn(t *testing.T, release, toolPath, home, dest, script string, env ...string) installRun {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	cmd := exec.Command(bash, script) // go test runs in this directory
	cmd.Env = append([]string{
		"PATH=" + toolPath,
		"HOME=" + home,
		"TMPDIR=" + t.TempDir(),
		"MASH_RELEASE_BASE=file://" + release,
		"MASH_INSTALL_DIR=" + dest,
	}, env...)
	out, err := cmd.CombinedOutput()
	return installRun{out: string(out), err: err, home: home, dest: dest}
}

func assertNothingInstalled(t *testing.T, dest string) {
	t.Helper()
	entries, err := os.ReadDir(dest)
	if err != nil {
		return // no folder at all is nothing installed
	}
	for _, e := range entries {
		t.Errorf("%s holds %s, want nothing installed", dest, e.Name())
	}
}

func TestInstallSh_installsBothAndPrintsOneLineEach(t *testing.T) {
	rel, _, _ := fakeRelease(t)
	tools := installTools(t, nil)
	r := runInstall(t, rel, tools)
	if r.err != nil {
		t.Fatalf("install failed: %v\n%s", r.err, r.out)
	}
	for _, n := range []string{"mash", "mashctl"} {
		fi, err := os.Stat(filepath.Join(r.dest, n))
		if err != nil {
			t.Fatalf("%s not installed: %v", n, err)
		}
		if fi.Mode().Perm() != 0o755 {
			t.Errorf("%s has mode %v, want 0755", n, fi.Mode().Perm())
		}
	}
	if entries, _ := os.ReadDir(r.dest); len(entries) != 2 {
		t.Errorf("the folder holds %d entries, want exactly mash and mashctl", len(entries))
	}
	for _, want := range []string{"Installed " + fakeMashVersion + " in ~/bin", "Installed " + fakeCtlVersion + " in ~/bin"} {
		if !strings.Contains(r.out, want+"\n") {
			t.Errorf("output lacks %q:\n%s", want, r.out)
		}
	}
	if !strings.Contains(r.out, "~/bin is not on your PATH; add it to use mash\n") {
		t.Errorf("no PATH note:\n%s", r.out)
	}
	if strings.Count(r.out, "is not on your PATH") != 1 {
		t.Errorf("the PATH note must be one line:\n%s", r.out)
	}
}

func TestInstallSh_showsHomeAsTildeAndStaysQuietWhenOnPath(t *testing.T) {
	rel, _, _ := fakeRelease(t)
	tools := installTools(t, nil)
	home := tempHome(t)
	dest := filepath.Join(home, ".local", "bin")
	r := runInstallIn(t, rel, tools, home, dest, "install.sh", "MASH_INSTALL_DIR=", "PATH="+tools+":"+dest)
	if r.err != nil {
		t.Fatalf("install failed: %v\n%s", r.err, r.out)
	}
	if !strings.Contains(r.out, "Installed "+fakeMashVersion+" in ~/.local/bin\n") {
		t.Errorf("want the default folder shown as ~/.local/bin:\n%s", r.out)
	}
	if strings.Contains(r.out, "PATH") {
		t.Errorf("the folder is on PATH, so no note is wanted:\n%s", r.out)
	}
	if _, err := os.Stat(filepath.Join(dest, "mash")); err != nil {
		t.Errorf("not installed in the default folder: %v", err)
	}
}

func TestInstallSh_refusesATamperedBinary(t *testing.T) {
	for _, which := range []int{0, 1} {
		rel, mashName, ctlName := fakeRelease(t)
		victim := []string{mashName, ctlName}[which]
		writeFile(t, filepath.Join(rel, victim), "#!/bin/sh\necho evil\n")
		r := runInstall(t, rel, installTools(t, nil))
		if r.err == nil || !strings.Contains(r.out, victim+" does not match SHA256SUMS") {
			t.Errorf("%s tampered: want a refusal naming it, got err=%v\n%s", victim, r.err, r.out)
		}
		assertNothingInstalled(t, r.dest)
	}
}

func TestInstallSh_refusesAMissingSumsEntry(t *testing.T) {
	rel, mashName, ctlName := fakeRelease(t)
	sums, _ := os.ReadFile(filepath.Join(rel, "SHA256SUMS"))
	var keep []string
	for _, l := range strings.Split(strings.TrimSpace(string(sums)), "\n") {
		if !strings.HasSuffix(l, "  "+ctlName) {
			keep = append(keep, l)
		}
	}
	writeFile(t, filepath.Join(rel, "SHA256SUMS"), strings.Join(keep, "\n")+"\n")
	r := runInstall(t, rel, installTools(t, nil))
	if r.err == nil || !strings.Contains(r.out, "SHA256SUMS has no entry for "+ctlName) {
		t.Errorf("want a refusal naming %s, got err=%v\n%s", ctlName, r.err, r.out)
	}
	assertNothingInstalled(t, r.dest)
	_ = mashName
}

func TestInstallSh_refusesAnUnsupportedPlatform(t *testing.T) {
	rel, _, _ := fakeRelease(t)
	tools := installTools(t, map[string]string{"uname": "case \"$1\" in -s) echo FreeBSD;; -m) echo amd64;; esac\n"})
	r := runInstall(t, rel, tools)
	if r.err == nil || !strings.Contains(r.out, "error: freebsd/amd64 is not a platform mash is built for") {
		t.Errorf("want the platform refusal, got err=%v\n%s", r.err, r.out)
	}
	assertNothingInstalled(t, r.dest)
	// x86_64 and aarch64 are amd64 and arm64.
	tools = installTools(t, map[string]string{"uname": "case \"$1\" in -s) echo Linux;; -m) echo riscv64;; esac\n"})
	r = runInstall(t, rel, tools)
	if r.err == nil || !strings.Contains(r.out, "linux/riscv64 is not a platform mash is built for") {
		t.Errorf("want the refusal for riscv64, got err=%v\n%s", r.err, r.out)
	}
}

func TestInstallSh_mapsMachineNames(t *testing.T) {
	goos, goarch := hostPlatform(t)
	machine := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[goarch]
	rel, _, _ := fakeRelease(t)
	tools := installTools(t, map[string]string{"uname": "case \"$1\" in -s) echo " + strings.ToUpper(goos[:1]) + goos[1:] + ";; -m) echo " + machine + ";; esac\n"})
	r := runInstall(t, rel, tools)
	if r.err != nil {
		t.Fatalf("%s/%s (%s) was refused: %v\n%s", goos, goarch, machine, r.err, r.out)
	}
}

func TestInstallSh_refusesAnUnsafeFolder(t *testing.T) {
	rel, _, _ := fakeRelease(t)
	tools := installTools(t, nil)

	t.Run("symlink", func(t *testing.T) {
		home := tempHome(t)
		real := filepath.Join(home, "real")
		if err := os.Mkdir(real, 0o755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(home, "link")
		if err := os.Symlink(real, link); err != nil {
			t.Fatal(err)
		}
		r := runInstallIn(t, rel, tools, home, link, "install.sh")
		if r.err == nil || !strings.Contains(r.out, "symbolic link") {
			t.Errorf("want a symlink refusal, got err=%v\n%s", r.err, r.out)
		}
		assertNothingInstalled(t, real)
	})
	t.Run("world-writable", func(t *testing.T) {
		home := tempHome(t)
		dest := filepath.Join(home, "bin")
		if err := os.Mkdir(dest, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dest, 0o777); err != nil {
			t.Fatal(err)
		}
		r := runInstallIn(t, rel, tools, home, dest, "install.sh")
		if r.err == nil || !strings.Contains(r.out, "writable by its group or other users") {
			t.Errorf("want a writable refusal, got err=%v\n%s", r.err, r.out)
		}
		assertNothingInstalled(t, dest)
	})
	t.Run("group-writable parent", func(t *testing.T) {
		home := tempHome(t)
		if err := os.Chmod(home, 0o775); err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(home, "bin")
		r := runInstallIn(t, rel, tools, home, dest, "install.sh")
		if r.err == nil || !strings.Contains(r.out, "writable by its group or other users") {
			t.Errorf("want a writable refusal for the parent, got err=%v\n%s", r.err, r.out)
		}
		assertNothingInstalled(t, dest)
	})
	t.Run("symlinked parent", func(t *testing.T) {
		home := tempHome(t)
		real := filepath.Join(home, "real")
		if err := os.Mkdir(real, 0o755); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(home, "link")
		if err := os.Symlink(real, link); err != nil {
			t.Fatal(err)
		}
		r := runInstallIn(t, rel, tools, home, filepath.Join(link, "bin"), "install.sh")
		if r.err == nil || !strings.Contains(r.out, "symbolic link") {
			t.Errorf("want a symlink refusal for the parent, got err=%v\n%s", r.err, r.out)
		}
		assertNothingInstalled(t, filepath.Join(real, "bin"))
	})
}

func TestInstallSh_aTruncatedScriptInstallsNothing(t *testing.T) {
	rel, _, _ := fakeRelease(t)
	tools := installTools(t, nil)
	src, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	if !strings.HasSuffix(strings.TrimRight(text, "\n"), "main \"$@\"") {
		t.Fatal("install.sh must end with the call to main")
	}
	// Cut it at every tenth line: each prefix is what a broken download would hand to bash.
	lines := strings.Split(text, "\n")
	bash, _ := exec.LookPath("bash")
	for cut := 5; cut < len(lines)-1; cut += 10 {
		home := tempHome(t)
		dest := filepath.Join(home, "bin")
		cmd := exec.Command(bash)
		cmd.Stdin = strings.NewReader(strings.Join(lines[:cut], "\n"))
		cmd.Env = []string{"PATH=" + tools, "HOME=" + home, "MASH_RELEASE_BASE=file://" + rel, "MASH_INSTALL_DIR=" + dest}
		out, _ := cmd.CombinedOutput()
		if strings.Contains(string(out), "Installed") {
			t.Errorf("a script cut at line %d installed something:\n%s", cut, out)
		}
		assertNothingInstalled(t, dest)
	}
}

const ghStub = `echo "$*" >> "$GH_LOG"
exit "${GH_EXIT:-0}"
`

func TestInstallSh_verifiesTheAttestationWithGh(t *testing.T) {
	rel, _, _ := fakeRelease(t)
	log := filepath.Join(t.TempDir(), "gh.log")
	tools := installTools(t, map[string]string{"gh": ghStub})

	r := runInstall(t, rel, tools, "GH_LOG="+log, "MASH_VERSION=v0.5.6")
	if r.err != nil {
		t.Fatalf("install failed: %v\n%s", r.err, r.out)
	}
	calls, _ := os.ReadFile(log)
	want := "attestation verify SHA256SUMS -R bogworks/mash --bundle mash.intoto.jsonl --signer-workflow bogworks/mash/.github/workflows/release.yml --source-ref refs/tags/v0.5.6"
	if strings.TrimSpace(string(calls)) != want {
		t.Errorf("gh was called with\n%s\nwant\n%s", calls, want)
	}
	if strings.Contains(r.out, "gh not found") {
		t.Errorf("gh is there, so no note is wanted:\n%s", r.out)
	}

	// The tag also comes from a versioned base URL.
	log2 := filepath.Join(t.TempDir(), "gh.log")
	pinned := filepath.Join(t.TempDir(), "v0.5.6")
	if err := os.Mkdir(pinned, 0o755); err != nil {
		t.Fatal(err)
	}
	ents, _ := os.ReadDir(rel)
	for _, e := range ents {
		b, _ := os.ReadFile(filepath.Join(rel, e.Name()))
		writeFile(t, filepath.Join(pinned, e.Name()), string(b))
	}
	r = runInstall(t, pinned, tools, "GH_LOG="+log2)
	if r.err != nil {
		t.Fatalf("install failed: %v\n%s", r.err, r.out)
	}
	calls, _ = os.ReadFile(log2)
	if !strings.Contains(string(calls), "--source-ref refs/tags/v0.5.6") {
		t.Errorf("the tag was not taken from the base URL: %s", calls)
	}

	// With no tag known, the flag is left out.
	log3 := filepath.Join(t.TempDir(), "gh.log")
	r = runInstall(t, rel, tools, "GH_LOG="+log3)
	if r.err != nil {
		t.Fatalf("install failed: %v\n%s", r.err, r.out)
	}
	calls, _ = os.ReadFile(log3)
	if strings.Contains(string(calls), "--source-ref") {
		t.Errorf("no tag is known, so no --source-ref: %s", calls)
	}
}

func TestInstallSh_refusesWhenTheAttestationFails(t *testing.T) {
	rel, _, _ := fakeRelease(t)
	log := filepath.Join(t.TempDir(), "gh.log")
	tools := installTools(t, map[string]string{"gh": ghStub})
	r := runInstall(t, rel, tools, "GH_LOG="+log, "GH_EXIT=1")
	if r.err == nil || !strings.Contains(r.out, "attestation") || !strings.Contains(r.out, "nothing was installed") {
		t.Errorf("want a refusal, got err=%v\n%s", r.err, r.out)
	}
	assertNothingInstalled(t, r.dest)
}

func TestInstallSh_withoutGhSaysSoInOneLine(t *testing.T) {
	rel, _, _ := fakeRelease(t)
	r := runInstall(t, rel, installTools(t, nil))
	if r.err != nil {
		t.Fatalf("install failed: %v\n%s", r.err, r.out)
	}
	const note = "gh not found: checked the checksums only (see docs/install.md to verify the attestation)\n"
	if strings.Count(r.out, note) != 1 {
		t.Errorf("want the one-line note once:\n%s", r.out)
	}
}

func TestInstallSh_refusesANonHTTPSBase(t *testing.T) {
	rel, _, _ := fakeRelease(t)
	r := runInstall(t, rel, installTools(t, nil), "MASH_RELEASE_BASE=http://example.invalid/x")
	if r.err == nil || !strings.Contains(r.out, "https://") {
		t.Errorf("want a refusal of http, got err=%v\n%s", r.err, r.out)
	}
	assertNothingInstalled(t, r.dest)
}

func TestInstallSh_isCleanAndEndsWithMain(t *testing.T) {
	if p, err := exec.LookPath("shellcheck"); err == nil {
		if out, err := exec.Command(p, "install.sh").CombinedOutput(); err != nil {
			t.Errorf("shellcheck: %v\n%s", err, out)
		}
	}
	src, _ := os.ReadFile("install.sh")
	if !strings.HasPrefix(string(src), "#!/usr/bin/env bash\n") || !strings.Contains(string(src), "set -euo pipefail") {
		t.Error("install.sh must be a bash script with set -euo pipefail")
	}
}

func TestInstallSh_namesAFolderOutsideHomeInFull(t *testing.T) {
	rel, _, _ := fakeRelease(t)
	home := tempHome(t)
	other := tempHome(t)
	dest := filepath.Join(other, "tools")
	r := runInstallIn(t, rel, installTools(t, nil), home, dest, "install.sh")
	if r.err != nil {
		t.Fatalf("install failed: %v\n%s", r.err, r.out)
	}
	if !strings.Contains(r.out, "Installed "+fakeMashVersion+" in "+dest+"\n") {
		t.Errorf("want the full path:\n%s", r.out)
	}
}
