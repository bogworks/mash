package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeKeyPEM has the shape of a PEM private key (three body lines, the last short) and is not one.
func fakeKeyPEM() ([]byte, [3]string) {
	l := [3]string{strings.Repeat("A", 64), strings.Repeat("b", 64), "Zq9xLm2Rt5"}
	return []byte("-----BEGIN PRIVATE KEY-----\n" + l[0] + "\n" + l[1] + "\n\n" + l[2] + "\n-----END PRIVATE KEY-----\n"), l
}

func TestRegisterKeyLinesAllLines(t *testing.T) {
	text, lines := fakeKeyPEM()
	sec := map[string][]byte{}
	registerKeyLines(sec, "k", text)
	if len(sec) != 3 {
		t.Fatalf("registered %d entries, want 3", len(sec))
	}
	for i, l := range lines {
		name := "k line " + string(rune('1'+i))
		if string(sec[name]) != l {
			t.Errorf("%s not registered", name)
		}
	}
	for n, v := range sec {
		if strings.HasPrefix(string(v), "-----") || len(v) == 0 {
			t.Errorf("%s is armour or blank", n)
		}
	}
}

func TestRegisterKeyLinesShort(t *testing.T) {
	sec := map[string][]byte{}
	registerKeyLines(sec, "k", []byte("-----BEGIN X-----\nshort\n-----END X-----\n"))
	if len(sec) != 0 {
		t.Errorf("a 5-byte line was registered")
	}
}

func TestScanForFindsAnyKeyLine(t *testing.T) {
	text, lines := fakeKeyPEM()
	sec := map[string][]byte{}
	registerKeyLines(sec, "server key", text)
	for _, i := range []int{0, 2} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "001-out.txt"), []byte("noise "+lines[i]+" noise\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		leaks, err := scanFor(dir, sec)
		if err != nil || len(leaks) != 1 {
			t.Fatalf("line %d: leaks %v err %v", i+1, leaks, err)
		}
		if leaks[0].Kind != "server key line "+string(rune('1'+i)) || leaks[0].File != "001-out.txt" {
			t.Errorf("line %d: leak %+v", i+1, leaks[0])
		}
		if strings.Contains(leaks[0].Kind, lines[i]) {
			t.Error("the leak names the key text")
		}
	}
}

func TestScanForArmourOnlyIsClean(t *testing.T) {
	text, _ := fakeKeyPEM()
	sec := map[string][]byte{}
	registerKeyLines(sec, "server key", text)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "001-out.txt"), []byte("-----BEGIN PRIVATE KEY-----\n-----END PRIVATE KEY-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	leaks, err := scanFor(dir, sec)
	if err != nil || len(leaks) != 0 {
		t.Fatalf("leaks %v err %v", leaks, err)
	}
}

func TestOwnLeaksDoesNotExemptCapturedOutput(t *testing.T) {
	a := &AT{startSeq: 5}
	in := []Leak{{File: "007-x.txt", Kind: "server key line 3"}, {File: "002-old.txt", Kind: "server key line 1"}}
	out := a.ownLeaks(in)
	if len(out) != 1 || out[0].File != "007-x.txt" {
		t.Errorf("ownLeaks = %v", out)
	}
}
