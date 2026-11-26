package endpoint

import (
	"context"
	"path/filepath"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/render"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/sapsdk"
)

// The views of refused mail (REQ-006 items 4 and 6). They read the two lists, not the stream, and every count is the
// number of list entries the stream still holds: a message that aged out of the stream is no longer counted. A
// sequence on both lists is one refusal, malformed, with its class. A refused message has no body, no sender and no
// preview in any view: its number, its time, and its class.

// TrustRefused is the trust value of a refused entry in the views (TO-2): the class is the reason.
const TrustRefused sap.Trust = "refused"

// ClassNotVerified is the class of a refusal because the sender could not be proved (the unverified list).
const ClassNotVerified = sapsdk.ClassNotVerified

// RefusedCounts is how many refused messages the stream still holds, in all and for each class.
type RefusedCounts struct {
	Total   int
	ByClass map[string]int
}

func countRefused(classes map[uint64]string) RefusedCounts {
	rc := RefusedCounts{Total: len(classes), ByClass: map[string]int{}}
	for _, c := range classes {
		rc.ByClass[c]++
	}
	return rc
}

// refusedClasses is the class of each refused message the stream, identified by stream and holding sequences first to
// last, may still hold. The malformed list wins for a sequence on both lists.
func (e *Endpoint) refusedClasses(stream string, first, last uint64) map[uint64]string {
	out := map[uint64]string{}
	refusedMu.Lock()
	malformed, unverified := e.refusedLists(stream)
	refusedMu.Unlock()
	addRefused(out, malformed, unverified, first, last)
	return out
}

func addRefused(out map[uint64]string, malformed []seqEntry, unverified []uint64, first, last uint64) {
	in := func(s uint64) bool { return s >= first && s <= last }
	for _, en := range malformed {
		if in(en.Seq) {
			class := en.Class
			if class == "" {
				class = sapsdk.ClassUnreadable
			}
			out[en.Seq] = class
		}
	}
	for _, s := range unverified {
		if _, ok := out[s]; !ok && in(s) {
			out[s] = ClassNotVerified
		}
	}
}

// RefusedCounts reads the inbox stream's range and counts the refused entries it still holds.
func (e *Endpoint) RefusedCounts(ctx context.Context) (RefusedCounts, error) {
	s, err := e.inbox(ctx)
	if err != nil {
		return RefusedCounts{}, err
	}
	info := s.CachedInfo()
	return countRefused(e.refusedClasses(streamIdentity(info), info.State.FirstSeq, info.State.LastSeq)), nil
}

// RefusedCountsOnDisk counts the entries of both lists with no stream to ask: for a check that cannot reach the server.
// An entry the stream has since dropped is still counted until the stream can be read.
func RefusedCountsOnDisk(cfg config.Config) RefusedCounts {
	dir, err := SpoolDir(cfg)
	if err != nil {
		return countRefused(nil)
	}
	mal := readSeqList(filepath.Join(dir, malformedSeqsFile))
	unv := readSeqList(filepath.Join(dir, refusedSeqsFile))
	id := mal.ID
	if id == "" {
		id = unv.ID
	}
	malformed, unverified := mergeRefusedLists(id, mal, unv)
	out := map[uint64]string{}
	addRefused(out, malformed, unverified, 0, ^uint64(0))
	return countRefused(out)
}

// refusedMail is the entry a view lists for a refused message: no sender, no body, no marks.
func refusedMail(raw *jetstream.RawStreamMsg, class string) render.Mail {
	return render.Mail{Seq: raw.Sequence, From: "?", Trust: TrustRefused, At: raw.Time, Refused: class}
}

// refusedClassOf is the class of seq when it is a refused message the stream still holds.
func (e *Endpoint) refusedClassOf(info *jetstream.StreamInfo, seq uint64) (string, bool) {
	if seq < info.State.FirstSeq || seq > info.State.LastSeq {
		return "", false
	}
	class, ok := e.refusedClasses(streamIdentity(info), seq, seq)[seq]
	return class, ok
}
