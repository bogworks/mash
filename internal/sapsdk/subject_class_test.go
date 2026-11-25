package sapsdk

import "testing"

// TestSubjectClassControlsAreUnreadable: a sub with a tab or newline has a control character, which ADR 0173
// classes as unreadable; only a space (not a control) is "space in subject".
func TestSubjectClassControlsAreUnreadable(t *testing.T) {
	for _, c := range []struct{ sub, want string }{
		{"a\tb", ClassUnreadable}, {"a\nb", ClassUnreadable}, {"a\x7fb", ClassUnreadable},
		{"a b", ClassSpaceInSubject}, {"a b", ClassSpaceInSubject}, {"a b", ClassSpaceInSubject},
		{"ab", ClassUnreadable},
	} {
		if got := subjectClass(`{"sub":` + quote(c.sub) + `,"sig":"x"}`); got != c.want {
			t.Errorf("sub %q: class %q, want %q", c.sub, got, c.want)
		}
	}
}

func quote(s string) string {
	b := []byte{'"'}
	for _, r := range s {
		switch {
		case r == '\t':
			b = append(b, `\t`...)
		case r == '\n':
			b = append(b, `\n`...)
		case r < 0x20 || r == 0x7f:
			b = append(b, `\u007f`...)
		default:
			b = append(b, string(r)...)
		}
	}
	return string(append(b, '"'))
}
