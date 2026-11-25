package cliframe

import "strings"

// Row is one line of a label block: a label without its colon, and its value.
type Row struct{ Label, Value string }

// Block renders rows as "label: value" lines, the labels padded to the longest label in the block (colon included)
// plus one space, so every value starts in one column and none overflows it. Lines end with no newline.
func Block(rows ...Row) string {
	width := 0
	for _, r := range rows {
		if n := len(r.Label) + 1; n > width {
			width = n
		}
	}
	lines := make([]string, len(rows))
	for i, r := range rows {
		lines[i] = r.Label + ":" + strings.Repeat(" ", width-len(r.Label)) + r.Value
	}
	return strings.Join(lines, "\n")
}
