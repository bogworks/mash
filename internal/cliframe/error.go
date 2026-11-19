package cliframe

import (
	"encoding/json"
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

// Report writes err to the user and returns the process exit code. A nil err writes nothing and returns 0. An *Error
// (found with errors.As) keeps its code when that is 1 or 2, otherwise the code is 1; any other error is a failure with
// code 1 and its text as the message.
//
// In text mode stderr gets "error: MSG" and, when there is a hint, "hint: HINT"; stdout gets nothing. In JSON mode stdout
// gets one line, {"error":"MSG","hint":"HINT"} with the hint left out when empty; stderr gets nothing.
func Report(stdout, stderr io.Writer, err error, jsonOut bool) int {
	if err == nil {
		return 0
	}
	msg, hint, code := err.Error(), "", 1
	var e *Error
	if errors.As(err, &e) && e != nil {
		msg, hint = e.Msg, e.Hint
		if e.Code == 2 {
			code = 2
		}
	}
	msg = strings.TrimPrefix(msg, "error: ")
	if jsonOut {
		out := struct {
			Error string `json:"error"`
			Hint  string `json:"hint,omitempty"`
		}{msg, hint}
		enc := json.NewEncoder(stdout) // one line plus newline; keep < > & readable
		enc.SetEscapeHTML(false)
		_ = enc.Encode(out)
		return code
	}
	fmt.Fprintf(stderr, "error: %s\n", msg)
	if hint != "" {
		fmt.Fprintf(stderr, "hint: %s\n", hint)
	}
	return code
}
