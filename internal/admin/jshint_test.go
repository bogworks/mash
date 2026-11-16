package admin

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/nats-io/nats.go/jetstream"
)

func TestJetStreamNotEnabledHint(t *testing.T) {
	if JetStreamNotEnabledHint(nil, 1<<31) != nil {
		t.Error("nil must stay nil")
	}
	other := errors.New("boom")
	if got := JetStreamNotEnabledHint(other, 1<<31); got != other {
		t.Errorf("another error must be returned untouched, got %v", got)
	}
	nf := fmt.Errorf("mash: bucket x: %w", jetstream.ErrStreamNotFound)
	if got := JetStreamNotEnabledHint(nf, 0); got != nf {
		t.Errorf("stream not found must be untouched, got %v", got)
	}
	for _, base := range []error{jetstream.ErrJetStreamNotEnabled, jetstream.ErrJetStreamNotEnabledForAccount} {
		orig := fmt.Errorf("mash: bucket x: %w", base)
		got := JetStreamNotEnabledHint(orig, 2<<30)
		if !errors.Is(got, base) || !errors.Is(got, orig) || errors.Unwrap(got) != orig {
			t.Errorf("original not kept: %v", got)
		}
		want := "the server could not enable JetStream for the account: it could not reserve the account's disk limit (2 GiB). nats-server gives JetStream 75% of the free disk by default, so the free disk under the server's store directory is probably under about 2.7 GiB; free some disk, or lower the account's limit (for example nsc edit account NAME --js-disk-storage 1G, then push)"
		if !strings.HasPrefix(got.Error(), want) {
			t.Errorf("text:\n%s", got)
		}
	}
	if s := JetStreamNotEnabledHint(jetstream.ErrJetStreamNotEnabled, 0).Error(); strings.Contains(s, "GiB") || !strings.Contains(s, "could not enable JetStream") {
		t.Errorf("unknown limit: %s", s)
	}
}
