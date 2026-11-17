package main

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Skews. docs/operations.md says: "keep admins' clocks within a few seconds of each other" (the shared locks in KV
// judge expiry by each machine's own clock, lock lifetime 30 s), `mash status` warns at 10 s, and `sap.LiveSkew` is 30 s.
// The default within-bound skew is 5 s (a few seconds, under the 10 s warning); the past-bound skew is 45 s (past the
// 30 s lock lifetime and sap.LiveSkew). Both are flags.
const (
	defaultSkew     = 5
	defaultPastSkew = 45
)

// FaketimeEnv is the environment that runs a dynamically linked program with its clock skew seconds ahead of the
// host's, under libfaketime at lib. The clock of the host is never changed: only the one process sees the offset.
func FaketimeEnv(lib string, skew int) []string {
	sign := "+"
	if skew < 0 {
		sign = ""
	}
	return []string{"LD_PRELOAD=" + lib, "FAKETIME=" + sign + strconv.Itoa(skew), "FAKETIME_NO_CACHE=1"}
}

// FindFaketime looks for libfaketime: the flag value if given, else the ldconfig cache and the usual directories.
// "" and a reason when there is none.
func FindFaketime(flagLib string) (string, string) {
	if flagLib != "" {
		if st, err := os.Stat(flagLib); err != nil || !st.Mode().IsRegular() {
			return "", "--faketime-lib " + flagLib + " is not a file"
		}
		return flagLib, ""
	}
	if out, err := exec.Command("/sbin/ldconfig", "-p").Output(); err == nil {
		for _, l := range strings.Split(string(out), "\n") {
			if strings.Contains(l, "libfaketime.so") {
				if i := strings.LastIndex(l, "=> "); i >= 0 {
					return strings.TrimSpace(l[i+3:]), ""
				}
			}
		}
	}
	for _, g := range []string{"/usr/lib/x86_64-linux-gnu/faketime/libfaketime.so.1", "/usr/lib/faketime/libfaketime.so.1", "/usr/local/lib/faketime/libfaketime.so.1", "/usr/lib64/faketime/libfaketime.so.1"} {
		if st, err := os.Stat(g); err == nil && st.Mode().IsRegular() {
			return g, ""
		}
	}
	return "", "libfaketime missing (not in the ldconfig cache or the usual directories; pass --faketime-lib PATH)"
}

var clockRE = regexp.MustCompile(`clock:\s*(\S.*)`)

// clockSeconds reads the skew a `mash status` clock line reports ("clock: +5.0s from the server" or similar): the
// first signed number in the line, in seconds. ok is false when the line has none.
func clockSeconds(statusText string) (float64, bool) {
	m := clockRE.FindStringSubmatch(statusText)
	if m == nil {
		return 0, false
	}
	n := regexp.MustCompile(`[+-]?\d+(\.\d+)?\s*(ms|s)?`).FindString(m[1])
	if n == "" {
		return 0, false
	}
	n = strings.TrimSpace(n)
	unit := time.Second
	if strings.HasSuffix(n, "ms") {
		unit, n = time.Millisecond, strings.TrimSuffix(n, "ms")
	} else {
		n = strings.TrimSuffix(n, "s")
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
	if err != nil {
		return 0, false
	}
	return f * float64(unit) / float64(time.Second), true
}

// faketimeWorks runs `mash status` as the given endpoint environment on h under the skew and under no skew, and reports
// whether the clock line moved by about the skew. libfaketime patches libc, and the Go runtime reads the clock through
// the vDSO, so on some builds it moves nothing: that is a finding, not a pass.
func (s *At5) faketimeWorks(h Host, lib string, skew int, epEnv []string) (bool, string) {
	base := s.mash(h, "clock-plain", epEnv, "status")
	skewed := s.mash(h, "clock-skewed", append(append([]string{}, epEnv...), FaketimeEnv(lib, skew)...), "status")
	b, ok1 := clockSeconds(base.Text())
	k, ok2 := clockSeconds(skewed.Text())
	if !ok1 || !ok2 {
		return false, "no clock line in mash status"
	}
	moved := b - k // the line is server minus local: a clock running ahead makes it smaller
	d := fmt.Sprintf("clock line %.1fs plain, %.1fs under libfaketime +%d", b, k, skew)
	return moved > float64(skew)/2 && moved < float64(skew)*2, d
}
