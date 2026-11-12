package listset

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

// drySet is the set in force for the dry-run tests: account foo, owner group c, status shared and the rest closed;
// core allows lead and sdk, lead allows core, sdk has no exception.
func drySet() ListSet {
	return ListSet{
		Range:    "foo",
		Switches: switches("foo.c", Closed, Closed, Shared, Closed),
		Senders: []SenderLists{
			{Sender: "foo.c.core", Allow: []string{"foo.c.lead", "foo.c.sdk"}},
			{Sender: "foo.c.lead", Allow: []string{"foo.c.core"}},
			{Sender: "foo.c.sdk"},
		},
	}
}

// at008Next is AT-008's change against drySet: adds one receiver (lead may reach sdk), removes one (core no longer
// reaches sdk), and closes one verb (status).
func at008Next() ListSet {
	return ListSet{
		Range:    "foo",
		Switches: switches("foo.c", Closed, Closed, Closed, Closed),
		Senders: []SenderLists{
			{Sender: "foo.c.core", Allow: []string{"foo.c.lead"}},
			{Sender: "foo.c.lead", Allow: []string{"foo.c.core", "foo.c.sdk"}},
			{Sender: "foo.c.sdk"},
		},
	}
}

func dry(t *testing.T, inForce *ListSet, next ListSet, servers []ServerLimits) Plan {
	t.Helper()
	p := DryRun(inForce, next, servers)
	if p.Refused {
		t.Fatalf("refused: %+v", p.Refusals)
	}
	return p
}

func receiver(p Plan, sender string, v Verb) (ReceiverChange, bool) {
	for _, r := range p.Receivers {
		if r.Sender == sender && r.Verb == v {
			return r, true
		}
	}
	return ReceiverChange{}, false
}

func wantList(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	if want == nil {
		want = []string{}
	}
	if !slices.Equal(got, want) {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

func wantReceivers(t *testing.T, p Plan, sender string, v Verb, added, removed []string) {
	t.Helper()
	r, ok := receiver(p, sender, v)
	if !ok {
		t.Errorf("no receiver change for %s %s, want +%v -%v", sender, v, added, removed)
		return
	}
	wantList(t, sender+" "+string(v)+" added", r.Added, added...)
	wantList(t, sender+" "+string(v)+" removed", r.Removed, removed...)
}

func TestDryRunAddOneReceiver(t *testing.T) {
	base := drySet()
	next := drySet()
	next.Senders[2].Allow = []string{"foo.c.lead"}
	p := dry(t, &base, next, nil)
	if len(p.Receivers) != 3 {
		t.Fatalf("receivers = %+v, want mail, prompt and heartbeat of sdk", p.Receivers)
	}
	for _, v := range []Verb{Mail, Prompt, Heartbeat} {
		wantReceivers(t, p, "foo.c.sdk", v, []string{"foo.c.lead"}, nil)
	}
	// Status is shared: sdk already reached lead through the owner line.
	if _, ok := receiver(p, "foo.c.sdk", Status); ok {
		t.Error("status has no change: the owner line already reached lead")
	}
	if len(p.Templates) != 0 {
		t.Errorf("templates = %+v, want none", p.Templates)
	}
	wantList(t, "new_credential", p.NewCredential, "foo.c.sdk")
	wantList(t, "rotate_and_revoke", p.RotateAndRevoke)
	wantList(t, "revoke", p.Revoke)
	wantList(t, "first_credential", p.FirstCredential)
	if len(p.Tags) != 1 || !slices.Equal(p.Tags[0].Old, []string{}) || !slices.Equal(p.Tags[0].New, []string{"allow:c.lead"}) {
		t.Errorf("tags = %+v", p.Tags)
	}
}

func TestDryRunRemoveOneReceiverRotates(t *testing.T) {
	base := drySet()
	next := drySet()
	next.Senders[0].Allow = []string{"foo.c.lead"}
	p := dry(t, &base, next, nil)
	for _, v := range []Verb{Mail, Prompt, Heartbeat} {
		wantReceivers(t, p, "foo.c.core", v, nil, []string{"foo.c.sdk"})
	}
	wantList(t, "rotate_and_revoke", p.RotateAndRevoke, "foo.c.core")
	wantList(t, "new_credential", p.NewCredential)
	if len(p.Templates) != 0 {
		t.Errorf("templates = %+v, want none", p.Templates)
	}
}

func TestDryRunNarrowByDenyHasSentinelEffects(t *testing.T) {
	base := at003Deny()
	base.Senders[0].Deny = nil
	next := at003Deny()
	p := dry(t, &base, next, nil)

	for _, v := range []Verb{Mail, Prompt, Heartbeat} {
		wantReceivers(t, p, "foo.c.core", v, nil, []string{"foo.c.sdk"})
	}
	wantList(t, "rotate_and_revoke", p.RotateAndRevoke, "foo.c.core")
	// Every other sender of the key gains the sentinel: a new credential, no revoke.
	wantList(t, "new_credential", p.NewCredential, "foo.c.lead", "foo.c.sdk")
	if len(p.Templates) != 1 || p.Templates[0].Group != "foo.c" || p.Templates[0].Change != TemplateChanged {
		t.Fatalf("templates = %+v, want foo.c changed", p.Templates)
	}
	wantList(t, "disconnected", p.Templates[0].DisconnectedSenders, "foo.c.core", "foo.c.lead", "foo.c.sdk")
	if !p.Templates[0].Disconnects {
		t.Error("a changed template disconnects its group")
	}
	var lead TagChange
	for _, tc := range p.Tags {
		if tc.Sender == "foo.c.lead" {
			lead = tc
		}
	}
	wantList(t, "lead new tags", lead.New, NoDenyTag)
	wantList(t, "revoke", p.Revoke)
}

func TestDryRunWidenNeedsOnlyANewCredential(t *testing.T) {
	base := at001()
	next := at001()
	next.Senders[0].Allow = []string{"foo.c.lead", "foo.c.sdk"}
	p := dry(t, &base, next, nil)
	wantList(t, "new_credential", p.NewCredential, "foo.c.core")
	wantList(t, "rotate_and_revoke", p.RotateAndRevoke)
	wantList(t, "revoke", p.Revoke)
	wantReceivers(t, p, "foo.c.core", Mail, []string{"foo.c.sdk"}, nil)
	if len(p.Templates) != 0 {
		t.Errorf("templates = %+v", p.Templates)
	}
}

func TestDryRunCloseAVerbIsADisconnectNotARotation(t *testing.T) {
	base := at001() // status shared
	next := at001()
	next.Switches = switches("foo.c", Closed, Closed, Closed, Closed)
	p := dry(t, &base, next, nil)
	if len(p.Templates) != 1 || p.Templates[0].Change != TemplateChanged || !p.Templates[0].Disconnects {
		t.Fatalf("templates = %+v", p.Templates)
	}
	wantList(t, "disconnected", p.Templates[0].DisconnectedSenders, "foo.c.core", "foo.c.lead", "foo.c.sdk")
	all := []string{"foo.c.core", "foo.c.lead", "foo.c.sdk"}
	// The allow tags still reach (core keeps lead); the owner line is what goes.
	wantReceivers(t, p, "foo.c.core", Status, nil, []string{"foo.c.core", "foo.c.sdk"})
	wantReceivers(t, p, "foo.c.lead", Status, nil, all)
	wantReceivers(t, p, "foo.c.sdk", Status, nil, all)
	if len(p.Tags) != 0 || len(p.RotateAndRevoke) != 0 || len(p.NewCredential) != 0 || len(p.Revoke) != 0 || len(p.FirstCredential) != 0 {
		t.Errorf("a verb flip changes no tags and re-issues nobody: %+v", p)
	}
}

func TestDryRunSenderLeaves(t *testing.T) {
	base := at003Deny()
	next := at003Deny()
	next.Senders = next.Senders[:3] // foo.d.solo leaves, and with it owner group foo.d
	p := dry(t, &base, next, nil)
	wantList(t, "revoke", p.Revoke, "foo.d.solo")
	if len(p.Templates) != 1 || p.Templates[0].Group != "foo.d" || p.Templates[0].Change != TemplateRemoved || p.Templates[0].Disconnects {
		t.Errorf("templates = %+v", p.Templates)
	}
	if len(p.Receivers) != 0 {
		t.Errorf("solo reached nobody, so no receiver changes: %+v", p.Receivers)
	}
}

func TestDryRunNewSender(t *testing.T) {
	base := at001()
	next := at001()
	next.Senders = append(next.Senders, SenderLists{Sender: "foo.c.new", Allow: []string{"foo.c.core"}})
	p := dry(t, &base, next, nil)
	wantList(t, "first_credential", p.FirstCredential, "foo.c.new")
	wantList(t, "revoke", p.Revoke)
	wantReceivers(t, p, "foo.c.new", Mail, []string{"foo.c.core"}, nil)
	wantReceivers(t, p, "foo.c.new", Status, []string{"foo.c.core", "foo.c.lead", "foo.c.new", "foo.c.sdk"}, nil)
	for _, r := range p.Receivers {
		if r.Sender != "foo.c.new" {
			t.Errorf("only the new sender changes, got %+v", r)
		}
	}
	if len(p.Templates) != 0 {
		t.Errorf("templates = %+v", p.Templates)
	}
}

func TestDryRunRefusedCarriesEveryRefusalAndNoChanges(t *testing.T) {
	base := drySet()
	next := at008Next()
	next.Senders[0].Allow = append(next.Senders[0].Allow, "foo.c.*")
	next.Senders[1].Allow = append(next.Senders[1].Allow, "bar.c.lead")
	next.Switches = next.Switches[:3]
	p := DryRun(&base, next, nil)
	if !p.Refused {
		t.Fatal("want refused")
	}
	if len(p.Refusals) != 3 {
		t.Fatalf("refusals = %+v, want 3", p.Refusals)
	}
	for _, r := range p.Refusals {
		if r.Stage != StageValidate {
			t.Errorf("stage = %q, want validate (compile cannot run)", r.Stage)
		}
	}
	if len(p.Receivers)+len(p.Templates)+len(p.Tags)+len(p.RotateAndRevoke)+len(p.NewCredential)+len(p.Revoke)+len(p.FirstCredential) != 0 {
		t.Errorf("a refused plan changes nothing: %+v", p)
	}
}

func TestDryRunNothingInForce(t *testing.T) {
	p := dry(t, nil, at001(), nil)
	wantList(t, "first_credential", p.FirstCredential, "foo.c.core", "foo.c.lead", "foo.c.sdk")
	wantList(t, "revoke", p.Revoke)
	wantList(t, "rotate_and_revoke", p.RotateAndRevoke)
	if len(p.Templates) != 1 || p.Templates[0].Change != TemplateAdded || p.Templates[0].Disconnects {
		t.Errorf("templates = %+v", p.Templates)
	}
	wantReceivers(t, p, "foo.c.core", Mail, []string{"foo.c.lead"}, nil)
	if len(p.Tags) != 1 || p.Tags[0].Sender != "foo.c.core" {
		t.Errorf("tags = %+v", p.Tags)
	}
}

func TestDryRunInForceThatDoesNotValidateIsRefused(t *testing.T) {
	bad := at001()
	bad.Senders[0].Allow = []string{"foo.c.*"}
	p := DryRun(&bad, at001(), nil)
	if !p.Refused || len(p.Refusals) != 1 || p.Refusals[0].Stage != StageInForce {
		t.Errorf("plan = %+v", p)
	}
}

// longSender is a sender with n allow entries at long names.
func longSet(n int) ListSet {
	ls := ListSet{Range: "foo", Switches: switches("foo.ownerwithalongname", Closed, Closed, Closed, Closed)}
	var allow []string
	for i := 0; i < n; i++ {
		allow = append(allow, fmt.Sprintf("foo.ownerwithalongname.receiver-with-a-long-name-%03d", i))
	}
	ls.Senders = []SenderLists{
		{Sender: "foo.ownerwithalongname.big", Allow: allow},
		{Sender: "foo.ownerwithalongname.small"},
	}
	return ls
}

func TestSizeCheckNamesTheServerThatDoesNotFit(t *testing.T) {
	ls := longSet(40)
	c, err := Compile(ls)
	if err != nil {
		t.Fatal(err)
	}
	servers := []ServerLimits{{Server: "alpha"}, {Server: "beta", MaxConnectLine: 2000}}
	ps := SizeCheck(c, servers)
	if len(ps) != 1 {
		t.Fatalf("problems = %v, want one", ps)
	}
	p := ps[0]
	if !strings.Contains(p.Entry, "foo.ownerwithalongname.big") || !strings.Contains(p.Entry, `"beta"`) || strings.Contains(p.Entry, "alpha") {
		t.Errorf("entry = %q", p.Entry)
	}
	if !strings.Contains(p.Reason, "over the 2000 byte limit") || !strings.Contains(p.Reason, "40 tags") {
		t.Errorf("reason = %q", p.Reason)
	}
	// The sender that fits is not named, and nothing was dropped.
	if len(c.Users[0].Tags) != 40 {
		t.Errorf("SizeCheck changed the compiled tags: %d", len(c.Users[0].Tags))
	}

	// DryRun carries the same refusal and no changes.
	plan := DryRun(nil, ls, servers)
	if !plan.Refused || len(plan.Refusals) != 1 || plan.Refusals[0].Stage != StageSize || len(plan.FirstCredential) != 0 {
		t.Errorf("plan = %+v", plan)
	}
	// A server with room for everything and no servers at all accept it.
	if ps := SizeCheck(c, []ServerLimits{{Server: "alpha"}}); len(ps) != 0 {
		t.Errorf("default server refused: %v", ps)
	}
	if ps := SizeCheck(c, nil); len(ps) != 0 {
		t.Errorf("no servers refused: %v", ps)
	}
}

func TestSizeCheckAccountJWT(t *testing.T) {
	c, err := Compile(at001())
	if err != nil {
		t.Fatal(err)
	}
	ps := SizeCheck(c, []ServerLimits{{Server: "tiny", MaxAccountJWT: 100}})
	if len(ps) != 1 || !strings.HasPrefix(ps[0].Entry, `account "foo"`) || !strings.Contains(ps[0].Reason, "over the 100 byte limit") {
		t.Errorf("problems = %v", ps)
	}
}

func TestDryRunDoesNotMutateItsInputs(t *testing.T) {
	base, next := drySet(), at008Next()
	servers := []ServerLimits{{Server: "a"}, {Server: "b", MaxConnectLine: 3000}}
	baseCopy, nextCopy := drySet(), at008Next()
	serversCopy := slices.Clone(servers)
	DryRun(&base, next, servers)
	if !reflect.DeepEqual(base, baseCopy) || !reflect.DeepEqual(next, nextCopy) || !reflect.DeepEqual(servers, serversCopy) {
		t.Error("DryRun modified an argument")
	}
}

// reorder returns ls with every slice reversed.
func reorder(ls ListSet) ListSet {
	out := ListSet{Range: ls.Range}
	out.Switches = slices.Clone(ls.Switches)
	slices.Reverse(out.Switches)
	for _, s := range ls.Senders {
		s.Allow, s.Deny = slices.Clone(s.Allow), slices.Clone(s.Deny)
		slices.Reverse(s.Allow)
		slices.Reverse(s.Deny)
		out.Senders = append(out.Senders, s)
	}
	slices.Reverse(out.Senders)
	return out
}

func TestDryRunJSONIsDeterministic(t *testing.T) {
	base, next := drySet(), at008Next()
	servers := []ServerLimits{{Server: "a"}, {Server: "b"}}
	marshal := func(p Plan) []byte {
		b, err := json.MarshalIndent(p, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	first := marshal(DryRun(&base, next, servers))
	if !bytes.Equal(first, marshal(DryRun(&base, next, servers))) {
		t.Error("two runs differ")
	}
	rb, rn := reorder(base), reorder(next)
	if !bytes.Equal(first, marshal(DryRun(&rb, rn, []ServerLimits{servers[1], servers[0]}))) {
		t.Error("input order changes the plan")
	}

	// A refused plan too: refusals are sorted, not in input order.
	bad := at008Next()
	bad.Senders[0].Allow = append(bad.Senders[0].Allow, "foo.c.*", "bar.c.x")
	rbad := reorder(bad)
	if !bytes.Equal(marshal(DryRun(&base, bad, nil)), marshal(DryRun(&rb, rbad, nil))) {
		t.Error("input order changes the refusals")
	}
}

func TestDryRunAT008Golden(t *testing.T) {
	base, next := drySet(), at008Next()
	got, err := json.MarshalIndent(DryRun(&base, next, nil), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join("testdata", "dryrun_at008.golden.json")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test -run Golden -update)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("plan differs from %s (run with -update to rewrite):\n%s", path, got)
	}
}

func TestAT008Content(t *testing.T) {
	base, next := drySet(), at008Next()
	p := dry(t, &base, next, nil)
	wantReceivers(t, p, "foo.c.lead", Mail, []string{"foo.c.sdk"}, nil)
	wantReceivers(t, p, "foo.c.core", Mail, nil, []string{"foo.c.sdk"})
	all := []string{"foo.c.core", "foo.c.lead", "foo.c.sdk"}
	wantReceivers(t, p, "foo.c.sdk", Status, nil, all)
	wantList(t, "rotate_and_revoke", p.RotateAndRevoke, "foo.c.core")
	wantList(t, "new_credential", p.NewCredential, "foo.c.lead")
	if len(p.Templates) != 1 || !p.Templates[0].Disconnects {
		t.Fatalf("templates = %+v", p.Templates)
	}
	wantList(t, "disconnected", p.Templates[0].DisconnectedSenders, all...)
}
