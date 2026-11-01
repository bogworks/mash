package testenv

import "testing"

// The framer splits a protocol stream however it is cut into reads, and skips payloads (which may hold newlines).
func TestFramerSplitsAnyChunking(t *testing.T) {
	stream := "CONNECT {}\r\nPING\r\nPUB a.b 2\r\n\r\n\r\nHPUB x.y rep 4 6\r\nK:V\r\nhi\r\nSUB q 1\r\nMSG r 1 3\r\nabc\r\n"
	want := "CONNECT PING PUB:a.b HPUB:x.y SUB MSG:r"
	for step := 1; step <= len(stream); step++ {
		f := &framer{}
		var got []frame
		for i := 0; i < len(stream); i += step {
			got = append(got, f.feed([]byte(stream[i:min(i+step, len(stream))]))...)
		}
		s := ""
		for i, fr := range got {
			if i > 0 {
				s += " "
			}
			s += fr.verb
			if fr.subject != "" {
				s += ":" + fr.subject
			}
		}
		if s != want {
			t.Fatalf("chunks of %d: %q, want %q", step, s, want)
		}
	}
}

// Requests sent together are one wave; one that follows a delivered response is the next.
func TestWavesFollowResponses(t *testing.T) {
	p := &RTTProxy{sinceWave: true}
	req := func(subj string) { p.note(frame{verb: "PUB", subject: subj}, true) }
	resp := func() { p.note(frame{verb: "MSG", subject: "_INBOX.x"}, false) }
	p.note(frame{verb: "INFO"}, false)
	p.note(frame{verb: "CONNECT"}, true)
	p.note(frame{verb: "PONG"}, false)
	req("a")
	req("b")
	req("c")
	resp()
	resp()
	req("d")
	p.note(frame{verb: "MSG", subject: "agents.hb.cc.o.x"}, false) // heartbeats are not answers
	req("e")
	resp()
	req("f")
	if got := p.Waves(); got != 4 {
		t.Fatalf("%d waves, want 4 (connect, a-c, d-e, f)\n%s", got, p.Describe())
	}
}

// A reply to someone else's request is the client answering, not waiting: it neither starts a wave nor splits one,
// wherever it lands relative to the responses the real chain waits on.
func TestRepliesAreNotWaves(t *testing.T) {
	p := &RTTProxy{sinceWave: true}
	req := func(subj string) { p.note(frame{verb: "PUB", subject: subj}, true) }
	resp := func() { p.note(frame{verb: "MSG", subject: "_INBOX.x"}, false) }
	p.note(frame{verb: "CONNECT"}, true)
	resp()
	req("a")
	req("b")
	resp()                 // the loopback echo of b
	req("_INBOX.peer.zzz") // the service answering it
	resp()                 // the answer to a
	req("c")
	if got := p.Waves(); got != 3 {
		t.Fatalf("%d waves, want 3 (connect, a-b, c)\n%s", got, p.Describe())
	}
}
