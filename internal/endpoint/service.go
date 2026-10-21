package endpoint

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/micro"
	"github.com/nats-io/nuid"

	"github.com/randybias/mash/internal/sap"
)

// Version is the mash version, reported as the SAP service version.
var Version = "0.1.0"

// Timings (spec 7.4). Variables so tests can shorten them.
var (
	HeartbeatInterval = 30 * time.Second
	PromptKeepalive   = 30 * time.Second
	PromptTimeout     = 10 * time.Minute
)

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
	return map[string]string{"max_payload": "1MB", "attachments_ok": "false", "min_sender_trust": "signed"}
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
	}{sap.AgentName, e.cfg.Owner, e.cfg.Name, e.Instance(), time.Now().UTC().Format("2006-01-02T15:04:05Z"),
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
	h, err := e.id.Sign(e.subj.HB, payload, e.cfg.Name, ts, "")
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
	idSig, err := e.id.IDSig(sap.AgentName, e.cfg.Owner, e.subj.Prompt)
	if err != nil {
		return err
	}
	s := &service{prompts: map[string]*openPrompt{}, nonces: sap.NewNonceSet(2 * sap.LiveSkew), quit: make(chan struct{})}
	svc, err := micro.AddService(e.nc, micro.Config{
		Name:        sap.ServiceName,
		Version:     Version,
		Description: "Claude Code (mash): " + e.cfg.Name,
		Metadata: map[string]string{
			"agent": sap.AgentName, "owner": e.cfg.Owner, "session": e.cfg.Name,
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
	if err := svc.AddEndpoint("prompt", micro.HandlerFunc(func(r micro.Request) { e.handlePrompt(r, onPrompt) }),
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

func (e *Endpoint) handlePrompt(r micro.Request, onPrompt func(PromptEvent)) {
	reply := r.Reply()
	if reply == "" {
		return
	}
	h, tr := sap.VerifyLive(r.Headers().Get(sap.HeaderName), r.Subject(), r.Data(), time.Now(), e.svc().nonces)
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
	text, err := promptText(r.Data())
	if err != nil {
		e.end(reply, "400", err.Error())
		return
	}
	_ = e.nc.Publish(reply, chunk("status", "ack"))
	id := nuid.Next()
	p := &openPrompt{reply: reply, done: make(chan struct{})}
	svc := e.svc()
	svc.mu.Lock()
	svc.prompts[id] = p
	svc.mu.Unlock()
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
				e.forget(id)
				return
			}
		}
	}()
	onPrompt(PromptEvent{RequestID: id, From: from, Trust: tr, Text: text})
}

// forget removes a finished prompt from the open set.
func (e *Endpoint) forget(id string) {
	svc := e.svc()
	svc.mu.Lock()
	delete(svc.prompts, id)
	svc.mu.Unlock()
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
	if !ok || !e.send(p, chunk("response", text), done, "", "") {
		return fmt.Errorf("mash: no open prompt %q (already answered or timed out)", requestID)
	}
	if done {
		e.forget(requestID)
	}
	return nil
}

// Discover finds name's prompt subject from $SRV.INFO.agents (SAP §4.3).
func Discover(ctx context.Context, nc *nats.Conn, owner, name string) (string, error) {
	inbox := nc.NewInbox()
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
		if json.Unmarshal(m.Data, &info) != nil || info.Metadata["owner"] != owner {
			continue
		}
		for _, ep := range info.Endpoints {
			if ep.Name == "prompt" {
				if n, ok := sap.NameFromSubject(ep.Subject); ok && n == name {
					return ep.Subject, nil
				}
			}
		}
	}
	return "", fmt.Errorf("mash: %s is not online (no SAP registration answered)", name)
}

// Prompt sends a signed live prompt to subject and writes the streamed reply
// to out (SAP §5-§6, §13). It fails on an error chunk or 60 s of silence.
func Prompt(ctx context.Context, nc *nats.Conn, id sap.Identity, name, subject, text string, out io.Writer) error {
	inbox := nc.NewInbox()
	sub, err := nc.SubscribeSync(inbox)
	if err != nil {
		return err
	}
	defer sub.Unsubscribe()
	payload := []byte(text)
	h, err := id.Sign(subject, payload, name, time.Now(), "")
	if err != nil {
		return err
	}
	m := nats.NewMsg(subject)
	m.Reply = inbox
	m.Data = payload
	m.Header.Set(sap.HeaderName, h.Encode())
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
