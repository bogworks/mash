package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/nats-io/nats.go/jetstream"
)

const quorumHint = "JetStream is unavailable (a server is down, or too few servers are up to keep quorum)"

func quorumErr() error {
	return fmt.Errorf("read the directory: %w", &jetstream.APIError{Code: 503, ErrorCode: 10008, Description: "JetStream system temporarily unavailable"})
}

func TestAuditFailedRewordsUnavailable(t *testing.T) {
	var buf bytes.Buffer
	if rc := auditFailed(&buf, quorumErr()); rc != 1 {
		t.Fatalf("exit %d, want 3", rc)
	}
	if !strings.Contains(buf.String(), quorumHint) || !strings.Contains(buf.String(), "temporarily unavailable") {
		t.Fatalf("no hint or raw text: %q", buf.String())
	}
	buf.Reset()
	auditFailed(&buf, fmt.Errorf("other"))
	if buf.String() != "other\n" {
		t.Fatalf("other error changed: %q", buf.String())
	}
}

func TestIssuedFailedRewordsUnavailable(t *testing.T) {
	var buf bytes.Buffer
	if rc := issuedFailed(&buf, "list", quorumErr()); rc != 2 {
		t.Fatalf("exit %d, want 2", rc)
	}
	if !strings.Contains(buf.String(), quorumHint) || !strings.HasPrefix(buf.String(), "mashctl: issued list: ") {
		t.Fatalf("bad output: %q", buf.String())
	}
}

const offlineRaw = "issue record bucket: nats: API error: code=500 err_code=10118 description=stream is offline"

func TestAuditFailedRewordsStreamOffline(t *testing.T) {
	var buf bytes.Buffer
	if rc := auditFailed(&buf, fmt.Errorf("%s", offlineRaw)); rc != 1 {
		t.Fatalf("exit %d, want 3", rc)
	}
	if !strings.Contains(buf.String(), "the server holding a stream or bucket is down: bring it back and run the command again") || !strings.Contains(buf.String(), "stream is offline") {
		t.Fatalf("no hint or raw text: %q", buf.String())
	}
}

func TestIssuedFailedRewordsStreamOffline(t *testing.T) {
	var buf bytes.Buffer
	if rc := issuedFailed(&buf, "check", fmt.Errorf("%s", offlineRaw)); rc != 2 {
		t.Fatalf("exit %d, want 2", rc)
	}
	if !strings.Contains(buf.String(), "is down: bring it back and run the command again") || !strings.HasPrefix(buf.String(), "mashctl: issued check: ") {
		t.Fatalf("bad output: %q", buf.String())
	}
}
