package scripts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/bogworks/mash/internal/testenv"
)

const fakeToken = "fake-token-for-tests-123456"

// fakeSynadia serves just the control-plane endpoints quickstart-synadia.sh uses, with NATS users whose creds are
// minted by the in-process test server with exactly the permissions the user was created with.
type fakeSynadia struct {
	e            *testenv.Env
	srv          *httptest.Server
	maxStreams   int
	failCreds    bool   // endpoint users' creds answer 500 (the admin user's do not)
	failCredsFor string // the creds of the user with this name answer 500
	wrongCreds   bool   // endpoint users' creds are another (freshly minted) user's

	mu    sync.Mutex
	users map[string]*fakeUser // by id
	posts int                  // POST /nats-users creations
	reqs  int                  // every request that reached the server
	next  int
}

type fakeUser struct {
	id, name, key string
	creds         []byte
}

func newFake(t *testing.T, e *testenv.Env, maxStreams int) *fakeSynadia {
	f := &fakeSynadia{e: e, maxStreams: maxStreams, users: map[string]*fakeUser{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeSynadia) names() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, u := range f.users {
		out = append(out, u.name)
	}
	return out
}

func (f *fakeSynadia) set(fn func(f *fakeSynadia)) { f.mu.Lock(); defer f.mu.Unlock(); fn(f) }

func (f *fakeSynadia) postCount() int { f.mu.Lock(); defer f.mu.Unlock(); return f.posts }
func (f *fakeSynadia) reqCount() int  { f.mu.Lock(); defer f.mu.Unlock(); return f.reqs }

func (f *fakeSynadia) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.reqs++
	f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+fakeToken {
		http.Error(w, `{"error":"unauthorized"}`, 401)
		return
	}
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	defer f.mu.Unlock()
	p := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	js := func(v any) { w.Header().Set("Content-Type", "application/json"); _ = json.NewEncoder(w).Encode(v) }
	switch {
	case r.Method == "GET" && r.URL.Path == "/teams":
		js(map[string]any{"items": []any{map[string]any{"id": "team-1", "name": "Test Team"}}})
	case r.Method == "GET" && r.URL.Path == "/teams/team-1/systems":
		js(map[string]any{"items": []any{map[string]any{"id": "sys-1", "name": "Test System"}}})
	case r.Method == "GET" && r.URL.Path == "/systems/sys-1/accounts":
		js(map[string]any{"items": []any{map[string]any{
			"id": "acct-1", "name": "Default", "account_public_key": f.e.Account,
			"system": map[string]any{"id": "sys-1", "name": "Test System", "server_urls": f.e.URL},
			"claims": map[string]any{"nats": map[string]any{"limits": map[string]any{
				"tiered_limits": map[string]any{"R1": map[string]any{"streams": f.maxStreams}}}}},
			"jwt": "fake-account-jwt-do-not-leak"}}})
	case r.Method == "GET" && r.URL.Path == "/accounts/acct-1/account-sk-groups":
		js(map[string]any{"items": []any{map[string]any{"id": "skg-1", "name": "Default", "disabled": false, "is_scoped": false, "missing_seed": false}}})
	case r.Method == "GET" && r.URL.Path == "/accounts/acct-1/nats-users":
		items := []any{}
		for _, u := range f.users {
			items = append(items, map[string]any{"id": u.id, "name": u.name, "user_public_key": u.key})
		}
		js(map[string]any{"items": items})
	case r.Method == "POST" && r.URL.Path == "/accounts/acct-1/nats-users":
		var in struct {
			Name string `json:"name"`
			SKG  string `json:"sk_group_id"`
			JWT  struct {
				Pub struct{ Allow []string } `json:"pub"`
				Sub struct{ Allow []string } `json:"sub"`
			} `json:"jwt_settings"`
		}
		if json.Unmarshal(body, &in) != nil || in.Name == "" || in.SKG != "skg-1" {
			http.Error(w, `{"error":"bad"}`, 400)
			return
		}
		f.next++
		f.posts++
		creds, key, err := f.e.MintUser(in.JWT.Pub.Allow, in.JWT.Sub.Allow)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		u := &fakeUser{id: fmt.Sprintf("user-%d", f.next), name: in.Name, key: key, creds: creds}
		f.users[u.id] = u
		w.WriteHeader(201)
		js(map[string]any{"id": u.id, "name": u.name, "user_public_key": u.key})
	case r.Method == "POST" && len(p) == 3 && p[0] == "nats-users" && p[2] == "creds":
		u := f.users[p[1]]
		if u == nil {
			http.Error(w, `{"error":"not found"}`, 404)
			return
		}
		if f.failCreds && !strings.HasPrefix(u.name, "mash-quickstart-admin-") {
			http.Error(w, `{"error":"boom"}`, 500)
			return
		}
		if f.failCredsFor != "" && u.name == f.failCredsFor {
			http.Error(w, `{"error":"boom"}`, 500)
			return
		}
		creds := u.creds
		if f.wrongCreds && !strings.HasPrefix(u.name, "mash-quickstart-admin-") {
			other, _, err := f.e.MintUser([]string{"x.>"}, []string{"x.>"})
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			creds = other
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write(creds)
	case r.Method == "DELETE" && len(p) == 2 && p[0] == "nats-users":
		if f.users[p[1]] == nil {
			http.Error(w, `{"error":"not found"}`, 404)
			return
		}
		delete(f.users, p[1])
		w.WriteHeader(204)
	default:
		http.Error(w, `{"error":"not found"}`, 404)
	}
}

var builtMash string

// mashBinary builds mash once per test binary run.
func mashBinary(t *testing.T) string {
	t.Helper()
	if builtMash != "" {
		return builtMash
	}
	dir, err := os.MkdirTemp("", "quickstart-mash-")
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "mash")
	if out, err := exec.Command("go", "build", "-o", bin, "../cmd/mash").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	builtMash = bin
	return bin
}

func TestMain(m *testing.M) {
	code := m.Run()
	if builtMash != "" {
		_ = os.RemoveAll(filepath.Dir(builtMash))
	}
	os.Exit(code)
}

type run struct {
	out  string
	code int
	home string
}

func runScript(t *testing.T, f *fakeSynadia, home string, extraEnv []string, args ...string) run {
	t.Helper()
	for _, tool := range []string{"curl", "jq", "bash"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	script, _ := filepath.Abs("quickstart-synadia.sh")
	margs := []string{script}
	var pathEnv []string
	if slices.Contains(extraEnv, "QS_DEFAULT_MASH=1") { // no --mash: the script finds mash on PATH, as a user's would
		extraEnv = slices.DeleteFunc(slices.Clone(extraEnv), func(kv string) bool { return kv == "QS_DEFAULT_MASH=1" })
		bin := mashBinary(t)
		dir := t.TempDir()
		if err := os.Symlink(bin, filepath.Join(dir, "mash")); err != nil {
			t.Fatal(err)
		}
		pathEnv = []string{"PATH=" + dir + string(os.PathListSeparator) + os.Getenv("PATH")}
	} else {
		margs = append(margs, "--mash", mashBinary(t))
	}
	cmd := exec.Command("bash", append(margs, args...)...)
	var env []string
	for _, kv := range os.Environ() { // a developer's own MASH_* settings must not leak into the run
		if !strings.HasPrefix(kv, "MASH_") && !strings.HasPrefix(kv, "XDG_CONFIG_HOME=") {
			env = append(env, kv)
		}
	}
	cmd.Env = append(env, "HOME="+home, "SYNADIA_API_URL="+f.srv.URL, "SYNADIA_API_TOKEN="+fakeToken,
		"MASH_SETTLE_MS=20", "USER=alice")
	cmd.Env = append(cmd.Env, extraEnv...)
	cmd.Env = append(cmd.Env, pathEnv...)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	cmd.Stdin = strings.NewReader("")
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return run{buf.String(), code, home}
}

func readConfig(t *testing.T, home string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(home, ".config", "mash", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestQuickstartCreatesEndpointsAndDeletesAdmin(t *testing.T) {
	e := testenv.Start(t)
	f := newFake(t, e, 5)
	home := t.TempDir()
	r := runScript(t, f, home, nil, "--owner", "alice", "--endpoint", "alice-a", "--endpoint", "alice-b")
	if r.code != 0 {
		t.Fatalf("exit %d:\n%s", r.code, r.out)
	}
	if strings.Contains(r.out, fakeToken) || strings.Contains(r.out, "fake-account-jwt") {
		t.Errorf("output leaks a secret:\n%s", r.out)
	}
	cfg := readConfig(t, home)
	if cfg["url"] != e.URL || cfg["owner"] != "alice" {
		t.Errorf("config = %v, want url %s owner alice", cfg, e.URL)
	}
	for _, n := range []string{"alice-a", "alice-b"} {
		fi, err := os.Stat(filepath.Join(home, ".config", "mash", n+".creds"))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("%s.creds mode = %v, want 0600", n, fi.Mode().Perm())
		}
	}
	names := f.names()
	if len(names) != 2 {
		t.Errorf("users left = %v, want only the two endpoints (the admin user must be deleted)", names)
	}
	for _, n := range names {
		if strings.HasPrefix(n, "mash-quickstart-admin-") {
			t.Errorf("admin user %s survived", n)
		}
	}
	if !strings.Contains(r.out, "[verified]") {
		t.Errorf("no verified test message in output:\n%s", r.out)
	}
}

// A --config-dir that does not end in .config/mash works: mash is run with MASH_CONFIG_DIR set to it, and HOME is untouched.
func TestQuickstartCustomConfigDir(t *testing.T) {
	e := testenv.Start(t)
	f := newFake(t, e, 5)
	home := t.TempDir()
	custom := filepath.Join(t.TempDir(), "elsewhere")
	r := runScript(t, f, home, nil, "--config-dir", custom, "--owner", "alice", "--endpoint", "alice-a", "--endpoint", "alice-b")
	if r.code != 0 {
		t.Fatalf("exit %d:\n%s", r.code, r.out)
	}
	for _, n := range []string{"config.json", "alice-a.creds", "alice-b.creds"} {
		if _, err := os.Stat(filepath.Join(custom, n)); err != nil {
			t.Errorf("%s not in the custom config dir: %v", n, err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".config")); err == nil {
		t.Errorf("the default config directory was created")
	}
	if !strings.Contains(r.out, "[verified]") {
		t.Errorf("no verified test message in output:\n%s", r.out)
	}
}

// With no --config-dir, XDG_CONFIG_HOME/mash is the default.
func TestQuickstartXDGConfigHomeDefault(t *testing.T) {
	e := testenv.Start(t)
	f := newFake(t, e, 5)
	home := t.TempDir()
	xdg := t.TempDir()
	r := runScript(t, f, home, []string{"XDG_CONFIG_HOME=" + xdg}, "--no-test", "--owner", "alice", "--endpoint", "alice-a")
	if r.code != 0 {
		t.Fatalf("exit %d:\n%s", r.code, r.out)
	}
	if _, err := os.Stat(filepath.Join(xdg, "mash", "config.json")); err != nil {
		t.Errorf("config.json not under XDG_CONFIG_HOME/mash: %v", err)
	}
}

func TestQuickstartRelativeConfigDirRefused(t *testing.T) {
	e := testenv.Start(t)
	f := newFake(t, e, 5)
	r := runScript(t, f, t.TempDir(), nil, "--dry-run", "--config-dir", "rel/dir")
	if r.code == 0 || !strings.Contains(r.out, "absolute") {
		t.Fatalf("exit %d:\n%s", r.code, r.out)
	}
}

func TestQuickstartDryRunCreatesNothing(t *testing.T) {
	e := testenv.Start(t)
	f := newFake(t, e, 5)
	home := t.TempDir()
	r := runScript(t, f, home, nil, "--dry-run", "--owner", "alice", "--endpoint", "alice-a")
	if r.code != 0 || !strings.Contains(r.out, "Dry run") {
		t.Fatalf("exit %d:\n%s", r.code, r.out)
	}
	if f.postCount() != 0 || len(f.names()) != 0 {
		t.Errorf("dry run created users: %v (%d posts)", f.names(), f.postCount())
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "mash")); err == nil {
		t.Errorf("dry run created the config directory")
	}
}

func TestQuickstartDeletesAdminOnFailure(t *testing.T) {
	e := testenv.Start(t)
	f := newFake(t, e, 5)
	f.mu.Lock()
	f.failCreds = true
	f.mu.Unlock()
	r := runScript(t, f, t.TempDir(), nil, "--owner", "alice", "--endpoint", "alice-a")
	if r.code == 0 {
		t.Fatalf("want a failure:\n%s", r.out)
	}
	for _, n := range f.names() {
		if strings.HasPrefix(n, "mash-quickstart-admin-") {
			t.Errorf("admin user %s survived a failed run", n)
		}
	}
	if f.postCount() == 0 {
		t.Errorf("the run never got as far as creating the admin user")
	}
}

func TestQuickstartRefusals(t *testing.T) {
	e := testenv.Start(t)
	t.Run("capacity", func(t *testing.T) {
		f := newFake(t, e, 3)
		r := runScript(t, f, t.TempDir(), nil, "--owner", "alice", "--endpoint", "a", "--endpoint", "b")
		if r.code == 0 || !strings.Contains(r.out, "allows 3 streams") || f.postCount() != 0 {
			t.Errorf("exit %d, posts %d:\n%s", r.code, f.postCount(), r.out)
		}
	})
	t.Run("directory already holds endpoints", func(t *testing.T) {
		// Two endpoints are provisioned first; the account then allows 5 streams, so 2 shared + 2 held + 2 planned
		// does not fit and the script must stop before it creates any endpoint user.
		home := t.TempDir()
		first := runScript(t, newFake(t, e, 5), home, nil, "--owner", "alice", "--endpoint", "old-a", "--endpoint", "old-b", "--no-test")
		if first.code != 0 {
			t.Fatalf("setup run: exit %d:\n%s", first.code, first.out)
		}
		f := newFake(t, e, 5)
		r := runScript(t, f, home, nil, "--owner", "alice", "--endpoint", "new-a", "--endpoint", "new-b")
		if r.code == 0 || !strings.Contains(r.out, "allows 5 streams") || !strings.Contains(r.out, "old-a") ||
			!strings.Contains(r.out, "old-b") || !strings.Contains(r.out, "Removing an endpoint") {
			t.Fatalf("want the stream hint naming old-a and old-b, exit %d:\n%s", r.code, r.out)
		}
		if f.postCount() != 1 {
			t.Errorf("posts = %d, want 1 (the admin user only)", f.postCount())
		}
		if n := f.names(); len(n) != 0 {
			t.Errorf("users left = %v, want none (the admin user must be deleted)", n)
		}
	})
	// A refusal after config.json was written (mash admin init has run) must not leave the new url and owner behind.
	t.Run("failed run restores or removes the config", func(t *testing.T) {
		e2 := testenv.Start(t) // its own directory, so this subtest does not depend on the others
		first := runScript(t, newFake(t, e2, 5), t.TempDir(), nil, "--owner", "alice", "--endpoint", "old-a", "--endpoint", "old-b", "--no-test")
		if first.code != 0 {
			t.Fatalf("setup run: exit %d:\n%s", first.code, first.out)
		}
		cfgPath := func(home string) string { return filepath.Join(home, ".config", "mash", "config.json") }
		// Same directory, so the plan does not fit (2 shared + 2 held + 2 planned > 5), after init.
		t.Run("existing config with force", func(t *testing.T) {
			f := newFake(t, e2, 5)
			home := t.TempDir()
			if err := os.MkdirAll(filepath.Dir(cfgPath(home)), 0o700); err != nil {
				t.Fatal(err)
			}
			old := []byte("{ \"url\": \"nats://other:4222\",\n  \"owner\": \"alice\" }\n")
			if err := os.WriteFile(cfgPath(home), old, 0o600); err != nil {
				t.Fatal(err)
			}
			r := runScript(t, f, home, nil, "--force", "--owner", "alice", "--endpoint", "new-a", "--endpoint", "new-b")
			if r.code == 0 || !strings.Contains(r.out, "allows 5 streams") || !strings.Contains(r.out, "restored the previous") {
				t.Fatalf("want a refusal that restores the config, exit %d:\n%s", r.code, r.out)
			}
			if got, err := os.ReadFile(cfgPath(home)); err != nil || !bytes.Equal(got, old) {
				t.Errorf("config.json = %q, %v; want the old bytes %q", got, err, old)
			}
		})
		t.Run("fresh run", func(t *testing.T) {
			f := newFake(t, e2, 5)
			home := t.TempDir()
			r := runScript(t, f, home, nil, "--owner", "alice", "--endpoint", "new-a", "--endpoint", "new-b")
			if r.code == 0 || !strings.Contains(r.out, "allows 5 streams") {
				t.Fatalf("want the stream refusal, exit %d:\n%s", r.code, r.out)
			}
			if _, err := os.Stat(cfgPath(home)); !os.IsNotExist(err) {
				t.Errorf("config.json left behind after a failed fresh run: %v", err)
			}
		})
	})
	t.Run("different config without force", func(t *testing.T) {
		f := newFake(t, e, 5)
		home := t.TempDir()
		dir := filepath.Join(home, ".config", "mash")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"url":"nats://other:4222","owner":"alice"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		r := runScript(t, f, home, nil, "--owner", "alice", "--endpoint", "a")
		if r.code == 0 || !strings.Contains(r.out, "--force") || f.postCount() != 0 {
			t.Errorf("exit %d, posts %d:\n%s", r.code, f.postCount(), r.out)
		}
	})
	t.Run("existing creds file", func(t *testing.T) {
		f := newFake(t, e, 5)
		home := t.TempDir()
		dir := filepath.Join(home, ".config", "mash")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		creds := filepath.Join(dir, "a.creds")
		keep := []byte("precious creds of another account\n")
		if err := os.WriteFile(creds, keep, 0o600); err != nil {
			t.Fatal(err)
		}
		for _, extra := range [][]string{nil, {"--dry-run"}} {
			r := runScript(t, f, home, nil, append([]string{"--owner", "alice", "--endpoint", "a"}, extra...)...)
			if r.code == 0 || !strings.Contains(r.out, creds) || !strings.Contains(r.out, "already exists") || f.postCount() != 0 {
				t.Errorf("%v: exit %d, posts %d:\n%s", extra, r.code, f.postCount(), r.out)
			}
			if got, err := os.ReadFile(creds); err != nil || !bytes.Equal(got, keep) {
				t.Errorf("%v: creds file changed: %q, %v", extra, got, err)
			}
		}
	})
	t.Run("API URL", func(t *testing.T) {
		f := newFake(t, e, 5)
		for _, u := range []string{"http://example.com/api", "ftp://x", "http://127.0.0.1@evil.example/", "http://localhost.evil.example/", "cloud.synadia.com"} {
			r := runScript(t, f, t.TempDir(), []string{"SYNADIA_API_URL=" + u}, "--dry-run", "--endpoint", "a")
			if r.code == 0 || !strings.Contains(r.out, "SYNADIA_API_URL") || strings.Contains(r.out, fakeToken) || f.reqCount() != 0 {
				t.Errorf("%s: exit %d, requests %d:\n%s", u, r.code, f.reqCount(), r.out)
			}
		}
		// https and loopback http pass the URL check (an https host that does not exist fails later, in curl).
		r := runScript(t, f, t.TempDir(), []string{"SYNADIA_API_URL=https://127.0.0.1:1/x"}, "--dry-run", "--endpoint", "a")
		if strings.Contains(r.out, "SYNADIA_API_URL") {
			t.Errorf("https refused:\n%s", r.out)
		}
		r = runScript(t, f, t.TempDir(), nil, "--dry-run", "--owner", "alice", "--endpoint", "a") // the suite's own http://127.0.0.1:port
		if r.code != 0 || f.reqCount() == 0 {
			t.Errorf("loopback http refused: exit %d:\n%s", r.code, r.out)
		}
	})
	t.Run("no token", func(t *testing.T) {
		f := newFake(t, e, 5)
		r := runScript(t, f, t.TempDir(), []string{"SYNADIA_API_TOKEN="}, "--endpoint", "a")
		if r.code == 0 || !strings.Contains(r.out, "no Synadia API token") {
			t.Errorf("exit %d:\n%s", r.code, r.out)
		}
	})
}

// With no --mash the script runs the mash found on PATH. Its mash() wrapper must call that binary by absolute path:
// resolved by name it would call itself forever.
func TestQuickstartDefaultMashFromPath(t *testing.T) {
	e := testenv.Start(t)
	f := newFake(t, e, 5)
	r := runScript(t, f, t.TempDir(), []string{"QS_DEFAULT_MASH=1"}, "--owner", "alice", "--endpoint", "alice-a", "--endpoint", "alice-b")
	if r.code != 0 || !strings.Contains(r.out, "provisioned alice-a") {
		t.Fatalf("exit %d:\n%s", r.code, r.out)
	}
}

func credsPath(home, n string) string { return filepath.Join(home, ".config", "mash", n+".creds") }

// The second endpoint's creds cannot be fetched after the first was provisioned: the first endpoint's user, creds and
// the config stay, the second's user is deleted, and so is the admin user.
func TestQuickstartPartialFailureKeepsWhatSucceeded(t *testing.T) {
	e := testenv.Start(t)
	f := newFake(t, e, 6)
	f.set(func(f *fakeSynadia) { f.failCredsFor = "alice-b" })
	home := t.TempDir()
	r := runScript(t, f, home, nil, "--owner", "alice", "--endpoint", "alice-a", "--endpoint", "alice-b")
	if r.code == 0 {
		t.Fatalf("want a failure:\n%s", r.out)
	}
	if names := f.names(); len(names) != 1 || names[0] != "alice-a" {
		t.Errorf("users left = %v, want only alice-a", names)
	}
	if _, err := os.Stat(credsPath(home, "alice-a")); err != nil {
		t.Errorf("alice-a creds gone: %v", err)
	}
	if _, err := os.Stat(credsPath(home, "alice-b")); err == nil {
		t.Errorf("alice-b creds exist")
	}
	if cfg := readConfig(t, home); cfg["owner"] != "alice" {
		t.Errorf("config = %v", cfg)
	}
	if !strings.Contains(r.out, "removed the unprovisioned NATS user(s): alice-b") || !strings.Contains(r.out, "Remaining, provisioned and working: alice-a") {
		t.Errorf("output does not say which users were removed and kept:\n%s", r.out)
	}
}

func TestQuickstartRefusesStaleDirectoryRecord(t *testing.T) {
	e := testenv.Start(t)
	home := t.TempDir()
	if first := runScript(t, newFake(t, e, 6), home, nil, "--owner", "alice", "--endpoint", "old-a", "--no-test"); first.code != 0 {
		t.Fatalf("setup run: exit %d:\n%s", first.code, first.out)
	}
	_ = os.Remove(credsPath(home, "old-a")) // the user and its creds are gone; only the directory record is left
	// A new fake has no NATS users: old-a's directory record is stale.
	f := newFake(t, e, 6)
	r := runScript(t, f, home, nil, "--owner", "alice", "--endpoint", "old-a2", "--endpoint", "old-a")
	if r.code == 0 || !strings.Contains(r.out, "mash admin deprovision old-a") || !strings.Contains(r.out, "stale or taken") {
		t.Fatalf("want a stale-record refusal, exit %d:\n%s", r.code, r.out)
	}
	if f.postCount() != 1 || len(f.names()) != 0 {
		t.Errorf("posts = %d, users = %v; want only the (deleted) admin user", f.postCount(), f.names())
	}
}

func TestQuickstartRefusesMismatchedCreds(t *testing.T) {
	e := testenv.Start(t)
	f := newFake(t, e, 5)
	f.set(func(f *fakeSynadia) { f.wrongCreds = true })
	home := t.TempDir()
	r := runScript(t, f, home, nil, "--owner", "alice", "--endpoint", "alice-a")
	if r.code == 0 || !strings.Contains(r.out, "different user public key") {
		t.Fatalf("want a refusal, exit %d:\n%s", r.code, r.out)
	}
	if n := f.names(); len(n) != 0 {
		t.Errorf("users left = %v, want none", n)
	}
	if _, err := os.Stat(credsPath(home, "alice-a")); err == nil {
		t.Errorf("mismatched creds were written")
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "mash", "config.json")); !os.IsNotExist(err) {
		t.Errorf("config.json left behind: %v", err)
	}
}

// wrapperMash writes a --mash script that records every call in <dir>/calls, runs body first (it may exit), and then
// delegates to the real binary.
func wrapperMash(t *testing.T, body string) (script, calls string) {
	t.Helper()
	dir := t.TempDir()
	script, calls = filepath.Join(dir, "mash"), filepath.Join(dir, "calls")
	src := "#!/bin/sh\necho \"$*\" >>'" + calls + "'\n" + body + "\nexec '" + mashBinary(t) + "' \"$@\"\n"
	if err := os.WriteFile(script, []byte(src), 0o700); err != nil {
		t.Fatal(err)
	}
	return script, calls
}

func lockPath(home string) string { return filepath.Join(home, ".config", "mash", ".quickstart.lock") }

func TestQuickstartConfigDirLock(t *testing.T) {
	e := testenv.Start(t)
	t.Run("held lock refuses a second run", func(t *testing.T) {
		f := newFake(t, e, 5)
		home := t.TempDir()
		if err := os.MkdirAll(lockPath(home), 0o700); err != nil {
			t.Fatal(err)
		}
		r := runScript(t, f, home, nil, "--owner", "alice", "--endpoint", "a")
		if r.code == 0 || !strings.Contains(r.out, lockPath(home)) || !strings.Contains(r.out, "another quickstart run") || !strings.Contains(r.out, "rm "+lockPath(home)+"/pid && rmdir "+lockPath(home)) || f.postCount() != 0 {
			t.Fatalf("exit %d, posts %d:\n%s", r.code, f.postCount(), r.out)
		}
		if _, err := os.Stat(lockPath(home)); err != nil {
			t.Errorf("the refused run removed the other run's lock: %v", err)
		}
		if _, err := os.Stat(filepath.Join(home, ".config", "mash", "config.json")); err == nil {
			t.Errorf("config.json written despite the lock")
		}
	})
	t.Run("lock removed after success", func(t *testing.T) {
		home := t.TempDir()
		r := runScript(t, newFake(t, e, 5), home, nil, "--owner", "alice", "--endpoint", "a", "--no-test")
		if r.code != 0 {
			t.Fatalf("exit %d:\n%s", r.code, r.out)
		}
		if _, err := os.Stat(lockPath(home)); !os.IsNotExist(err) {
			t.Errorf("lock left after success: %v", err)
		}
	})
	t.Run("lock removed after failure", func(t *testing.T) {
		f := newFake(t, e, 5)
		f.set(func(f *fakeSynadia) { f.failCreds = true })
		home := t.TempDir()
		r := runScript(t, f, home, nil, "--owner", "alice", "--endpoint", "a")
		if r.code == 0 {
			t.Fatalf("want a failure:\n%s", r.out)
		}
		if _, err := os.Stat(lockPath(home)); !os.IsNotExist(err) {
			t.Errorf("lock left after failure: %v", err)
		}
	})
	t.Run("dry run takes no lock and creates nothing", func(t *testing.T) {
		home := t.TempDir()
		if r := runScript(t, newFake(t, e, 5), home, nil, "--dry-run", "--owner", "alice", "--endpoint", "a"); r.code != 0 {
			t.Fatalf("exit %d:\n%s", r.code, r.out)
		}
		if _, err := os.Stat(filepath.Join(home, ".config")); err == nil {
			t.Errorf("dry run created the config directory")
		}
	})
}

// failProvisionBody makes `admin provision alice-b` fail after remembering its --user key. A guarded `admin deprovision
// alice-b --user KEY` then behaves as mode says: "own" succeeds (the record is this run's), "foreign" refuses as the real
// guard does for another admin's record, "changed" refuses because the record kept changing or now names another user, and "norecord"
// runs the real mash against the live test server, where no record exists.
func failProvisionBody(mode string) string {
	body := `if [ "$1" = admin ] && [ "$2" = provision ] && [ "$3" = alice-b ]; then echo "$5" >"$0.key"; echo "provision: boom" >&2; exit 1; fi
if [ "$1" = admin ] && [ "$2" = deprovision ] && [ "$3" = alice-b ]; then
  [ "$4" = --user ] && [ "$5" = "$(cat "$0.key")" ] || { echo "deprovision: wrong or missing --user" >&2; exit 1; }
`
	switch mode {
	case "own":
		body += "  exit 0\n"
	case "foreign":
		body += "  echo 'mash: refusing to deprovision alice-b: its record belongs to another user key (left in place)' >&2; exit 1\n"
	case "changed":
		body += "  echo 'mash: refusing to deprovision alice-b: the record for alice-b changed; left in place' >&2; exit 1\n"
	}
	return body + "fi"
}

func TestQuickstartDeprovisionsHalfProvisionedEndpoint(t *testing.T) {
	e := testenv.Start(t)
	f := newFake(t, e, 6)
	wrapper, calls := wrapperMash(t, failProvisionBody("own"))
	home := t.TempDir()
	r := runScript(t, f, home, nil, "--mash", wrapper, "--owner", "alice", "--endpoint", "alice-a", "--endpoint", "alice-b")
	if r.code == 0 {
		t.Fatalf("want a failure:\n%s", r.out)
	}
	b, _ := os.ReadFile(calls)
	if !strings.Contains(string(b), "admin deprovision alice-b --user U") || strings.Contains(string(b), "admin deprovision alice-a") || strings.Count(string(b), "admin deprovision") != 1 {
		t.Errorf("want exactly one guarded deprovision of alice-b, calls:\n%s", b)
	}
	if !strings.Contains(r.out, "deprovision alice-b") {
		t.Errorf("output does not mention the deprovision:\n%s", r.out)
	}
	for _, n := range f.names() {
		if strings.HasPrefix(n, "mash-quickstart-admin-") || n == "alice-b" {
			t.Errorf("user %s survived", n)
		}
	}
}

func TestQuickstartLeavesForeignRecordAfterFailedProvision(t *testing.T) {
	for _, mode := range []string{"foreign", "changed"} {
		t.Run(mode, func(t *testing.T) {
			e := testenv.Start(t)
			f := newFake(t, e, 6)
			wrapper, calls := wrapperMash(t, failProvisionBody(mode))
			home := t.TempDir()
			r := runScript(t, f, home, nil, "--mash", wrapper, "--owner", "alice", "--endpoint", "alice-a", "--endpoint", "alice-b")
			if r.code == 0 {
				t.Fatalf("want a failure:\n%s", r.out)
			}
			b, _ := os.ReadFile(calls)
			if !strings.Contains(string(b), "admin deprovision alice-b --user U") {
				t.Errorf("no guarded deprovision, calls:\n%s", b)
			}
			if !strings.Contains(r.out, "a record for alice-b exists that is not this run's; left in place") {
				t.Errorf("output does not say the record was left:\n%s", r.out)
			}
			_, credsErr := os.Stat(filepath.Join(home, ".config", "mash", "alice-b.creds"))
			kept := false
			for _, n := range f.names() {
				if n == "alice-b" {
					kept = true
				}
			}
			if mode == "changed" {
				// The endpoint may still use this run's user: keep it and its creds file.
				if !strings.Contains(r.out, "its NATS user and creds file kept") {
					t.Errorf("output does not say the user and creds were kept:\n%s", r.out)
				}
				if !kept {
					t.Errorf("the NATS user alice-b was deleted although its record remains")
				}
				if credsErr != nil {
					t.Errorf("the creds file alice-b.creds was removed: %v", credsErr)
				}
			} else {
				if kept {
					t.Errorf("the never-used NATS user alice-b survived a foreign record")
				}
				if credsErr == nil {
					t.Errorf("the creds file alice-b.creds survived a foreign record")
				}
			}
		})
	}
}

func TestQuickstartNoRecordAfterFailedProvision(t *testing.T) {
	e := testenv.Start(t)
	f := newFake(t, e, 6)
	wrapper, _ := wrapperMash(t, failProvisionBody("norecord"))
	r := runScript(t, f, t.TempDir(), nil, "--mash", wrapper, "--owner", "alice", "--endpoint", "alice-a", "--endpoint", "alice-b")
	if r.code == 0 {
		t.Fatalf("want a failure:\n%s", r.out)
	}
	if !strings.Contains(r.out, "no directory record for alice-b to remove") {
		t.Errorf("output does not say there was no record:\n%s", r.out)
	}
}

func TestQuickstartMailTestNeedsItsOwnToken(t *testing.T) {
	e := testenv.Start(t)
	f := newFake(t, e, 6)
	// The inbox shows an old [verified] message without this run's token.
	wrapper, _ := wrapperMash(t, `if [ "$1" = inbox ]; then echo '#1 [verified] alice-a: an older message'; exit 0; fi`)
	r := runScript(t, f, t.TempDir(), nil, "--mash", wrapper, "--owner", "alice", "--endpoint", "alice-a", "--endpoint", "alice-b")
	if r.code == 0 || !strings.Contains(r.out, "did not arrive as [verified]") {
		t.Fatalf("a stale verified message passed the mail test, exit %d:\n%s", r.code, r.out)
	}
}
