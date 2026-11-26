package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/bogworks/mash/scripts/upgradeproof/inventory"
)

// upgradeOrder reads the upgrade page of the candidate and returns the order of actions the run follows. A page with no
// numbered steps fails the check and the run follows the previous release's notes instead.
func (r *runner) upgradeOrder(s *stepCtx) []string {
	b, err := os.ReadFile(filepath.Join(r.src, "docs", "compatibility.md"))
	if !s.need(err == nil, "read the upgrade page docs/compatibility.md at the candidate", "%v", err) {
		return fallbackOrder
	}
	steps := pageSteps(string(b))
	has := s.ok(len(steps) > 0, "the upgrade page has numbered steps in its Upgrading section", "%d found", len(steps))
	if !has {
		s.note("falling back to the order of the 0.5.10 release notes: %s", strings.Join(fallbackOrder, "; "))
		return fallbackOrder
	}
	order, unknown := orderFrom(steps)
	s.need(len(unknown) == 0, "the runner understands every step of the page", "not understood: %v", unknown)
	i, j := indexOf(order, actInstall), indexOf(order, actRestart)
	s.ok(i >= 0 && j > i, "the page puts the install before the restart", "order %v", order)
	if i < 0 || j <= i {
		return fallbackOrder
	}
	return order
}

func indexOf(ss []string, x string) int {
	for i, s := range ss {
		if s == x {
			return i
		}
	}
	return -1
}

// step3: build and install the candidate for Alice, follow the page, REQ-023.
func (r *runner) step3(s *stepCtx) {
	alice, bob := r.personas["alice"], r.personas["bob"]
	order := r.upgradeOrder(s)
	r.evid["upgrade-order.json"] = order
	r.upgrade(s, alice, order, bob, "r01", true)
}

// promptBoth has from ask to's live session a question, answers it as the session, and checks the answer came back.
func (r *runner) promptBoth(s *stepCtx, from, to *persona, token string) {
	ss := r.sessions[to.name]
	answer := "answer-" + token
	done := make(chan cmdResult, 1)
	go func() { done <- r.mash(from, "prompt", to.addr, "question "+token) }()
	id, ok := ss.promptEvent(60 * time.Second)
	if !s.ok(ok, from.name+" asks "+to.name+": the live session receives the prompt", "") {
		<-done
		return
	}
	_, err := ss.call("reply", map[string]any{"request_id": id, "text": answer, "done": true}, 30*time.Second)
	s.need(err == nil, to.name+"'s session answers through the reply tool", "%v", err)
	select {
	case res := <-done:
		s.ok(res.ok() && strings.Contains(res.Out, answer), from.name+" gets "+to.name+"'s answer back", "exit %d", res.Code)
	case <-time.After(60 * time.Second):
		s.ok(false, from.name+" gets "+to.name+"'s answer back", "no answer within 60 s")
	}
}

// step4: Alice is on the candidate and Bob is not: mail, who and prompt both ways.
func (r *runner) step4(s *stepCtx) {
	alice, bob := r.personas["alice"], r.personas["bob"]
	r.mixed(s, alice, bob, "step4", "m")
}

// mixed does one round of mail, who and prompt between two personas, a on one version and b on the other.
func (r *runner) mixed(s *stepCtx, a, b *persona, phase, pre string) {
	if r.mailEntry(s, a, b, pre+"01", "from "+a.name+" to "+b.name+" "+pre+"01", phase, inventory.Pending) {
		time.Sleep(3 * time.Second)
		s.need(r.drain(b) == nil, b.name+"'s hook runs", "")
	}
	if r.mailEntry(s, b, a, pre+"02", "from "+b.name+" to "+a.name+" "+pre+"02", phase, inventory.Pending) {
		time.Sleep(3 * time.Second)
		s.need(r.drain(a) == nil, a.name+"'s hook runs", "")
	}
	for _, p := range []*persona{a, b} {
		res := r.mash(p, "who")
		s.ok(res.ok() && strings.Contains(res.Out, a.addr) && strings.Contains(res.Out, b.addr), p.name+": mash who lists both endpoints", "exit %d", res.Code)
	}
	r.promptBoth(s, a, b, pre+"p1")
	r.promptBoth(s, b, a, pre+"p2")
}

// step5: Bob upgrades the same way, then both are on the candidate.
func (r *runner) step5(s *stepCtx) {
	alice, bob := r.personas["alice"], r.personas["bob"]
	order := r.upgradeOrder(s)
	if !r.upgrade(s, bob, order, alice, "r02", false) {
		return
	}
	r.mixed(s, alice, bob, "step5", "n")
}

// step6: every hook is drained, Alice's own listing of refused mail is read, and each message is judged alone.
func (r *runner) step6(s *stepCtx) {
	alice, bob := r.personas["alice"], r.personas["bob"]
	s.need(r.drain(alice) == nil, "Alice's hook is drained", "")
	s.need(r.drain(bob) == nil, "Bob's hook is drained", "")
	res := r.mash(alice, "inbox", "--json", "--limit", "100")
	r.evid["alice-mash-inbox.json"] = rawOrText(res.Out)
	if s.need(res.ok(), "mash inbox --json for Alice", "exit %d", res.Code) {
		rows, err := rowsFrom(res.Out)
		s.need(err == nil, "read Alice's inbox listing", "%v", err)
		n := 0
		for _, row := range rows {
			if row.Refused {
				r.invOf(alice.addr).Tally.AddRefused(row.Seq, row.Reason)
				n++
			}
		}
		s.note("Alice's own listing marks %d messages refused", n)
	}
	for _, p := range []*persona{alice, bob} {
		iv := r.invOf(p.addr)
		s.need(len(inventory.Check(iv.Entries)) == 0, p.name+"'s inventory is well formed", "%v", inventory.Check(iv.Entries))
		probs := inventory.Compare(iv.Entries, iv.Tally)
		counts := map[inventory.Expect]int{}
		for _, e := range iv.Entries {
			counts[e.Expect]++
		}
		s.note("%s's inventory: %d messages (%d delivered, %d pending, %d refused)", p.name, len(iv.Entries),
			counts[inventory.Delivered], counts[inventory.Pending], counts[inventory.Refused])
		s.ok(len(probs) == 0, p.name+": every message ended exactly as expected, judged one by one", "%d problems", len(probs))
		for _, pr := range probs {
			s.note("%s: %s", p.name, pr)
		}
	}
	r.evid["inventory-alice.json"] = r.invOf(alice.addr).Entries
	r.evid["inventory-bob.json"] = r.invOf(bob.addr).Entries
}

// step7: the admin's listing of Alice's inbox before and after is the same set of messages, refused ones marked.
func (r *runner) step7(s *stepCtx) {
	alice := r.personas["alice"]
	after, raw, err := r.adminInbox(alice.addr)
	r.evid["alice-inbox-after.json"] = rawOrText(raw)
	if !s.need(err == nil, "mashctl lists Alice's inbox after the upgrade", "%v", err) {
		return
	}
	limit := maxSeq(r.before)
	var kept []inventory.Row
	for _, row := range after {
		if row.Seq <= limit {
			kept = append(kept, row)
		}
	}
	probs := inventory.Same(r.before, kept, r.invOf(alice.addr).Entries)
	s.ok(len(probs) == 0, "the same messages are listed before and after, by identity, refused ones marked", "%d problems", len(probs))
	for _, p := range probs {
		s.note("%s", p)
	}
	s.note("%d messages before, %d after (those up to number %d compared; the later ones were stored by this run)", len(r.before), len(kept), limit)
}

var (
	oneWayRE   = regexp.MustCompile(`(?i)one[- ]way|no way back|cannot (be )?(rolled|go|roll) back|does not roll back|forward only|not roll back`)
	runningRE  = regexp.MustCompile(`(?is)(open|running)[^.]{0,80}session[^.]{0,200}(old|same|previous|until)[^.]{0,200}(version|restart)`)
	statusRE   = regexp.MustCompile(`\b400\b[\s\S]*\b401\b[\s\S]*\b403\b`)
	tenRefuseR = regexp.MustCompile(`(?is)(ten|10)[^.]{0,200}refus|refus[^.]{0,200}(ten|10)`)
)

// step8: the upgrade page and the release notes at the candidate.
func (r *runner) step8(s *stepCtx) {
	page, err := os.ReadFile(filepath.Join(r.src, "docs", "compatibility.md"))
	if !s.need(err == nil, "read the upgrade page", "%v", err) {
		return
	}
	steps := pageSteps(string(page))
	s.ok(len(steps) > 0, "the upgrade page has numbered steps", "%d found", len(steps))
	s.ok(runningRE.Match(page), "the upgrade page says an open session keeps its old version until it restarts (REQ-023)", "")
	s.ok(oneWayRE.Match(page), "the upgrade page carries the one-way line (no way back)", "")
	notes := filepath.Join(r.src, "docs", "release-notes", r.cfg.version+".md")
	b, err := os.ReadFile(notes)
	if !s.ok(err == nil, "docs/release-notes/"+r.cfg.version+".md exists at the candidate", "%v", err) {
		return
	}
	first := string(b)
	if len(first) > 1500 {
		first = first[:1500]
	}
	s.ok(tenRefuseR.MatchString(first), "the release notes say first that the ten shapes are now refused", "")
	s.ok(statusRE.Match(b), "the release notes name the status codes that move (400, 401, 403)", "")
}
