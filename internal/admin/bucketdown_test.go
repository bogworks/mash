package admin

import (
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
