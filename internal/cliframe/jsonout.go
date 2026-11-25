package cliframe

import (
	"bytes"
	"encoding/json"
	"io"
	"os"

	"golang.org/x/term"
)

// IsTerminal says whether w is a terminal. WriteJSON asks it to choose between the indented and the compact form; a
// test replaces it to force either.
var IsTerminal = func(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// JSONBytes is v as --json prints it, without the final newline: indented by two spaces when w is a terminal and one
// compact line when w is a pipe or a file (the rule "--json prints one object"). < > & are kept readable.
func JSONBytes(w io.Writer, v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if IsTerminal(w) {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}

// WriteJSON prints v and a newline to w as JSONBytes says. It is the one place both tools write a --json object,
// a success object and an error object alike.
func WriteJSON(w io.Writer, v any) error {
	b, err := JSONBytes(w, v)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}
