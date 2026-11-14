// Characterisation tests (plan 2b, Phase 0): these pin today's identifiers and validity for legacy identities; a change here is a change to a legacy identifier and needs its own decision.

package store

import "testing"

const (
	goldenN48 = "abcdefghijklmnopqrstuvwxyz0123456789abcdefghijkl"
)

func TestGoldenBucketAndConsumerNames(t *testing.T) {
	if DirectoryBucket != "mash-directory" || LeaseBucket != "mash-leases" || Consumer != "deliver" {
		t.Errorf("constants: %q %q %q", DirectoryBucket, LeaseBucket, Consumer)
	}
	if DirectoryConfig().Bucket != "mash-directory" || LeaseConfig().Bucket != "mash-leases" {
		t.Error("bucket config names changed")
	}
	if ConsumerConfig().Durable != "deliver" {
		t.Errorf("durable %q", ConsumerConfig().Durable)
	}
}

func TestGoldenMailSubjectFor(t *testing.T) {
	for _, c := range []struct{ owner, name, want string }{
		{"o", "a", "agents.mail.cc.o.a"},
		{"acme-corp", "build-1", "agents.mail.cc.acme-corp.build-1"},
		{goldenN48, goldenN48, "agents.mail.cc." + goldenN48 + "." + goldenN48},
	} {
		if got := MailSubjectFor(c.owner, c.name); got != c.want {
			t.Errorf("MailSubjectFor(%q,%q) = %q, want %q", c.owner, c.name, got, c.want)
		}
	}
}

func TestGoldenSuspendedSubject(t *testing.T) {
	for _, c := range []struct{ name, want string }{
		{"a", "mash.suspended.a"},
		{"build-1", "mash.suspended.build-1"},
		{goldenN48, "mash.suspended." + goldenN48},
	} {
		if got := SuspendedSubject(c.name); got != c.want {
			t.Errorf("SuspendedSubject(%q) = %q", c.name, got)
		}
	}
	if MetaSuspendedSubjects != "mash_suspended_subjects" || MetaSuspendedAt != "mash_suspended_at" ||
		MetaSuspendedID != "mash_suspended_id" || MetaSuspendedUser != "mash_suspended_user" {
		t.Error("a suspension metadata key changed")
	}
}

func TestGoldenInboxStreamConfig(t *testing.T) {
	c := InboxConfig("o", "a")
	if c.Name != "inbox-a" || len(c.Subjects) != 1 || c.Subjects[0] != "agents.mail.*.o.a" || c.Description != "mash inbox for a" {
		t.Errorf("legacy inbox config: %q %q %q", c.Name, c.Subjects, c.Description)
	}
}

// The directory and lease buckets are keyed by the bare endpoint name: ParseRecord requires the key to equal the
// record's name, and its mail subject to be the one MailSubjectFor derives.
func TestGoldenDirectoryKeyIsName(t *testing.T) {
	const rec = `{"name":"a","owner":"o","agent":"claude-code","agent_token":"cc","account":"A","user":"U","mail_subject":"agents.mail.cc.o.a","created":"2026-01-01T00:00:00Z"}`
	if r, err := ParseRecord("a", []byte(rec)); err != nil || r.Name != "a" {
		t.Fatalf("legacy record under key a: %v", err)
	}
	if _, err := ParseRecord("o.a", []byte(rec)); err == nil {
		t.Error("record accepted under a key that is not its name")
	}
	const zrec = `{"name":"a","owner":"o","agent":"claude-code","agent_token":"cc","account":"A","user":"U","mail_subject":"agents.mail.cc.o.z.a","created":"2026-01-01T00:00:00Z","zone":"z"}`
	if r, err := ParseRecord("a", []byte(zrec)); err != nil || r.Zone != "z" {
		t.Fatalf("zoned record under key a: %v", err)
	}
	const bad = `{"name":"a","owner":"o","user":"U","mail_subject":"agents.mail.cc.o.z.a","zone":"z "}`
	if _, err := ParseRecord("a", []byte(bad)); err == nil {
		t.Error("record with an invalid zone accepted")
	}
}
