package main

import (
	"context"
	"os/exec"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestLinkedPolicyPackages(t *testing.T) {
	const m = "github.com/bogworks/mash/"
	var all []string
	for _, p := range policyEnginePackages {
		all = append(all, m+p)
	}
	sorted := append([]string(nil), policyEnginePackages...)
	sort.Strings(sorted)
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"clean, with the size constants", "fmt\n" + m + "internal/policy\n" + m + "cmd/mash\n", nil},
		{"empty", "", nil},
		{"lookalikes", m + "internal/policyfoo\n" + m + "internal/listsetx\n" + m + "internal/xissue\nother.example/internal/issue\n", nil},
		{"each engine package", strings.Join(all, "\n") + "\n", sorted},
		{"duplicates collapse", m + "internal/issue\n" + m + "internal/issue\n" + m + "internal/policydoc\n", []string{"internal/issue", "internal/policydoc"}},
		{"the size constants do not hide a neighbour", m + "internal/policy\n" + m + "internal/policyview\n", []string{"internal/policyview"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := linkedPolicyPackages(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPolicyUnlinkedStepSeam(t *testing.T) {
	called := false
	o := Options{unlinked: func(context.Context, Options, string) error { called = true; return nil }}
	if err := policyUnlinkedStep(context.Background(), o, "x"); err != nil || !called {
		t.Fatalf("seam not honoured: err=%v called=%v", err, called)
	}
}

func TestPolicyUnlinkedStepGoList(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not on PATH")
	}
	const mod = "github.com/bogworks/mash"
	const empty = "package main\n\nfunc main() {}\n"
	imp := func(path string) string {
		return "package main\n\nimport _ \"" + mod + "/" + path + "\"\n\nfunc main() {}\n"
	}
	cases := []struct {
		name         string
		mash, mashct string
		want         string // "" the step passes; else the error names it
	}{
		{"nothing linked", empty, empty, ""},
		{"mashctl links listset", empty, imp("internal/listset"), "internal/listset"},
		{"mash links only the size constants", imp("internal/policy"), empty, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			gitT(t, repo, "init", "-q")
			write(t, repo, "go.mod", "module "+mod+"\n\ngo 1.27.4\n")
			write(t, repo, "cmd/mash/main.go", tc.mash)
			write(t, repo, "cmd/mashctl/main.go", tc.mashct)
			write(t, repo, "internal/policy/policy.go", "package policy\n")
			write(t, repo, "internal/listset/listset.go", "package listset\n")
			gitT(t, repo, "add", "-A")
			gitT(t, repo, "commit", "-q", "-m", "c")
			x := gitT(t, repo, "rev-parse", "HEAD")
			err := policyUnlinkedStep(context.Background(), Options{Repo: repo}, x)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("want nil, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want an error naming %s, got %v", tc.want, err)
			}
			var oe opError
			if asOp(err, &oe) {
				t.Errorf("a linked package is a refusal, got an operational error: %v", err)
			}
		})
	}
}
