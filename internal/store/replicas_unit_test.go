package store

import (
	"strings"
	"testing"

	"github.com/bogworks/mash/internal/sap"
)

func TestReplicas(t *testing.T) {
	for _, c := range []struct {
		setting string
		servers int
		want    int
		errHas  string
	}{
		{"auto", 0, 1, ""}, {"", 1, 1, ""}, {"auto", 2, 1, ""}, {"auto", 3, 3, ""}, {"AUTO", 5, 3, ""},
		{"1", 0, 1, ""}, {"1", 3, 1, ""},
		{"3", 3, 3, ""}, {"3", 5, 3, ""},
		{"3", 2, 0, "needs at least 3 servers and 2 are declared"}, {"3", 0, 0, "and none are declared"},
		{"5", 5, 0, "auto, 1 and 3"}, {"2", 3, 0, "auto, 1 and 3"}, {"0", 3, 0, "auto, 1 and 3"}, {"x", 3, 0, "auto, 1 and 3"}, {"-1", 3, 0, "auto, 1 and 3"},
	} {
		got, err := Replicas(c.setting, c.servers)
		if c.errHas == "" {
			if err != nil || got != c.want {
				t.Errorf("Replicas(%q, %d) = %d, %v; want %d", c.setting, c.servers, got, err, c.want)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.errHas) || got != 0 {
			t.Errorf("Replicas(%q, %d) = %d, %v; want an error with %q", c.setting, c.servers, got, err, c.errHas)
		}
	}
	if _, err := Replicas("5", 5); err == nil || !strings.Contains(err.Error(), "not tested") {
		t.Errorf("5 is refused with a reason: %v", err)
	}
}

func TestLimitsReplicaCountAndValidate(t *testing.T) {
	l := DefaultLimits()
	if l.Replicas != 0 || l.ReplicaCount() != 1 {
		t.Fatalf("default limits replicas %d, count %d", l.Replicas, l.ReplicaCount())
	}
	for _, r := range []int{0, 1, 3} {
		l.Replicas = r
		if err := l.Validate(); err != nil {
			t.Errorf("replicas %d: %v", r, err)
		}
	}
	for _, r := range []int{2, 5, -1} {
		l.Replicas = r
		if err := l.Validate(); err == nil || !strings.Contains(err.Error(), "auto, 1 and 3") {
			t.Errorf("replicas %d accepted or badly worded: %v", r, err)
		}
	}
}

// Every config function carries the count: R1 by default (unchanged), R3 when asked.
func TestConfigFunctionsCarryTheReplicaCount(t *testing.T) {
	r1, r3 := DefaultLimits(), DefaultLimits()
	r3.Replicas = 3
	if got := DirectoryConfigFor(r1).Replicas; got != 1 {
		t.Errorf("directory R1: %d", got)
	}
	if got := DirectoryConfigFor(r3).Replicas; got != 3 {
		t.Errorf("directory R3: %d", got)
	}
	if got := DirectoryConfig().Replicas; got != 1 {
		t.Errorf("DirectoryConfig: %d", got)
	}
	if got := LeaseConfigFor(r1).Replicas; got != 1 {
		t.Errorf("lease R1: %d", got)
	}
	if got := LeaseConfigFor(r3).Replicas; got != 3 {
		t.Errorf("lease R3: %d", got)
	}
	for n, want := range map[int]int{0: 1, 1: 1, 3: 3} {
		if got := RetiredConfigFor(n).Replicas; got != want {
			t.Errorf("retired(%d): %d", n, got)
		}
		if got := IssuedConfigFor(n).Replicas; got != want {
			t.Errorf("issued(%d): %d", n, got)
		}
		if got := AdoptionConfigFor(n).Replicas; got != want {
			t.Errorf("adoption(%d): %d", n, got)
		}
	}
	if RetiredConfig().Replicas != 1 || IssuedConfig().Replicas != 1 || AdoptionConfig().Replicas != 1 {
		t.Error("the plain config functions are R1")
	}
	if got := InboxConfigFor("o", "a", r1).Replicas; got != 1 {
		t.Errorf("inbox R1: %d", got)
	}
	if got := InboxConfigFor("o", "a", r3).Replicas; got != 3 {
		t.Errorf("inbox R3: %d", got)
	}
	if got := InboxConfig("o", "a").Replicas; got != 1 {
		t.Errorf("InboxConfig: %d", got)
	}
	id, err := sap.New("agent", "o", "a")
	if err != nil {
		t.Fatal(err)
	}
	if got := InboxConfigIdentity(id, r1).Replicas; got != 1 {
		t.Errorf("identity inbox R1: %d", got)
	}
	if got := InboxConfigIdentity(id, r3).Replicas; got != 3 {
		t.Errorf("identity inbox R3: %d", got)
	}
	leg, _ := sap.Legacy("o", "a")
	if got := InboxConfigIdentity(leg, r3).Replicas; got != 3 {
		t.Errorf("legacy identity inbox R3: %d", got)
	}
	// Only the count differs between R1 and R3.
	a, b := InboxConfigFor("o", "a", r1), InboxConfigFor("o", "a", r3)
	b.Replicas = a.Replicas
	if a.Name != b.Name || a.MaxBytes != b.MaxBytes || a.MaxAge != b.MaxAge || a.DenyPurge != b.DenyPurge || a.Duplicates != b.Duplicates {
		t.Errorf("R3 changed another field: %+v vs %+v", a, b)
	}
}

func TestReplicaShortfallText(t *testing.T) {
	if ReplicaShortfallText(1, []string{"x"}) != "" || ReplicaShortfallText(3, nil) != "" {
		t.Error("no finding at R1 or with no shortfall")
	}
	got := ReplicaShortfallText(3, []string{"a", "b", "c", "d", "e"})
	if !strings.Contains(got, "5 mash-owned object(s)") || !strings.Contains(got, "a, b, c and 2 more") || !strings.Contains(got, "raise the replica count of mash-owned state to 3") {
		t.Errorf("%q", got)
	}
}
