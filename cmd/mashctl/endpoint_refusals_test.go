package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/enroll"
	"github.com/bogworks/mash/internal/sap"
)

var updateEndpointRefusals = flag.Bool("update-endpoint-refusals", false, "rewrite cmd/mashctl/testdata/endpoint_refusals.golden.txt")

// refEnv is what a refusal case works with: the fleet environment, a directory for the files the case names in its
// arguments, and two valid public key files.
type refEnv struct {
	h    *fleetEnv
	aux  string
	pub  string // path of a public key file
	pub2 string // path of another one
	k1   string // the public key in pub
}

// refCase is one refusal or error: the arguments, what it sets up first, the text its error line holds and its exit
// code. {AUX}, {PUB} and {PUB2} in an argument are replaced by the case's directory and its two public key files.
type refCase struct {
	name     string
	args     []string
	setup    func(t *testing.T, r *refEnv)
	want     string // what the error line holds, after "error: "
	wantExit int    // 2 for a wrong command line, else 1
}

func noEnv(keys ...string) func(t *testing.T, r *refEnv) {
	return func(t *testing.T, r *refEnv) {
		for _, k := range keys {
			delete(r.h.env, k)
		}
	}
}

func setEnv(k, v string) func(t *testing.T, r *refEnv) {
	return func(t *testing.T, r *refEnv) { r.h.env[k] = v }
}

// seq runs the setups in order.
func seq(fs ...func(t *testing.T, r *refEnv)) func(t *testing.T, r *refEnv) {
	return func(t *testing.T, r *refEnv) {
		for _, f := range fs {
			f(t, r)
		}
	}
}

// must runs a command a setup needs and fails the test when it does not exit 0.
func (r *refEnv) must(t *testing.T, args ...string) {
	t.Helper()
	if _, se, code := r.h.run(args...); code != 0 {
		t.Fatalf("setup %v: exit %d: %s", args, code, se)
	}
}

func seeded(instance string) func(t *testing.T, r *refEnv) {
	return func(t *testing.T, r *refEnv) { r.h.seed(ad(instance)) }
}

// noKeys points the key store at an empty directory.
func noKeys(t *testing.T, r *refEnv) {
	r.h.env["MASH_KEYS_DIR"] = filepath.Join(r.aux, "nokeys")
}

func unreachable(t *testing.T, r *refEnv) { r.h.env["MASH_SERVER"] = "nats://127.0.0.1:1" }

func noConfigDir(t *testing.T, r *refEnv) {
	delete(r.h.env, "MASH_CONFIG_DIR")
	r.h.env["HOME"] = ""
}

// badConfig leaves a configuration that does not load: a name rule file that is not there.
func badConfig(t *testing.T, r *refEnv) {
	r.h.env["MASH_NAME_RULE_FILE"] = filepath.Join(r.aux, "no-such-rule.json")
}

// groupWritableDir makes the creds directory one that is refused as a place for creds.
func groupWritableDir(t *testing.T, r *refEnv) {
	if err := os.Chmod(r.h.dir, 0o707); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(r.h.dir, 0o700) })
}

func shortServerSet(t *testing.T, r *refEnv) {
	old := checkServerSet
	checkServerSet = func(context.Context, config.Config, func(string) string, int) error {
		return errors.New("only 2 of 3 servers answered the account lookup")
	}
	t.Cleanup(func() { checkServerSet = old })
}

// enrollFails replaces the library calls of create and reissue by ones that fail with an error that is not a refusal,
// as a server fault in the middle of the enrollment does.
func enrollFails(t *testing.T, r *refEnv) {
	prevCreate, prevReissue := enrollCreate, enrollReissue
	enrollCreate = func(context.Context, enroll.Deps, enroll.Request) (enroll.Result, error) {
		return enroll.Result{}, errors.New("the server stopped answering")
	}
	enrollReissue = func(context.Context, enroll.Deps, sap.Address, string) (enroll.Result, error) {
		return enroll.Result{}, errors.New("the server stopped answering")
	}
	t.Cleanup(func() { enrollCreate, enrollReissue = prevCreate, prevReissue })
}

func lockHeld(instance string) func(t *testing.T, r *refEnv) {
	return func(t *testing.T, r *refEnv) {
		holdLock(t, r.h.creds(ad(instance))+".lock")
		prev := epLockWait
		epLockWait = 20 * time.Millisecond
		t.Cleanup(func() { epLockWait = prev })
	}
}

// burned creates the endpoint with the first public key and deletes it: its address is retired and its key revoked.
func burned(instance string) func(t *testing.T, r *refEnv) {
	return func(t *testing.T, r *refEnv) {
		r.must(t, "endpoint", "create", ad(instance).String(), "--out", filepath.Join(r.aux, "first.join"), "--public-key", r.pub)
		r.must(t, "endpoint", "delete", ad(instance).String(), "--yes")
	}
}

// futureIssued dates the issue record of the seeded endpoint's key an hour ahead.
func futureIssued(instance string) func(t *testing.T, r *refEnv) {
	return func(t *testing.T, r *refEnv) {
		r.h.seed(ad(instance))
		rec, _ := r.h.record(ad(instance))
		r.h.recordIssuedAt(rec.User, time.Now().Add(time.Hour))
	}
}

// otherCreds seeds the endpoints a and b and puts b's creds file at a's path.
func otherCreds(t *testing.T, r *refEnv) {
	r.h.seed(ad("a"))
	r.h.seed(ad("b"))
	other, err := os.ReadFile(r.h.creds(ad("b")))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.h.creds(ad("a")), other, 0o600); err != nil {
		t.Fatal(err)
	}
}

// junkCreds seeds the endpoint a and puts a file that is not creds at its path.
func junkCreds(t *testing.T, r *refEnv) {
	r.h.seed(ad("a"))
	if err := os.WriteFile(r.h.creds(ad("a")), []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// revocationUnknown makes the check of whether a key is revoked fail.
func revocationUnknown(t *testing.T, r *refEnv) {
	r.h.seed(ad("a"))
	fakeKeyRevoked(t, func(string) (bool, error) { return false, errors.New("no system account answered") })
}

var refusalCases = []refCase{
	// create
	{"create_too_many", []string{"endpoint", "create", "o.g.a", "o.g.b"}, nil, "too many arguments", 2},
	{"create_unknown_option", []string{"endpoint", "create", "o.g.a", "--bogus"}, nil, `unknown flag --bogus`, 2},
	{"create_removed_option", []string{"endpoint", "create", "o.g.a", "--make-key"}, nil, `unknown flag --make-key`, 2},
	{"create_slash_form", []string{"endpoint", "create", "o/g/a"}, nil, "not an endpoint address", 2},
	{"create_flat_form", []string{"endpoint", "create", "a"}, nil, "not an endpoint address", 2},
	{"create_public_key_needs_out", []string{"endpoint", "create", "o.g.a", "--public-key", "{PUB}"}, nil, "--public-key needs --out, the join file to write", 2},
	{"create_name_rule", []string{"endpoint", "create", "o.g.blocked"}, func(t *testing.T, r *refEnv) {
		p := filepath.Join(r.aux, "rule.json")
		rule := `{"version":1,"rules":[{"id":"no-blocked","match":{"instance":{"is":"blocked"}},"message":"the name blocked is not allowed"}]}`
		if err := os.WriteFile(p, []byte(rule), 0o600); err != nil {
			t.Fatal(err)
		}
		r.h.env["MASH_NAME_RULE_FILE"] = p
	}, "the name blocked is not allowed", 1},
	{"create_config", []string{"endpoint", "create", "o.g.a"}, badConfig, "no-such-rule.json", 1},
	{"create_no_server", []string{"endpoint", "create", "o.g.a"}, noEnv("MASH_SERVER"), "no server is configured", 1},
	{"create_no_config_dir", []string{"endpoint", "create", "o.g.a"}, noConfigDir, "no config folder to write the credentials file in", 1},
	{"create_public_key_unreadable", []string{"endpoint", "create", "o.g.a", "--public-key", "{AUX}/missing.pub", "--out", "{AUX}/out.join"}, nil, "cannot read the public key file", 1},
	{"create_public_key_invalid", []string{"endpoint", "create", "o.g.a", "--public-key", "{AUX}/bad.pub", "--out", "{AUX}/out.join"}, func(t *testing.T, r *refEnv) {
		if err := os.WriteFile(filepath.Join(r.aux, "bad.pub"), []byte("not a key\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}, "does not hold a NATS user public key", 1},
	{"create_path_exists", []string{"endpoint", "create", "o.g.a", "--out", "{AUX}/exists.join"}, func(t *testing.T, r *refEnv) {
		if err := os.WriteFile(filepath.Join(r.aux, "exists.join"), []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}, "exists.join already exists", 1},
	{"create_file_exists_late", []string{"endpoint", "create", "o.g.a"}, func(t *testing.T, r *refEnv) {
		epAfterEnroll = func() { os.WriteFile(r.h.creds(ad("a")), []byte("x\n"), 0o600) }
		t.Cleanup(func() { epAfterEnroll = nil })
	}, "appeared while the command ran", 1},
	{"create_creds_exists", []string{"endpoint", "create", "o.g.a"}, func(t *testing.T, r *refEnv) {
		// a run killed after it staged its key, then another creds file put at the path
		if code := killAt(r.h, &epAfterStage, "endpoint", "create", "o.g.a"); code != -1 {
			t.Fatalf("not stopped: %d", code)
		}
		if err := os.WriteFile(r.h.creds(ad("a")), []byte("another key\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}, "o.g.a.creds already exists", 1},
	{"create_staged_key_revoked", []string{"endpoint", "create", "o.g.a"}, func(t *testing.T, r *refEnv) {
		// a run killed after it staged its key, then the account revokes that key
		if code := killAt(r.h, &epAfterStage, "endpoint", "create", "o.g.a"); code != -1 {
			t.Fatalf("not stopped: %d", code)
		}
		fakeKeyRevoked(t, func(string) (bool, error) { return true, nil })
	}, "the staged key", 1},
	{"create_no_account_key", []string{"endpoint", "create", "o.g.a", "--public-key", "{PUB}", "--out", "{AUX}/out.join"}, noKeys, "no account identity key", 1},
	{"create_server", []string{"endpoint", "create", "o.g.a", "--public-key", "{PUB}", "--out", "{AUX}/out.join"}, unreachable, "nothing was changed", 1},
	{"create_enroll_server", []string{"endpoint", "create", "o.g.a", "--public-key", "{PUB}", "--out", "{AUX}/out.join"}, enrollFails, "the server stopped answering", 1},
	{"create_write", []string{"endpoint", "create", "o.g.a"}, groupWritableDir, "nothing was changed", 1},
	{"create_address_exists", []string{"endpoint", "create", "o.g.a", "--public-key", "{PUB}", "--out", "{AUX}/out.join"}, seeded("a"), "o.g.a already exists", 1},
	{"create_deleted", []string{"endpoint", "create", "o.g.a", "--public-key", "{PUB2}", "--out", "{AUX}/out.join"}, burned("a"), "o.g.a was deleted and its address cannot be used again", 1},
	{"create_public_key_issued", []string{"endpoint", "create", "o.g.c", "--public-key", "{PUB}", "--out", "{AUX}/out.join"}, func(t *testing.T, r *refEnv) {
		r.must(t, "endpoint", "create", "o.g.a", "--out", filepath.Join(r.aux, "first.join"), "--public-key", r.pub)
	}, "was already issued to another address", 1},
	{"create_key_revoked", []string{"endpoint", "create", "o.g.d", "--public-key", "{PUB}", "--out", "{AUX}/out.join"}, burned("b"), "revoked", 1},

	// reissue
	{"reissue_too_many", []string{"endpoint", "reissue", "o.g.a", "o.g.b"}, nil, "too many arguments", 2},
	{"reissue_unknown_option", []string{"endpoint", "reissue", "o.g.a", "--bogus"}, nil, `unknown flag --bogus`, 2},
	{"reissue_removed_all", []string{"endpoint", "reissue", "--all"}, nil, `unknown flag --all`, 2},
	{"reissue_removed_make_key", []string{"endpoint", "reissue", "o.g.a", "--make-key"}, nil, `unknown flag --make-key`, 2},
	{"reissue_address_missing", []string{"endpoint", "reissue"}, nil, "missing the endpoint address", 2},
	{"reissue_slash_form", []string{"endpoint", "reissue", "o/g/a"}, nil, "not an endpoint address", 2},
	{"reissue_public_key_needs_out", []string{"endpoint", "reissue", "o.g.a", "--public-key", "{PUB}"}, nil, "--public-key needs --out, the join file to write", 2},
	{"delete_servers_option_gone", []string{"endpoint", "delete", "o.g.a", "--servers", "2"}, nil, "unknown flag --servers", 2},
	{"delete_sys_creds_option_gone", []string{"endpoint", "delete", "o.g.a", "--sys-creds", "x"}, nil, "unknown flag --sys-creds", 2},
	{"reissue_config", []string{"endpoint", "reissue", "o.g.a"}, badConfig, "no-such-rule.json", 1},
	{"reissue_no_config_dir", []string{"endpoint", "reissue", "o.g.a"}, noConfigDir, "no config folder to write the credentials file in", 1},
	{"reissue_public_key_unreadable", []string{"endpoint", "reissue", "o.g.a", "--public-key", "{AUX}/missing.pub", "--out", "{AUX}/out.join"}, nil, "cannot read the public key file", 1},
	{"reissue_path_exists", []string{"endpoint", "reissue", "o.g.a", "--public-key", "{PUB}", "--out", "{AUX}/exists.join"}, func(t *testing.T, r *refEnv) {
		if err := os.WriteFile(filepath.Join(r.aux, "exists.join"), []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}, "exists.join already exists", 1},
	{"reissue_out_path_exists", []string{"endpoint", "reissue", "o.g.a", "--out", "{AUX}/exists.join"}, func(t *testing.T, r *refEnv) {
		if err := os.WriteFile(filepath.Join(r.aux, "exists.join"), []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}, "exists.join already exists", 1},
	{"reissue_no_account_key", []string{"endpoint", "reissue", "o.g.a"}, noKeys, "no account identity key", 1},
	{"reissue_server", []string{"endpoint", "reissue", "o.g.a"}, unreachable, "nothing was changed", 1},
	{"reissue_enroll_server", []string{"endpoint", "reissue", "o.g.a"}, seq(seeded("a"), enrollFails), "the server stopped answering", 1},
	{"reissue_endpoint_missing", []string{"endpoint", "reissue", "o.g.a"}, nil, "no endpoint o.g.a", 1},
	{"reissue_deleted", []string{"endpoint", "reissue", "o.g.a"}, burned("a"), "o.g.a was deleted and its address cannot be used again", 1},
	{"reissue_busy", []string{"endpoint", "reissue", "o.g.a"}, seq(seeded("a"), lockHeld("a")), "another reissue of o.g.a holds the lock", 1},
	{"reissue_write", []string{"endpoint", "reissue", "o.g.a"}, seq(seeded("a"), groupWritableDir), "nothing was changed", 1},
	{"reissue_superseded", []string{"endpoint", "reissue", "o.g.a"}, seq(seeded("a"), func(t *testing.T, r *refEnv) {
		// another run replaces the directory key between this run's write and its check
		epBeforeVerify = func() {
			rec, _ := r.h.record(ad("a"))
			_, other := r.h.e.Mint(t, ad("other"))
			if _, err := r.h.adminOf().SetUser(context.Background(), ad("a"), r.h.e.Account, rec.User, other); err != nil {
				t.Error(err)
			}
		}
		t.Cleanup(func() { epBeforeVerify = nil })
	}), "another reissue changed o.g.a's key while this one ran", 1},
	{"reissue_public_key_issued", []string{"endpoint", "reissue", "o.g.b", "--public-key", "{PUB}", "--out", "{AUX}/out.join"}, func(t *testing.T, r *refEnv) {
		r.must(t, "endpoint", "create", "o.g.a", "--out", filepath.Join(r.aux, "first.join"), "--public-key", r.pub)
		r.h.seed(ad("b"))
	}, "was already issued to another address", 1},
	{"reissue_key_unchanged", []string{"endpoint", "reissue", "o.g.a", "--public-key", "{PUB}", "--out", "{AUX}/out.join"}, func(t *testing.T, r *refEnv) {
		r.must(t, "endpoint", "create", "o.g.a", "--out", filepath.Join(r.aux, "first.join"), "--public-key", r.pub)
	}, "the public key is already this endpoint's key", 1},
	{"reissue_future_issued", []string{"endpoint", "reissue", "o.g.a"}, futureIssued("a"), "issued", 1},
	{"reissue_creds_mismatch", []string{"endpoint", "reissue", "o.g.a"}, otherCreds, "is not this endpoint's current creds", 1},
	{"reissue_creds_unreadable", []string{"endpoint", "reissue", "o.g.a"}, junkCreds, "exists but", 1},
	{"reissue_servers_short", []string{"endpoint", "reissue", "o.g.a"}, seq(seeded("a"), shortServerSet), "only 2 of 3 servers answered", 1},
}

var (
	reUserKey   = regexp.MustCompile(`U[A-Z2-7]{55}`)
	reAcctKey   = regexp.MustCompile(`A[A-Z2-7]{55}`)
	reShortKey  = regexp.MustCompile(`\bU[A-Z2-7]{7}\b(\.\.\.)?`)
	reTimestamp = regexp.MustCompile(`\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(\.\d+)?(Z|[+-]\d\d:\d\d)`)
)

// normalise replaces what changes from run to run by fixed words.
func (r *refEnv) normalise(s string) string {
	s = strings.ReplaceAll(s, r.aux, "<TMP>")
	s = strings.ReplaceAll(s, r.h.dir, "<TMP>")
	s = strings.ReplaceAll(s, fmt.Sprintf("(pid %d on %s)", os.Getpid(), hostName(r.h.t)), "(pid <PID> on <HOST>)")
	s = strings.ReplaceAll(s, r.h.e.URL, "<URL>")
	s = reUserKey.ReplaceAllString(s, "<KEY>")
	s = reAcctKey.ReplaceAllString(s, "<KEY>")
	s = reShortKey.ReplaceAllString(s, "<KEY>")
	return reTimestamp.ReplaceAllString(s, "<TIME>")
}

func withNewline(s string) string {
	if s != "" && !strings.HasSuffix(s, "\n") {
		return s + "\n"
	}
	return s
}

func TestEndpointRefusals(t *testing.T) {
	cwdFiles, _ := filepath.Glob("*.join")
	var golden strings.Builder
	seen := map[string]bool{}
	for _, c := range refusalCases {
		if seen[c.name] {
			t.Fatalf("case %s is listed twice", c.name)
		}
		seen[c.name] = true
		t.Run(c.name, func(t *testing.T) {
			h := newFleetEnv(t)
			fakeKeyRevoked(t, func(string) (bool, error) { return false, nil })
			r := &refEnv{h: h, aux: privateDir(t)}
			r.k1, r.pub, _ = epKey(t, r.aux, "one")
			_, r.pub2, _ = epKey(t, r.aux, "two")
			if c.setup != nil {
				c.setup(t, r)
			}
			args := make([]string, 0, len(c.args))
			for _, a := range c.args {
				a = strings.ReplaceAll(a, "{AUX}", r.aux)
				a = strings.ReplaceAll(a, "{PUB}", r.pub)
				a = strings.ReplaceAll(a, "{PUB2}", r.pub2)
				args = append(args, a)
			}
			so, se, code := h.run(args...)
			if code != c.wantExit {
				t.Errorf("exit %d, want %d: %q %q", code, c.wantExit, so, se)
			}
			// one `error:` line, at most one `hint:` line after it, nothing on stdout
			var lines []string
			for _, l := range strings.Split(strings.TrimRight(se, "\n"), "\n") {
				if !strings.HasPrefix(l, "mashctl: target") { // the line that names the server is not part of the error
					lines = append(lines, l)
				}
			}
			if so != "" || len(lines) < 1 || len(lines) > 2 || !strings.HasPrefix(lines[0], "error: ") || !strings.Contains(lines[0], c.want) ||
				len(lines) == 2 && !strings.HasPrefix(lines[1], "hint: ") {
				t.Errorf("want one error line holding %q and at most one hint line: stdout %q stderr %q", c.want, so, se)
			}
			if c.wantExit == 2 && !strings.HasPrefix(lines[0], "error: unknown flag ") && (len(lines) != 2 || !strings.HasSuffix(lines[1], "--help")) {
				t.Errorf("a wrong command line needs the help hint: %q", se)
			}
			noSecrets(t, so, se)
			fmt.Fprintf(&golden, "== %s\nexit %d\nstderr:\n%sstdout:\n%s", c.name, code, withNewline(r.normalise(se)), withNewline(r.normalise(so)))
		})
	}
	if now, _ := filepath.Glob("*.join"); len(now) != len(cwdFiles) {
		t.Errorf("a case wrote a join file into the working directory: %v", now)
	}

	path := filepath.Join("testdata", "endpoint_refusals.golden.txt")
	if *updateEndpointRefusals {
		if err := os.WriteFile(path, []byte(golden.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("golden file: %v", err)
	}
	if golden.String() != string(want) {
		gs, ws := strings.Split(golden.String(), "\n"), strings.Split(string(want), "\n")
		for i := 0; i < len(gs) && i < len(ws); i++ {
			if gs[i] != ws[i] {
				t.Fatalf("%s differs at line %d (run with -update-endpoint-refusals after checking the diff)\n got: %s\nwant: %s", path, i+1, gs[i], ws[i])
			}
		}
		t.Fatalf("%s differs in length: got %d lines, want %d (run with -update-endpoint-refusals after checking the diff)", path, len(gs), len(ws))
	}
}
