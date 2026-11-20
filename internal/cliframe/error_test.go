package cliframe

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
)

func TestReport(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		json   bool
		code   int
		stdout string
		stderr string
	}{
		{"nil text", nil, false, 0, "", ""},
		{"nil json", nil, true, 0, "", ""},
		{"failure text", Failf("no creds at %s", "/x"), false, 1, "", "error: no creds at /x\n"},
		{"failure json", Failf("no creds"), true, 1, `{"error":"no creds"}` + "\n", ""},
		{"usage text", Usagef("bad flag"), false, 2, "", "error: bad flag\n"},
		{"usage json", Usagef("bad flag"), true, 2, `{"error":"bad flag"}` + "\n", ""},
		{"hint text", Failf("down").WithHint("try mash status"), false, 1, "", "error: down\nhint: try mash status\n"},
		{"hint json", Usagef("bad").WithHint("see --help"), true, 2, `{"error":"bad","hint":"see --help"}` + "\n", ""},
		{"wrapped text", fmt.Errorf("ctx: %w", Usagef("bad").WithHint("h")), false, 2, "", "error: bad\nhint: h\n"},
		{"wrapped json", fmt.Errorf("ctx: %w", Failf("bad")), true, 1, `{"error":"bad"}` + "\n", ""},
		{"plain text", errors.New("boom"), false, 1, "", "error: boom\n"},
		{"plain json", errors.New("boom"), true, 1, `{"error":"boom"}` + "\n", ""},
		{"leading error prefix text", Failf("error: boom"), false, 1, "", "error: boom\n"},
		{"leading error prefix json", errors.New("error: boom"), true, 1, `{"error":"boom"}` + "\n", ""},
		{"code 0 becomes 1", &Error{Msg: "x"}, false, 1, "", "error: x\n"},
		{"code 3 becomes 1", &Error{Msg: "x", Code: 3}, false, 1, "", "error: x\n"},
		{"json escaping", Failf(`say "hi" <now>`), true, 1, `{"error":"say \"hi\" <now>"}` + "\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			code := Report(&out, &errb, tc.err, tc.json)
			if code != tc.code {
				t.Errorf("code = %d, want %d", code, tc.code)
			}
			if out.String() != tc.stdout {
				t.Errorf("stdout = %q, want %q", out.String(), tc.stdout)
			}
			if errb.String() != tc.stderr {
				t.Errorf("stderr = %q, want %q", errb.String(), tc.stderr)
			}
		})
	}
}

func TestWithHintCopies(t *testing.T) {
	e := Failf("x")
	h := e.WithHint("h")
	if e.Hint != "" || h.Hint != "h" || h == e {
		t.Errorf("e = %+v h = %+v", e, h)
	}
}

func TestErrorString(t *testing.T) {
	if got := Usagef("a %d", 1).Error(); got != "a 1" {
		t.Errorf("Error() = %q", got)
	}
}

func TestReportWrap(t *testing.T) {
	long := "~/.config/mash was set up with a different operator key; nothing was changed"
	for _, tc := range []struct {
		name, msg, want string
		wrap            bool
	}{
		{"semicolon", long, "error: ~/.config/mash was set up with a different operator key;\n       nothing was changed\n", true},
		{"short stays one line", "~/cfg was set up with a different owner; nothing was changed", "error: ~/cfg was set up with a different owner; nothing was changed\n", true},
		{"no semicolon wraps at the last space that fits", "aaaa bbbb cccc dddd eeee ffff gggg hhhh iiii jjjj kkkk llll mmmm nnnn oooo pppp qqqq rrrr", "error: aaaa bbbb cccc dddd eeee ffff gggg hhhh iiii jjjj kkkk llll mmmm nnnn\n       oooo pppp qqqq rrrr\n", true},
		{"not asked, not wrapped", long, "error: " + long + "\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := Failf("%s", tc.msg)
			e.Wrap = tc.wrap
			var out, errb bytes.Buffer
			Report(&out, &errb, e, false)
			if errb.String() != tc.want {
				t.Errorf("stderr = %q, want %q", errb.String(), tc.want)
			}
			out.Reset()
			errb.Reset()
			Report(&out, &errb, e, true)
			if out.String() != `{"error":"`+tc.msg+`"}`+"\n" {
				t.Errorf("json = %q", out.String())
			}
		})
	}
}

func TestReportWrapAt(t *testing.T) {
	msg := "acme.demo.bob was not deleted: 1 of 2 servers did not confirm its key was cancelled"
	e := Failf("%s", msg).WithWrapAt(68).WithHint("mashctl endpoint delete acme.demo.bob")
	var out, errb bytes.Buffer
	if code := Report(&out, &errb, e, false); code != 1 {
		t.Errorf("code %d", code)
	}
	want := "error: acme.demo.bob was not deleted: 1 of 2 servers did not confirm\n       its key was cancelled\nhint: mashctl endpoint delete acme.demo.bob\n"
	if errb.String() != want || out.Len() != 0 {
		t.Errorf("stderr = %q stdout = %q", errb.String(), out.String())
	}
	out.Reset()
	errb.Reset()
	Report(&out, &errb, e, true)
	if out.String() != `{"error":"`+msg+`","hint":"mashctl endpoint delete acme.demo.bob"}`+"\n" || errb.Len() != 0 {
		t.Errorf("json = %q", out.String())
	}
	if d := Failf("%s", msg).WithWrap(); d.WrapAt != 0 || e.WrapAt != 68 {
		t.Errorf("WrapAt %d %d", d.WrapAt, e.WrapAt)
	}
}
