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
		r >= 0x1F900 && r <= 0x1F9FF,
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
