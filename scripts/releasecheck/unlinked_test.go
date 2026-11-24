package main

import (
	"context"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

func TestLinkedPolicyPackages(t *testing.T) {
	const m = "github.com/bogworks/mash/"
	const sdk = "github.com/bogworks/synadia-agents.go"
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"clean, with the size constants", "fmt\n" + m + "internal/policy\n" + m + "cmd/mash\n", nil},
		{"empty", "", nil},
		{"prefix, new name", m + "internal/policyfoo\n", []string{"internal/policyfoo"}},
		{"policy itself allowed", m + "internal/policy\n", nil},
		{"policy subpackage", m + "internal/policy/sub\n", []string{"internal/policy/sub"}},
		{"policydoc", m + "internal/policydoc\n", []string{"internal/policydoc"}},
		{"the four named elsewhere", m + "internal/requesterreg\n" + m + "internal/issue\n" + m + "internal/reconcile\n" + m + "internal/listset\n",
			[]string{"internal/issue", "internal/listset", "internal/reconcile", "internal/requesterreg"}},
		{"lookalikes", m + "internal/listsetx\n" + m + "internal/xissue\nother.example/internal/issue\nother.example/internal/policyfoo\n", nil},
		{"duplicates collapse", m + "internal/issue\n" + m + "internal/issue\n" + m + "internal/policydoc\n", []string{"internal/issue", "internal/policydoc"}},
		{"subpackages of the named four", m + "internal/listset/foo\n" + m + "internal/issue/v2\n" + m + "internal/reconcile/x/y\n" + m + "internal/requesterreg/z\n",
			[]string{"internal/issue/v2", "internal/listset/foo", "internal/reconcile/x/y", "internal/requesterreg/z"}},
		{"policy subpackage deep, policyfoo subpackage", m + "internal/policyfoo/x\n", []string{"internal/policyfoo/x"}},
		{"the size constants do not hide a neighbour", m + "internal/policy\n" + m + "internal/policyview\n", []string{"internal/policyview"}},
		{"sdk caller and host refused, identity allowed", sdk + "/identity\n" + sdk + "/caller\n" + sdk + "/host\n",
			[]string{sdk + "/caller", sdk + "/host"}},
		{"sdk identity alone allowed", sdk + "/identity\n" + sdk + "/identity/sub\n", nil},
		{"sdk caller alone", sdk + "/caller\n", []string{sdk + "/caller"}},
		{"sdk host alone", sdk + "/host\n", []string{sdk + "/host"}},
		{"sdk subpackage of a refused one", sdk + "/host/x\n", []string{sdk + "/host/x"}},
		{"sdk root allowed", sdk + "\n", nil},
		{"sdk root and other subpackages allowed", sdk + "\n" + sdk + "/other\n" + sdk + "/identityx\n", nil},
		{"sdk path as prefix of another module", sdk + "-fork/identity\n" + sdk + ".x/host\n" + "other.example/synadia-agents.go/caller\n", nil},
		{"sapsdk allowed", m + "internal/sapsdk\n" + m + "internal/sapsdk/sub\n", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := linkedPolicyPackages("github.com/bogworks/mash", tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLinkedPolicyPackagesOtherModule(t *testing.T) {
	in := "example.org/other/internal/listset\ngithub.com/bogworks/mash/internal/issue\nexample.org/other/internal/policy\n"
	got := linkedPolicyPackages("example.org/other", in)
	if want := []string{"internal/listset"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestRefusalReason(t *testing.T) {
	for pkg, want := range map[string]string{
		"github.com/bogworks/synadia-agents.go/caller": "SDK caller and host",
		"internal/policyfoo":                           "policy engine",
	} {
		if got := refusalReason(pkg); !strings.Contains(got, want) {
			t.Errorf("%s: got %q, want it to contain %q", pkg, got, want)
		}
	}
}

func TestDescribeLinked(t *testing.T) {
	if got := describeLinked([]targetLinks{{"linux/amd64", nil}, {"darwin/arm64", nil}}); got != "" {
		t.Errorf("clean: got %q", got)
	}
	got := describeLinked([]targetLinks{
		{"darwin/amd64", []string{"internal/policyapply"}},
		{"darwin/arm64", []string{"internal/issue", "internal/policyapply"}},
		{"linux/amd64", nil},
	})
	want := "internal/issue (darwin/arm64), internal/policyapply (darwin/amd64, darwin/arm64)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
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
	const empty = "package main\n\nfunc main() {}\n"
	type file struct{ path, body string }
	cases := []struct {
		name         string
		mod          string
		mash, mashct string
		extra        []file
		want         string // "" the step passes; else the error names it
	}{
		{"nothing linked", "github.com/bogworks/mash", empty, empty, nil, ""},
		{"mashctl links listset", "github.com/bogworks/mash", empty, "imp:internal/listset", nil, "internal/listset"},
		{"mash links only the size constants", "github.com/bogworks/mash", "imp:internal/policy", empty, nil, ""},
		{"mash links a new policy package", "github.com/bogworks/mash", "imp:internal/policyfoo", empty, nil, "internal/policyfoo"},
		{"another module path", "example.org/other", "imp:internal/listset", empty, nil, "internal/listset"},
		{"listset subpackage", "github.com/bogworks/mash", "imp:internal/listset/sub", empty,
			[]file{{"internal/listset/sub/sub.go", "package sub\n"}}, "internal/listset/sub"},
		{"darwin-only import", "github.com/bogworks/mash", empty, empty,
			[]file{{"cmd/mashctl/x_darwin.go", "extra:internal/listset"}}, "darwin"},
		{"linux-only import", "github.com/bogworks/mash", empty, empty,
			[]file{{"cmd/mashctl/x_linux.go", "extra:internal/listset"}}, "linux"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			expand := func(body string) string {
				if p, ok := strings.CutPrefix(body, "imp:"); ok {
					return "package main\n\nimport _ \"" + tc.mod + "/" + p + "\"\n\nfunc main() {}\n"
				}
				if p, ok := strings.CutPrefix(body, "extra:"); ok {
					return "package main\n\nimport _ \"" + tc.mod + "/" + p + "\"\n"
				}
				return body
			}
			repo := t.TempDir()
			gitT(t, repo, "init", "-q")
			write(t, repo, "go.mod", "module "+tc.mod+"\n\ngo 1.27.4\n")
			write(t, repo, "cmd/mash/main.go", expand(tc.mash))
			write(t, repo, "cmd/mashctl/main.go", expand(tc.mashct))
			write(t, repo, "internal/policy/policy.go", "package policy\n")
			write(t, repo, "internal/listset/listset.go", "package listset\n")
			write(t, repo, "internal/policyfoo/policyfoo.go", "package policyfoo\n")
			for _, f := range tc.extra {
				write(t, repo, f.path, expand(f.body))
			}
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
