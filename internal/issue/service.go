package issue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/micro"

	"github.com/bogworks/mash/internal/store"
)

// ServiceName is the name the issue service registers as a NATS micro service.
const ServiceName = "mash-issue"

// SubjectPrefix opens the issue subject: mash.issue.<owner>. It does not begin with agents, so it is none of the
// address subjects (agents.<verb>.<agent>.<owner>.<name>), and no permission template mash compiles has a line that
// reaches it, so an endpoint cannot publish to it. A wrangler's permission to publish on it, and to receive the
// reply, belongs to account setup.
const SubjectPrefix = "mash.issue"

// requestTimeout bounds one request's reads and writes of the issue record.
const requestTimeout = 15 * time.Second

// serveFlushWait bounds the wait for the server to confirm the service's subscription.
const serveFlushWait = 10 * time.Second

// Subject is the subject a wrangler publishes the issue request of owner group owner on.
func Subject(owner string) string { return SubjectPrefix + "." + owner }

// Serve registers the issue service on nc, listening on mash.issue.* in the queue group of the service, and returns
// a stop func. Every request is answered with one Response; the purpose is always store.PurposeIssue.
func Serve(nc *nats.Conn, is *Issuer, version string) (stop func() error, err error) {
	if err := is.Check(); err != nil {
		return nil, err
	}
	svc, err := micro.AddService(nc, micro.Config{
		Name: ServiceName, Version: version, Description: "mash issue service: signs a user JWT for a public key",
	})
	if err != nil {
		return nil, fmt.Errorf("mash: issue service registration: %w", err)
	}
	h := micro.HandlerFunc(func(req micro.Request) { handle(is, req) })
	if err := svc.AddEndpoint("issue", h, micro.WithEndpointSubject(SubjectPrefix+".*"), micro.WithEndpointQueueGroup(ServiceName)); err != nil {
		_ = svc.Stop()
		return nil, fmt.Errorf("mash: issue service endpoint: %w", err)
	}
	// A subscription is registered on the server asynchronously. Serve returns, and the caller reports the service as
	// listening, only after the server has acknowledged it, so a request published right after cannot go unanswered.
	if err := nc.FlushTimeout(serveFlushWait); err != nil {
		_ = svc.Stop()
		return nil, fmt.Errorf("mash: issue service subscription not confirmed: %w", err)
	}
	return svc.Stop, nil
}

func handle(is *Issuer, req micro.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	respond := func(r Response) {
		b, _ := json.Marshal(r)
		_ = req.Respond(b)
	}
	r, rf := Parse(req.Data())
	if rf == nil {
		// The subject names the group the wrangler believes it asks for; the address must be in it.
		owner, _, _ := r.AddressParts()
		if req.Subject() != Subject(owner) {
			rf = refuse(CodeSubject, "the address is not in the owner group of the subject")
		}
	}
	if rf != nil {
		is.logRefusal(rf, r, store.PurposeIssue)
		respond(Response{Error: rf.Msg, Code: rf.Code})
		return
	}
	out, err := is.Issue(ctx, r, store.PurposeIssue)
	var ref *Refusal
	switch {
	case err == nil:
		respond(Response{JWT: out.JWT})
	case errors.As(err, &ref):
		respond(Response{Error: ref.Msg, Code: ref.Code})
	default:
		respond(Response{Error: "the issue failed", Code: CodeInternal})
	}
}
