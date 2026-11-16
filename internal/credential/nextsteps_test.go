package credential

import (
	"flag"
	"os"
	"testing"

	"github.com/bogworks/mash/internal/policydirect"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func TestRenderNextStepsGolden(t *testing.T) {
	cases := map[string][]policydirect.NextStep{
		"nextsteps_none": {},
		"nextsteps_all": {
			{Sender: "acme.build", Step: policydirect.StepFirstCredential, Action: policydirect.ActionIssue, Address: "acme.build", Purpose: "issue"},
			{Sender: "acme.lint", Step: policydirect.StepNewCredential, Action: policydirect.ActionIssue, Address: "acme.lint", Purpose: "issue"},
			{Sender: "acme.test", Step: policydirect.StepRotateAndRevoke, Action: policydirect.ActionIssueThenRevokeOld, Address: "acme.test", Purpose: "rotation"},
			{Sender: "acme.old", Step: policydirect.StepRevoke, Action: policydirect.ActionRevoke, Address: "acme.old"},
		},
	}
	for name, steps := range cases {
		t.Run(name, func(t *testing.T) {
			got := RenderNextSteps(steps)
			path := "testdata/" + name + ".golden"
			if *update {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Fatalf("got:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}
