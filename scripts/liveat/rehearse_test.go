package main

import "testing"

func TestParsePut(t *testing.T) {
	ok, err := parsePut("/a/mash:/node/v055/mash:0755")
	if err != nil || ok.Local != "/a/mash" || ok.Dest != "/node/v055/mash" || ok.Mode != 0o755 {
		t.Fatalf("got %+v, %v", ok, err)
	}
	if d, err := parsePut("/a/f:/node/f"); err != nil || d.Mode != 0o644 {
		t.Fatalf("default mode: %+v, %v", d, err)
	}
	for _, bad := range []string{"/a", "a:/b", "/a:b", "/a:/b:9", "/a:/b:1777", "/a:/b:0755:x"} {
		if _, err := parsePut(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}
