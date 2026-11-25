package endpoint

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/micro"
	"github.com/nats-io/nuid"

	"github.com/bogworks/mash/internal/sap"
)

// Version is the mash version, reported as the SAP service version. A release build stamps it by ldflag; everything
// else reads and writes it through CurrentVersion and SetVersion, which are synchronized (live sessions read it
// while a command may resolve it).
var Version = DevVersion

var versionMu sync.RWMutex

// CurrentVersion is the version to report.
func CurrentVersion() string {
	versionMu.RLock()
	defer versionMu.RUnlock()
	return Version
}

// SetVersion replaces it (done once, at startup, when the module version or build info resolves it).
func SetVersion(v string) {
	versionMu.Lock()
	Version = v
	versionMu.Unlock()
}

// Commit is the VCS revision of this build ("" when unknown), read and written like Version.
var Commit string

// CurrentCommit is the revision to report.
func CurrentCommit() string {
	versionMu.RLock()
	defer versionMu.RUnlock()
	return Commit
}

// SetCommit replaces it (done once, at startup, from the ldflag or the build info).
func SetCommit(c string) {
	versionMu.Lock()
	Commit = c
	versionMu.Unlock()
}

// DevVersion is what an unstamped build reports. It is valid SemVer because the SAP service (micro.AddService)
// refuses anything else as its version.
const DevVersion = "0.0.0-dev"

// Timings (spec 7.4). Variables so tests can shorten them.
var (
	HeartbeatInterval = 30 * time.Second
	PromptKeepalive   = 30 * time.Second
	PromptTimeout     = 10 * time.Minute
)

// MaxPromptBytes is the most bytes of text a live prompt may carry (the decoded text, before any escaping). A longer
// prompt is refused to its caller before it is acknowledged, so the session is never shown a cut-off request and no
// caller waits on one; the longer text goes as mail. The session's channel notice (cmd/mash PromptTextMax) uses this
// same number, so the notice never has to cut.
const MaxPromptBytes = 32 << 10

// PromptTooLargeCode is the stable error code of a refused oversized prompt; its description starts with
// "prompt_too_large:".
const PromptTooLargeCode = "413"

// promptTooLarge is the error description a caller gets for a prompt over MaxPromptBytes.
var promptTooLarge = fmt.Sprintf("prompt_too_large: a live prompt is at most %d bytes; send the longer text as mail", MaxPromptBytes)

// PromptEvent is a verified live SAP prompt for the session to answer.
type PromptEvent struct {
	RequestID string
	From      string
	Trust     sap.Trust
	Text      string
}

// openPrompt is one live prompt. mu serialises everything published on its
// reply subject, so nothing can follow the terminator (SAP §6.5).
type openPrompt struct {
	from   string // the asker's directory key: the identity a reply answers
	reply  string
	done   chan struct{}
	mu     sync.Mutex
	closed bool
}

// send publishes a chunk unless the prompt is closed; final also terminates
// it (after an optional error), exactly once.
func (e *Endpoint) send(p *openPrompt, data []byte, final bool, code, desc string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return false
	}
	if data != nil {
		_ = e.nc.Publish(p.reply, data)
	}
	if final {
		p.closed = true
		close(p.done)
		e.end(p.reply, code, desc)
	}
	return true
}

type service struct {
	svc      micro.Service
	mu       sync.Mutex
	prompts  map[string]*openPrompt
	nonces   sap.NonceSet
	quit     chan struct{} // closed by stop: ends the heartbeat
	stopOnce sync.Once
}

// stop deregisters the SAP service and ends the heartbeat. Idempotent.
// serviceFlush waits until the server has the service's subscriptions, for at most 5 s and no longer than ctx: serve's
// ctx ends when its stdin closes, so a connection that is reconnecting cannot hold up the exit. A seam for tests.
var serviceFlush = func(ctx context.Context, nc *nats.Conn) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return nc.FlushWithContext(ctx)
}

func (s *service) stop() {
	s.stopOnce.Do(func() {
		close(s.quit)
		if s.svc != nil {
			_ = s.svc.Stop()
		}
	})
}

// StopService deregisters this endpoint's SAP service and stops its heartbeat.
func (e *Endpoint) StopService() {
	if s := e.svc(); s != nil {
		s.stop()
	}
}

func chunk(typ string, data any) []byte {
	b, _ := json.Marshal(struct {
		Type string `json:"type"`
		Data any    `json:"data"`
	}{typ, data})
	return b
}

// svc returns the SAP service once StartService has registered it.
func (e *Endpoint) svc() *service {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.service
}

// Instance is the SAP instance id (the micro service id once registered).
func (e *Endpoint) Instance() string {
	if s := e.svc(); s != nil {
		return s.svc.Info().ID
	}
	return e.instance
}

func (e *Endpoint) promptMetadata() map[string]string {
	return map[string]string{"max_payload": PromptMaxPayload(), "attachments_ok": "false", "min_sender_trust": "signed"}
}

// PromptMaxPayload is the max_payload the prompt endpoint advertises, in the SAP grammar (a count and B, KB, MB or
// GB, base 1024; SAP section 2.1): exactly MaxPromptBytes, the text cap the endpoint enforces. SAP counts the whole
// request on the wire against max_payload, the Agent-Sender header included (SAP section 5.4), so a request within
// it carries at most MaxPromptBytes of text and is never refused for size. A caller that obeys the advertisement
// gets a little less text room than the endpoint accepts (the headers count), which is the safe direction.
func PromptMaxPayload() string {
	return formatMaxPayload(MaxPromptBytes)
}

// formatMaxPayload writes n bytes as SAP's \d+(B|KB|MB|GB) with the largest unit that divides n evenly.
func formatMaxPayload(n int) string {
	for _, u := range []struct {
		name string
		size int
	}{{"GB", 1 << 30}, {"MB", 1 << 20}, {"KB", 1 << 10}} {
		if n >= u.size && n%u.size == 0 {
			return fmt.Sprintf("%d%s", n/u.size, u.name)
		}
	}
	return fmt.Sprintf("%dB", n)
}

// heartbeat builds the SAP §8.3 payload (also the status reply).
func (e *Endpoint) heartbeat() []byte {
	type ep struct {
		Subject  string            `json:"subject"`
		Metadata map[string]string `json:"metadata"`
	}
	b, _ := json.Marshal(struct {
		Agent           string        `json:"agent"`
		Owner           string        `json:"owner"`
		Session         string        `json:"session"`
		InstanceID      string        `json:"instance_id"`
		TS              string        `json:"ts"`
		IntervalS       int           `json:"interval_s"`
		ProtocolVersion string        `json:"protocol_version"`
		Endpoints       map[string]ep `json:"endpoints"`
	}{sap.AgentName, e.cfg.Owner, e.key, e.Instance(), time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		int(HeartbeatInterval / time.Second), sap.ProtocolVersion, map[string]ep{
			"prompt": {e.subj.Prompt, e.promptMetadata()},
			"mail":   {e.subj.Mail, map[string]string{"durable": "true"}},
		}})
	return b
}

func (e *Endpoint) publishHeartbeat() {
	payload := e.heartbeat()
	var hb struct {
		TS string `json:"ts"`
	}
	_ = json.Unmarshal(payload, &hb)
	ts, _ := time.Parse(time.RFC3339, hb.TS)
	h, err := e.id.Sign(e.subj.HB, payload, e.key, ts, "")
	if err != nil {
		e.log.Printf("heartbeat: %v", err)
		return
	}
	m := nats.NewMsg(e.subj.HB)
	m.Data = payload
	m.Header.Set(sap.HeaderName, h.Encode())
	if err := e.nc.PublishMsg(m); err != nil {
		e.log.Printf("heartbeat: %v", err)
	}
}

// StartService registers this endpoint as a SAP 0.3 agent (spec 7.4) and
// starts the signed heartbeat. Prompts that pass verification go to onPrompt.
func (e *Endpoint) StartService(ctx context.Context, onPrompt func(PromptEvent)) error {
	idSig, err := e.id.IDSig(sap.AgentName, e.ident.Owner, e.subj.Prompt)
	if err != nil {
		return err
	}
	s := &service{prompts: map[string]*openPrompt{}, nonces: sap.NewNonceSet(2 * sap.LiveSkew), quit: make(chan struct{})}
	svc, err := micro.AddService(e.nc, micro.Config{
		Name:        sap.ServiceName,
		Version:     CurrentVersion(),
		Description: "Claude Code (mash): " + e.ident.String(),
		Metadata: map[string]string{
			"agent": sap.AgentName, "owner": e.ident.Owner, "group": e.ident.Group, "instance": e.ident.Instance, "session": e.key,
			"protocol_version": sap.ProtocolVersion, "user_nkey": e.id.User, "account": e.id.Account,
			"id_sig": idSig, "mail_subject": e.subj.Mail,
		},
	})
	if err != nil {
		return fmt.Errorf("mash: SAP registration: %w", err)
	}
	s.svc = svc
	e.mu.Lock()
	e.service = s
	e.mu.Unlock()
	// Any failure from here stops the service, as StopService does: it would otherwise take prompts with no
	// heartbeat, and the endpoint would look offline while it answered (codex H1 on #20). It stays installed, stopped,
	// so a prompt callback still running from before the stop finds it (codex H1 on #20, round 2).
	started := false
	defer func() {
		if !started {
			s.stop()
		}
	}()
	if err := svc.AddEndpoint("prompt", micro.HandlerFunc(func(r micro.Request) { e.handlePrompt(r, s, onPrompt) }),
		micro.WithEndpointSubject(e.subj.Prompt), micro.WithEndpointQueueGroup(sap.QueueGroup),
		micro.WithEndpointMetadata(e.promptMetadata())); err != nil {
		return fmt.Errorf("mash: prompt endpoint: %w", err)
	}
	if err := svc.AddEndpoint("status", micro.HandlerFunc(func(r micro.Request) {
		if _, tr := sap.VerifyLive(r.Headers().Get(sap.HeaderName), r.Subject(), r.Data(), time.Now(), s.nonces); tr != sap.TrustVerified && tr != sap.TrustAbsent {
			e.log.Printf("status probe with a %s sender header", tr)
		}
		_ = r.Respond(e.heartbeat())
	}), micro.WithEndpointSubject(e.subj.Status), micro.WithEndpointQueueGroup(sap.QueueGroup)); err != nil {
		return fmt.Errorf("mash: status endpoint: %w", err)
	}
	// The endpoints' subscriptions reach the server before StartService returns, so a prompt sent from another
	// connection right after it finds a responder (#19: "no responders" under load).
	if err := serviceFlush(ctx, e.nc); err != nil {
		return fmt.Errorf("mash: service registration: %w", err)
	}
	started = true
	e.publishHeartbeat()
	go func() {
		t := time.NewTicker(HeartbeatInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.quit:
				return
			case <-t.C:
				e.publishHeartbeat()
			}
		}
	}()
	return nil
}

func (e *Endpoint) end(reply string, code, desc string) {
	if code != "" {
		m := nats.NewMsg(reply)
		m.Header.Set("Nats-Service-Error-Code", code)
		m.Header.Set("Nats-Service-Error", desc)
		_ = e.nc.PublishMsg(m)
	}
	_ = e.nc.Publish(reply, nil) // §6.5 terminator: no body, no headers
}

// promptText applies the §5.3 discrimination rule.
func promptText(data []byte) (string, error) {
	trimmed := bytes.TrimLeft(data, "\t\n\r ")
	if len(data) == 0 {
		return "", errors.New("empty request")
	}
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return string(data), nil
	}
	var env struct {
		Prompt      *string           `json:"prompt"`
		Attachments []json.RawMessage `json:"attachments"`
	}
	if err := json.Unmarshal(trimmed, &env); err != nil || env.Prompt == nil || *env.Prompt == "" {
		return "", errors.New("the envelope has no prompt")
	}
	if len(env.Attachments) > 0 {
		return "", errors.New("attachments are not accepted (attachments_ok=false)")
	}
	return *env.Prompt, nil
}

// handlePrompt serves one prompt for the service s it was registered with, never the endpoint's current one.
func (e *Endpoint) handlePrompt(r micro.Request, s *service, onPrompt func(PromptEvent)) {
	reply := r.Reply()
	if reply == "" {
		return
	}
	h, tr := sap.VerifyLive(r.Headers().Get(sap.HeaderName), r.Subject(), r.Data(), time.Now(), s.nonces)
	switch tr {
	case sap.TrustMalformed:
		e.end(reply, "400", "malformed Agent-Sender header")
		return
	case sap.TrustVerified:
	default:
		e.end(reply, "401", "a signed Agent-Sender header is required (min_sender_trust=signed)")
		return
	}
	e.mu.RLock()
	from := e.byUser[h.User]
	e.mu.RUnlock()
	if from == "" || (h.Name != "" && h.Name != from) {
		e.end(reply, "403", "the sender is not a mash endpoint")
		return
	}
	// The name rule applies to the asker too: a peer on an older client may not have checked it on its side.
	if err := e.checkKey(pointPromptAsker, from); err != nil {
		e.end(reply, "403", err.Error())
		return
	}
	text, err := promptText(r.Data())
	if err != nil {
		e.end(reply, "400", err.Error())
		return
	}
	if len(text) > MaxPromptBytes {
		e.end(reply, PromptTooLargeCode, promptTooLarge)
		return
	}
	_ = e.nc.Publish(reply, chunk("status", "ack"))
	id := nuid.Next()
	p := &openPrompt{from: from, reply: reply, done: make(chan struct{})}
	s.mu.Lock()
	s.prompts[id] = p
	s.mu.Unlock()
	go func() {
		keep := time.NewTicker(PromptKeepalive)
		defer keep.Stop()
		timeout := time.NewTimer(PromptTimeout)
		defer timeout.Stop()
		for {
			select {
			case <-p.done:
				return
			case <-keep.C:
				e.send(p, chunk("status", "working"), false, "", "")
			case <-timeout.C:
				e.send(p, nil, true, "500", "no reply from the session")
				s.forget(id)
				return
			}
		}
	}()
	onPrompt(PromptEvent{RequestID: id, From: from, Trust: tr, Text: text})
}

// forget removes a finished prompt from the open set.
func (s *service) forget(id string) {
	s.mu.Lock()
	delete(s.prompts, id)
	s.mu.Unlock()
}

// Reply answers a live prompt with a response chunk; done ends the stream.
// Nothing is sent once the prompt has ended (answered or timed out).
func (e *Endpoint) Reply(requestID, text string, done bool) error {
	svc := e.svc()
	if svc == nil {
		return errors.New("mash: no live prompts (SAP service not started)")
	}
	svc.mu.Lock()
	p, ok := svc.prompts[requestID]
	svc.mu.Unlock()
	if ok {
		// The identity being answered is checked first. A refused one is told why (the prompt ends with the rule's
		// error) and the caller gets the same error.
		if err := e.checkKey(pointReply, p.from); err != nil {
			e.send(p, nil, true, "403", err.Error())
			svc.forget(requestID)
			return err
		}
	}
	if !ok || !e.send(p, chunk("response", text), done, "", "") {
		return fmt.Errorf("mash: no open prompt %q (already answered or timed out)", requestID)
	}
	if done {
		svc.forget(requestID)
	}
	return nil
}

// DiscoverRecipient is Discover for a recipient argument: the dotted address owner.group.instance as who shows it, or
// its flat key owner_group_instance. Any other argument is an error that names the form to give.
func DiscoverRecipient(ctx context.Context, nc *nats.Conn, arg string) (string, error) {
	a, err := parseRecipient(sap.KeyOf(arg))
	if err != nil {
		return "", err
	}
	return Discover(ctx, nc, a)
}

// Discover finds the prompt subject of the endpoint at a from $SRV.INFO.agents (SAP §4.3). It matches the prompt
// subject alone, so an endpoint hosted by any SAP SDK is found by its T3.T4.T5; metadata is not read for routing.
func Discover(ctx context.Context, nc *nats.Conn, a sap.Address) (string, error) {
	s, err := a.Subjects()
	if err != nil {
		return "", err
	}
	return discoverSubject(ctx, nc, s.Prompt, a.Flat())
}

func discoverSubject(ctx context.Context, nc *nats.Conn, want, name string) (string, error) {
	inbox := secretInbox(nc)
	sub, err := nc.SubscribeSync(inbox)
	if err != nil {
		return "", err
	}
	defer sub.Unsubscribe()
	if err := nc.PublishRequest("$SRV.INFO."+sap.ServiceName, inbox, nil); err != nil {
		return "", err
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		m, err := sub.NextMsg(time.Until(deadline))
		if err != nil {
			break
		}
		var info micro.Info
		if json.Unmarshal(m.Data, &info) != nil {
			continue
		}
		for _, ep := range info.Endpoints {
			if ep.Name == "prompt" {
				// Any peer can answer the broadcast: accept only the subject SAP derives from the target's name, so
				// a forged answer can neither redirect the prompt nor name a subject its sender could read.
				if ep.Subject == want {
					return want, nil
				}
			}
		}
	}
	return "", fmt.Errorf("mash: %s is not online (no SAP registration answered)", sap.Display(name))
}

// Prompt sends a signed live prompt to subject and writes the streamed reply
// to out (SAP §5-§6, §13). It fails on an error chunk or 60 s of silence.
func Prompt(ctx context.Context, nc *nats.Conn, id sap.Identity, name, subject, text string, out io.Writer) error {
	if len(text) > MaxPromptBytes {
		return fmt.Errorf("mash: prompt refused: %s %s (this one is %d)", PromptTooLargeCode, promptTooLarge, len(text))
	}
	return promptSend(ctx, nc, id, name, subject, text, out)
}

// promptSend is Prompt without the size pre-check, so a test can send what the service must refuse.
func promptSend(ctx context.Context, nc *nats.Conn, id sap.Identity, name, subject, text string, out io.Writer) error {
	inbox := secretInbox(nc)
	sub, err := nc.SubscribeSync(inbox)
	if err != nil {
		return err
	}
	defer sub.Unsubscribe()
	m, err := promptMsg(id, name, subject, inbox, text)
	if err != nil {
		return err
	}
	if err := nc.PublishMsg(m); err != nil {
		return err
	}
	for {
		wait := 60 * time.Second
		if d, ok := ctx.Deadline(); ok && time.Until(d) < wait {
			wait = time.Until(d)
		}
		r, err := sub.NextMsg(wait)
		if err != nil {
			return fmt.Errorf("mash: prompt: no reply chunk within %s: %w", wait, err)
		}
		if code := r.Header.Get("Nats-Service-Error-Code"); code != "" {
			return fmt.Errorf("mash: prompt refused: %s %s", code, r.Header.Get("Nats-Service-Error"))
		}
		if len(r.Data) == 0 && len(r.Header) == 0 {
			return nil
		}
		var c struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(r.Data, &c) != nil || c.Type != "response" {
			continue // status chunks and unknown types are ignored (§6.6)
		}
		var s string
		if json.Unmarshal(c.Data, &s) != nil {
			var obj struct {
				Text string `json:"text"`
			}
			_ = json.Unmarshal(c.Data, &obj)
			s = obj.Text
		}
		if _, err := io.WriteString(out, s); err != nil {
			return err
		}
	}
}

// promptMsg is the signed request a prompt sends: the text as the payload and the Agent-Sender header.
func promptMsg(id sap.Identity, name, subject, inbox, text string) (*nats.Msg, error) {
	payload := []byte(text)
	h, err := id.Sign(subject, payload, name, time.Now(), "")
	if err != nil {
		return nil, err
	}
	m := nats.NewMsg(subject)
	m.Reply = inbox
	m.Data = payload
	m.Header.Set(sap.HeaderName, h.Encode())
	return m, nil
}

// secretInbox is a reply subject under the connection's inbox prefix with 128 random bits. nc.NewInbox ends in a NUID,
// whose next value a peer that saw one (every endpoint reads the $SRV discovery requests and may publish to the inboxes of its own owner, _INBOX.<owner>.>)
// can predict, and so answer a prompt in the target's place.
func secretInbox(nc *nats.Conn) string {
	in := nc.NewInbox()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand does not fail on supported platforms
	}
	return in[:strings.LastIndex(in, ".")+1] + hex.EncodeToString(b[:])
}

// BuildRevision is the commit a binary was built from: the ldflag value, else the VCS revision go build stamped (with
// "+dirty" for a modified tree), else "".
func BuildRevision(ldflags string, bi *debug.BuildInfo) string {
	rev := ldflags
	if rev == "" && bi != nil {
		modified := false
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.modified":
				modified = s.Value == "true"
			}
		}
		if rev != "" && modified {
			rev += "+dirty"
		}
	}
	return rev
}
