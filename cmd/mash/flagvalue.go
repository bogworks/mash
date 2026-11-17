package main

import (
	"fmt"
	"strings"
)

// flagValue reads the value of the value-taking flag name at args[*i]. With the inline form (--flag=VALUE, inline and
// hasInline from strings.Cut) the value is taken as written. Otherwise the value is the next argument and *i moves
// onto it. The next argument may not start with '-' (a bare "-" is allowed), so that a flag left without its value does
// not swallow the next flag. The rule and the text match cmd/mashctl/flagvalue.go.
func flagValue(args []string, i *int, name, inline string, hasInline bool) (string, error) {
	if hasInline {
		if inline == "" {
			return "", fmt.Errorf("%s needs a value", name)
		}
		return inline, nil
	}
	if *i+1 >= len(args) || args[*i+1] == "" {
		return "", fmt.Errorf("%s needs a value", name)
	}
	next := args[*i+1]
	if next != "-" && strings.HasPrefix(next, "-") {
		shown, _, _ := strings.Cut(next, "=") // never echo what follows an '=': it may be a secret
		if len(shown) > 32 {
			shown = shown[:32]
		}
		hint := "VALUE"
		if !strings.Contains(next, "=") && len(next) <= 32 {
			hint = next
		}
		return "", fmt.Errorf("%s needs a value; %q looks like a flag (write %s=%s to use it as the value)", name, shown, name, hint)
	}
	*i++
	return next, nil
}
