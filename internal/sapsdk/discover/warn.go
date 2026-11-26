package discover

import "fmt"

// AnswersNotVerifiedMember is the JSON member `who --json` and the MCP who tool gain (TO-12): an integer, always
// present, 0 when none.
const AnswersNotVerifiedMember = "answers_not_verified"

// Warning is the line `mash who` writes once on standard error when n answers could not be verified (TO-12), without a
// newline. It is empty when n is 0. The "warning:" prefix is mash's own.
func Warning(n int) string {
	switch {
	case n <= 0:
		return ""
	case n == 1:
		return "warning: 1 discovery answer could not be verified"
	}
	return fmt.Sprintf("warning: %d discovery answers could not be verified", n)
}
