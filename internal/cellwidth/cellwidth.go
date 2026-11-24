// Package cellwidth measures text in terminal cells: 1 for most characters, 2 for East Asian wide and fullwidth
// ones and emoji, 0 for combining marks. It is a small table, not the full Unicode width data.
package cellwidth

import "unicode"

// Rune is the number of terminal cells r takes.
func Rune(r rune) int {
	switch {
	case unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf):
		return 0
	case r >= 0x1100 && r <= 0x115F,
		r >= 0x2E80 && r <= 0xA4CF && r != 0x303F,
		r >= 0xAC00 && r <= 0xD7A3,
		r >= 0xF900 && r <= 0xFAFF,
		r >= 0xFE30 && r <= 0xFE6F,
		r >= 0xFF00 && r <= 0xFF60,
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x1F300 && r <= 0x1F64F,
		r >= 0x1F680 && r <= 0x1F6FF,
		r >= 0x1F1E6 && r <= 0x1F1FF,
		r >= 0x1F900 && r <= 0x1F9FF,
		r >= 0x1FA70 && r <= 0x1FAFF,
		r == 0x1F004, r == 0x1F0CF, r == 0x1F18E,
		r >= 0x1F191 && r <= 0x1F19A,
		r >= 0x1F200 && r <= 0x1F2FF,
		r >= 0x231A && r <= 0x231B,
		r >= 0x23E9 && r <= 0x23EC,
		r == 0x23F0, r == 0x23F3,
		r >= 0x25FD && r <= 0x25FE,
		r >= 0x2614 && r <= 0x2615,
		r >= 0x2648 && r <= 0x2653,
		r == 0x267F, r == 0x2693, r == 0x26A1,
		r >= 0x26AA && r <= 0x26AB,
		r >= 0x26BD && r <= 0x26BE,
		r >= 0x26C4 && r <= 0x26C5,
		r == 0x26CE, r == 0x26D4, r == 0x26EA,
		r >= 0x26F2 && r <= 0x26F3,
		r == 0x26F5, r == 0x26FA, r == 0x26FD, r == 0x2705,
		r >= 0x270A && r <= 0x270B,
		r == 0x2728, r == 0x274C, r == 0x274E,
		r >= 0x2753 && r <= 0x2755,
		r == 0x2757,
		r >= 0x2795 && r <= 0x2797,
		r == 0x27B0, r == 0x27BF,
		r >= 0x2B1B && r <= 0x2B1C,
		r == 0x2B50, r == 0x2B55,
		r >= 0x20000 && r <= 0x3FFFD:
		return 2
	}
	return 1
}

// String is the number of terminal cells s takes.
func String(s string) int {
	n := 0
	for _, r := range s {
		n += Rune(r)
	}
	return n
}
