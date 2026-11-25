package cliframe

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

// Error is a command failure that carries what the user sees and the process exit code. Code is 1 for a failure and 2
// for a usage error.
type Error struct {
	Msg  string // what went wrong, without a leading "error:"
	Hint string // an optional next step
	Code int    // 1 failure, 2 usage
	Wrap bool   // the text rendering wraps a long message at WrapWidth columns (WrapAt when set); the JSON message stays one line
	// WrapAt is the width of a wrapped line for this error; 0 means WrapWidth.
	WrapAt int
}

// Error returns the message.
func (e *Error) Error() string { return e.Msg }

// Failf returns a failure (exit code 1).
func Failf(format string, a ...any) *Error {
	return &Error{Msg: fmt.Sprintf(format, a...), Code: 1}
}

// Usagef returns a usage error (exit code 2).
func Usagef(format string, a ...any) *Error {
	return &Error{Msg: fmt.Sprintf(format, a...), Code: 2}
}

// WithHint returns a copy of e with the hint set.
func (e *Error) WithHint(h string) *Error {
	c := *e
	c.Hint = h
	return &c
}

// WrapWidth is the width of a wrapped error line, and WrapIndent the continuation: it sits under the text after "error: ".
const (
	WrapWidth  = 80
	WrapIndent = "       "
)

// WithWrap returns a copy of e that the text rendering wraps at WrapWidth columns.
func (e *Error) WithWrap() *Error {
	c := *e
	c.Wrap = true
	return &c
}

// WithWrapAt returns a copy of e that the text rendering wraps at n columns instead of WrapWidth.
func (e *Error) WithWrapAt(n int) *Error {
	c := *e
	c.Wrap = true
	c.WrapAt = n
	return &c
}

// wrapError lays out "error: MSG" in lines of at most width columns (WrapWidth when width is 0), a continuation indented by WrapIndent. A line
// breaks at a space, after a semicolon when one lies within the width, else at the last space that fits; a word too
// long for a line keeps one to itself.
func wrapError(msg string, width int) string {
	if width <= 0 {
		width = WrapWidth
	}
	line := "error: " + msg
	var out []string
	for len([]rune(line)) > width {
		r := []rune(line)
		cut, semi := -1, -1
		for i := 1; i < len(r) && i <= width; i++ {
			if r[i] == ' ' && r[i-1] != ' ' {
				cut = i
				if r[i-1] == ';' {
					semi = i
				}
			}
		}
		if semi >= 0 {
			cut = semi
		}
		if cut < 0 { // no space within the width: break at the first one after it
			for i := width; i < len(r); i++ {
				if r[i] == ' ' {
					cut = i
					break
				}
			}
			if cut < 0 {
				break
			}
		}
		out = append(out, string(r[:cut]))
		line = WrapIndent + strings.TrimLeft(string(r[cut:]), " ")
	}
	return strings.Join(append(out, line), "\n")
}

// Report writes err to the user and returns the process exit code. A nil err writes nothing and returns 0. An *Error
// (found with errors.As) keeps its code when that is 1 or 2, otherwise the code is 1; any other error is a failure with
// code 1 and its text as the message.
//
// In text mode stderr gets "error: MSG" and, when there is a hint, "hint: HINT"; stdout gets nothing. In JSON mode stdout
// gets the object {"error":"MSG","hint":"HINT"} with the hint left out when empty, indented when stdout is a terminal and one
// compact line otherwise (WriteJSON); stderr gets nothing.
func Report(stdout, stderr io.Writer, err error, jsonOut bool) int {
	if err == nil {
		return 0
	}
	msg, hint, code, wrap, width := err.Error(), "", 1, false, 0
	var e *Error
	if errors.As(err, &e) && e != nil {
		wrap, width = e.Wrap, e.WrapAt
		msg, hint = e.Msg, e.Hint
		if e.Code == 2 {
			code = 2
		}
	}
	var xc interface{ ExitCode() int } // an error that carries its own code (a refused plain URL is 2)
	if errors.As(err, &xc) && xc.ExitCode() == 2 {
		code = 2
	}
	msg = strings.TrimPrefix(msg, "error: ")
	if jsonOut {
		out := struct {
			Error string `json:"error"`
			Hint  string `json:"hint,omitempty"`
		}{msg, hint}
		_ = WriteJSON(stdout, out) // indented at a terminal, one compact line otherwise
		return code
	}
	if wrap {
		fmt.Fprintf(stderr, "%s\n", wrapError(msg, width))
	} else {
		fmt.Fprintf(stderr, "error: %s\n", msg)
	}
	if hint != "" {
		fmt.Fprintf(stderr, "hint: %s\n", hint)
	}
	return code
}
