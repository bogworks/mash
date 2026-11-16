package credential

import (
	"fmt"
	"strings"

	"github.com/bogworks/mash/internal/policydirect"
)

// RenderNextSteps is the text of policydirect.Result.NextSteps: for each step, the commands to run, with what only the
// endpoint knows left as a named placeholder. It is a pure function of the steps; the JSON form of a result keeps the
// steps as data.
func RenderNextSteps(steps []policydirect.NextStep) string {
	if len(steps) == 0 {
		return "next steps: none\n"
	}
	label := func(s policydirect.NextStep) string {
		switch s.Step {
		case policydirect.StepFirstCredential:
			return "first credential"
		case policydirect.StepNewCredential:
			return "new credential"
		case policydirect.StepRotateAndRevoke:
			return "rotate and revoke"
		case policydirect.StepRevoke:
			return "revoke"
		}
		return strings.ReplaceAll(s.Step, "_", " ")
	}
	wa, wl := 0, 0
	for _, s := range steps {
		wa, wl = max(wa, len(s.Address)), max(wl, len(label(s)))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "next steps (%d):\n", len(steps))
	cont := "  " + strings.Repeat(" ", wa) + "  "
	for _, s := range steps {
		head := func(text string) { fmt.Fprintf(&b, "  %-*s  %-*s  %s\n", wa, s.Address, wl, label(s), text) }
		more := func(text string) { fmt.Fprintf(&b, "%s%s\n", cont, text) }
		purpose := ""
		if s.Purpose == PurposeRotation {
			purpose = " --purpose rotation"
		}
		switch s.Step {
		case policydirect.StepFirstCredential:
			head(fmt.Sprintf("run on %s's host: mash credential key --out ep.seed", s.Address))
			more(fmt.Sprintf("then, where the keys are: mashctl credential issue --address %s --key <PUBLIC KEY from the line above>%s", s.Address, purpose))
		case policydirect.StepNewCredential:
			head(fmt.Sprintf("where the keys are: mashctl credential issue --address %s --key <ITS PUBLIC KEY>%s", s.Address, purpose))
			more(fmt.Sprintf("its public key: mashctl credential show %s", s.Address))
		case policydirect.StepRotateAndRevoke:
			head(fmt.Sprintf("run on %s's host: mash credential key --out ep.seed", s.Address))
			more(fmt.Sprintf("then: mashctl credential issue --address %s --key <NEW KEY>%s", s.Address, purpose))
			more("then: mashctl credential revoke <OLD KEY>")
		case policydirect.StepRevoke:
			head("where the keys are: mashctl credential revoke <KEY>")
			more(fmt.Sprintf("its keys: mashctl credential show %s", s.Address))
		default:
			head(s.Detail)
		}
	}
	return b.String()
}
