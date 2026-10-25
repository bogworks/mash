package endpoint

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/randybias/mash/internal/config"
	"github.com/randybias/mash/internal/render"
	"github.com/randybias/mash/internal/sap"
	"github.com/randybias/mash/internal/store"
)

// Deliverer writes one channel event to the Claude session. A nil error
// means it was written; only then is the mail acked.
type Deliverer func(text string, meta map[string]string) error

// InReplyToHeader carries the seq a message answers (optional).
const InReplyToHeader = "Mash-In-Reply-To"

// InstanceHeader carries the sender's instance id and its signature over it
// (sap.Identity.SignInstance). Only a value that verifies is ever shown.
const InstanceHeader = "Mash-Sender-Instance"

// maxPending flushes a batch early so a flood can't grow it without bound.
const maxPending = 200

// MaxSignedAge bounds how long before its storage a message may have been
// signed. Older (or future) signatures are labelled stale: a replayed capture.
var MaxSignedAge = 5 * time.Minute

// shownName is the sender name as displayed: a self-declared name only when it
// is a valid endpoint name, else a short form of the key (never raw text).
func shownName(h sap.SenderHeader) string {
	if config.ValidName(h.Name) {
		return h.Name
	}
	if len(h.User) > 8 {
		return h.User[:4] + "…" + h.User[len(h.User)-4:]
	}
	return "?"
}

// senderInstance is the sender's instance id, only when the sender's own key
// signed it onto this very message and the message itself verified.
func senderInstance(hdr nats.Header, trust sap.Trust, h sap.SenderHeader) string {
	if trust != sap.TrustVerified && trust != sap.TrustStale {
		return ""
	}
	inst, _ := sap.VerifyInstance(hdr.Get(InstanceHeader), h)
	return inst
}

// classify verifies a stored message and names its sender (spec section 4).
// storedAt is when the stream stored it. ok is false for forged or malformed
// mail, which is never shown.
func (e *Endpoint) classify(subject string, hdr nats.Header, data []byte, storedAt time.Time) (from string, trust sap.Trust, h sap.SenderHeader, ok bool) {
	h, trust = sap.VerifyStored(hdr.Get(sap.HeaderName), subject, data)
	switch trust {
	case sap.TrustForged, sap.TrustMalformed:
		return "", trust, h, false
	case sap.TrustAbsent:
		return "?", trust, h, true
	case sap.TrustClaimed:
		return shownName(h), trust, h, true
	}
	e.mu.RLock()
	name := e.byUser[h.User]
	e.mu.RUnlock()
	if name == "" || (h.Name != "" && h.Name != name) {
		return shownName(h), sap.TrustUnknownSigner, h, true
	}
	if signed, err := time.Parse(time.RFC3339Nano, h.TS); err == nil {
		if age := storedAt.Sub(signed); age > MaxSignedAge || age < -sap.LiveSkew {
			return name, sap.TrustStale, h, true
		}
	}
	return name, sap.TrustVerified, h, true
}

type pending struct {
	mail render.Mail
	msg  jetstream.Msg
	key  string // (user, nonce) of signed mail, for de-duplication
}

// consume starts the inbox consumer and its delivery worker. The consumer's
// callback only classifies and queues; one worker goroutine gathers messages
// until SettleMS pass with no new one, pushes them as one event, then acks
// them (a failed write naks them for redelivery). Being the only writer, the
// worker keeps stream order and makes de-duplication exact. The returned
// stop waits for the consumer to close and the worker to finish.
func (e *Endpoint) consume(ctx context.Context, deliver Deliverer) (func(), error) {
	cons, err := e.js.Consumer(ctx, sap.InboxStream(e.cfg.Name), store.Consumer)
	if err != nil {
		return nil, fmt.Errorf("mash: inbox consumer: %w", err)
	}
	queue := make(chan pending, maxPending)
	quit := make(chan struct{})
	workerDone := make(chan struct{})
	go e.deliverLoop(deliver, queue, quit, workerDone)

	handler := func(m jetstream.Msg) {
		meta, err := m.Metadata()
		if err != nil {
			e.log.Printf("message without metadata: %v", err)
			_ = m.Term()
			return
		}
		from, trust, h, ok := e.classify(m.Subject(), m.Headers(), m.Data(), meta.Timestamp)
		if !ok {
			e.dropped.Add(1)
			e.log.Printf("dropped #%d: %s sender header (claimed user %s)", meta.Sequence.Stream, trust, h.User)
			_ = m.Ack()
			return
		}
		var key string
		if trust == sap.TrustVerified || trust == sap.TrustUnknownSigner || trust == sap.TrustStale {
			key = h.User + "\x00" + h.Nonce
		}
		p := pending{msg: m, key: key, mail: render.Mail{Seq: meta.Sequence.Stream, From: from, Trust: trust,
			At: meta.Timestamp, Body: m.Data(), Instance: senderInstance(m.Headers(), trust, h)}}
		select {
		case queue <- p:
		case <-quit:
		}
	}
	cc, err := cons.Consume(handler, jetstream.PullMaxMessages(100))
	if err != nil {
		close(quit)
		<-workerDone
		return nil, fmt.Errorf("mash: consume: %w", err)
	}
	return func() {
		cc.Stop()
		select {
		case <-cc.Closed():
		case <-time.After(5 * time.Second):
		}
		close(quit)
		<-workerDone
	}, nil
}

// deliverLoop is the single writer of channel events (see consume).
func (e *Endpoint) deliverLoop(deliver Deliverer, queue <-chan pending, quit <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	settle := time.Duration(e.cfg.SettleMS) * time.Millisecond
	seen := map[string]bool{} // (user, nonce) of signed mail already shown
	for {
		var batch []pending
		select {
		case <-quit:
			return
		case p := <-queue:
			batch = append(batch, p)
		}
	gather:
		for len(batch) < maxPending {
			select {
			case <-quit:
				return // unacked: redelivered to the next session
			case p := <-queue:
				batch = append(batch, p)
			case <-time.After(settle):
				break gather
			}
		}
		var show []pending
		inBatch := map[string]bool{}
		for _, p := range batch {
			if p.key != "" && (seen[p.key] || inBatch[p.key]) {
				_ = p.msg.Ack()
				continue
			}
			if p.key != "" {
				inBatch[p.key] = true
			}
			show = append(show, p)
		}
		if len(show) == 0 {
			continue
		}
		mails := make([]render.Mail, len(show))
		seqs := make([]string, len(show))
		for i, p := range show {
			mails[i] = p.mail
			seqs[i] = strconv.FormatUint(p.mail.Seq, 10)
		}
		text := render.Batch(e.cfg.Name, mails, e.cfg.BatchMsgs, e.cfg.BatchBytes)
		meta := map[string]string{"to": e.cfg.Name, "count": strconv.Itoa(len(show)), "seqs": strings.Join(seqs, ",")}
		if err := deliver(text, meta); err != nil {
			e.log.Printf("delivery failed, will redeliver: %v", err)
			for _, p := range show {
				_ = p.msg.NakWithDelay(500 * time.Millisecond)
			}
			continue
		}
		for _, p := range show {
			if p.key != "" {
				seen[p.key] = true
			}
			if err := p.msg.Ack(); err != nil {
				e.log.Printf("ack #%d: %v", p.mail.Seq, err)
			}
		}
	}
}

// Send mails text to endpoint to, signed by this endpoint, and returns the
// recipient-inbox sequence from the stream's acknowledgement.
func (e *Endpoint) Send(ctx context.Context, to, text string, inReplyTo uint64) (uint64, error) {
	rec, err := e.lookup(ctx, to)
	if err != nil {
		return 0, err
	}
	data := []byte(text)
	if len(data) == 0 {
		return 0, errors.New("mash: empty message")
	}
	h, err := e.id.Sign(rec.MailSubject, data, e.cfg.Name, time.Now(), "")
	if err != nil {
		return 0, err
	}
	msg := nats.NewMsg(rec.MailSubject)
	msg.Data = data
	msg.Header.Set(sap.HeaderName, h.Encode())
	msg.Header.Set(jetstream.MsgIDHeader, h.Nonce)
	if iv, err := e.id.SignInstance(h, e.Instance()); err == nil {
		msg.Header.Set(InstanceHeader, iv)
	}
	if inReplyTo > 0 {
		msg.Header.Set(InReplyToHeader, strconv.FormatUint(inReplyTo, 10))
	}
	if size := len(data) + headerSize(msg.Header); size > store.MaxMsgSize {
		return 0, fmt.Errorf("mash: message is %d bytes with headers; the limit is %d", size, store.MaxMsgSize)
	}
	ack, err := e.js.PublishMsg(ctx, msg)
	if err != nil {
		return 0, fmt.Errorf("mash: send to %s: %w", to, err)
	}
	return ack.Sequence, nil
}

func headerSize(h nats.Header) int {
	n := len("NATS/1.0\r\n\r\n")
	for k, vs := range h {
		for _, v := range vs {
			n += len(k) + 2 + len(v) + 2
		}
	}
	return n
}

// lookup resolves a name through the directory (the live copy, else a read).
func (e *Endpoint) lookup(ctx context.Context, name string) (store.Record, error) {
	e.mu.RLock()
	rec, ok := e.dir[name]
	e.mu.RUnlock()
	if ok {
		return rec, nil
	}
	rec, err := store.GetRecord(ctx, e.dirKV, name)
	if err != nil {
		return store.Record{}, fmt.Errorf("mash: no endpoint named %q (see who)", name)
	}
	return rec, nil
}

func (e *Endpoint) inbox(ctx context.Context) (jetstream.Stream, error) {
	s, err := e.js.Stream(ctx, sap.InboxStream(e.cfg.Name))
	if err != nil {
		return nil, fmt.Errorf("mash: inbox: %w", err)
	}
	return s, nil
}

func (e *Endpoint) toMail(raw *jetstream.RawStreamMsg) (render.Mail, bool) {
	from, trust, h, ok := e.classify(raw.Subject, raw.Header, raw.Data, raw.Time)
	if !ok {
		return render.Mail{Seq: raw.Sequence, From: "?", Trust: trust, At: raw.Time}, false
	}
	return render.Mail{Seq: raw.Sequence, From: from, Trust: trust, At: raw.Time, Body: raw.Data,
		Instance: senderInstance(raw.Header, trust, h)}, true
}

// Read returns message seq from this endpoint's own inbox.
func (e *Endpoint) Read(ctx context.Context, seq uint64) (render.Mail, error) {
	s, err := e.inbox(ctx)
	if err != nil {
		return render.Mail{}, err
	}
	raw, err := s.GetMsg(ctx, seq)
	if err != nil {
		return render.Mail{}, fmt.Errorf("mash: no message #%d in %s's inbox", seq, e.cfg.Name)
	}
	m, ok := e.toMail(raw)
	if !ok {
		return m, fmt.Errorf("mash: message #%d has a %s sender header and is not shown", seq, m.Trust)
	}
	return m, nil
}

// Inbox returns up to limit of the most recent messages in this endpoint's
// inbox, oldest first. Forged mail is skipped.
func (e *Endpoint) Inbox(ctx context.Context, limit int) ([]render.Mail, error) {
	s, err := e.inbox(ctx)
	if err != nil {
		return nil, err
	}
	info, err := s.Info(ctx)
	if err != nil {
		return nil, err
	}
	var out []render.Mail
	for seq := info.State.LastSeq; seq >= info.State.FirstSeq && seq > 0 && len(out) < limit; seq-- {
		raw, err := s.GetMsg(ctx, seq)
		if err != nil {
			continue
		}
		if m, ok := e.toMail(raw); ok {
			out = append(out, m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out, nil
}
