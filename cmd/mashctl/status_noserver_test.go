package main

import "testing"

// #1097: with no server configured, status says so and exits 1; doctor's config row says the same.
func TestStatusNoServerConfigured(t *testing.T) {
	w := newConfWorld(t, false)
	r := w.run("status")
	if r.out != "" || r.errs != "error: no server is configured\nhint: mashctl init --config <file>\n" || r.code != 1 {
		t.Errorf("status: %d %q %q", r.code, r.out, r.errs)
	}
	r = w.run("status", "--json")
	if r.out != "{\"error\":\"no server is configured\",\"hint\":\"mashctl init --config <file>\"}\n" || r.errs != "" || r.code != 1 {
		t.Errorf("status --json: %d %q %q", r.code, r.out, r.errs)
	}
}

func TestDoctorNoServerConfigured(t *testing.T) {
	w := newConfWorld(t, false)
	r := w.run("doctor")
	want := "config     FAIL  no server is configured\nserver     skip  needs the config\nsign-in    skip  needs the server\n" +
		"storage    skip  needs sign-in\nendpoints  skip  needs the storage\nkeys       skip  needs sign-in\n\n1 problem.\n"
	if r.out != want || r.errs != "hint: mashctl init --config <file>\n" || r.code != 1 {
		t.Errorf("doctor: %d %q %q", r.code, r.out, r.errs)
	}
}
