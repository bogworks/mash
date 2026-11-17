package endpoint

import (
	"context"
	"fmt"
	"sync"

	"github.com/bogworks/mash/internal/spool"
)

// Fail-closed delivery (MASH_SPOOL_REQUIRED). The caller sets NoChannel, so mail the spool cannot take is held
// unacked in the inbox, never delivered by the channel. This file adds what makes that visible and recoverable: a spool
// fault that is reported, not routed around, and a spool that can be bound after Run has started.

// spoolFault is the spool failure in force, for the status tool and the report callback.
type spoolFault struct {
	mu     sync.Mutex
	err    string
	report func(msg string) // called once when a fault starts
	// active and held track a spool write failure in every mode, reported or not: active from a failed write until the
	// next successful one; held is whether a message the spool would not take stays unacked in the inbox (true) or is
	// delivered by the channel (false), as last decided by the fallback in spoolBatch.
	active, held bool
}

// ReportSpoolFaults registers report, called with the plain-text notice each time a spool fault starts (not again for
// the same spell), and makes a spool write failure a held message instead of a channel fallback. Call it before Run.
func (e *Endpoint) ReportSpoolFaults(report func(msg string)) {
	e.fault.mu.Lock()
	e.fault.report = report
	e.fault.mu.Unlock()
	e.faultOn = true
}

// SpoolFaultText is the notice for a spool failing with err.
func SpoolFaultText(err error) string {
	return fmt.Sprintf("mail is being held unacked in the inbox because the spool is failing: %v; read it with the inbox and read tools; it is delivered once the spool recovers", err)
}

// NoteSpoolFault records that the spool is failing with err. The fault is tracked in every mode (SpoolWriteFault);
// the notice and the status line are kept only when ReportSpoolFaults was called, once per spell.
func (e *Endpoint) NoteSpoolFault(err error) {
	if err == nil {
		return
	}
	e.fault.mu.Lock()
	if !e.fault.active {
		e.fault.active, e.fault.held = true, true // until the fallback decides, a failed message is nak'd, so unacked
	}
	if !e.faultOn {
		e.fault.mu.Unlock()
		return
	}
	first := e.fault.err == ""
	e.fault.err = err.Error()
	report := e.fault.report
	e.fault.mu.Unlock()
	if first && report != nil {
		report(SpoolFaultText(err))
	}
}

// ClearSpoolFault ends the spell: the spool works again.
func (e *Endpoint) ClearSpoolFault() {
	e.fault.mu.Lock()
	e.fault.err = ""
	e.fault.active, e.fault.held = false, false
	e.fault.mu.Unlock()
}

// noteFaultHeld records what became of a message the spool would not take: held unacked, or left to the channel.
func (e *Endpoint) noteFaultHeld(held bool) {
	e.fault.mu.Lock()
	if e.fault.active {
		e.fault.held = held
	}
	e.fault.mu.Unlock()
}

// SpoolWriteFault reports a spool write failure at run time in any mode, not only under ReportSpoolFaults: active from
// a failed write until the next successful one (or a bind). held is true when a message the spool would not take stays
// unacked in the inbox, false when the channel delivers it instead.
func (e *Endpoint) SpoolWriteFault() (active, held bool) {
	e.fault.mu.Lock()
	defer e.fault.mu.Unlock()
	return e.fault.active, e.fault.active && e.fault.held
}

// SpoolFaultLine is the status tool's line for a failing spool. ok is false when the spool is not failing, and always
// when fail-closed delivery is off.
func (e *Endpoint) SpoolFaultLine() (line string, ok bool) {
	e.fault.mu.Lock()
	defer e.fault.mu.Unlock()
	if e.fault.err == "" {
		return "", false
	}
	return "spool: FAILING - " + SpoolFaultText(fmt.Errorf("%s", e.fault.err)), true
}

type spoolBind struct {
	s         spool.Spool
	owner     string
	fromStart bool
	res       chan error
}

// BindSpool does SetSpool later, once Run has started: the deliver loop makes the call, since it is the only reader of
// the spool. With fromStart it then does SpoolFromStart, and a bound spool ends the fault. It returns SetSpool's error,
// or ctx's when the loop is not running.
func (e *Endpoint) BindSpool(ctx context.Context, s spool.Spool, owner string, fromStart bool) error {
	req := spoolBind{s: s, owner: owner, fromStart: fromStart, res: make(chan error, 1)}
	select {
	case e.spoolReq <- req:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-req.res:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// bindSpool runs on the deliver loop.
func (e *Endpoint) bindSpool(req spoolBind) {
	err := e.SetSpool(req.s, req.owner)
	if err == nil {
		if req.fromStart {
			e.SpoolFromStart()
		}
		e.ClearSpoolFault()
	}
	req.res <- err
}
