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

// at001 is the AT-001 shape: account foo, foo.c.core, foo.c.lead, foo.c.sdk; mail, prompt and heartbeat closed, status
// shared; the only exception is that core allows lead.
func at001() ListSet {
	return ListSet{
		Range:    "foo",
		Switches: switches("foo.c", Closed, Closed, Shared, Closed),
		Senders: []SenderLists{
			{Sender: "foo.c.core", Allow: []string{"foo.c.lead"}},
			{Sender: "foo.c.lead"},
			{Sender: "foo.c.sdk"},
		},
	}
}

// at003Deny is a deny key: mail, prompt and heartbeat shared, status closed, core denies sdk; another owner group
// beside it has every verb closed and no deny.
func at003Deny() ListSet {
	return ListSet{
		Range: "foo",
		Switches: concat(
			switches("foo.c", Shared, Shared, Closed, Shared),
			switches("foo.d", Closed, Closed, Closed, Closed)),
		Senders: []SenderLists{
			{Sender: "foo.c.core", Deny: []string{"foo.c.sdk"}},
			{Sender: "foo.c.lead"},
			{Sender: "foo.c.sdk"},
			{Sender: "foo.d.solo"},
		},
	}
}

func corpus() []corpusCase {
	return []corpusCase{
		{
			name: "AT-001 shape",
			ls:   at001(),
			want: reachWant{
				"foo.c.core": {Mail: {"foo.c.lead"}, Prompt: {"foo.c.lead"}, Status: {"foo.c.core", "foo.c.lead", "foo.c.sdk"}, Heartbeat: {"foo.c.lead"}},
				"foo.c.lead": {Status: {"foo.c.core", "foo.c.lead", "foo.c.sdk"}},
				"foo.c.sdk":  {Status: {"foo.c.core", "foo.c.lead", "foo.c.sdk"}},
			},
		},
		{
			name: "a sender allowed nobody, zero tags, every verb closed",
			ls: ListSet{
				Range:    "foo",
				Switches: switches("foo.c", Closed, Closed, Closed, Closed),
				Senders:  []SenderLists{{Sender: "foo.c.core"}, {Sender: "foo.c.lead"}},
			},
			want: reachWant{"foo.c.core": {}, "foo.c.lead": {}},
		},
		{
			name: "a key with deny exceptions: the sentinel on the other users, none on a key without one",
			ls:   at003Deny(),
			want: reachWant{
				"foo.c.core": {Mail: {"foo.c.core", "foo.c.lead"}, Prompt: {"foo.c.core", "foo.c.lead"}, Heartbeat: {"foo.c.core", "foo.c.lead"}},
				"foo.c.lead": {Mail: {"foo.c.core", "foo.c.lead", "foo.c.sdk"}, Prompt: {"foo.c.core", "foo.c.lead", "foo.c.sdk"}, Heartbeat: {"foo.c.core", "foo.c.lead", "foo.c.sdk"}},
				"foo.c.sdk":  {Mail: {"foo.c.core", "foo.c.lead", "foo.c.sdk"}, Prompt: {"foo.c.core", "foo.c.lead", "foo.c.sdk"}, Heartbeat: {"foo.c.core", "foo.c.lead", "foo.c.sdk"}},
				"foo.d.solo": {},
			},
			denyKeys: []string{"foo.c"},
		},
		{
			name: "cross-group allows go one way; an allow of a shared verb's own group is harmless",
			ls: ListSet{
				Range: "foo",
				Switches: concat(
					switches("foo.a", Closed, Closed, Closed, Closed),
					switches("foo.b", Shared, Shared, Shared, Shared)),
				Senders: []SenderLists{
					{Sender: "foo.a.lead", Allow: []string{"foo.b.cve"}},
					{Sender: "foo.b.cve", Allow: []string{"foo.b.other"}},
					{Sender: "foo.b.other"},
				},
			},
			want: reachWant{
				"foo.a.lead":  all4("foo.b.cve"),
				"foo.b.cve":   all4("foo.b.cve", "foo.b.other"),
				"foo.b.other": all4("foo.b.cve", "foo.b.other"),
			},
		},
		{
			name: "an exception covers all four verbs with one tag, whatever the group's mix",
			ls: ListSet{
				Range:    "foo",
				Switches: switches("foo.c", Shared, Closed, Shared, Closed),
				Senders: []SenderLists{
					{Sender: "foo.c.core", Allow: []string{"foo.c.sdk", "foo.x.out"}},
					{Sender: "foo.c.sdk"},
				},
			},
			want: reachWant{
				"foo.c.core": {Mail: {"foo.c.core", "foo.c.sdk", "foo.x.out"}, Prompt: {"foo.c.sdk", "foo.x.out"}, Status: {"foo.c.core", "foo.c.sdk", "foo.x.out"}, Heartbeat: {"foo.c.sdk", "foo.x.out"}},
				"foo.c.sdk":  {Mail: {"foo.c.core", "foo.c.sdk"}, Status: {"foo.c.core", "foo.c.sdk"}},
			},
		},
	}
}
