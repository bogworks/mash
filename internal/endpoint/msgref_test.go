package endpoint

import "testing"

func TestParseAndFormatMsgRef(t *testing.T) {
	for _, c := range []struct {
		in   string
		want MsgRef
		text string
	}{
		{"5", MsgRef{Seq: 5}, "5"},
		{"#5", MsgRef{Seq: 5}, "5"},
		{" 12 ", MsgRef{Seq: 12}, "12"},
	} {
		got, err := ParseMsgRef(c.in)
		if err != nil || got != c.want {
			t.Errorf("ParseMsgRef(%q) = %+v, %v", c.in, got, err)
			continue
		}
		if f := FormatMsgRef(got); f != c.text {
			t.Errorf("FormatMsgRef(%+v) = %q, want %q", got, f, c.text)
		}
	}
	for _, bad := range []string{"", "#", "L", "0", "L0", "L5", "#L5", "-1", "5x", "l5", "1.5", "99999999999999999999999"} {
		if r, err := ParseMsgRef(bad); err == nil {
			t.Errorf("ParseMsgRef(%q) = %+v, want an error", bad, r)
		}
	}
	if f := FormatMsgRef(MsgRef{Stream: "other", Seq: 3}); f != "other:3" {
		t.Errorf("other stream: %q", f)
	}
}

// An endpoint reads one inbox stream: #n resolves, and a qualified number is not a message number.
func TestResolveRef(t *testing.T) {
	e := &Endpoint{}
	if r, err := e.ResolveRef("7"); err != nil || r != (MsgRef{Seq: 7}) {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := e.ResolveRef("L7"); err == nil {
		t.Fatal("L7 resolved")
	}
}
