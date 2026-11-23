package policydoc

import (
	"fmt"
	"strings"

	"github.com/bogworks/mash/internal/listset"
)

const describesLine = "This describes the list set in force, not any installed credential."

func list(items []string) string {
	if len(items) == 0 {
		return "(none)"
	}
	return strings.Join(items, ", ")
}

func renderHeader(b *strings.Builder, h StateHeader) {
	if h.InForceGeneration == 0 {
		b.WriteString("in force: none\n")
	} else {
		fmt.Fprintf(b, "in force: generation %d\n", h.InForceGeneration)
	}
	switch l := h.Latest; {
	case l == nil:
		b.WriteString("state: no status written\n")
	case l.Generation != h.InForceGeneration || !l.Applied:
		if l.Generation == 0 {
			fmt.Fprintf(b, "latest attempt: %s\n", l.State)
		} else {
			fmt.Fprintf(b, "latest attempt: generation %d, %s\n", l.Generation, l.State)
		}
	}
	for _, s := range h.Servers {
		fmt.Fprintf(b, "server %s: generation %s\n", s.Server, genWord(s.Generation))
	}
	if h.Notice != "" {
		fmt.Fprintf(b, "notice: %s\n", h.Notice)
	}
}

// RenderShow is the plain, line-oriented text of a ShowResult.
func RenderShow(r ShowResult) string {
	var b strings.Builder
	b.WriteString(describesLine + "\n")
	renderHeader(&b, r.Header)
	if r.View == nil {
		b.WriteString("no list set is in force\n")
		b.WriteString("\n" + listset.RawClientLimit + "\n")
		return b.String()
	}
	v := r.View
	fmt.Fprintf(&b, "generation: %d\nprefix: %s\n\nswitches:\n", v.Generation, v.Prefix)
	for _, s := range v.Switches {
		fmt.Fprintf(&b, "  %s %s %s\n", s.Group, s.Verb, s.Switch)
	}
	b.WriteString("\nsenders:\n")
	for _, s := range v.Senders {
		fmt.Fprintf(&b, "  %s\n    allow: %s\n    deny: %s\n", s.Sender, list(s.Allow), list(s.Deny))
		for _, vr := range s.Reach {
			fmt.Fprintf(&b, "    reaches by %s: %s\n", vr.Verb, list(vr.Receivers))
		}
		fmt.Fprintf(&b, "    open publish: %s\n    open subscribe: %s\n", list(s.OpenSubjects.Publish), list(s.OpenSubjects.Subscribe))
	}
	b.WriteString("\n" + v.RawClientLimit + "\n")
	return b.String()
}

// RenderExplain is the plain, line-oriented text of an ExplainResult.
func RenderExplain(r ExplainResult) string {
	var b strings.Builder
	b.WriteString(describesLine + "\n")
	renderHeader(&b, r.Header)
	if r.Explanation == nil {
		b.WriteString("no list set is in force\n")
		b.WriteString("\n" + listset.RawClientLimit + "\n")
		return b.String()
	}
	e := r.Explanation
	fmt.Fprintf(&b, "sender: %s\nreceiver: %s\n", e.Sender, e.Receiver)
	for _, v := range e.Verbs {
		word := "denied"
		if v.Allowed {
			word = "allowed"
		}
		fmt.Fprintf(&b, "%s: %s (%s) %s\n", v.Verb, word, v.Reason, v.Detail)
	}
	fmt.Fprintf(&b, "open publish: %s\nopen subscribe: %s\n", list(e.OpenSubjects.Publish), list(e.OpenSubjects.Subscribe))
	b.WriteString("\n" + e.RawClientLimit + "\n")
	return b.String()
}

// RenderPlan is the plain, line-oriented text of a Plan.
func RenderPlan(p DiffPlan) string {
	var b strings.Builder
	if p.Refused {
		fmt.Fprintf(&b, "refused: nothing would change\n")
		for _, r := range p.Refusals {
			fmt.Fprintf(&b, "  [%s] %s: %s\n", r.Stage, r.Entry, r.Reason)
		}
		return b.String()
	}
	if p.rotation == "" && !p.completes() && len(p.Receivers) == 0 && len(p.Templates) == 0 && len(p.Tags) == 0 && len(p.RotateAndRevoke) == 0 &&
		len(p.NewCredential) == 0 && len(p.Revoke) == 0 && len(p.FirstCredential) == 0 {
		return "accepted: nothing would change\n" + SizeLimitsLine + "\n" + p.signatureNote()
	}
	b.WriteString("accepted: this would change\n" + SizeLimitsLine + "\n")
	if p.rotation != "" {
		b.WriteString("rotation: " + p.rotation + "\n")
	}
	section := func(name string, items []string) {
		fmt.Fprintf(&b, "%s: %s\n", name, list(items))
	}
	for _, c := range p.Receivers {
		fmt.Fprintf(&b, "receivers %s %s: added %s; removed %s\n", c.Sender, c.Verb, list(c.Added), list(c.Removed))
	}
	for _, t := range p.Templates {
		fmt.Fprintf(&b, "template %s: %s", t.Group, t.Change)
		if t.Disconnects {
			fmt.Fprintf(&b, "; disconnects %s", list(t.DisconnectedSenders))
		}
		b.WriteString("\n")
	}
	for _, t := range p.Tags {
		fmt.Fprintf(&b, "tags %s: %s -> %s\n", t.Sender, list(t.Old), list(t.New))
	}
	section("rotate and revoke", p.RotateAndRevoke)
	section("new credential (not issued by the apply: request it from the issue service, `mashctl issue serve`)", p.NewCredential)
	section("revoke", p.Revoke)
	section("first credential (not issued by the apply: request it from the issue service, `mashctl issue serve`)", p.FirstCredential)
	b.WriteString(p.signatureNote())
	return b.String()
}

// signatureNote is the line that says the document's signature was not checked, when no --submitter was given.
func (p DiffPlan) signatureNote() string {
	var s string
	if p.inboxUnchecked {
		s = InboxNotCheckedLine + "\n"
	}
	if p.adoptionUnchecked {
		s += "adoption not checked: the adoption marker was not read\n"
	}
	if p.unsigned {
		s += "signature not checked: give --submitter to check it against the adoption marker\n"
	}
	return s
}
