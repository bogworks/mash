package main

import (
	"strings"
	"testing"
)

func TestPubOfIsTheWholeSeedPathWithPubAdded(t *testing.T) {
	if got := pubOf("/h/bob.seed"); got != "/h/bob.seed.pub" {
		t.Errorf("pubOf = %q", got)
	}
}

func TestParseJoinJSON(t *testing.T) {
	good := `{"endpoint":"acme.demo.bob","servers":["tls://myserver:4222"],"creds":"~/.config/mash/acme.demo.bob.creds"}` + "\n"
	j, err := parseJoinJSON(good)
	if err != nil || j.Endpoint != "acme.demo.bob" || j.Creds != "~/.config/mash/acme.demo.bob.creds" || len(j.Servers) != 1 {
		t.Fatalf("good document: %+v, %v", j, err)
	}
	for name, doc := range map[string]string{
		"not JSON":    "Joined as acme.demo.bob.\n",
		"no endpoint": `{"servers":["tls://x:4222"],"creds":"/c"}`,
		"no creds":    `{"endpoint":"a.b.c","servers":["tls://x:4222"]}`,
		"no servers":  `{"endpoint":"a.b.c","creds":"/c"}`,
		"empty":       "",
	} {
		if _, err := parseJoinJSON(doc); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestExpandHome(t *testing.T) {
	for in, want := range map[string]string{
		"~/.config/mash/a.creds": "/h/.config/mash/a.creds",
		"~":                      "/h",
		"/abs/a.creds":           "/abs/a.creds",
		"~other/a.creds":         "~other/a.creds",
	} {
		if got := expandHome(in, "/h"); got != want {
			t.Errorf("expandHome(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReplaceHoldsFailsWithoutTheProtection(t *testing.T) {
	file := fileGuard{Path: "/c", Kind: "file", Mode: 0o600, Data: []byte("creds")}
	ok0 := Result{Code: 0}
	tb := fileGuard{Path: "/t", Kind: "file", Mode: 0o600, Data: []byte("planted")}
	if ok, d := replaceHolds(ok0, file, &tb, &tb); !ok {
		t.Fatalf("a correct replace was refused: %s", d)
	}
	if ok, d := replaceHolds(ok0, file, nil, nil); !ok {
		t.Fatalf("a correct replace over a file was refused: %s", d)
	}
	wrote := tb
	wrote.Data = []byte("creds")
	link := fileGuard{Path: "/c", Kind: "symlink", Link: "/t", Mode: 0o600, Data: []byte("creds")}
	loose := file
	loose.Mode = 0o644
	empty := file
	empty.Data = nil
	for name, c := range map[string]struct {
		r      Result
		path   fileGuard
		before *fileGuard
		after  *fileGuard
	}{
		"exit 1":                 {Result{Code: 1}, file, &tb, &tb},
		"written through":        {ok0, link, &tb, &wrote},
		"target changed":         {ok0, file, &tb, &wrote},
		"link left in place":     {ok0, link, &tb, &tb},
		"mode 0644":              {ok0, loose, &tb, &tb},
		"empty file":             {ok0, empty, &tb, &tb},
		"path absent":            {ok0, fileGuard{Path: "/c", Kind: "absent"}, nil, nil},
		"target removed (after)": {ok0, file, &tb, &fileGuard{Path: "/t", Kind: "absent"}},
	} {
		if ok, _ := replaceHolds(c.r, c.path, c.before, c.after); ok {
			t.Errorf("%s: a result lacking the protection passed", name)
		}
	}
}

func TestUnknownCommandHolds(t *testing.T) {
	if ok, d := unknownCommandHolds(Result{Code: 2, Stderr: "error: unknown command: credential\nhint: mash --help\n"}); !ok {
		t.Fatalf("a real unknown command failed: %s", d)
	}
	if ok, d := unknownCommandHolds(Result{Code: 2, Stderr: "error: mash has no command credential\nhint: mash --help\n"}); !ok {
		t.Fatalf("the real message failed: %s", d)
	}
	for name, r := range map[string]Result{
		"the command still works":  {Code: 0, Stdout: "Created x.creds\n"},
		"it fails but exit 1":      {Code: 1, Stderr: "error: unknown command: credential\n"},
		"exit 2 for another cause": {Code: 2, Stderr: "error: --seed needs a path\n"},
		"unknown but printed":      {Code: 2, Stdout: "something\n", Stderr: "error: unknown command: credential\n"},
	} {
		if ok, _ := unknownCommandHolds(r); ok {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestJoinSecretFreeFindsAnEchoedSecret(t *testing.T) {
	secrets := map[string][]byte{"design seed": []byte(testSeed)}
	clean := []labelled{{"a", Result{Stdout: "Joined as alice.dev.design.\n"}}, {"b", Result{Stderr: "error: alice.dev.design is already installed here\n"}}}
	if bad := joinSecretFree(clean, secrets); len(bad) != 0 {
		t.Fatalf("a clean run was flagged: %v", bad)
	}
	for name, r := range map[string]Result{
		"seed on stdout": {Stdout: "key " + testSeed + "\n"},
		"jwt on stderr":  {Stderr: "error: bad " + testJWT},
	} {
		bad := joinSecretFree(append(clean, labelled{"x", r}), secrets)
		if len(bad) != 1 || !strings.HasPrefix(bad[0], "x holds") || strings.Contains(bad[0], testSeed) {
			t.Errorf("%s: %v", name, bad)
		}
	}
}

func TestJoinRefusalsAreJudgedByTheRealRefusalCheck(t *testing.T) {
	// the refusal of an existing creds file must fail when mash join wrote over the file, and when it succeeded
	before := fileGuard{Path: "/c", Kind: "file", Mode: 0o600, Data: []byte("old")}
	changed := before
	changed.Data = []byte("new")
	msg := Result{Code: 1, Stderr: "error: alice.dev.design is already installed here\nhint: mash join f.join --replace\n"}
	if ok, d := refusalHolds(msg, []fileGuard{before}, []fileGuard{before}, installedRE, nil); !ok {
		t.Fatalf("a real refusal failed: %s", d)
	}
	if ok, _ := refusalHolds(msg, []fileGuard{before}, []fileGuard{changed}, installedRE, nil); ok {
		t.Error("an overwritten creds file passed")
	}
	if ok, _ := refusalHolds(Result{Code: 0, Stdout: "Joined as alice.dev.design.\n"}, []fileGuard{before}, []fileGuard{before}, installedRE, nil); ok {
		t.Error("a join that succeeded passed as a refusal")
	}
	if ok, _ := refusalHolds(Result{Code: 1, Stderr: "error: x is not the key this join file was made for\n"}, nil, nil, wrongKeyRE, nil); !ok {
		t.Error("the wrong-seed text was not recognised")
	}
	if wrongKeyRE.MatchString("error: ok") || needsSeedRE.MatchString("error: ok") {
		t.Error("an unrelated message matched")
	}
}
