package endpoint

import (
	"context"
	"path/filepath"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ledger"
	"github.com/bogworks/mash/internal/sap"
	"github.com/bogworks/mash/internal/spool"
)

// The presented ledger (ADR 0109): serve appends what it has handed to the host to a local file before it acks, so a
// later status can say what was presented and what is missing. It is informational. A failed write is logged once and
// reported in status; the ack goes ahead.

// ledgerInfoTimeout bounds the stream-info request that names a generation the spool did not bind.
const ledgerInfoTimeout = 2 * time.Second

// ledgerState is the ledger an endpoint writes, the generations of its streams, and the last write failure.
type ledgerState struct {
	mu     sync.Mutex
	l      *ledger.Ledger
	gens   map[string]string // ref-stream ("" or LegacyStream) -> generation identity
	errMsg string            // the last write's failure, "" once a write works
}

// LedgerPath is the ledger file of the endpoint cfg configures, in its spool directory (see StateLocation). "" without a
// state directory.
func LedgerPath(cfg config.Config) string {
	if cfg.StateDir == "" {
		return ""
	}
	d, err := SpoolDir(cfg)
	if err != nil {
		return ""
	}
	return filepath.Join(d, ledger.File)
}

func (e *Endpoint) useLedger(path string) {
	e.ledg.mu.Lock()
	defer e.ledg.mu.Unlock()
	if e.ledg.l == nil || e.ledg.l.Path() != path {
		e.ledg.l = ledger.New(path)
	}
}

func (e *Endpoint) ledgerFile() *ledger.Ledger {
	e.ledg.mu.Lock()
	defer e.ledg.mu.Unlock()
	return e.ledg.l
}

// streamIdent is the identity of an inbox stream that the spool is bound to and the ledger keys its records by: the
// sequences restart at 1 when the stream is recreated.
func streamIdent(info *jetstream.StreamInfo) string {
	return info.Config.Name + "@" + info.Created.UTC().Format(time.RFC3339Nano)
}

func (e *Endpoint) noteGen(stream, ident string) {
	e.ledg.mu.Lock()
	defer e.ledg.mu.Unlock()
	if e.ledg.gens == nil {
		e.ledg.gens = map[string]string{}
	}
	e.ledg.gens[stream] = ident
}

// genOf is the generation of stream: the identity SetSpool bound, or else asked of the server once and kept. When it
// cannot be read the records go under ledger.UnknownGen, apart from every real generation.
func (e *Endpoint) genOf(stream string) string {
	e.ledg.mu.Lock()
	g, ok := e.ledg.gens[stream]
	e.ledg.mu.Unlock()
	if ok {
		return g
	}
	if e.js == nil {
		return ledger.UnknownGen
	}
	key := e.key
	if stream == LegacyStream {
		key = e.legacyKey
	}
	ctx, cancel := context.WithTimeout(context.Background(), ledgerInfoTimeout)
	defer cancel()
	st, err := e.js.Stream(ctx, sap.InboxStream(key))
	if err != nil {
		return ledger.UnknownGen
	}
	id := streamIdent(st.CachedInfo())
	e.noteGen(stream, id)
	return id
}

// ledgerResult keeps the outcome of a write for status and logs the first failure of a spell.
func (e *Endpoint) ledgerResult(err error) {
	e.ledg.mu.Lock()
	defer e.ledg.mu.Unlock()
	if err == nil {
		e.ledg.errMsg = ""
		return
	}
	if e.ledg.errMsg == "" && e.log != nil {
		e.log.Printf("presented ledger: %v (mail is still acked; status reports it)", err)
	}
	e.ledg.errMsg = err.Error()
}

// ledgerNote records refs under kind, before the ack of each. One line per stream and call; one sync.
func (e *Endpoint) ledgerNote(kind string, refs ...spool.Ref) {
	l := e.ledgerFile()
	if l == nil || len(refs) == 0 {
		return
	}
	by := map[string][]uint64{}
	var order []string
	for _, r := range refs {
		if _, ok := by[r.Stream]; !ok {
			order = append(order, r.Stream)
		}
		by[r.Stream] = append(by[r.Stream], r.Seq)
	}
	for _, stream := range order {
		e.ledgerResult(l.Note(e.genOf(stream), kind, by[stream]...))
	}
}

// ledgerDuplicate records ref as acked without being shown again; of is the sequence it repeats in the same stream, or 0.
func (e *Endpoint) ledgerDuplicate(ref spool.Ref, of uint64) {
	if l := e.ledgerFile(); l != nil {
		e.ledgerResult(l.NoteDuplicate(e.genOf(ref.Stream), ref.Seq, of))
	}
}

// LedgerLine is the delivery line of the status tool: the ledger's counts, and the loss it can prove against the
// inbox streams as they are now. ok is false without a ledger file path.
func (e *Endpoint) LedgerLine(ctx context.Context) (line string, ok bool) {
	l := e.ledgerFile()
	if l == nil {
		return "", false
	}
	e.ledg.mu.Lock()
	werr := e.ledg.errMsg
	e.ledg.mu.Unlock()
	st, err := ledger.Read(l.Path())
	if err != nil {
		return ledger.Unreadable(err, werr), true
	}
	sum := st.Summarize()
	return sum.Text(e.ledgerLoss(ctx, st), werr), true
}

// ledgerLoss words the loss part. When no inbox can be read it says so and claims nothing.
func (e *Endpoint) ledgerLoss(ctx context.Context, st ledger.State) string {
	ctx, cancel := context.WithTimeout(ctx, evictionInfoTimeout)
	defer cancel()
	type src struct{ ref, key string }
	streams := []src{{"", e.key}}
	if e.hasLegacyStream() {
		streams = append(streams, src{LegacyStream, e.legacyKey})
	}
	var parts []ledger.StreamState
	for _, s := range streams {
		stream, err := e.js.Stream(ctx, sap.InboxStream(s.key))
		if err != nil {
			continue
		}
		info := stream.CachedInfo()
		id := streamIdent(info)
		e.noteGen(s.ref, id)
		parts = append(parts, ledger.StreamState{Gen: st.Gens[id], FirstSeq: info.State.FirstSeq})
	}
	return ledger.LossText(parts)
}
