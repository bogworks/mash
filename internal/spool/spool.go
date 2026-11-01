// Package spool is the hand-over between serve and a Claude Code session's hooks (hook delivery, mash v0.2.0). serve writes
// each message it has received to the spool and holds it unacked; a hook (`mash hook`) shows the spooled messages to
// the session and marks them drained; serve then acks them, so a message is acked only once it has been seen.
//
// Delivery is at-least-once: a message is never lost, but one may be shown twice. A hook prints its batch and then
// marks each entry drained; a kill between the two leaves the entries waiting, and the next run shows them again.
// To make that visible, a hook first records the seqs it is about to show in shown/<seq>; an entry read back with
// its marker still present has Redelivered set, and the host adapter tells the model it may already have been seen.
//
// Layout, under Dir: <seq>.json for each message waiting (0600), shown/<seq> for each one a hook is about to show
// and has not yet drained, drained/<seq> for each one a hook has shown and serve has not yet acked, and .hooked,
// whose mtime is the last hook run, listed/<seq> for each one a hook has only listed in a digest (still waiting, not
// drained: it is drained when the model reads it or a later hook shows it in full, ADR 0046), and .alive, whose mtime is the last run of a subagent's hook. The directories are 0700.
package spool

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Entry is one spooled message: what render needs to show it.
type Entry struct {
	Seq      uint64    `json:"seq"`
	From     string    `json:"from"`
	Trust    string    `json:"trust"`
	Instance string    `json:"instance,omitempty"`
	Owner    string    `json:"owner,omitempty"` // the sender's owner
	Urgent   bool      `json:"urgent,omitempty"`
	Kind     string    `json:"kind,omitempty"` // blocker, question, task, result or fyi; empty (an older serve) reads as task
	At       time.Time `json:"at"`
	Body     []byte    `json:"body"`
	// Redelivered is set by Waiting, never persisted: a hook had begun to show this entry and did not finish
	// draining it, so the session may already have seen it.
	Redelivered bool `json:"-"`
	// Listed is never persisted. Waiting sets it for an entry a hook has listed in a digest (its listed/<seq> mark is
	// there); the host adapter's Present sets it on the entries it rendered as digest lines, which the hook then marks
	// listed instead of drained.
	Listed bool `json:"-"`
}

// Spool is one endpoint's spool directory.
type Spool struct{ Dir string }

// Dir is the spool of endpoint name of owner under stateDir (config.Config.StateDir).
func Dir(stateDir, owner, name string) string {
	return filepath.Join(stateDir, "spool", owner, name)
}

const (
	marker      = ".hooked"
	streamFile  = ".stream"
	sessionFile = ".session"
	ownerFile   = ".owner"
	refusedFile = ".refused"
	toldFile    = ".told"
	aliveFile   = ".alive"
	waiterPref  = ".waiting."

	// refusedEvery is how often a refused hook rewrites .refused.
	refusedEvery = 60 * time.Second
)

func (s Spool) drained() string { return filepath.Join(s.Dir, "drained") }
func (s Spool) shown() string   { return filepath.Join(s.Dir, "shown") }
func (s Spool) listed() string  { return filepath.Join(s.Dir, "listed") }

func (s Spool) listedFile(seq uint64) string {
	return filepath.Join(s.listed(), strconv.FormatUint(seq, 10))
}

func (s Spool) shownFile(seq uint64) string {
	return filepath.Join(s.shown(), strconv.FormatUint(seq, 10))
}

func (s Spool) file(seq uint64) string {
	return filepath.Join(s.Dir, strconv.FormatUint(seq, 10)+".json")
}

// Init makes the spool's directories, 0700, and tightens them if they exist.
func (s Spool) Init() error {
	for _, d := range []string{s.Dir, s.drained(), s.shown(), s.listed()} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
		if err := os.Chmod(d, 0o700); err != nil {
			return err
		}
	}
	return nil
}

// ClearWaiting removes every waiting <seq>.json and leftover .put-* temporary file, keeping the drained/ tombstones
// and .hooked, and the shown/ marks like the tombstones: a message shown and not drained is redelivered, and may have
// been seen. The listed/ marks go with the files: a message only listed in a digest was not seen, and its redelivery
// is listed again, once (ADR 0046). A restarted serve calls it: NATS redelivers what was never acked (codex M1 on #21).
func (s Spool) ClearWaiting() error {
	if err := s.clearDir(s.listed()); err != nil {
		return err
	}
	des, err := os.ReadDir(s.Dir)
	if err != nil {
		return err
	}
	for _, d := range des {
		if d.IsDir() {
			continue
		}
		_, ok := seqOf(d.Name())
		if !ok && !strings.HasPrefix(d.Name(), ".put-") {
			continue
		}
		if err := os.Remove(filepath.Join(s.Dir, d.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("spool: %w", err)
		}
	}
	return nil
}

// clearDir removes the files in dir; a missing dir is empty.
func (s Spool) clearDir(dir string) error {
	des, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, d := range des {
		if err := os.Remove(filepath.Join(dir, d.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("spool: %w", err)
		}
	}
	return nil
}

// Bind scopes the spool to stream, the identity of the inbox stream. Sequences restart at 1 when the stream is
// recreated (deprovision, then provision), so a tombstone or waiting file left by the old stream would match new
// mail: Bind drops them all when stream differs from the one recorded, and records it. Equal, it changes nothing
// (codex H1 on #21, round 2). Call it before anything is spooled.
func (s Spool) Bind(stream string) error {
	p := filepath.Join(s.Dir, streamFile)
	if b, err := os.ReadFile(p); err == nil && string(b) == stream {
		return nil
	}
	if err := s.ClearWaiting(); err != nil {
		return err
	}
	for _, dir := range []string{s.drained(), s.shown(), s.listed()} {
		des, err := os.ReadDir(dir)
		if err != nil && !(dir != s.drained() && errors.Is(err, fs.ErrNotExist)) {
			return err
		}
		for _, d := range des {
			if err := os.Remove(filepath.Join(dir, d.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("spool: %w", err)
			}
		}
	}
	f, err := os.CreateTemp(s.Dir, ".stream-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // a no-op once renamed
	err = f.Chmod(0o600)
	if err == nil {
		_, err = f.WriteString(stream)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// Put writes e atomically (a 0600 temporary file, synced, renamed into place). A message put again (a redelivery)
// replaces its file.
func (s Spool) Put(e Entry) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(s.Dir, ".put-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // a no-op once renamed
	err = f.Chmod(0o600)
	if err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp, s.file(e.Seq))
}

// Waiting lists the spooled messages, oldest first. An unreadable file is skipped, not fatal: one bad entry must not
// stop the rest.
func (s Spool) Waiting() ([]Entry, error) {
	des, err := os.ReadDir(s.Dir)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, d := range des {
		seq, ok := seqOf(d.Name())
		if !ok || !d.Type().IsRegular() {
			continue
		}
		b, err := os.ReadFile(s.file(seq))
		if err != nil {
			continue
		}
		var e Entry
		if json.Unmarshal(b, &e) == nil && e.Seq == seq {
			out = append(out, e)
		}
	}
	if len(out) > 0 {
		if sh, err := os.ReadDir(s.shown()); err == nil {
			marked := map[string]bool{}
			for _, d := range sh {
				marked[d.Name()] = true
			}
			for i := range out {
				out[i].Redelivered = marked[strconv.FormatUint(out[i].Seq, 10)]
			}
		}
	}
	if len(out) > 0 {
		if ls, err := os.ReadDir(s.listed()); err == nil {
			marked := map[string]bool{}
			for _, d := range ls {
				marked[d.Name()] = true
			}
			for i := range out {
				out[i].Listed = marked[strconv.FormatUint(out[i].Seq, 10)]
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out, nil
}

func seqOf(name string) (uint64, bool) {
	n, ok := strings.CutSuffix(name, ".json")
	if !ok {
		return 0, false
	}
	seq, err := strconv.ParseUint(n, 10, 64)
	return seq, err == nil
}

// MarkShown records that a hook is about to show seqs, before it writes them out. If the hook is then killed, the
// entries stay waiting and the next Waiting flags them Redelivered. A seq that cannot be marked stops the rest; the
// caller shows anyway (at-least-once wins) and reports.
func (s Spool) MarkShown(seqs ...uint64) error {
	if err := os.MkdirAll(s.shown(), 0o700); err != nil {
		return err
	}
	for _, seq := range seqs {
		if err := os.WriteFile(s.shownFile(seq), nil, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// Listed marks seq as listed in a digest and not seen: its listed mark is written (atomically: a temporary file,
// renamed) and its shown mark removed, so a later run does not flag it re-delivered. Its file stays, so a later hook
// can show it in full; it is not drained, so serve does not ack it (ADR 0046).
func (s Spool) Listed(seq uint64) error {
	if err := os.MkdirAll(s.listed(), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(s.listed(), ".put-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // a no-op once renamed
	err = f.Chmod(0o600)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, s.listedFile(seq)); err != nil {
		return err
	}
	if err := os.Remove(s.shownFile(seq)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// IsListed reports whether a hook has listed seq in a digest and nothing has drained it since.
func (s Spool) IsListed(seq uint64) bool {
	_, err := os.Stat(s.listedFile(seq))
	return err == nil
}

// Drained marks seq shown: its tombstone is written, then its file removed, then its shown mark. Tombstone first, so
// a crash in between leaves the message both waiting and drained, which shows it again (flagged Redelivered, as the
// mark is still there) rather than losing it.
func (s Spool) Drained(seq uint64) error {
	t := filepath.Join(s.drained(), strconv.FormatUint(seq, 10))
	if err := os.WriteFile(t, nil, 0o600); err != nil {
		return err
	}
	if err := os.Remove(s.file(seq)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.Remove(s.shownFile(seq)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.Remove(s.listedFile(seq)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// IsDrained reports whether a hook has shown seq and serve has not yet acked it.
func (s Spool) IsDrained(seq uint64) bool {
	_, err := os.Stat(filepath.Join(s.drained(), strconv.FormatUint(seq, 10)))
	return err == nil
}

// Acked forgets seq once serve has acked it: its tombstone, its shown mark and any waiting file (a redelivery spooled
// again).
func (s Spool) Acked(seq uint64) error {
	for _, p := range []string{filepath.Join(s.drained(), strconv.FormatUint(seq, 10)), s.file(seq), s.shownFile(seq), s.listedFile(seq)} {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("spool: %w", err)
		}
	}
	return nil
}

// Touch records a hook run (the marker's mtime).
func (s Spool) Touch() error {
	p := filepath.Join(s.Dir, marker)
	now := time.Now()
	if err := os.Chtimes(p, now, now); err == nil {
		return nil
	}
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		return err
	}
	// A new file's mtime is the filesystem's coarse clock, which can read earlier than a time.Now() taken just
	// before: set it exactly, so a hook run after serve's start is always seen as such.
	return os.Chtimes(p, now, now)
}

// Alive records a hook run by one of this session's own subagents (never a drain): hooks work in this session, though
// the main session is busy in a subagent. The marker's mtime is the run.
func (s Spool) Alive() error {
	p := filepath.Join(s.Dir, aliveFile)
	now := time.Now()
	if err := os.Chtimes(p, now, now); err == nil {
		return nil
	}
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		return err
	}
	return os.Chtimes(p, now, now)
}

// AliveSince reports whether a subagent's hook has run at or after t.
func (s Spool) AliveSince(t time.Time) bool {
	fi, err := os.Stat(filepath.Join(s.Dir, aliveFile))
	return err == nil && !fi.ModTime().Before(t)
}

// LastHook is the time of the last hook run recorded in the spool, and whether there is one.
func (s Spool) LastHook() (time.Time, bool) {
	fi, err := os.Stat(filepath.Join(s.Dir, marker))
	if err != nil {
		return time.Time{}, false
	}
	return fi.ModTime(), true
}

// HookedSince reports whether a hook has run at or after t.
func (s Spool) HookedSince(t time.Time) bool {
	fi, err := os.Stat(filepath.Join(s.Dir, marker))
	return err == nil && !fi.ModTime().Before(t)
}

// Own binds the spool to the owner token of the serve that holds the lease, so a hook of another session with the
// same endpoint name leaves it alone (codex H1 on #21, round 3). It writes the legacy .session text and removes any
// .owner record; "" removes the binding. Atomic: a hook never reads a half-written id.
func (s Spool) Own(legacy string) error { return s.OwnRecord(legacy, "") }

// OwnRecord is Own with the identity record (JSON {pid, session, launch}, ADR 0040) in .owner. .session keeps the
// legacy "pid:N" text, so a hook built before the record existed still enforces ancestry instead of reading an
// unknown owner as "accept all". The record is written first, then .session; each write is atomic and the caller
// holds the spool lock. An empty record removes .owner.
func (s Spool) OwnRecord(legacy, record string) error {
	// A new lease holder starts clean: a refusal recorded against the previous holder is not its.
	if err := os.Remove(filepath.Join(s.Dir, refusedFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.Remove(filepath.Join(s.Dir, toldFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	// The new holder's own waiting record (a serve that waited, then bound) is done; so is any other for its pid.
	var rec struct {
		PID int `json:"pid"`
	}
	if json.Unmarshal([]byte(record), &rec) == nil && rec.PID > 0 {
		if err := s.ClearWaiter(rec.PID); err != nil {
			return err
		}
	}
	if err := s.writeOrRemove(ownerFile, record); err != nil {
		return err
	}
	return s.writeOrRemove(sessionFile, legacy)
}

// LegacyToken is the "pid:N" text an older hook understands, for a record: N is the record's pid, else this
// process's parent (serve's parent, the Claude process). The one place that text is made.
func LegacyToken(record string) string {
	var r struct {
		PID int `json:"pid"`
	}
	if json.Unmarshal([]byte(record), &r) != nil || r.PID <= 0 {
		r.PID = os.Getppid()
	}
	return "pid:" + strconv.Itoa(r.PID)
}

func (s Spool) writeOrRemove(name, content string) error {
	p := filepath.Join(s.Dir, name)
	if content == "" {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	f, err := os.CreateTemp(s.Dir, ".own-")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.WriteString(content)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, p)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

// OwnerUnreadable is what Owner returns for an .owner that is present but unreadable, empty or not JSON. It starts with "{" and
// is not valid JSON, so the hook's Match refuses it: a damaged record is never read as "no owner".
const OwnerUnreadable = "{owner record unreadable"

// Owner is what a hook matches against: the JSON record in .owner when there is one, else the legacy .session text,
// "" when neither exists (an older serve, or none).
func (s Spool) Owner() string {
	b, err := os.ReadFile(filepath.Join(s.Dir, ownerFile))
	switch {
	case err == nil:
		if json.Valid(b) {
			return strings.TrimSpace(string(b))
		}
		return OwnerUnreadable
	case !errors.Is(err, fs.ErrNotExist):
		return OwnerUnreadable
	}
	b, err = os.ReadFile(filepath.Join(s.Dir, sessionFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Refusal is what a hook that matched none of the ownership rules leaves in .refused.
type Refusal struct {
	Time      time.Time `json:"time"`
	Why       string    `json:"why"`
	Session   string    `json:"session,omitempty"` // the hook's session id
	Ancestors []int     `json:"ancestors,omitempty"`
}

// NoteRefused records a refused hook in .refused, only if there is none or it is older than 60 s. Call it under Lock.
func (s Spool) NoteRefused(r Refusal) error {
	p := filepath.Join(s.Dir, refusedFile)
	if fi, err := os.Stat(p); err == nil && time.Since(fi.ModTime()) < refusedEvery {
		return nil
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o600)
}

// Refused is the last recorded refusal: when (the file's mtime), why, and whether there is one.
func (s Spool) Refused() (at time.Time, why string, ok bool) {
	p := filepath.Join(s.Dir, refusedFile)
	fi, err := os.Stat(p)
	if err != nil {
		return time.Time{}, "", false
	}
	var r Refusal
	if b, err := os.ReadFile(p); err == nil {
		_ = json.Unmarshal(b, &r)
	}
	return fi.ModTime(), r.Why, true
}

// Lock takes the spool's exclusive advisory lock (the file .lock, blocking) and returns the function that releases
// it. Serve holds it while it rebinds and re-owns the spool, and a hook holds it across its owner check, read and
// drained marks, so a hook that passed the owner check can never drain mail a new lease holder spooled after it
// (codex H1 on #21, round 7).
func (s Spool) Lock() (unlock func(), err error) {
	f, err := os.OpenFile(filepath.Join(s.Dir, ".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

// Has reports whether seq's waiting file is present and parses (what Waiting would list).
func (s Spool) Has(seq uint64) bool {
	b, err := os.ReadFile(s.file(seq))
	if err != nil {
		return false
	}
	var e Entry
	return json.Unmarshal(b, &e) == nil && e.Seq == seq
}

// Told reports whether the in-session notice for key (a short hash of one copy of a session) was already printed.
// Call it under Lock.
func (s Spool) Told(key string) bool {
	b, err := os.ReadFile(filepath.Join(s.Dir, toldFile))
	return err == nil && strings.TrimSpace(string(b)) == key
}

// NoteTold records that the notice for key was printed, replacing any earlier key. Call it under Lock.
func (s Spool) NoteTold(key string) error { return s.writeOrRemove(toldFile, key) }

// WaiterRecord is what a serve that waits for the lease leaves in .waiting.<pid> (ADR 0043): its parent Claude
// process and that process's session id.
type WaiterRecord struct {
	PID     int    `json:"pid"`
	Session string `json:"session,omitempty"`
}

// NoteWaiter records a serve waiting for the lease, atomically, in .waiting.<pid>. Call it under Lock.
func (s Spool) NoteWaiter(r WaiterRecord) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return s.writeOrRemove(waiterPref+strconv.Itoa(r.PID), string(b))
}

// ClearWaiter removes the waiting record of pid, and every other .waiting.* that names it. Call it under Lock.
func (s Spool) ClearWaiter(pid int) error {
	des, err := os.ReadDir(s.Dir)
	if err != nil {
		return err
	}
	for _, d := range des {
		if !strings.HasPrefix(d.Name(), waiterPref) {
			continue
		}
		rec, ok := s.readWaiter(d.Name())
		if d.Name() != waiterPref+strconv.Itoa(pid) && !(ok && rec.PID == pid) {
			continue
		}
		if err := os.Remove(filepath.Join(s.Dir, d.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (s Spool) readWaiter(name string) (WaiterRecord, bool) {
	b, err := os.ReadFile(filepath.Join(s.Dir, name))
	var r WaiterRecord
	if err != nil || json.Unmarshal(b, &r) != nil || r.PID <= 0 {
		return r, false
	}
	return r, true
}

// Waiters lists the recorded waiting serves whose process is still alive. A junk file or a dead pid is skipped.
func (s Spool) Waiters() []WaiterRecord {
	des, err := os.ReadDir(s.Dir)
	if err != nil {
		return nil
	}
	var out []WaiterRecord
	for _, d := range des {
		if !strings.HasPrefix(d.Name(), waiterPref) || !d.Type().IsRegular() {
			continue
		}
		if r, ok := s.readWaiter(d.Name()); ok {
			if err := syscall.Kill(r.PID, 0); err == nil || errors.Is(err, syscall.EPERM) {
				out = append(out, r)
			}
		}
	}
	return out
}
