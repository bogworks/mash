package policyfile

import (
	"strings"
	"testing"
)

func TestAddRefusesNamesAndKeysParseRefuses(t *testing.T) {
	for _, tc := range []struct {
		name string
		lab  map[string]string
		want string
	}{
		{"null", nil, `endpoint name "null" would read as a YAML null, not a name: choose another name`},
		{"true", nil, "YAML bool"},
		{"0", nil, "not valid"},
		{"Zed", nil, "not valid"},
		{"ok", map[string]string{"null": "x"}, `label key "null" would read as a YAML null`},
		{"ok", map[string]string{"Bad": "x"}, "label key"},
	} {
		d := loadText(t, "endpoints:\n  a: {}\n")
		if err := d.AddEndpoint(tc.name, tc.lab); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("AddEndpoint(%q, %v): %v, want %q", tc.name, tc.lab, err, tc.want)
		}
	}
	d := loadText(t, "endpoints:\n  a: {}\n")
	if err := d.SetLabels("a", map[string]string{"false": "x"}, nil); err == nil || !strings.Contains(err.Error(), "YAML bool") {
		t.Errorf("SetLabels: %v", err)
	}
}
