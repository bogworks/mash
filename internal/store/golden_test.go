// Characterisation tests: these pin the bucket, consumer and endpoint identifiers; a change here is a change to an identifier and needs its own decision.

package store

import (
	"testing"

	"github.com/bogworks/mash/internal/sap"
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

func TestGoldenAddressIdentifiers(t *testing.T) {
	for _, c := range []struct{ owner, group, instance, mail, flat string }{
		{"o", "g", "a", "agents.mail.o.g.a", "o_g_a"},
		{"acme-corp", "build", "build-1", "agents.mail.acme-corp.build.build-1", "acme-corp_build_build-1"},
	} {
		a, err := sap.NewAddress(c.owner, c.group, c.instance)
		if err != nil {
			t.Fatal(err)
		}
		if got := MailSubjectFor(a); got != c.mail {
			t.Errorf("MailSubjectFor(%v) = %q, want %q", a, got, c.mail)
		}
		if got := SuspendedSubject(a.Flat()); got != "mash.suspended."+c.flat {
			t.Errorf("SuspendedSubject = %q", got)
		}
		cfg := InboxConfig(a)
		if cfg.Name != "inbox-"+c.flat || len(cfg.Subjects) != 1 || cfg.Subjects[0] != c.mail || cfg.Description != "mash inbox for "+c.flat {
			t.Errorf("inbox config: %q %q %q", cfg.Name, cfg.Subjects, cfg.Description)
		}
	}
	if MetaSuspendedSubjects != "mash_suspended_subjects" || MetaSuspendedAt != "mash_suspended_at" ||
		MetaSuspendedID != "mash_suspended_id" || MetaSuspendedUser != "mash_suspended_user" {
		t.Error("a suspension metadata key changed")
	}
}
