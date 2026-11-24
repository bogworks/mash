package cellwidth

import "testing"

func TestRune(t *testing.T) {
	for _, c := range []struct {
		r    rune
		want int
	}{
		{'a', 1}, {'é', 1}, {' ', 1},
		{'世', 2}, {'あ', 2}, {'한', 2}, {'Ａ', 2},
		{0x303F, 1},
		{0x0301, 0}, {0x200D, 0},
		{'🙂', 2}, {'🤖', 2},
	} {
		if got := Rune(c.r); got != c.want {
			t.Errorf("Rune(%U) = %d, want %d", c.r, got, c.want)
		}
	}
}

func TestString(t *testing.T) {
	for _, c := range []struct {
		s    string
		want int
	}{
		{"", 0}, {"abc", 3}, {"世界", 4}, {"é", 1}, {"a世🙂", 5},
	} {
		if got := String(c.s); got != c.want {
			t.Errorf("String(%q) = %d, want %d", c.s, got, c.want)
		}
	}
}
