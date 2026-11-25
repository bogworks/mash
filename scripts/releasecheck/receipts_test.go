package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func rsha(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

var receiptPlatforms = []Platform{{"linux", "amd64"}, {"linux", "arm64"}, {"darwin", "amd64"}, {"darwin", "arm64"}}

func receiptExpected() Expected {
	e := Expected{
		Commit:       "c0ffee",
		Platforms:    append([]Platform(nil), receiptPlatforms...),
		InstallSHA:   rsha("install.sh"),
		SumsSHA:      rsha("SHA256SUMS"),
		PrevVersion:  "0.5.9",
		UpgradePage:  "blob-1234",
		OlderVersion: "0.5.9",
		NewerVersion: "0.6.0",
		CorpusSHA:    rsha("corpus"),
	}
	for _, p := range e.Platforms {
		for _, n := range executableNames {
			e.Executables = append(e.Executables, Exec{n, p.GOOS, p.GOARCH, rsha("new " + n + p.String()),
				BuildConfig{"go1.25.0", []string{"a", "b"}, []string{"-trimpath"}}})
			e.Prior = append(e.Prior, Prior{n, p.GOOS, p.GOARCH, rsha("old " + n + p.String())})
		}
	}
	return e
}

// validReceipts builds a full valid set for the line: one AT-011 receipt per platform, plus AT-012 per platform.
func validReceipts(exp Expected, line proofLine) []Receipt {
	var rs []Receipt
	for _, p := range exp.Platforms {
		r := Receipt{Kind: kindAT011, Commit: exp.Commit, Environment: "vm-" + p.String(), Result: resultPassed,
			InstallSHA256: exp.InstallSHA, SumsSHA256: exp.SumsSHA, PrevVersion: exp.PrevVersion,
			Prior: append([]Prior(nil), exp.Prior...), UpgradePage: exp.UpgradePage}
		for _, n := range executableNames {
			e, _ := findExec(exp.Executables, n, p)
			e.Build.Tags = append([]string(nil), e.Build.Tags...)
			e.Build.Flags = append([]string(nil), e.Build.Flags...)
			r.Executables = append(r.Executables, e)
		}
		rs = append(rs, r)
	}
	if line == lineSwitchOn {
		for _, p := range exp.Platforms {
			om, _ := findPrior(exp.Prior, "mash", p)
			oc, _ := findPrior(exp.Prior, "mashctl", p)
			nm, _ := findExec(exp.Executables, "mash", p)
			nc, _ := findExec(exp.Executables, "mashctl", p)
			rs = append(rs, Receipt{Kind: kindAT012, Commit: exp.Commit, Environment: "vm12-" + p.String(),
				Result: resultPassed, ExitStatus: 0, OlderVersion: exp.OlderVersion, NewerVersion: exp.NewerVersion,
				OlderMashSHA256: om.SHA256, OlderMashctlSHA: oc.SHA256, NewerMashSHA256: nm.SHA256,
				NewerMashctlSHA: nc.SHA256, CorpusSHA256: exp.CorpusSHA, GOOS: p.GOOS, GOARCH: p.GOARCH})
		}
	}
	return rs
}

func lineName(l proofLine) string {
	if l == lineAT011 {
		return "0.5.x"
	}
	return "switch-on"
}

func TestReceiptsPassOnBothLines(t *testing.T) {
	for _, line := range []proofLine{lineAT011, lineSwitchOn} {
		exp := receiptExpected()
		if ps := checkReceipts(exp, validReceipts(exp, line), line); len(ps) != 0 {
			t.Errorf("%s: complete matching set refused: %v", lineName(line), ps)
		}
	}
}

// idx finds the first receipt of kind k (on platform p for AT-012, holding exec n/p for AT-011).
func at11(rs []Receipt, p Platform) *Receipt {
	for i := range rs {
		if rs[i].Kind == kindAT011 && rs[i].Executables[0].GOOS == p.GOOS && rs[i].Executables[0].GOARCH == p.GOARCH {
			return &rs[i]
		}
	}
	return nil
}

func at12(rs []Receipt, p Platform) *Receipt {
	for i := range rs {
		if rs[i].Kind == kindAT012 && rs[i].GOOS == p.GOOS && rs[i].GOARCH == p.GOARCH {
			return &rs[i]
		}
	}
	return nil
}

func TestReceiptsRefused(t *testing.T) {
	lin := Platform{"linux", "arm64"}
	cases := []struct {
		name   string
		only   proofLine // 0: both lines
		mutate func(rs []Receipt) []Receipt
		want   []string // substrings, each must appear in some problem
	}{
		{"no receipts at all", 0, func(rs []Receipt) []Receipt { return nil },
			[]string{"AT-011 mash linux/amd64", "AT-011 mashctl darwin/arm64", "AT-011 install.sh", "AT-011 SHA256SUMS"}},
		{"AT-011 failed", 0, func(rs []Receipt) []Receipt { at11(rs, lin).Result = resultFailed; return rs },
			[]string{"AT-011 mash linux/arm64", "AT-011 mashctl linux/arm64", "result is not passed"}},
		{"digest mismatch, one executable on one platform", 0, func(rs []Receipt) []Receipt {
			at11(rs, lin).Executables[1].SHA256 = rsha("other")
			return rs
		}, []string{"AT-011 mashctl linux/arm64: no passing receipt matches (sha256 differs)"}},
		{"build tags differ", 0, func(rs []Receipt) []Receipt {
			at11(rs, lin).Executables[0].Build.Tags = []string{"a", "extra"}
			return rs
		}, []string{"AT-011 mash linux/arm64: no passing receipt matches (build tags differ)"}},
		{"toolchain differs", 0, func(rs []Receipt) []Receipt {
			at11(rs, lin).Executables[0].Build.Toolchain = "go1.24.0"
			return rs
		}, []string{"AT-011 mash linux/arm64: no passing receipt matches (toolchain differs)"}},
		{"flags differ", 0, func(rs []Receipt) []Receipt {
			at11(rs, lin).Executables[0].Build.Flags = nil
			return rs
		}, []string{"build flags differ"}},
		{"commit differs", 0, func(rs []Receipt) []Receipt { at11(rs, lin).Commit = "bad"; return rs },
			[]string{"AT-011 mash linux/arm64", "candidate commit differs"}},
		{"install.sh differs", 0, func(rs []Receipt) []Receipt {
			for i := range rs {
				if rs[i].Kind == kindAT011 {
					rs[i].InstallSHA256 = rsha("tampered")
				}
			}
			return rs
		}, []string{"AT-011 install.sh", "install.sh sha256 differs"}},
		{"SHA256SUMS differs", 0, func(rs []Receipt) []Receipt {
			for i := range rs {
				if rs[i].Kind == kindAT011 {
					rs[i].SumsSHA256 = rsha("tampered")
				}
			}
			return rs
		}, []string{"AT-011 SHA256SUMS", "SHA256SUMS sha256 differs"}},
		{"prior artifact digest differs", 0, func(rs []Receipt) []Receipt {
			r := at11(rs, lin)
			r.Prior[0].SHA256 = rsha("not the published one")
			return rs
		}, []string{"AT-011 mash linux/arm64", "previous artifact digests differ"}},
		{"previous version differs", 0, func(rs []Receipt) []Receipt { at11(rs, lin).PrevVersion = "0.5.8"; return rs },
			[]string{"previous version differs"}},
		{"upgrade page revision differs", 0, func(rs []Receipt) []Receipt {
			at11(rs, lin).UpgradePage = "blob-other"
			return rs
		}, []string{"AT-011 mashctl linux/arm64", "upgrade page revision differs"}},
		{"every mash receipt and no mashctl", 0, func(rs []Receipt) []Receipt {
			for i := range rs {
				if rs[i].Kind == kindAT011 {
					rs[i].Executables = rs[i].Executables[:1]
				}
			}
			return rs
		}, []string{"AT-011 mashctl linux/amd64", "AT-011 mashctl linux/arm64", "AT-011 mashctl darwin/amd64", "AT-011 mashctl darwin/arm64"}},
		{"linux green and no macOS", 0, func(rs []Receipt) []Receipt {
			var out []Receipt
			for _, r := range rs {
				if r.Kind == kindAT011 && r.Executables[0].GOOS == "darwin" {
					continue
				}
				out = append(out, r)
			}
			return out
		}, []string{"AT-011 mash darwin/amd64: no passing receipt matches (no receipt covers it)", "AT-011 mashctl darwin/arm64"}},
		{"extra and duplicate receipts do not mask a missing one", 0, func(rs []Receipt) []Receipt {
			r := *at11(rs, lin)
			r.Executables = nil
			dup := *at11(rs, Platform{"linux", "amd64"})
			unknown := Receipt{Kind: "at-099", Commit: "c0ffee", Result: resultPassed}
			bogus := *at11(rs, lin)
			bogus.Executables = []Exec{{Name: "mash", GOOS: "plan9", GOARCH: "mips", SHA256: rsha("x")}}
			var out []Receipt
			for _, x := range rs {
				if x.Kind == kindAT011 && x.Executables[0].GOOS == "darwin" && x.Executables[0].GOARCH == "arm64" {
					continue
				}
				out = append(out, x)
			}
			return append(out, r, dup, dup, unknown, bogus)
		}, []string{"AT-011 mash darwin/arm64", "AT-011 mashctl darwin/arm64"}},
		{"AT-012 failed", lineSwitchOn, func(rs []Receipt) []Receipt { at12(rs, lin).Result = resultFailed; return rs },
			[]string{"AT-012 linux/arm64: no passing receipt matches (result is not passed)"}},
		{"AT-012 passed with nonzero exit", lineSwitchOn, func(rs []Receipt) []Receipt { at12(rs, lin).ExitStatus = 3; return rs },
			[]string{"AT-012 linux/arm64", "exit status is not 0"}},
		{"AT-012 missing entirely", lineSwitchOn, func(rs []Receipt) []Receipt {
			var out []Receipt
			for _, r := range rs {
				if r.Kind != kindAT012 {
					out = append(out, r)
				}
			}
			return out
		}, []string{"AT-012 linux/amd64: no passing receipt matches (no receipt for the platform)", "AT-012 darwin/arm64"}},
		{"AT-012 short on a platform", lineSwitchOn, func(rs []Receipt) []Receipt {
			var out []Receipt
			for _, r := range rs {
				if r.Kind == kindAT012 && r.GOOS == "darwin" && r.GOARCH == "arm64" {
					continue
				}
				out = append(out, r)
			}
			return out
		}, []string{"AT-012 darwin/arm64: no passing receipt matches"}},
		{"AT-012 newer binary differs from the gate's build", lineSwitchOn, func(rs []Receipt) []Receipt {
			at12(rs, lin).NewerMashctlSHA = rsha("other build")
			return rs
		}, []string{"AT-012 linux/arm64", "newer mashctl sha256 differs from the gate's build"}},
		{"AT-012 older binary differs from the published release", lineSwitchOn, func(rs []Receipt) []Receipt {
			at12(rs, lin).OlderMashSHA256 = rsha("other")
			return rs
		}, []string{"older mash sha256 differs"}},
		{"AT-012 corpus differs", lineSwitchOn, func(rs []Receipt) []Receipt {
			at12(rs, lin).CorpusSHA256 = rsha("other corpus")
			return rs
		}, []string{"mail corpus sha256 differs"}},
		{"AT-012 commit differs", lineSwitchOn, func(rs []Receipt) []Receipt { at12(rs, lin).Commit = "bad"; return rs },
			[]string{"AT-012 linux/arm64", "candidate commit differs"}},
		{"AT-012 version differs", lineSwitchOn, func(rs []Receipt) []Receipt { at12(rs, lin).NewerVersion = "0.6.1"; return rs },
			[]string{"newer version differs"}},
		{"AT-012 environment empty", lineSwitchOn, func(rs []Receipt) []Receipt { at12(rs, lin).Environment = ""; return rs },
			[]string{"AT-012 linux/arm64", "environment empty"}},
	}
	for _, line := range []proofLine{lineAT011, lineSwitchOn} {
		for _, c := range cases {
			if c.only != 0 && c.only != line {
				continue
			}
			t.Run(lineName(line)+"/"+c.name, func(t *testing.T) {
				exp := receiptExpected()
				ps := checkReceipts(exp, c.mutate(validReceipts(exp, line)), line)
				if len(ps) == 0 {
					t.Fatal("gate passed; want refused")
				}
				for _, w := range c.want {
					found := false
					for _, p := range ps {
						if strings.Contains(p, w) {
							found = true
						}
					}
					if !found {
						t.Errorf("no problem contains %q; problems:\n%s", w, strings.Join(ps, "\n"))
					}
				}
			})
		}
	}
}

func TestReceiptsAT012IgnoredOnAT011Line(t *testing.T) {
	exp := receiptExpected()
	rs := validReceipts(exp, lineSwitchOn)
	// The 0.5.x line has no AT-012; failed AT-012 receipts and empty AT-012 Expected fields do not matter.
	exp.OlderVersion, exp.NewerVersion, exp.CorpusSHA = "", "", ""
	for i := range rs {
		if rs[i].Kind == kindAT012 {
			rs[i].Result = resultFailed
		}
	}
	if ps := checkReceipts(exp, rs, lineAT011); len(ps) != 0 {
		t.Errorf("refused: %v", ps)
	}
	if ps := checkReceipts(exp, rs, lineSwitchOn); len(ps) == 0 {
		t.Error("switch-on line passed with empty AT-012 expectations")
	}
}

func TestReceiptsExpectedEmptyFailsClosed(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(e *Expected)
		want   string
	}{
		{"zero value", func(e *Expected) { *e = Expected{} }, "expected candidate commit"},
		{"commit", func(e *Expected) { e.Commit = "" }, "expected candidate commit"},
		{"platforms", func(e *Expected) { e.Platforms = nil }, "expected platforms"},
		{"install.sh", func(e *Expected) { e.InstallSHA = "" }, "expected install.sh sha256"},
		{"SHA256SUMS", func(e *Expected) { e.SumsSHA = "" }, "expected SHA256SUMS sha256"},
		{"page", func(e *Expected) { e.UpgradePage = "" }, "expected upgrade page"},
		{"previous version", func(e *Expected) { e.PrevVersion = "" }, "expected previous version"},
		{"executables", func(e *Expected) { e.Executables = e.Executables[:7] }, "expected mashctl darwin/arm64"},
		{"executable digest", func(e *Expected) { e.Executables[0].SHA256 = "" }, "expected mash linux/amd64 sha256"},
		{"toolchain", func(e *Expected) { e.Executables[0].Build.Toolchain = "" }, "toolchain"},
		{"prior", func(e *Expected) { e.Prior = e.Prior[:7] }, "expected previous release mashctl darwin/arm64"},
		{"corpus", func(e *Expected) { e.CorpusSHA = "" }, "expected mail corpus"},
		{"older version", func(e *Expected) { e.OlderVersion = "" }, "expected older version"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			exp := receiptExpected()
			rs := validReceipts(exp, lineSwitchOn)
			c.mutate(&exp)
			ps := checkReceipts(exp, rs, lineSwitchOn)
			if len(ps) == 0 {
				t.Fatal("passed with a missing Expected field")
			}
			if !strings.Contains(strings.Join(ps, "\n"), c.want) {
				t.Errorf("want %q in:\n%s", c.want, strings.Join(ps, "\n"))
			}
		})
	}
	// Receipts that are as empty as the Expected must not match it.
	if ps := checkReceipts(Expected{}, []Receipt{{Kind: kindAT011, Result: resultPassed}}, lineAT011); len(ps) == 0 {
		t.Error("empty receipt matched an empty Expected")
	}
	if ps := checkReceipts(receiptExpected(), nil, proofLine(0)); len(ps) == 0 {
		t.Error("unknown line passed")
	}
}

func TestReceiptsLoad(t *testing.T) {
	write := func(dir, name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	exp := receiptExpected()
	good, _ := json.Marshal(validReceipts(exp, lineAT011)[0])

	t.Run("loads json files only", func(t *testing.T) {
		d := t.TempDir()
		write(d, "a.json", string(good))
		write(d, "b.json", string(good))
		write(d, "notes.txt", "not json")
		if err := os.Mkdir(filepath.Join(d, "sub.json"), 0o700); err != nil {
			t.Fatal(err)
		}
		rs, err := loadReceipts(d)
		if err != nil || len(rs) != 2 {
			t.Fatalf("got %d receipts, err %v", len(rs), err)
		}
		if rs[0].Commit != exp.Commit || len(rs[0].Executables) != 2 {
			t.Errorf("round trip lost fields: %+v", rs[0])
		}
	})
	t.Run("round trip passes the gate", func(t *testing.T) {
		d := t.TempDir()
		for i, r := range validReceipts(exp, lineSwitchOn) {
			b, _ := json.Marshal(r)
			write(d, string(rune('a'+i))+".json", string(b))
		}
		rs, err := loadReceipts(d)
		if err != nil {
			t.Fatal(err)
		}
		if ps := checkReceipts(exp, rs, lineSwitchOn); len(ps) != 0 {
			t.Errorf("refused: %v", ps)
		}
	})
	t.Run("empty directory is no receipts", func(t *testing.T) {
		rs, err := loadReceipts(t.TempDir())
		if err != nil || len(rs) != 0 {
			t.Fatalf("got %v, %v", rs, err)
		}
		if ps := checkReceipts(exp, rs, lineAT011); len(ps) == 0 {
			t.Error("no receipts passed the gate")
		}
	})
	for _, c := range []struct{ name, body string }{
		{"unparseable", "{not json"},
		{"empty file", ""},
		{"unknown field", `{"kind":"at-011","surprise":1}`},
		{"trailing data", string(good) + `{}`},
		{"wrong type", `{"kind":5}`},
	} {
		t.Run(c.name+" is an error", func(t *testing.T) {
			d := t.TempDir()
			write(d, "ok.json", string(good))
			write(d, "bad.json", c.body)
			if _, err := loadReceipts(d); err == nil {
				t.Fatal("no error")
			}
		})
	}
	t.Run("missing directory is an error", func(t *testing.T) {
		if _, err := loadReceipts(filepath.Join(t.TempDir(), "absent")); err == nil {
			t.Fatal("no error")
		}
	})
	t.Run("unreadable file is an error", func(t *testing.T) {
		d := t.TempDir()
		// A dangling symlink named *.json is listed but cannot be read.
		if err := os.Symlink(filepath.Join(d, "nowhere"), filepath.Join(d, "dangling.json")); err != nil {
			t.Skip("symlinks unavailable")
		}
		if _, err := loadReceipts(d); err == nil {
			t.Fatal("no error")
		}
	})
}
