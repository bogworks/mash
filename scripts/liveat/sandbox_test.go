package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestContainerfileIsAPinnedDebianWithTheScriptsTools(t *testing.T) {
	if !strings.Contains(containerfile, "\nFROM "+baseImage+"\n") || baseImage != "docker.io/library/debian:bookworm-slim@"+baseImageDigest || !strings.Contains(containerfile, "\n# The base is pinned by the digest") {
		t.Errorf("base image: %q", baseImage)
	}
	for _, want := range []string{"--no-install-recommends", "bash", "openssl", "iproute2", "ca-certificates", "coreutils", "grep", "sed", "gawk",
		"COPY mash mashctl nats-server nodeinit nats mash-local-nats-setup.sh /usr/local/bin/", "COPY plugin /opt/mash-repo/plugin", "USER mash", "--uid 1000", "WORKDIR /node"} {
		if !strings.Contains(containerfile, want) {
			t.Errorf("the Containerfile lacks %q", want)
		}
	}
	if strings.Contains(containerfile, "FROM scratch") || strings.Contains(containerfile, "USER root") {
		t.Error("the node image is not scratch and not root")
	}
}

func TestBuildContextHoldsTheBinariesTheNatsCommandAndTheScript(t *testing.T) {
	linuxOnly(t)
	r := newPodRun(t)
	openPodman(t, podmanFake(nil), r)
	ctx := filepath.Join(r.Dir, "podman-ctx")
	for _, f := range []string{"nats-server", "nats", "mash-local-nats-setup.sh", "Containerfile"} {
		if _, err := os.Stat(filepath.Join(ctx, f)); err != nil {
			t.Errorf("%s is not in the build context: %v", f, err)
		}
	}
	if st, _ := os.Stat(filepath.Join(ctx, "nats")); st == nil || st.Mode().Perm()&0o111 == 0 {
		t.Error("the nats command is not executable")
	}
	if len(r.ImageLines) != 3 || !strings.HasPrefix(r.ImageLines[2], "setup script: setup.sh sha256:") || !strings.HasPrefix(r.ImageLines[0], "base image: "+baseImage+", digest "+baseImageDigest) || !strings.HasPrefix(r.ImageLines[1], "nats command: natscli "+natsCLIVersion) {
		t.Errorf("record lines: %v", r.ImageLines)
	}
	if got := strings.Join(r.EnvLines(nil), "\n"); !strings.Contains(got, "base image: ") || !strings.Contains(got, "nats command: ") {
		t.Errorf("EnvLines lack the image lines: %s", got)
	}
}

func TestBaseImageDigestIsRequestedAndVerified(t *testing.T) {
	linuxOnly(t)
	r := newPodRun(t)
	openPodman(t, podmanFake(nil), r)
	want := "base image: " + baseImage + ", digest " + baseImageDigest + " (requested; verified after the build)"
	if r.ImageLines[0] != want {
		t.Errorf("%v", r.ImageLines)
	}
	if !strings.Contains(containerfile, "\nFROM "+baseImageTag+"@"+baseImageDigest+"\n") {
		t.Error("the Containerfile does not pin the base by the digest in the Go constant")
	}
	// another digest after the build fails the run
	r2 := newPodRun(t)
	f := podmanFake(func(c Cmd) (string, string, int, bool) {
		if strings.HasPrefix(strings.Join(c.Argv, " "), "podman image inspect --format {{.Digest}} "+baseImage) {
			return "sha256:abc123\n", "", 0, true
		}
		return "", "", 0, false
	})
	p := &Podman{Runner: f, LookPath: func(string) (string, error) { return "/usr/bin/podman", nil }, Pick: func(int) int { return 9 }}
	if _, err := p.Open(r2); err == nil || !strings.Contains(err.Error(), "not the requested") {
		t.Errorf("a base with another digest must fail the run: %v", err)
	}
	if verifyBaseImage(baseImageDigest+"\n") != nil || verifyBaseImage("") == nil {
		t.Error("verifyBaseImage")
	}
}

func TestCleanLeakedRemovesOnlyTheNamedPaths(t *testing.T) {
	dir := t.TempDir()
	export := filepath.Join(dir, "sandbox-export")
	out := filepath.Join(dir, "out")
	for _, d := range []string{filepath.Join(export, "out"), out} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	seed := "SUAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	leaked := filepath.Join(out, "03-x.out")
	other := filepath.Join(out, "04-y.out")
	for _, f := range []string{filepath.Join(export, "out", "a.out"), leaked} {
		if err := os.WriteFile(f, []byte(seed), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(other, []byte("fine"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cleanLeaked(export, leaked, filepath.Join(dir, "gone")); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{export, leaked} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s was not removed", p)
		}
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("a file that was not named was removed: %v", err)
	}
}

func TestNatsCLIPinTable(t *testing.T) {
	// the table as shipped (TestMain replaces the entry of this architecture only for the build tests)
	for _, arch := range []string{"amd64", "arm64"} {
		a, err := natsCLIFor(arch)
		if err != nil {
			t.Fatal(err)
		}
		if len(a.SHA256) != 64 || strings.Trim(a.SHA256, "0123456789abcdef") != "" {
			t.Errorf("%s: sha256 %q", arch, a.SHA256)
		}
		if arch == runtime.GOARCH {
			continue // replaced by TestMain
		}
		if !strings.HasPrefix(a.URL, natsCLIURLPrefix+natsCLIVersion+"/") || !strings.HasSuffix(a.URL, "-linux-"+arch+".zip") || !strings.Contains(a.URL, strings.TrimPrefix(natsCLIVersion, "v")) {
			t.Errorf("%s: URL %q", arch, a.URL)
		}
	}
	if _, err := natsCLIFor("riscv64"); err == nil {
		t.Error("an unpinned architecture is accepted")
	}
	if natsCLIVersion != "v0.5.1" || !strings.HasPrefix(natsCLIURLPrefix, "https://") {
		t.Errorf("pin %q %q", natsCLIVersion, natsCLIURLPrefix)
	}
}

func natsZip(t *testing.T, name string, body string) ([]byte, string) {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	w, _ := zw.Create(name)
	_, _ = w.Write([]byte(body))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b.Bytes())
	return b.Bytes(), hex.EncodeToString(sum[:])
}

func TestNatsCLIBinaryVerifiesTheSha(t *testing.T) {
	zb, sha := natsZip(t, "nats-0.5.1-linux-amd64/nats", "the binary")
	b, err := natsCLIBinary(zb, sha)
	if err != nil || string(b) != "the binary" {
		t.Fatalf("%q %v", b, err)
	}
	if _, err := natsCLIBinary(zb, strings.Repeat("0", 64)); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Errorf("a wrong sha256 is accepted: %v", err)
	}
	other, osha := natsZip(t, "nats-0.5.1-linux-amd64/README", "x")
	if _, err := natsCLIBinary(other, osha); err == nil {
		t.Error("a zip without a nats file is accepted")
	}
	// the download goes into the context directory only, and a bad download writes nothing
	dir := t.TempDir()
	if err := writeNatsCLI(dir, func(string) ([]byte, error) { return []byte("not the pinned file"), nil }); err == nil {
		t.Error("a download that does not match the pin is accepted")
	}
	if _, err := os.Stat(filepath.Join(dir, "nats")); err == nil {
		t.Error("an unverified download was written")
	}
}

func TestInsideIsRefusedOutsideAContainer(t *testing.T) {
	old := insideContainer
	insideContainer = func() bool { return false }
	defer func() { insideContainer = old }()
	var so, se bytes.Buffer
	if code := realMain([]string{"at001", "--port", "4222", "--inside"}, &so, &se); code != 2 || !strings.Contains(se.String(), "--inside is refused outside a container") {
		t.Fatalf("code %d, stderr %q", code, se.String())
	}
	if so.Len() != 0 {
		t.Errorf("stdout %q", so.String())
	}
}

func TestHostNeverStartsAServerWithoutTheFlag(t *testing.T) {
	old := insideContainer
	insideContainer = func() bool { return false }
	defer func() { insideContainer = old }()
	// StartServer itself refuses on a host: the script is not run.
	a := &AT{R: &Run{Runner: &Runner{Out: t.TempDir()}}, C: &Checks{}}
	if a.StartServer(Home{Dir: t.TempDir()}) {
		t.Fatal("StartServer ran on the host")
	}
	rows := a.C.All()
	if len(rows) != 1 || rows[0].Pass || !strings.Contains(rows[0].Detail, "setup runs only inside a node container") {
		t.Errorf("%+v", rows)
	}
	if a.R.Runner.seq != 0 {
		t.Error("a command was run")
	}
}

func TestLocalHostsRefuseTheSetupScript(t *testing.T) {
	old := insideContainer
	insideContainer = func() bool { return false }
	defer func() { insideContainer = old }()
	r := &Run{Dir: t.TempDir(), Runner: &Runner{Out: t.TempDir()}}
	s := &At5{R: r, C: &Checks{}, secrets: map[string][]byte{}}
	hs, err := Local{}.Open(r)
	if err != nil {
		t.Fatal(err)
	}
	s.Hosts = hs
	if s.InitM1() {
		t.Fatal("InitM1 ran the script on a local host")
	}
	rows := s.C.All()
	if len(rows) != 1 || !strings.Contains(rows[0].Detail, "setup runs only inside a node container") || r.Runner.seq != 0 {
		t.Errorf("%+v seq %d", rows, r.Runner.seq)
	}
}

func TestSandboxArgvAndEnv(t *testing.T) {
	got := strings.Join(sandboxArgs("at002", 4222), " ")
	if got != "/node/bin/liveat at002 --port 4222 --inside" {
		t.Errorf("%s", got)
	}
	r := &Run{Repo: "../..", Revision: "abc123 (clean)", ImageLines: []string{"a: 1", "b: 2"}}
	env := strings.Join(sandboxEnv(r), "\n")
	for _, want := range []string{"HOME=/node", "TMPDIR=/node/tmp", "LIVEAT_REVISION=abc123 (clean)", "LIVEAT_NATS_PIN=v", "LIVEAT_IMAGE_LINES=a: 1|b: 2"} {
		if !strings.Contains(env, want) {
			t.Errorf("env lacks %q:\n%s", want, env)
		}
	}
	if strings.Contains(env, "MASH_") || strings.Contains(env, "PATH=") {
		t.Errorf("the caller's environment leaks:\n%s", env)
	}
}

func TestSandboxContainerIsRootlessNonRootNoNetworkNoPorts(t *testing.T) {
	linuxOnly(t)
	r := newPodRun(t)
	f := podmanFake(nil)
	p := &Podman{Runner: f, LookPath: func(string) (string, error) { return "/usr/bin/podman", nil }}
	h, err := p.OpenSandbox(r)
	if err != nil {
		t.Fatal(err)
	}
	want := "podman run -d --name mash-at005-" + testRunID + "-sbx --network none --user 1000:1000 --label mash-at005=" + testRunID + " --read-only=false mash-at005:" + testRunID + " /usr/local/bin/nodeinit serve"
	if !f.has(want) || h.name != "mash-at005-"+testRunID+"-sbx" {
		t.Errorf("missing %q in:\n%s", want, strings.Join(f.lines(), "\n"))
	}
	for _, l := range f.lines() {
		for _, bad := range []string{" -p ", "--publish", "--privileged", "-v ", "--volume", "--mount", "--user root", "--user 0"} {
			if strings.Contains(l+" ", bad) {
				t.Errorf("%q has %q", l, bad)
			}
		}
	}
	if f.has("podman network create") {
		t.Error("the sandbox makes a network")
	}
	// the one container is removed by this run's label at the end
	if err := p.destroy(f, r); err != nil && !strings.Contains(err.Error(), "remain") {
		t.Error(err)
	}
	if !f.has("--filter label=mash-at005=" + testRunID) {
		t.Error("the sandbox is not removed by its run's label")
	}
}

func TestExportRunCopiesOnlyOutputRecordsAndLog(t *testing.T) {
	dir := t.TempDir()
	r := &Run{Dir: filepath.Join(dir, "run"), Out: filepath.Join(dir, "run", "out")}
	for _, d := range []string{r.Dir, r.Out, filepath.Join(r.Dir, "at001"), filepath.Join(r.Dir, "bin")} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for f, body := range map[string]string{"out/01-x.out": "hello", "record-AT-001.md": "# record", "run.log": "log", "at001/server.key": "SECRETKEY", "bin/mash": "elf", "stray.txt": "x"} {
		if err := os.WriteFile(filepath.Join(r.Dir, f), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dst := filepath.Join(dir, "export")
	if err := exportRun(r, dst); err != nil {
		t.Fatal(err)
	}
	var got []string
	_ = filepath.WalkDir(dst, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(dst, p)
			got = append(got, rel)
		}
		return nil
	})
	if strings.Join(got, ",") != "out/01-x.out,record-AT-001.md,run.log" {
		t.Errorf("exported %v", got)
	}
	if err := exportRun(r, dst); err == nil {
		t.Error("export overwrote an existing directory")
	}
}

func TestPrepareInsideNeedsWhatTheSandboxHands(t *testing.T) {
	r := &Run{Runner: &Runner{}}
	if err := r.PrepareInside(func(string) string { return "" }); err == nil || !strings.Contains(err.Error(), "--inside is for the run the sandbox starts") {
		t.Errorf("%v", err)
	}
}

func TestPrepareInsideSetsTheRepoToThePluginCopy(t *testing.T) {
	r := &Run{Runner: &Runner{}, Dir: t.TempDir()}
	_ = r.PrepareInside(func(k string) string {
		if k == envInsideRevision || k == envInsideNatsPin {
			return "x"
		}
		return ""
	})
	if r.Repo != nodeRepo || nodeRepo != "/opt/mash-repo" {
		t.Errorf("Repo %q", r.Repo)
	}
}
