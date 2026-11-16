package admin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/nats-io/nats.go/jetstream"
)

func TestBucketDownNamesTheBucketAndNeverSuggestsInit(t *testing.T) {
	offline := &jetstream.APIError{Code: 500, ErrorCode: 10118, Description: "stream is offline"}
	for _, err := range []error{offline, fmt.Errorf("wrapped: %w", offline), errors.New("nats: stream is offline")} {
		got := bucketDown("KV_lease", err)
		if got == nil {
			t.Fatalf("%v: not recognised as offline", err)
		}
		s := got.Error()
		if !strings.Contains(s, "the server holding bucket KV_lease is down: bring it back and run the command again") {
			t.Errorf("text %q", s)
		}
		if strings.Contains(s, "admin init") {
			t.Errorf("suggests init: %q", s)
		}
	}
}

func TestBucketDownIgnoresOtherErrors(t *testing.T) {
	for _, err := range []error{nil, jetstream.ErrBucketNotFound, errors.New("timeout")} {
		if got := bucketDown("b", err); got != nil {
			t.Errorf("%v: got %v", err, got)
		}
	}
}

func TestBucketDownWordsLostQuorumAndNeverSuggestsInit(t *testing.T) {
	unavail := &jetstream.APIError{Code: 503, ErrorCode: 10008, Description: "JetStream system temporarily unavailable"}
	for _, err := range []error{unavail, fmt.Errorf("wrapped: %w", unavail), errors.New("nats: JetStream system temporarily unavailable")} {
		got := bucketDown("KV_lease", err)
		if got == nil {
			t.Fatalf("%v: not recognised", err)
		}
		s := got.Error()
		if !strings.Contains(s, "mash: JetStream is unavailable (a server is down, or too few servers are up to keep quorum): bring the servers back and run the command again: ") {
			t.Errorf("text %q", s)
		}
		if strings.Contains(s, "admin init") || !errors.Is(got, err) {
			t.Errorf("bad text or lost cause: %q", s)
		}
	}
}

func TestRewordUnavailable(t *testing.T) {
	raw := "mashctl: add x: nats: JetStream system temporarily unavailable"
	got := RewordUnavailable(raw)
	if !strings.Contains(got, "mash: JetStream is unavailable (a server is down") || !strings.Contains(got, "temporarily unavailable") {
		t.Errorf("got %q", got)
	}
	if RewordUnavailable(got) != got || RewordUnavailable("other") != "other" {
		t.Errorf("not idempotent or touched an unrelated text")
	}
}

func TestBucketDownDeadlineIsNotAnInitHint(t *testing.T) {
	err := bucketDown("mash-leases", fmt.Errorf("open: %w", context.DeadlineExceeded))
	if err == nil || strings.Contains(err.Error(), "admin init") || !strings.Contains(err.Error(), "mash-leases") {
		t.Fatalf("got %v", err)
	}
}

func TestRewordUnavailableStreamOffline(t *testing.T) {
	raw := "mash: stream inbox-a: nats: API error: code=500 err_code=10118 description=stream is offline"
	got := RewordUnavailable(raw)
	want := "mash: the server holding stream inbox-a is down: bring it back and run the command again: " + raw
	if got != want {
		t.Errorf("named: got %q", got)
	}
	nameless := "issue record bucket: nats: API error: code=500 err_code=10118 description=stream is offline"
	got = RewordUnavailable(nameless)
	if got != "mash: the server holding a stream or bucket is down: bring it back and run the command again: "+nameless {
		t.Errorf("nameless: got %q", got)
	}
	if RewordUnavailable(got) != got {
		t.Errorf("not idempotent")
	}
	// the text bucketDown already wrote is left as it is
	added := "mashctl: add c: mash: the server holding bucket mash-issued is down: bring it back and run the command again: nats: API error: code=500 err_code=10118 description=stream is offline; nothing was issued"
	if RewordUnavailable(added) != added {
		t.Errorf("reworded twice: %q", RewordUnavailable(added))
	}
	if got := RewordUnavailable("x: err_code=10118 y"); !strings.Contains(got, "is down: bring it back") {
		t.Errorf("code only: %q", got)
	}
}
