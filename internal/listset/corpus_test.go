package listset

// The AT-004 corpus: list sets as Go values (there is no file format, DEC-004), each with the exact reach the lists
// give every sender, written out by hand and never derived from the code under test.

type reachWant map[string]map[Verb][]string

type corpusCase struct {
	name string
	ls   ListSet
	want reachWant
	// denyKeys are the owner groups whose template must have tag-driven deny lines.
	denyKeys []string
}

func switches(group string, mail, prompt, status, hb Switch) []SwitchEntry {
	return []SwitchEntry{
		{group, Mail, mail}, {group, Prompt, prompt}, {group, Status, status}, {group, Heartbeat, hb},
	}
}

func concat(parts ...[]SwitchEntry) []SwitchEntry {
	var out []SwitchEntry
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func all4(addrs ...string) map[Verb][]string {
	return map[Verb][]string{Mail: addrs, Prompt: addrs, Status: addrs, Heartbeat: addrs}
}

// at001 is the AT-001 shape: account foo, c.core, c.lead, c.sdk; mail, prompt and heartbeat closed, status
// shared; the only exception is that core allows lead.
func at001() ListSet {
	return ListSet{
		Switches: switches("c", Closed, Closed, Shared, Closed),
		Senders: []SenderLists{
			{Sender: "c.core", Allow: []string{"c.lead"}},
			{Sender: "c.lead"},
			{Sender: "c.sdk"},
		},
	}
}

// at003Deny is a deny key: mail, prompt and heartbeat shared, status closed, core denies sdk; another owner group
// beside it has every verb closed and no deny.
func at003Deny() ListSet {
	return ListSet{
		Switches: concat(
			switches("c", Shared, Shared, Closed, Shared),
			switches("d", Closed, Closed, Closed, Closed)),
		Senders: []SenderLists{
			{Sender: "c.core", Deny: []string{"c.sdk"}},
			{Sender: "c.lead"},
			{Sender: "c.sdk"},
			{Sender: "d.solo"},
		},
	}
}

func corpus() []corpusCase {
	return []corpusCase{
		{
			name: "AT-001 shape",
			ls:   at001(),
			want: reachWant{
				"c.core": {Mail: {"c.lead"}, Prompt: {"c.lead"}, Status: {"c.core", "c.lead", "c.sdk"}, Heartbeat: {"c.lead"}},
				"c.lead": {Status: {"c.core", "c.lead", "c.sdk"}},
				"c.sdk":  {Status: {"c.core", "c.lead", "c.sdk"}},
			},
		},
		{
			name: "a sender allowed nobody, zero tags, every verb closed",
			ls: ListSet{
				Switches: switches("c", Closed, Closed, Closed, Closed),
				Senders:  []SenderLists{{Sender: "c.core"}, {Sender: "c.lead"}},
			},
			want: reachWant{"c.core": {}, "c.lead": {}},
		},
		{
			name: "a key with deny exceptions: the sentinel on the other users, none on a key without one",
			ls:   at003Deny(),
			want: reachWant{
				"c.core": {Mail: {"c.core", "c.lead"}, Prompt: {"c.core", "c.lead"}, Heartbeat: {"c.core", "c.lead"}},
				"c.lead": {Mail: {"c.core", "c.lead", "c.sdk"}, Prompt: {"c.core", "c.lead", "c.sdk"}, Heartbeat: {"c.core", "c.lead", "c.sdk"}},
				"c.sdk":  {Mail: {"c.core", "c.lead", "c.sdk"}, Prompt: {"c.core", "c.lead", "c.sdk"}, Heartbeat: {"c.core", "c.lead", "c.sdk"}},
				"d.solo": {},
			},
			denyKeys: []string{"c"},
		},
		{
			name: "cross-group allows go one way; an allow of a shared verb's own group is harmless",
			ls: ListSet{
				Switches: concat(
					switches("a", Closed, Closed, Closed, Closed),
					switches("b", Shared, Shared, Shared, Shared)),
				Senders: []SenderLists{
					{Sender: "a.lead", Allow: []string{"b.cve"}},
					{Sender: "b.cve", Allow: []string{"b.other"}},
					{Sender: "b.other"},
				},
			},
			want: reachWant{
				"a.lead":  all4("b.cve"),
				"b.cve":   all4("b.cve", "b.other"),
				"b.other": all4("b.cve", "b.other"),
			},
		},
		{
			name: "an exception covers all four verbs with one tag, whatever the group's mix",
			ls: ListSet{
				Switches: switches("c", Shared, Closed, Shared, Closed),
				Senders: []SenderLists{
					{Sender: "c.core", Allow: []string{"c.sdk", "x.out"}},
					{Sender: "c.sdk"},
				},
			},
			want: reachWant{
				"c.core": {Mail: {"c.core", "c.sdk", "x.out"}, Prompt: {"c.sdk", "x.out"}, Status: {"c.core", "c.sdk", "x.out"}, Heartbeat: {"c.sdk", "x.out"}},
				"c.sdk":  {Mail: {"c.core", "c.sdk"}, Status: {"c.core", "c.sdk"}},
			},
		},
	}
}
