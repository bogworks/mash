package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/admin"
)

const quorumHint = "JetStream is unavailable (a server is down, or too few servers are up to keep quorum)"

func quorumErr() error {
	return fmt.Errorf("read the directory: %w", &jetstream.APIError{Code: 503, ErrorCode: 10008, Description: "JetStream system temporarily unavailable"})
}

const offlineRaw = "issue record bucket: nats: API error: code=500 err_code=10118 description=stream is offline"

// The detail doctor prints for a server that cannot serve JetStream says so, and keeps the server's own words.
func TestDoctorRewordsUnavailable(t *testing.T) {
	got := oneLine(admin.RewordUnavailable(quorumErr().Error()))
	if !strings.Contains(got, quorumHint) || !strings.Contains(got, "temporarily unavailable") || strings.Contains(got, "\n") {
		t.Fatalf("no hint or raw text: %q", got)
	}
	if got := oneLine(admin.RewordUnavailable("other")); got != "other" {
		t.Fatalf("other error changed: %q", got)
	}
	got = oneLine(admin.RewordUnavailable(offlineRaw))
	if !strings.Contains(got, "the server holding a stream or bucket is down: bring it back and run the command again") || !strings.Contains(got, "stream is offline") {
		t.Fatalf("no hint or raw text: %q", got)
	}
}
