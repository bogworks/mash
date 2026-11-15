package policyserve

import (
	"slices"
	"testing"

	"github.com/bogworks/mash/internal/listset"
	"github.com/bogworks/mash/internal/policyapply"
)

// witnessSet is a list set of the group c with every verb closed, and the given senders.
func witnessSet(t *testing.T, senders ...listset.SenderLists) *listset.Compiled {
	t.Helper()
	var sw []listset.SwitchEntry
	for _, v := range []listset.Verb{listset.Mail, listset.Prompt, listset.Status, listset.Heartbeat} {
		sw = append(sw, listset.SwitchEntry{Group: "c", Verb: v, Switch: listset.Closed})
	}
	c, err := listset.Compile(listset.ListSet{Switches: sw, Senders: senders})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// witnessSetWith is witnessSet with the given verbs of c left shared (open) instead of closed.
func witnessSetWith(t *testing.T, shared []listset.Verb, senders ...listset.SenderLists) *listset.Compiled {
	t.Helper()
	var sw []listset.SwitchEntry
	for _, v := range []listset.Verb{listset.Mail, listset.Prompt, listset.Status, listset.Heartbeat} {
		sw_ := listset.Closed
		if slices.Contains(shared, v) {
			sw_ = listset.Shared
		}
		sw = append(sw, listset.SwitchEntry{Group: "c", Verb: v, Switch: sw_})
	}
	c, err := listset.Compile(listset.ListSet{Switches: sw, Senders: senders})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func sendersOf(creds []policyapply.ProbeCred) []string {
	var out []string
	for _, c := range creds {
		out = append(out, c.Sender)
	}
	return out
}

// A witness is opened only for a sender the gate needs a closed one for: a change that only adds an allow leaves every
// connection open (the template does not change), so it opens none; a change that removes a reach or a sender opens one
// for each sender affected and for no other.
func TestWitnessSenders(t *testing.T) {
	lead := listset.SenderLists{Sender: "c.lead"}
	sdk := listset.SenderLists{Sender: "c.sdk"}
	core := func(allow ...string) listset.SenderLists {
		return listset.SenderLists{Sender: "c.core", Allow: allow}
	}
	cases := []struct {
		name         string
		prev, target *listset.Compiled
		want         []string
	}{
		{"first generation", nil, witnessSet(t, core("c.lead"), lead), nil},
		{"unchanged", witnessSet(t, core("c.lead"), lead, sdk), witnessSet(t, core("c.lead"), lead, sdk), nil},
		{"an allow is added", witnessSet(t, core("c.lead"), lead, sdk), witnessSet(t, core("c.lead", "c.sdk"), lead, sdk), nil},
		{"an allow is removed", witnessSet(t, core("c.lead", "c.sdk"), lead, sdk), witnessSet(t, core("c.lead"), lead, sdk), []string{"c.core"}},
		{"a sender is removed", witnessSet(t, core("c.lead"), lead, sdk), witnessSet(t, core("c.lead"), lead), []string{"c.sdk"}},
		{"a sender is added", witnessSet(t, core("c.lead"), lead), witnessSet(t, core("c.lead"), lead, sdk), nil},
		// A closed verb is a group-level change to the template, which closes every connection under the scoped key
		// although Expect names nobody for it: every previous sender needs a witness.
		{"a verb is closed, template changes",
			witnessSetWith(t, []listset.Verb{listset.Mail}, core("c.lead"), lead, sdk),
			witnessSet(t, core("c.lead"), lead, sdk),
			[]string{"c.core", "c.lead", "c.sdk"}},
		{"an allow is added and a verb is shared, template changes",
			witnessSet(t, core("c.lead"), lead, sdk),
			witnessSetWith(t, []listset.Verb{listset.Mail}, core("c.lead", "c.sdk"), lead, sdk),
			[]string{"c.core", "c.lead", "c.sdk"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ex, err := policyapply.NewExpect(c.prev, c.target, nil)
			if err != nil {
				t.Fatal(err)
			}
			got := sendersOf(witnessSenders(c.prev, c.target, ex, settledState{}))
			slices.Sort(got)
			if !slices.Equal(got, c.want) {
				t.Errorf("witnesses for %v, want %v", got, c.want)
			}
		})
	}
}

// With no witness opened, the Applier's witnesses are still a valid transport: nothing is open, so nothing is unclosed.
func TestNoWitnessesIsValid(t *testing.T) {
	w := &policyapply.LiveWitnesses{}
	closed, err := w.Closed(t.Context())
	if err != nil || len(closed) != 0 {
		t.Fatalf("closed %v, err %v", closed, err)
	}
	w.Close()
	if m := w.Minted(); len(m) != 0 {
		t.Fatalf("minted %v", m)
	}
}
