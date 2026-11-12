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
	if h.State == "" {
		b.WriteString("state: no status written\n")
	} else {
		fmt.Fprintf(b, "state: %s\nin force: %s\nstatus generation: %d\n", h.State, yesNo(h.InForce), h.StatusGeneration)
	}
	for _, s := range h.Servers {
		fmt.Fprintf(b, "server %s: generation %s\n", s.Server, genWord(s.Generation))
	}
	if h.Notice != "" {
		fmt.Fprintf(b, "notice: %s\n", h.Notice)
	}
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
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
	fmt.Fprintf(&b, "generation: %d\nrange: %s\n\nswitches:\n", v.Generation, v.Range)
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
func RenderPlan(p listset.Plan) string {
	var b strings.Builder
	if p.Refused {
		fmt.Fprintf(&b, "refused: nothing would change\n")
		for _, r := range p.Refusals {
			fmt.Fprintf(&b, "  [%s] %s: %s\n", r.Stage, r.Entry, r.Reason)
		}
		return b.String()
	}
	b.WriteString("accepted: this would change\n")
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
	section("new credential", p.NewCredential)
	section("revoke", p.Revoke)
	section("first credential", p.FirstCredential)
	return b.String()
}
