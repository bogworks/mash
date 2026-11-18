package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/bogworks/mash/internal/apply"
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

// Exit codes by code, from the contract. Usage-class codes (the command line is wrong, nothing ran) exit 2; a refusal
// exits 1; a server or write failure is outcome error and exits 6. Where a code's exit differs between verbs the case
// says so:
//   - no_config_dir exits 1 for create and reissue of one endpoint (a refusal before anything changes) and 6 in a bulk
//     run, where it is the error of one endpoint among others.
//   - creds_unreadable exits 1 for reissue of one endpoint and 6 in a bulk run, for the same reason.
//   - creds_mismatch exits 1 for one endpoint and 5 in a bulk run, where the other endpoint was reissued and this one
//     was refused (some endpoints failed).
//   - no_local_creds exits 0: the endpoint is skipped, which is not a failure.
// refCase.wantExit holds the value for each case.

// refCase is one refusal or error: the arguments, what it sets up first, and the code its JSON document carries.
// {AUX}, {PUB} and {PUB2} in an argument are replaced by the case's directory and its two public key files; --json is
// added to every run.
type refCase struct {
	name     string
	args     []string
	setup    func(t *testing.T, r *refEnv)
	wantCode string
	wantExit int // the exit code of the contract for this code: 2 usage, 1 refusal, 5 some endpoints of a bulk run failed, 6 error
}

// untriggerable is every code the endpoint commands can print that no case triggers, with the reason. The test fails
// when a code is in the source and in neither the cases nor this map, and when an entry here is no longer in the source
// or is covered by a case.
var untriggerable = map[string]string{}

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

func unreachable(t *testing.T, r *refEnv) { r.h.env["MASH_URL"] = "nats://127.0.0.1:1" }

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
	prevCreate, prevReissue := enrollCreate, enrollReissueThen
	enrollCreate = func(context.Context, enroll.Deps, enroll.Request) (enroll.Result, error) {
		return enroll.Result{}, errors.New("the server stopped answering")
	}
	enrollReissueThen = func(context.Context, enroll.Deps, sap.Address, string, func(enroll.Result) error) (enroll.Result, error) {
		return enroll.Result{}, errors.New("the server stopped answering")
	}
	t.Cleanup(func() { enrollCreate, enrollReissueThen = prevCreate, prevReissue })
}

func lockHeld(instance string) func(t *testing.T, r *refEnv) {
	return func(t *testing.T, r *refEnv) {
		if err := os.Mkdir(r.h.creds(ad(instance))+".lock", 0o700); err != nil {
			t.Fatal(err)
		}
		prev := epLockWait
		epLockWait = 20 * time.Millisecond
		t.Cleanup(func() { epLockWait = prev })
	}
}

// burned creates the endpoint with the first public key and deletes it: its address is retired and its key revoked.
func burned(instance string) func(t *testing.T, r *refEnv) {
	return func(t *testing.T, r *refEnv) {
		r.must(t, "endpoint", "create", ad(instance).String(), "--public-key", r.pub, "--jwt-out", filepath.Join(r.aux, "first.jwt"))
		r.must(t, "endpoint", "delete", ad(instance).String())
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
	{"create_usage", []string{"endpoint", "create", "o/g/a", "o/g/b"}, nil, "usage", 2},
	{"create_unknown_flag", []string{"endpoint", "create", "--bogus"}, nil, "unknown_flag", 2},
	{"create_name_invalid", []string{"endpoint", "create", "o//a", "--public-key", "{PUB}"}, nil, "name_invalid", 1},
	{"create_address_ambiguous", []string{"endpoint", "create", "g/a", "--public-key", "{PUB}"}, nil, "address_ambiguous", 2},
	{"create_naming_disagrees", []string{"endpoint", "create", "o/g/a", "--group", "h", "--public-key", "{PUB}"}, nil, "naming_disagrees", 2},
	{"create_group_missing", []string{"endpoint", "create", "a", "--public-key", "{PUB}"}, nil, "group_missing", 2},
	{"create_owner_missing", []string{"endpoint", "create", "--group", "g", "--name", "a", "--public-key", "{PUB}"}, noEnv("MASH_OWNER"), "owner_missing", 2},
	{"create_name_missing", []string{"endpoint", "create", "--group", "g", "--public-key", "{PUB}"}, nil, "name_missing", 2},
	{"create_name_rule", []string{"endpoint", "create", "o/g/blocked", "--public-key", "{PUB}"}, func(t *testing.T, r *refEnv) {
		p := filepath.Join(r.aux, "rule.json")
		rule := `{"version":1,"rules":[{"id":"no-blocked","match":{"instance":{"is":"blocked"}},"message":"the name blocked is not allowed"}]}`
		if err := os.WriteFile(p, []byte(rule), 0o600); err != nil {
			t.Fatal(err)
		}
		r.h.env["MASH_NAME_RULE_FILE"] = p
	}, "name_rule", 1},
	{"create_key_both", []string{"endpoint", "create", "o/g/a", "--make-key", "--public-key", "{PUB}"}, nil, "key_both", 2},
	{"create_key_missing", []string{"endpoint", "create", "o/g/a"}, nil, "key_missing", 2},
	{"create_labels_invalid", []string{"endpoint", "create", "o/g/a", "--make-key", "--label", "9bad=v"}, nil, "labels_invalid", 2},
	{"create_config", []string{"endpoint", "create", "o/g/a", "--make-key"}, badConfig, "config", 6},
	{"create_no_config_dir", []string{"endpoint", "create", "o/g/a", "--make-key"}, noConfigDir, "no_config_dir", 1},
	{"create_public_key_unreadable", []string{"endpoint", "create", "o/g/a", "--public-key", "{AUX}/missing.pub", "--jwt-out", "{AUX}/out.jwt"}, nil, "public_key_unreadable", 1},
	{"create_public_key_invalid", []string{"endpoint", "create", "o/g/a", "--public-key", "{AUX}/bad.pub", "--jwt-out", "{AUX}/out.jwt"}, func(t *testing.T, r *refEnv) {
		if err := os.WriteFile(filepath.Join(r.aux, "bad.pub"), []byte("not a key\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}, "public_key_invalid", 1},
	{"create_file_exists", []string{"endpoint", "create", "o/g/a", "--public-key", "{PUB}", "--jwt-out", "{AUX}/exists.jwt"}, func(t *testing.T, r *refEnv) {
		if err := os.WriteFile(filepath.Join(r.aux, "exists.jwt"), []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}, "file_exists", 1},
	{"create_file_exists_late", []string{"endpoint", "create", "o/g/a", "--make-key"}, func(t *testing.T, r *refEnv) {
		epAfterEnroll = func() { os.WriteFile(r.h.creds(ad("a")), []byte("x\n"), 0o600) }
		t.Cleanup(func() { epAfterEnroll = nil })
	}, "file_exists", 1},
	{"create_creds_exists", []string{"endpoint", "create", "o/g/a", "--make-key"}, func(t *testing.T, r *refEnv) {
		// a run killed after it staged its key, then another creds file put at the path
		if code := killAt(r.h, &epAfterStage, "endpoint", "create", "o/g/a", "--make-key"); code != -1 {
			t.Fatalf("not stopped: %d", code)
		}
		if err := os.WriteFile(r.h.creds(ad("a")), []byte("another key\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}, "creds_exists", 1},
	{"create_staged_key_revoked", []string{"endpoint", "create", "o/g/a", "--make-key"}, func(t *testing.T, r *refEnv) {
		// a run killed after it staged its key, then the account revokes that key
		if code := killAt(r.h, &epAfterStage, "endpoint", "create", "o/g/a", "--make-key"); code != -1 {
			t.Fatalf("not stopped: %d", code)
		}
		fakeKeyRevoked(t, func(string) (bool, error) { return true, nil })
	}, "staged_key_revoked", 1},
	{"create_no_account_key", []string{"endpoint", "create", "o/g/a", "--public-key", "{PUB}", "--jwt-out", "{AUX}/out.jwt"}, noKeys, "no_account_key", 1},
	{"create_server", []string{"endpoint", "create", "o/g/a", "--public-key", "{PUB}", "--jwt-out", "{AUX}/out.jwt"}, unreachable, "server", 6},
	{"create_enroll_server", []string{"endpoint", "create", "o/g/a", "--public-key", "{PUB}", "--jwt-out", "{AUX}/out.jwt"}, enrollFails, "server", 6},
	{"create_write", []string{"endpoint", "create", "o/g/a", "--make-key"}, groupWritableDir, "write", 6},
	{"create_address_taken", []string{"endpoint", "create", "o/g/a", "--public-key", "{PUB}", "--jwt-out", "{AUX}/out.jwt"}, seeded("a"), "address_taken", 1},
	{"create_key_retired", []string{"endpoint", "create", "o/g/a", "--public-key", "{PUB2}", "--jwt-out", "{AUX}/out.jwt"}, burned("a"), "key_retired", 1},
	{"create_public_key_issued", []string{"endpoint", "create", "o/g/c", "--public-key", "{PUB}", "--jwt-out", "{AUX}/out.jwt"}, func(t *testing.T, r *refEnv) {
		r.must(t, "endpoint", "create", "o/g/a", "--public-key", r.pub, "--jwt-out", filepath.Join(r.aux, "first.jwt"))
	}, "public_key_issued", 1},
	{"create_key_revoked", []string{"endpoint", "create", "o/g/d", "--public-key", "{PUB}", "--jwt-out", "{AUX}/out.jwt"}, burned("b"), "key_revoked", 1},

	// reissue
	{"reissue_usage", []string{"endpoint", "reissue", "o/g/a", "o/g/b"}, nil, "usage", 2},
	{"reissue_unknown_flag", []string{"endpoint", "reissue", "--bogus"}, nil, "unknown_flag", 2},
	{"reissue_address_missing", []string{"endpoint", "reissue", "--make-key"}, nil, "address_missing", 2},
	{"reissue_address_invalid", []string{"endpoint", "reissue", "bad address", "--make-key"}, nil, "address_invalid", 2},
	{"reissue_key_both", []string{"endpoint", "reissue", "o/g/a", "--make-key", "--public-key", "{PUB}"}, nil, "key_both", 2},
	{"reissue_key_missing", []string{"endpoint", "reissue", "o/g/a"}, nil, "key_missing", 2},
	{"reissue_servers_invalid", []string{"endpoint", "reissue", "o/g/a", "--make-key"}, setEnv("MASH_SERVERS", "0"), "servers_invalid", 2},
	{"reissue_config", []string{"endpoint", "reissue", "o/g/a", "--make-key"}, badConfig, "config", 6},
	{"reissue_no_config_dir", []string{"endpoint", "reissue", "o/g/a", "--make-key"}, noConfigDir, "no_config_dir", 1},
	{"reissue_public_key_unreadable", []string{"endpoint", "reissue", "o/g/a", "--public-key", "{AUX}/missing.pub", "--jwt-out", "{AUX}/out.jwt"}, nil, "public_key_unreadable", 1},
	{"reissue_file_exists", []string{"endpoint", "reissue", "o/g/a", "--public-key", "{PUB}", "--jwt-out", "{AUX}/exists.jwt"}, func(t *testing.T, r *refEnv) {
		if err := os.WriteFile(filepath.Join(r.aux, "exists.jwt"), []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}, "file_exists", 1},
	{"reissue_no_account_key", []string{"endpoint", "reissue", "o/g/a", "--make-key"}, noKeys, "no_account_key", 1},
	{"reissue_server", []string{"endpoint", "reissue", "o/g/a", "--make-key"}, unreachable, "server", 6},
	{"reissue_enroll_server", []string{"endpoint", "reissue", "o/g/a", "--make-key"}, seq(seeded("a"), enrollFails), "server", 6},
	{"reissue_endpoint_missing", []string{"endpoint", "reissue", "o/g/a", "--make-key"}, nil, "endpoint_missing", 1},
	{"reissue_key_retired", []string{"endpoint", "reissue", "o/g/a", "--make-key"}, burned("a"), "key_retired", 1},
	{"reissue_reissue_busy", []string{"endpoint", "reissue", "o/g/a", "--make-key"}, seq(seeded("a"), lockHeld("a")), "reissue_busy", 1},
	{"reissue_write", []string{"endpoint", "reissue", "o/g/a", "--make-key"}, seq(seeded("a"), groupWritableDir), "write", 6},
	{"reissue_public_key_issued", []string{"endpoint", "reissue", "o/g/b", "--public-key", "{PUB}", "--jwt-out", "{AUX}/out.jwt"}, func(t *testing.T, r *refEnv) {
		r.must(t, "endpoint", "create", "o/g/a", "--public-key", r.pub, "--jwt-out", filepath.Join(r.aux, "first.jwt"))
		r.h.seed(ad("b"))
	}, "public_key_issued", 1},
	{"reissue_key_unchanged", []string{"endpoint", "reissue", "o/g/a", "--public-key", "{PUB}", "--jwt-out", "{AUX}/out.jwt"}, func(t *testing.T, r *refEnv) {
		r.must(t, "endpoint", "create", "o/g/a", "--public-key", r.pub, "--jwt-out", filepath.Join(r.aux, "first.jwt"))
	}, "key_unchanged", 1},

	{"reissue_future_issued", []string{"endpoint", "reissue", "o/g/a", "--make-key"}, futureIssued("a"), "future_issued", 1},
	{"reissue_creds_mismatch", []string{"endpoint", "reissue", "o/g/a", "--make-key"}, otherCreds, "creds_mismatch", 1},
	{"reissue_creds_unreadable", []string{"endpoint", "reissue", "o/g/a", "--make-key"}, junkCreds, "creds_unreadable", 1},

	// reissue --all and --changed
	{"bulk_usage", []string{"endpoint", "reissue", "--all", "--changed", "--make-key"}, nil, "usage", 2},
	{"bulk_needs_make_key", []string{"endpoint", "reissue", "--all"}, nil, "bulk_needs_make_key", 2},
	{"bulk_servers_invalid", []string{"endpoint", "reissue", "--all", "--make-key"}, setEnv("MASH_SERVERS", "0"), "servers_invalid", 2},
	{"bulk_config", []string{"endpoint", "reissue", "--all", "--make-key"}, badConfig, "config", 6},
	{"bulk_no_account_key", []string{"endpoint", "reissue", "--all", "--make-key", "--yes"}, noKeys, "no_account_key", 1},
	{"bulk_server", []string{"endpoint", "reissue", "--all", "--make-key", "--yes"}, unreachable, "server", 6},
	{"bulk_servers", []string{"endpoint", "reissue", "--all", "--make-key", "--yes"}, seq(seeded("a"), shortServerSet), "servers", 6},
	{"bulk_no_config_dir", []string{"endpoint", "reissue", "--all", "--make-key", "--yes"}, func(t *testing.T, r *refEnv) {
		r.h.seed(ad("a"))
		noConfigDir(t, r)
	}, "no_config_dir", 6},
	{"bulk_no_local_creds", []string{"endpoint", "reissue", "--changed", "--make-key", "--yes"}, func(t *testing.T, r *refEnv) {
		r.h.seed(ad("a"))
		if err := os.Remove(r.h.creds(ad("a"))); err != nil {
			t.Fatal(err)
		}
	}, "no_local_creds", 0},
	{"bulk_creds_unreadable", []string{"endpoint", "reissue", "--changed", "--make-key", "--yes"}, func(t *testing.T, r *refEnv) {
		r.h.seed(ad("a"))
		if err := os.WriteFile(r.h.creds(ad("a")), []byte("junk\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}, "creds_unreadable", 6},
	{"bulk_reissue_busy", []string{"endpoint", "reissue", "--all", "--make-key", "--yes"}, seq(seeded("a"), lockHeld("a")), "reissue_busy", 1},

	{"bulk_future_issued", []string{"endpoint", "reissue", "--all", "--make-key", "--yes"}, futureIssued("a"), "future_issued", 1},
	{"bulk_creds_mismatch", []string{"endpoint", "reissue", "--all", "--make-key", "--yes"}, otherCreds, "creds_mismatch", 5},
	{"bulk_revocation_unknown", []string{"endpoint", "reissue", "--changed", "--make-key", "--yes"}, revocationUnknown, "revocation_unknown", 6},

	// delete
	{"delete_usage", []string{"endpoint", "delete", "o/g/a", "o/g/b"}, nil, "usage", 2},
	{"delete_unknown_flag", []string{"endpoint", "delete", "--bogus"}, nil, "unknown_flag", 2},
	{"delete_address_missing", []string{"endpoint", "delete"}, nil, "address_missing", 2},
	{"delete_address_invalid", []string{"endpoint", "delete", "bad address"}, nil, "address_invalid", 2},
	{"delete_servers_invalid", []string{"endpoint", "delete", "o/g/a"}, setEnv("MASH_SERVERS", "0"), "servers_invalid", 2},
	{"delete_not_found", []string{"endpoint", "delete", "o/g/none"}, nil, "not_found", 1},
	{"delete_no_creds_file", []string{"endpoint", "delete", "o/g/a", "--delete-creds"}, seq(seeded("a"), noConfigDir), "no_creds_file", 1},
	{"delete_server_set", []string{"endpoint", "delete", "o/g/a"}, seq(seeded("a"), shortServerSet), "server_set", 1},
	{"delete_future_issued", []string{"endpoint", "delete", "o/g/a"}, func(t *testing.T, r *refEnv) {
		r.h.seed(ad("a"))
		_, pub := r.h.credsClaims(ad("a"))
		r.h.recordIssuedAt(pub, time.Now().Add(48*time.Hour))
	}, "future_issued", 1},
	{"delete_revoke_failed", []string{"endpoint", "delete", "o/g/a"}, func(t *testing.T, r *refEnv) {
		r.h.seed(ad("a"))
		fakeRevoke(t, errors.New("no system account answered"))
	}, "revoke_failed", 1},
	{"delete_revoke_not_confirmed", []string{"endpoint", "delete", "o/g/a"}, func(t *testing.T, r *refEnv) {
		r.h.seed(ad("a"))
		fakeRevoke(t, fmt.Errorf("the revocation was not confirmed: %w", apply.ErrNotConfirmed))
	}, "revoke_not_confirmed", 1},
	{"delete_refused", []string{"endpoint", "delete", "o/g/a"}, func(t *testing.T, r *refEnv) {
		r.h.seed(ad("a"))
		first, _ := r.h.record(ad("a"))
		afterRetireRead = func() {
			// someone gives the record another user key between the read and the guarded removal
			adm := r.h.adminOf()
			_, other := r.h.e.Mint(t, ad("a2"))
			if _, err := adm.SetUser(context.Background(), ad("a"), r.h.e.Account, first.User, other); err != nil {
				t.Error(err)
			}
		}
		t.Cleanup(func() { afterRetireRead = nil })
	}, "refused", 1},

	// list and show
	{"list_usage", []string{"endpoint", "list", "o/g/a"}, nil, "usage", 2},
	{"list_unknown_flag", []string{"endpoint", "list", "--bogus"}, nil, "unknown_flag", 2},
	{"show_usage", []string{"endpoint", "show", "o/g/a", "o/g/b"}, nil, "usage", 2},
	{"show_unknown_flag", []string{"endpoint", "show", "--bogus"}, nil, "unknown_flag", 2},
	{"show_address_missing", []string{"endpoint", "show"}, nil, "address_missing", 2},
	{"show_address_invalid", []string{"endpoint", "show", "bad address"}, nil, "address_invalid", 2},
	{"show_not_found", []string{"endpoint", "show", "o/g/none"}, nil, "not_found", 1},
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
	cwdJWTs, _ := filepath.Glob("*.jwt")
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
			args := make([]string, 0, len(c.args)+1)
			for _, a := range c.args {
				a = strings.ReplaceAll(a, "{AUX}", r.aux)
				a = strings.ReplaceAll(a, "{PUB}", r.pub)
				a = strings.ReplaceAll(a, "{PUB2}", r.pub2)
				args = append(args, a)
			}
			args = append(args, "--json")
			so, se, code := h.run(args...)
			if code != c.wantExit {
				t.Errorf("exit %d, want %d for code %s: %q %q", code, c.wantExit, c.wantCode, so, se)
			}
			if code == 0 && c.wantCode != "no_local_creds" { // a skipped endpoint is not a failure
				t.Errorf("exit 0, want a refusal or error: %q %q", so, se)
			}
			if !strings.Contains(so, fmt.Sprintf(`"code": %q`, c.wantCode)) && !strings.Contains(so, fmt.Sprintf(`"reason": %q`, c.wantCode)) {
				t.Errorf("stdout carries no %q code: %q (stderr %q)", c.wantCode, so, se)
			}
			noSecrets(t, so, se)
			fmt.Fprintf(&golden, "== %s\nexit %d\nstderr:\n%sstdout:\n%s", c.name, code, withNewline(r.normalise(se)), withNewline(r.normalise(so)))
		})
	}
	if now, _ := filepath.Glob("*.jwt"); len(now) != len(cwdJWTs) {
		t.Errorf("a case wrote a .jwt file into the working directory: %v", now)
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

// codePatterns find the code strings the endpoint commands print, in the source of the commands.
var codePatterns = []*regexp.Regexp{
	regexp.MustCompile(`epRefusal\{"([a-z_]+)"`),
	regexp.MustCompile(`[cC]ode: +"([a-z_]+)"`),
	regexp.MustCompile(`\busage(?:Err)?\("([a-z_]+)"`),
	regexp.MustCompile(`usageCode :?= "([a-z_]+)"`),
	regexp.MustCompile(`epErr\([^,]+, [^,]+, "([a-z_]+)"`),
	regexp.MustCompile(`fail\(\d+, "[a-z]+", "([a-z_]+)"`),
	regexp.MustCompile(`stop\("[a-z]+", "([a-z_]+)"`),
	regexp.MustCompile(`"(?:error|skipped)", "([a-z_]+)", nil`),
}

// sourceCodes is every code the endpoint commands can print, from their source files.
func sourceCodes(t *testing.T) map[string]bool {
	t.Helper()
	enrollSrc, err := os.ReadFile(filepath.Join("..", "..", "internal", "enroll", "enroll.go"))
	if err != nil {
		t.Fatal(err)
	}
	enrollConst := map[string]string{}
	for _, m := range regexp.MustCompile(`(Code\w+)\s*=\s*"([a-z_]+)"`).FindAllStringSubmatch(string(enrollSrc), -1) {
		enrollConst[m[1]] = m[2]
	}
	files, err := filepath.Glob("endpoint*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no endpoint source files: %v", err)
	}
	codes := map[string]bool{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, re := range codePatterns {
			for _, m := range re.FindAllStringSubmatch(string(src), -1) {
				codes[m[1]] = true
			}
		}
		for _, m := range regexp.MustCompile(`enroll\.(Code\w+)`).FindAllStringSubmatch(string(src), -1) {
			v, ok := enrollConst[m[1]]
			if !ok {
				t.Fatalf("%s: enroll.%s has no value in internal/enroll/enroll.go", f, m[1])
			}
			codes[v] = true
		}
	}
	// endpoint_retire.go is read with the others; the codes the retire path gives are in it
	if _, err := os.Stat("endpoint_retire.go"); err != nil {
		t.Fatal(err)
	}
	return codes
}

func TestEndpointRefusalCodesAreAllCovered(t *testing.T) {
	src := sourceCodes(t)
	covered := map[string]bool{}
	for _, c := range refusalCases {
		covered[c.wantCode] = true
	}
	var missing, stale, both []string
	for code := range src {
		if !covered[code] {
			if _, ok := untriggerable[code]; !ok {
				missing = append(missing, code)
			}
		}
	}
	for code := range covered {
		if !src[code] {
			stale = append(stale, code)
		}
	}
	for code := range untriggerable {
		if !src[code] {
			stale = append(stale, code)
		}
		if covered[code] {
			both = append(both, code)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	sort.Strings(both)
	if len(missing) > 0 {
		t.Errorf("codes in the source with no case in refusalCases and no entry in untriggerable: %v", missing)
	}
	if len(stale) > 0 {
		t.Errorf("codes named by a case or by untriggerable that the source no longer has: %v", stale)
	}
	if len(both) > 0 {
		t.Errorf("codes in untriggerable that a case now triggers; remove them from the map: %v", both)
	}
}
