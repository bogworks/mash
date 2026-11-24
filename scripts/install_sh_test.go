package scripts

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
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
	for _, tool := range []string{"bash", "curl", "awk", "sha256sum", "mktemp", "tr", "head", "ls", "dirname", "mkdir", "cp", "chmod", "mv", "rm", "rmdir", "cat", "uname", "id"} {
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
	out    string // stdout then stderr
	stdout string
	stderr string
	err    error
	home   string
	dest   string
}

// runInstall runs the script (path, or "" for scripts/install.sh) with the given tool dir as PATH.
func runInstall(t *testing.T, release, toolPath string, env ...string) installRun {
	t.Helper()
	home := tempHome(t)
	dest := filepath.Join(home, "bin")
	return runInstallIn(t, release, toolPath, home, dest, "install.sh", env...)
}

// patchedInstall writes a test-only copy of install.sh that accepts a file:// base, made by install-testcopy.sh,
// into a fresh temp dir and returns its path.
func patchedInstall(t *testing.T) string {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	dst := filepath.Join(t.TempDir(), "install.sh")
	if out, err := exec.Command(bash, "install-testcopy.sh", "install.sh", dst).CombinedOutput(); err != nil {
		t.Fatalf("install-testcopy.sh: %v\n%s", err, out)
	}
	return dst
}

func runInstallIn(t *testing.T, release, toolPath, home, dest, script string, env ...string) installRun {
	t.Helper()
	return runInstallGroup(t, false, release, toolPath, home, dest, script, env...)
}

// runInstallGroup is runInstallIn with the option to start bash in its own process group, so a test can signal the
// whole group the way a terminal does for Ctrl-C.
func runInstallGroup(t *testing.T, ownGroup bool, release, toolPath, home, dest, script string, env ...string) installRun {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	if script == "install.sh" { // the tests run a patched copy; the shipped script is https-only
		script = patchedInstall(t)
	}
	cmd := exec.Command(bash, script) // go test runs in this directory
	cmd.Env = append([]string{
		"PATH=" + toolPath,
		"HOME=" + home,
		"TMPDIR=" + t.TempDir(),
		"MASH_RELEASE_BASE=file://" + release,
		"MASH_INSTALL_DIR=" + dest,
		"MASH_VERSION=v0.5.6",
	}, env...)
	if ownGroup {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	err = cmd.Run()
	return installRun{out: so.String() + se.String(), stdout: so.String(), stderr: se.String(), err: err, home: home, dest: dest}
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

	t.Run("symlink is followed", func(t *testing.T) {
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
		if r.err != nil {
			t.Fatalf("install failed: %v\n%s", r.err, r.out)
		}
		for _, n := range []string{"mash", "mashctl"} {
			if _, err := os.Stat(filepath.Join(real, n)); err != nil {
				t.Errorf("%s not installed in the real folder: %v", n, err)
			}
		}
		if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Errorf("the link itself must stay a link")
		}
	})
	t.Run("a folder named mash", func(t *testing.T) {
		home := tempHome(t)
		dest := filepath.Join(home, "bin")
		if err := os.MkdirAll(filepath.Join(dest, "mashctl"), 0o755); err != nil {
			t.Fatal(err)
		}
		r := runInstallIn(t, rel, tools, home, dest, "install.sh")
		if r.err == nil || !strings.Contains(r.out, "is a folder") {
			t.Errorf("want a refusal, got err=%v\n%s", r.err, r.out)
		}
		if _, err := os.Stat(filepath.Join(dest, "mash")); err == nil {
			t.Errorf("mash was installed")
		}
	})
	t.Run("owned by another user", func(t *testing.T) {
		if os.Getuid() == 0 {
			t.Skip("root owns everything it makes as an allowed owner")
		}
		home := tempHome(t)
		dest := filepath.Join(home, "bin")
		// id reports a uid that is neither the folder's owner nor root.
		other := installTools(t, map[string]string{"id": "echo 4242\n"})
		r := runInstallIn(t, rel, other, home, dest, "install.sh")
		if r.err == nil || !strings.Contains(r.out, "is owned by another user") {
			t.Errorf("want an owner refusal, got err=%v\n%s", r.err, r.out)
		}
		assertNothingInstalled(t, dest)
	})
	t.Run("world-writable ancestor", func(t *testing.T) {
		home := tempHome(t)
		if err := os.Chmod(home, 0o777); err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(home, "a", "bin")
		r := runInstallIn(t, rel, tools, home, dest, "install.sh")
		if r.err == nil || !strings.Contains(r.out, "writable by its group or other users") {
			t.Errorf("want a writable refusal, got err=%v\n%s", r.err, r.out)
		}
		assertNamesTheFix(t, r.out, home)
		assertNothingInstalled(t, dest)
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
		assertNamesTheFix(t, r.out, dest)
		assertNothingInstalled(t, dest)
	})
	t.Run("folder name with a space and a quote", func(t *testing.T) {
		home := tempHome(t)
		dest := filepath.Join(home, "a b's", "bin")
		if err := os.MkdirAll(dest, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dest, 0o777); err != nil {
			t.Fatal(err)
		}
		r := runInstallIn(t, rel, tools, home, dest, "install.sh")
		if r.err == nil || !strings.Contains(r.out, "writable by its group or other users") {
			t.Errorf("want a writable refusal, got err=%v\n%s", r.err, r.out)
		}
		assertNamesTheFix(t, r.out, dest)
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
		assertNamesTheFix(t, r.out, home)
		assertNothingInstalled(t, dest)
	})
}

// assertNamesTheFix checks the output tells the user to run chmod go-w on folder, in single quotes (resolved, as the script prints it).
func assertNamesTheFix(t *testing.T, out, folder string) {
	t.Helper()
	real, err := filepath.EvalSymlinks(folder)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "chmod go-w '"+strings.ReplaceAll(real, "'", `'\''`)+"'") {
		t.Errorf("want the fix chmod go-w '%s' (quoted) in the output, got:\n%s", real, out)
	}
}

// Claude Code's own installer makes ~/.local and ~/.local/bin mode 775. The refusal must name each folder and the fix.
func TestInstallSh_groupWritableLocalNamesTheFix(t *testing.T) {
	rel, _, _ := fakeRelease(t)
	tools := installTools(t, nil)
	home := tempHome(t)
	local := filepath.Join(home, ".local")
	dest := filepath.Join(local, "bin")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{local, dest} {
		if err := os.Chmod(d, 0o775); err != nil {
			t.Fatal(err)
		}
	}
	realLocal, err := filepath.EvalSymlinks(local)
	if err != nil {
		t.Fatal(err)
	}
	realDest := filepath.Join(realLocal, "bin")

	r := runInstallIn(t, rel, tools, home, dest, "install.sh")
	if r.err == nil || !strings.Contains(r.out, realDest+" is writable by its group or other users") ||
		!strings.Contains(r.out, "chmod go-w '"+realDest+"'") {
		t.Fatalf("want a refusal naming %s and the fix, got err=%v\n%s", realDest, r.err, r.out)
	}
	assertNothingInstalled(t, dest)

	if err := os.Chmod(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	r = runInstallIn(t, rel, tools, home, dest, "install.sh")
	if r.err == nil || !strings.Contains(r.out, realLocal+" is writable by its group or other users") ||
		!strings.Contains(r.out, "chmod go-w '"+realLocal+"'") {
		t.Fatalf("want a refusal naming %s and the fix, got err=%v\n%s", realLocal, r.err, r.out)
	}
	assertNothingInstalled(t, dest)

	if err := os.Chmod(local, 0o755); err != nil {
		t.Fatal(err)
	}
	r = runInstallIn(t, rel, tools, home, dest, "install.sh")
	if r.err != nil {
		t.Fatalf("install failed after the fixes: %v\n%s", r.err, r.out)
	}
	for _, n := range []string{"mash", "mashctl"} {
		if _, err := os.Stat(filepath.Join(dest, n)); err != nil {
			t.Errorf("%s not installed: %v", n, err)
		}
	}
}

// Every writable folder on the walk is named in one refusal with one fix command; a single folder keeps the one-folder text.
func TestInstallSh_oneRefusalNamesEveryWritableFolder(t *testing.T) {
	rel, _, _ := fakeRelease(t)
	tools := installTools(t, nil)
	home := tempHome(t)
	local := filepath.Join(home, ".local")
	dest := filepath.Join(local, "bin")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{local, dest} {
		if err := os.Chmod(d, 0o775); err != nil {
			t.Fatal(err)
		}
	}
	realLocal, err := filepath.EvalSymlinks(local)
	if err != nil {
		t.Fatal(err)
	}
	realDest := filepath.Join(realLocal, "bin")

	r := runInstallIn(t, rel, tools, home, dest, "install.sh")
	want := realDest + " is writable by its group or other users (so is " + realLocal + "): not installing there. " +
		"Fix: chmod go-w '" + realDest + "' '" + realLocal + "', then run the installer again"
	if r.err == nil || !strings.Contains(r.out, want) {
		t.Fatalf("want %q, got err=%v\n%s", want, r.err, r.out)
	}
	if n := strings.Count(r.out, "chmod go-w"); n != 1 {
		t.Errorf("want exactly one chmod go-w command, got %d\n%s", n, r.out)
	}
	assertNothingInstalled(t, dest)

	if err := os.Chmod(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	r = runInstallIn(t, rel, tools, home, dest, "install.sh")
	want = realLocal + " is writable by its group or other users: not installing there. Fix: chmod go-w '" + realLocal + "', then run the installer again"
	if r.err == nil || !strings.Contains(r.out, want) || strings.Contains(r.out, "(so is") {
		t.Fatalf("want %q, got err=%v\n%s", want, r.err, r.out)
	}
	assertNothingInstalled(t, dest)
}

func TestInstallSh_aTruncatedScriptInstallsNothing(t *testing.T) {
	rel, _, _ := fakeRelease(t)
	tools := installTools(t, nil)
	src, err := os.ReadFile(patchedInstall(t)) // the patched copy, so the file:// base is accepted
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
		cmd.Env = []string{"PATH=" + tools, "HOME=" + home, "MASH_RELEASE_BASE=file://" + rel, "MASH_INSTALL_DIR=" + dest, "MASH_VERSION=v0.5.6"}
		out, _ := cmd.CombinedOutput()
		if strings.Contains(string(out), "Installed") {
			t.Errorf("a script cut at line %d installed something:\n%s", cut, out)
		}
		assertNothingInstalled(t, dest)
	}
}

// ghStub answers --version with real gh's two lines for GH_VERSION (default 2.105.0), or GH_VERSION_OUT verbatim when
// set, exiting GH_VERSION_EXIT (default 0), without logging; every other call is logged and exits GH_EXIT (default 0).
const ghStub = `case "$*" in --version) if [ -n "${GH_VERSION_OUT+x}" ]; then printf '%s\n' "$GH_VERSION_OUT"; else printf 'gh version %s (2026-11-18)\nhttps://github.com/cli/cli/releases/tag/v%s\n' "${GH_VERSION:-2.105.0}" "${GH_VERSION:-2.105.0}"; fi; exit "${GH_VERSION_EXIT:-0}";; esac
case "$1 $2" in "auth status") exit "${GH_AUTH_EXIT:-0}";; esac
case "$1" in api) echo "$*" >> "$GH_LOG.api"; [ -z "${GH_API_EXIT:-}" ] || exit "$GH_API_EXIT"; printf '%s\n' "${GH_API_SHA-` + fakeCommit + `}"; exit 0;; esac
echo "$*" >> "$GH_LOG"
if [ -n "${GH_WANT_DIGEST:-}" ]; then case "$*" in *"--source-digest $GH_WANT_DIGEST") ;; *) exit 1;; esac; fi
exit "${GH_EXIT:-0}"
`

// fakeCommit is the commit the fake gh api reports for every tag.
const fakeCommit = "0123456789abcdef0123456789abcdef01234567"

// curlStub serves https://github.com/<repo>/releases/download/<tag>/<file> from $FAKE_DIR and answers the -I
// request for releases/latest with $RESOLVED; every call is logged to $CURL_LOG.
const curlStub = `echo "$*" >> "$CURL_LOG"
head=0; out=""; url=""; prev=""
for a in "$@"; do
  case "$a" in -*I*) case "$a" in --*) ;; *) head=1;; esac;; esac
  [ "$prev" = "-o" ] && out=$a
  prev=$a; url=$a
done
if [ "$head" = 1 ]; then printf '%s' "$RESOLVED"; exit 0; fi
case "$url" in
  "https://github.com/${WANT_REPO:-bogworks/mash}/releases/download/${WANT_TAG:-v0.5.6}/"*) cp "$FAKE_DIR/${url##*/}" "$out";;
  *) echo "unexpected url $url" >&2; exit 22;;
esac
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
	want := "attestation verify SHA256SUMS -R bogworks/mash --bundle mash.intoto.jsonl --signer-workflow bogworks/mash/.github/workflows/release.yml --source-ref refs/tags/v0.5.6 --source-digest " + fakeCommit
	if strings.TrimSpace(string(calls)) != want {
		t.Errorf("gh was called with\n%s\nwant\n%s", calls, want)
	}
	if strings.Contains(r.out, "warning: gh not found") {
		t.Errorf("gh is there, so no note is wanted:\n%s", r.out)
	}
	if !strings.HasPrefix(r.out, "Fetching mash and mashctl from the v0.5.6 release: file://") {
		t.Errorf("first line should name the tag:\n%s", r.out)
	}

	// A base needs a version.
	r = runInstall(t, rel, tools, "GH_LOG="+log, "MASH_VERSION=")
	if r.err == nil || !strings.Contains(r.out, "set MASH_VERSION with MASH_RELEASE_BASE") {
		t.Errorf("want a refusal, got err=%v\n%s", r.err, r.out)
	}
	assertNothingInstalled(t, r.dest)
}

func TestInstallSh_repoFollowsMashRepo(t *testing.T) {
	rel, _, _ := fakeRelease(t)
	log := filepath.Join(t.TempDir(), "gh.log")
	tools := installTools(t, map[string]string{"gh": ghStub})
	r := runInstall(t, rel, tools, "GH_LOG="+log, "MASH_REPO=acme/mash-fork")
	if r.err != nil {
		t.Fatalf("install failed: %v\n%s", r.err, r.out)
	}
	calls, _ := os.ReadFile(log)
	for _, w := range []string{"-R acme/mash-fork ", "--signer-workflow acme/mash-fork/.github/workflows/release.yml"} {
		if !strings.Contains(string(calls), w) {
			t.Errorf("gh call lacks %q: %s", w, calls)
		}
	}
	r = runInstall(t, rel, tools, "GH_LOG="+log, "MASH_REPO=not a repo")
	if r.err == nil || !strings.Contains(r.out, "MASH_REPO") {
		t.Errorf("want a refusal of a bad MASH_REPO, got err=%v\n%s", r.err, r.out)
	}
}

func TestInstallSh_latestIsResolvedToATagFirst(t *testing.T) {
	rel, _, _ := fakeRelease(t)
	ghLog := filepath.Join(t.TempDir(), "gh.log")
	curlLog := filepath.Join(t.TempDir(), "curl.log")
	tools := installTools(t, map[string]string{"gh": ghStub, "curl": curlStub})
	env := []string{"GH_LOG=" + ghLog, "CURL_LOG=" + curlLog, "FAKE_DIR=" + rel, "MASH_RELEASE_BASE=", "MASH_VERSION=",
		"RESOLVED=https://github.com/bogworks/mash/releases/tag/v0.5.6"}
	r := runInstall(t, rel, tools, env...)
	if r.err != nil {
		t.Fatalf("install failed: %v\n%s", r.err, r.out)
	}
	if !strings.HasPrefix(r.out, "Fetching mash and mashctl from the v0.5.6 release: https://github.com/bogworks/mash/releases/download/v0.5.6\n") {
		t.Errorf("first line should name the real version:\n%s", r.out)
	}
	calls, _ := os.ReadFile(ghLog)
	for _, w := range []string{"-R bogworks/mash ", "--source-ref refs/tags/v0.5.6 --source-digest " + fakeCommit} {
		if !strings.Contains(string(calls), w) {
			t.Errorf("gh call lacks %q: %s", w, calls)
		}
	}
	cl, _ := os.ReadFile(curlLog)
	lines := strings.Split(strings.TrimSpace(string(cl)), "\n")
	if !strings.Contains(lines[0], "releases/latest") || !strings.Contains(lines[0], "-fsSLI") {
		t.Errorf("the first curl call must resolve the tag: %s", lines[0])
	}
	for _, l := range lines {
		if !strings.Contains(l, "--proto-redir =https") {
			t.Errorf("curl call without --proto-redir =https: %s", l)
		}
		if strings.Contains(l, "releases/latest/download") {
			t.Errorf("the download must not use the moving latest address: %s", l)
		}
	}

	// A resolved address that is not a tag is refused.
	for _, bad := range []string{"https://github.com/bogworks/mash/releases", "https://github.com/bogworks/mash/releases/tag/main", "https://example.com/login"} {
		env[len(env)-1] = "RESOLVED=" + bad
		r = runInstall(t, rel, tools, env...)
		if r.err == nil || !strings.Contains(r.out, "error:") {
			t.Errorf("%s: want a refusal, got err=%v\n%s", bad, r.err, r.out)
		}
		assertNothingInstalled(t, r.dest)
	}
}

func TestInstallSh_ghBeforeTheMinimumFallsBackToChecksumsWithAnHonestNote(t *testing.T) {
	const note = "warning: gh is below 2.102.0: checked checksums only, not verified\n"
	for _, v := range []string{"2.48.0", "2.67.0", "2.101.0", "2.101.9", "2.9.0", "1.99.0", "2.102.0-rc1"} {
		t.Run(v, func(t *testing.T) {
			rel, _, _ := fakeRelease(t)
			log := filepath.Join(t.TempDir(), "gh.log")
			tools := installTools(t, map[string]string{"gh": ghStub})
			r := runInstall(t, rel, tools, "GH_LOG="+log, "GH_VERSION="+v)
			if r.err != nil {
				t.Fatalf("install failed: %v\n%s", r.err, r.out)
			}
			if strings.Count(r.stderr, note) != 1 || strings.Contains(r.stdout, "warning:") {
				t.Errorf("want the note once:\n%s", r.out)
			}
			if b, _ := os.ReadFile(log); len(b) != 0 {
				t.Errorf("verify must not run with an old gh: %s", b)
			}
			if _, err := os.Stat(log + ".api"); err == nil {
				t.Errorf("no commit lookup with an old gh")
			}
			if strings.Contains(r.out, "failed verification") || strings.Contains(r.out, "Verified") {
				t.Errorf("no verification text wanted:\n%s", r.out)
			}
		})
	}
}

func TestInstallSh_ghAtOrAboveTheMinimumVerifiesAndSaysSo(t *testing.T) {
	const want = "attestation verify SHA256SUMS -R bogworks/mash --bundle mash.intoto.jsonl --signer-workflow bogworks/mash/.github/workflows/release.yml --source-ref refs/tags/v0.5.6 --source-digest " + fakeCommit
	const line = "Verified the release attestation for bogworks/mash v0.5.6\n"
	for _, v := range []string{"2.102.0", "2.102.1", "2.103.0-rc1", "2.105.0", "2.200.1", "3.0.0"} {
		t.Run(v, func(t *testing.T) {
			rel, _, _ := fakeRelease(t)
			log := filepath.Join(t.TempDir(), "gh.log")
			tools := installTools(t, map[string]string{"gh": ghStub})
			r := runInstall(t, rel, tools, "GH_LOG="+log, "MASH_VERSION=v0.5.6", "GH_VERSION="+v)
			if r.err != nil {
				t.Fatalf("install failed: %v\n%s", r.err, r.out)
			}
			if calls, _ := os.ReadFile(log); strings.TrimSpace(string(calls)) != want {
				t.Errorf("gh was called with\n%s\nwant\n%s", calls, want)
			}
			if strings.Count(r.stdout, line) != 1 || strings.Contains(r.stderr, "Verified") {
				t.Errorf("want the Verified line once:\n%s", r.out)
			}
			if strings.Contains(r.out, "older than") || strings.Contains(r.out, "did not run") {
				t.Errorf("no fallback note wanted:\n%s", r.out)
			}
		})
	}
}

func TestInstallSh_ghThatDoesNotRunSaysSoAndFallsBack(t *testing.T) {
	const note = "warning: gh did not run: checked checksums only, not verified\n"
	for name, env := range map[string][]string{
		"exit 127":  {"GH_VERSION_EXIT=127"},
		"no output": {"GH_VERSION_OUT="},
	} {
		t.Run(name, func(t *testing.T) {
			rel, _, _ := fakeRelease(t)
			log := filepath.Join(t.TempDir(), "gh.log")
			tools := installTools(t, map[string]string{"gh": ghStub})
			r := runInstall(t, rel, tools, append([]string{"GH_LOG=" + log}, env...)...)
			if r.err != nil {
				t.Fatalf("install failed: %v\n%s", r.err, r.out)
			}
			if strings.Count(r.stderr, note) != 1 || strings.Contains(r.stdout, "warning:") {
				t.Errorf("want the note once:\n%s", r.out)
			}
			if b, _ := os.ReadFile(log); len(b) != 0 {
				t.Errorf("verify must not run: %s", b)
			}
		})
	}
}

func TestInstallSh_ghWhoseVersionCannotBeParsedSaysSoAndFallsBack(t *testing.T) {
	const note = "warning: gh version unreadable: checked checksums only, not verified\n"
	for name, v := range map[string]string{"DEV": "gh version DEV (2026-11-18)", "v prefix": "gh version v2.105.0 (2026-11-18)", "garbage": "garbage"} {
		t.Run(name, func(t *testing.T) {
			rel, _, _ := fakeRelease(t)
			log := filepath.Join(t.TempDir(), "gh.log")
			tools := installTools(t, map[string]string{"gh": ghStub})
			r := runInstall(t, rel, tools, "GH_LOG="+log, "GH_VERSION_OUT="+v+"\nhttps://github.com/cli/cli/releases/tag/x")
			if r.err != nil {
				t.Fatalf("install failed: %v\n%s", r.err, r.out)
			}
			if strings.Count(r.stderr, note) != 1 || strings.Contains(r.stdout, "warning:") {
				t.Errorf("want the note once:\n%s", r.out)
			}
			if b, _ := os.ReadFile(log); len(b) != 0 {
				t.Errorf("verify must not run: %s", b)
			}
		})
	}
}

func TestInstallSh_bothBinariesOrNeither(t *testing.T) {
	rel, _, _ := fakeRelease(t)
	home := tempHome(t)
	dest := filepath.Join(home, "bin")
	if err := os.Mkdir(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dest, "mash"), "OLD-MASH")
	writeFile(t, filepath.Join(dest, "mashctl"), "OLD-CTL")
	realMv, err := exec.LookPath("mv")
	if err != nil {
		t.Skip("no mv")
	}
	// mv fails when it is putting the new mashctl in place.
	tools := installTools(t, map[string]string{"mv": "for a; do last=$a; done\ncase \"$last\" in */mashctl) exit 1;; esac\nexec \"$REAL_MV\" \"$@\"\n"})
	r := runInstallIn(t, rel, tools, home, dest, "install.sh", "REAL_MV="+realMv)
	if r.err == nil || !strings.Contains(r.out, "nothing was installed") {
		t.Fatalf("want a refusal, got err=%v\n%s", r.err, r.out)
	}
	for n, want := range map[string]string{"mash": "OLD-MASH", "mashctl": "OLD-CTL"} {
		b, _ := os.ReadFile(filepath.Join(dest, n))
		if string(b) != want {
			t.Errorf("%s = %q, want it unchanged (%q)", n, b, want)
		}
	}
	if entries, _ := os.ReadDir(dest); len(entries) != 2 {
		t.Errorf("temporary files were left in the folder: %v", entries)
	}
}

// A signal that arrives while mashctl is being put in place must not leave the two binaries from different
// releases. bash runs in its own process group and the stub mv signals the whole group, as a terminal does for
// Ctrl-C (a signal to bash's PID alone is deferred by a non-interactive bash, which would hide a missing guard).
// The stub mv is a direct child of bash, so $PPID is bash, and bash is its group's leader, so -$PPID is the group.
// Without the guard the signal kills bash and the stub (both in the group), so mashctl is never renamed and the
// install fails. With the guard the ignore disposition is inherited by the stub and the real mv, so the install
// completes with both new.
func TestInstallSh_aSignalDuringTheRenamesLeavesOneRelease(t *testing.T) {
	for _, sig := range []string{"TERM", "INT", "HUP"} {
		t.Run(sig, func(t *testing.T) {
			rel, _, _ := fakeRelease(t)
			home := tempHome(t)
			dest := filepath.Join(home, "bin")
			if err := os.Mkdir(dest, 0o755); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(dest, "mash"), "OLD-MASH")
			writeFile(t, filepath.Join(dest, "mashctl"), "OLD-CTL")
			realMv, err := exec.LookPath("mv")
			if err != nil {
				t.Skip("no mv")
			}
			realSleep, err := exec.LookPath("sleep")
			if err != nil {
				t.Skip("no sleep")
			}
			stub := "for a; do last=$a; done\n" +
				"case \"$last\" in */mashctl)\n" +
				"  kill -s " + sig + " -- \"-$PPID\"\n" +
				"  \"$REAL_SLEEP\" 0.2;;\n" +
				"esac\n" +
				"exec \"$REAL_MV\" \"$@\"\n"
			tools := installTools(t, map[string]string{"mv": stub})
			r := runInstallGroup(t, true, rel, tools, home, dest, "install.sh", "REAL_MV="+realMv, "REAL_SLEEP="+realSleep)
			if r.err != nil {
				t.Errorf("want the install to finish despite SIG%s, got err=%v\n%s", sig, r.err, r.out)
			}
			m, _ := os.ReadFile(filepath.Join(dest, "mash"))
			c, _ := os.ReadFile(filepath.Join(dest, "mashctl"))
			if !strings.Contains(string(m), fakeMashVersion) || !strings.Contains(string(c), fakeCtlVersion) {
				t.Errorf("mash = %q, mashctl = %q: want both new", m, c)
			}
		})
	}
}

func TestInstallSh_refusesWhenTheAttestationFails(t *testing.T) {
	rel, _, _ := fakeRelease(t)
	log := filepath.Join(t.TempDir(), "gh.log")
	tools := installTools(t, map[string]string{"gh": ghStub})
	r := runInstall(t, rel, tools, "GH_LOG="+log, "GH_EXIT=1")
	const refusal = "error: the release failed verification; nothing was installed\n"
	if r.err == nil || r.stderr != refusal || strings.Contains(r.stdout, "error:") {
		t.Errorf("want exactly %q on stderr, got err=%v\nstdout:\n%s\nstderr:\n%s", refusal, r.err, r.stdout, r.stderr)
	}
	if strings.Contains(r.out, "Verified") {
		t.Errorf("no Verified line after a failure:\n%s", r.out)
	}
	assertNothingInstalled(t, r.dest)
}

func TestInstallSh_withoutGhSaysSoInOneLine(t *testing.T) {
	rel, _, _ := fakeRelease(t)
	r := runInstall(t, rel, installTools(t, nil))
	if r.err != nil {
		t.Fatalf("install failed: %v\n%s", r.err, r.out)
	}
	const note = "warning: gh not found: checked checksums only, not verified\n"
	if strings.Count(r.stderr, note) != 1 || strings.Contains(r.stdout, "warning:") {
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

func TestInstallSh_refusesAFileBase(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	rel, _, _ := fakeRelease(t)
	tools := installTools(t, nil)
	for _, allow := range []string{"", "MASH_INSTALL_ALLOW_FILE=1"} {
		home := tempHome(t)
		dest := filepath.Join(home, "bin")
		cmd := exec.Command(bash, "install.sh") // the shipped script, unpatched
		cmd.Env = []string{"PATH=" + tools, "HOME=" + home, "TMPDIR=" + t.TempDir(), "MASH_RELEASE_BASE=file://" + rel,
			"MASH_INSTALL_DIR=" + dest, "MASH_VERSION=v0.5.6"}
		if allow != "" {
			cmd.Env = append(cmd.Env, allow)
		}
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), "the release location must be an https:// address: file://") {
			t.Errorf("allow=%q: want a refusal of file://, got err=%v\n%s", allow, err, out)
		}
		assertNothingInstalled(t, dest)
	}
}

func TestInstallTestcopy_failsWithoutTheAnchor(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src.sh")
	writeFile(t, src, "#!/usr/bin/env bash\necho hi\n")
	dst := filepath.Join(dir, "dst.sh")
	out, err := exec.Command(bash, "install-testcopy.sh", src, dst).CombinedOutput()
	if err == nil || !strings.Contains(string(out), "exactly once") {
		t.Errorf("want a loud failure without the anchor, got err=%v\n%s", err, out)
	}
	if _, err := os.Stat(dst); err == nil {
		t.Error("a copy was written despite the missing anchor")
	}
	// Twice is as wrong as none.
	writeFile(t, src, "    https://*) ;;\n    https://*) ;;\n")
	if out, err := exec.Command(bash, "install-testcopy.sh", src, dst).CombinedOutput(); err == nil {
		t.Errorf("want a failure with the anchor twice, got success\n%s", out)
	}
}

func TestInstallTestcopy_differsOnlyByThePatch(t *testing.T) {
	dst := patchedInstall(t)
	fi, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Errorf("the copy has mode %v, want 0755", fi.Mode().Perm())
	}
	a, _ := os.ReadFile("install.sh")
	b, _ := os.ReadFile(dst)
	al, bl := strings.Split(string(a), "\n"), strings.Split(string(b), "\n")
	if len(bl) != len(al)+1 {
		t.Fatalf("the copy has %d lines, want %d", len(bl), len(al)+1)
	}
	added := 0
	for i, j := 0, 0; j < len(bl); j++ {
		if i < len(al) && al[i] == bl[j] {
			i++
			continue
		}
		added++
		if bl[j] != `    file://*) PROTO="=https,file" ;;` {
			t.Errorf("unexpected added line %q", bl[j])
		}
	}
	if added != 1 {
		t.Errorf("%d lines differ, want exactly the one patch line", added)
	}
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

func TestInstallSh_theAttestationIsPinnedToTheTagsCommit(t *testing.T) {
	rel, _, _ := fakeRelease(t)
	log := filepath.Join(t.TempDir(), "gh.log")
	tools := installTools(t, map[string]string{"gh": ghStub})
	other := "fedcba9876543210fedcba9876543210fedcba98"

	// (a) the digest is the commit gh api reports for the tag.
	r := runInstall(t, rel, tools, "GH_LOG="+log, "MASH_VERSION=v0.5.6", "GH_API_SHA="+other, "GH_WANT_DIGEST="+other)
	if r.err != nil {
		t.Fatalf("install failed: %v\n%s", r.err, r.out)
	}
	if calls, _ := os.ReadFile(log); !strings.HasSuffix(strings.TrimSpace(string(calls)), "--source-digest "+other) {
		t.Errorf("verify lacks the tags commit: %s", calls)
	}
	if api, _ := os.ReadFile(log + ".api"); strings.TrimSpace(string(api)) != "api repos/bogworks/mash/commits/v0.5.6 --jq .sha" {
		t.Errorf("gh api was called with %q", api)
	}
	if !strings.Contains(r.stdout, "Verified the release attestation for bogworks/mash v0.5.6\n") {
		t.Errorf("want the Verified line with the commit:\n%s", r.out)
	}

	// (d) a digest that is not the one the fake verify expects fails verification.
	r = runInstall(t, rel, tools, "GH_LOG="+log, "MASH_VERSION=v0.5.6", "GH_WANT_DIGEST="+other)
	if r.err == nil || r.stderr != "error: the release failed verification; nothing was installed\n" {
		t.Errorf("want a verification failure, got err=%v\n%s", r.err, r.out)
	}
	assertNothingInstalled(t, r.dest)
}

// apiFolder makes a folder that answers the commit lookup of tag v0.5.6 as a file:// MASH_API_BASE, with body.
func apiFolder(t *testing.T, body string) string {
	t.Helper()
	d := t.TempDir()
	if err := os.MkdirAll(filepath.Join(d, "repos", "bogworks", "mash", "commits"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(d, "repos", "bogworks", "mash", "commits", "v0.5.6"), body)
	return "file://" + d
}

func TestInstallSh_withGhNotLoggedInTheCommitComesFromThePublicAPI(t *testing.T) {
	rel, _, _ := fakeRelease(t)
	log := filepath.Join(t.TempDir(), "gh.log")
	tools := installTools(t, map[string]string{"gh": ghStub})
	api := apiFolder(t, "")
	sha := "abcdefabcdefabcdefabcdefabcdefabcdefabcd"
	writeFile(t, strings.TrimPrefix(api, "file://")+"/repos/bogworks/mash/commits/v0.5.6", sha+"\n")

	// (b) gh api fails (not logged in): the fallback still pins the digest.
	for name, env := range map[string][]string{"gh api fails": {"GH_API_EXIT=4"}, "gh api answers junk": {"GH_API_SHA=not-a-sha"}} {
		t.Run(name, func(t *testing.T) {
			r := runInstall(t, rel, tools, append([]string{"GH_LOG=" + log, "MASH_VERSION=v0.5.6", "MASH_API_BASE=" + api, "GH_WANT_DIGEST=" + sha}, env...)...)
			if r.err != nil {
				t.Fatalf("install failed: %v\n%s", r.err, r.out)
			}
			if !strings.Contains(r.stdout, "Verified the release attestation for bogworks/mash v0.5.6\n") {
				t.Errorf("want the Verified line with the commit:\n%s", r.out)
			}
		})
	}
}

func TestInstallSh_refusesWhenTheTagsCommitCannotBeRead(t *testing.T) {
	const refusal = "error: could not read the commit of bogworks/mash v0.5.6; nothing was installed\n"
	const hint = "hint: gh auth login, then run the install again\n"
	rel, _, _ := fakeRelease(t)
	log := filepath.Join(t.TempDir(), "gh.log")
	tools := installTools(t, map[string]string{"gh": ghStub})
	missing := "file://" + t.TempDir()

	// (c) both lookups fail, or the API answers something that is not a full sha. The hint is a fix only when gh is not logged in.
	for authName, auth := range map[string]struct{ env, want string }{
		"gh not logged in": {"GH_AUTH_EXIT=1", refusal + hint},
		"gh logged in":     {"GH_AUTH_EXIT=0", refusal},
	} {
		for name, api := range map[string]string{"api unreachable": missing, "api answers junk": apiFolder(t, "{\"message\": \"Not Found\"}"), "short sha": apiFolder(t, "0123abc\n")} {
			t.Run(authName+"/"+name, func(t *testing.T) {
				r := runInstall(t, rel, tools, "GH_LOG="+log, "MASH_VERSION=v0.5.6", "GH_API_EXIT=4", "MASH_API_BASE="+api, auth.env)
				if r.err == nil || r.stderr != auth.want {
					t.Errorf("want exactly %q on stderr, got err=%v\nstdout:\n%s\nstderr:\n%s", auth.want, r.err, r.stdout, r.stderr)
				}
				if b, _ := os.ReadFile(log); len(b) != 0 {
					t.Errorf("verify must not run without a commit: %s", b)
				}
				assertNothingInstalled(t, r.dest)
			})
		}
	}
}

func TestInstallSh_noGhMeansNoCommitLookup(t *testing.T) {
	rel, _, _ := fakeRelease(t)
	r := runInstall(t, rel, installTools(t, nil), "MASH_API_BASE=file:///nonexistent")
	if r.err != nil || strings.Contains(r.out, "commit") {
		t.Errorf("want a plain checksums-only install, got err=%v\n%s", r.err, r.out)
	}
}
