// Package ledger is the local, append-only record of what serve has handed to the host, kept next to the spool (ADR
// 0109). It is informational: a failed write never holds an ack, and nothing but `mash status` reads it.
//
// The file holds one JSON object per line:
//
//	{"gen":"<stream identity>","t":"<RFC3339>","kind":"presented","seqs":[[1,57],[59,60]]}
//
// gen is the identity the spool binds a stream to (name@created), because sequences restart at 1 when a stream is
// recreated. kind is presented (the hook wrote the message to stdout, or the read tool returned it), refused (mail
// dropped by design: a forged or malformed sender header), duplicate (acked without being shown again; dup_of names the
// sequence it repeats when that is known) or channel (acked after a channel write: delivered, not proven shown). seqs
// are ranges with contiguous runs collapsed. A snapshot line (kind "snapshot") replaces the folded history when the
// file passes CompactBytes.
//
// Writes are whole lines appended with O_APPEND and synced, so a reader never sees a half line except a torn last
// one after a crash, which Read skips and counts. A line is kept under MaxLineBytes (the pipe-atomic size) where it can
// be; O_APPEND makes each write(2) land at the end of the file on a local file system, so two writers do not
// interleave inside a line, but a long line is not guaranteed whole by POSIX beyond that size.
package ledger

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// compactAt is CompactBytes; a test lowers it.
var compactAt int64 = CompactBytes

const (
	// File is the ledger's name in the endpoint's spool directory. The spool scans parse only "<seq>.json" and the
	// files it names itself, so this is never mistaken for an entry.
	File = "presented.log"

	// CompactBytes is the file size past which a write folds the history into one snapshot line per generation.
	CompactBytes = 1 << 20
	// MaxLineBytes is the size a line is kept under where it can be (POSIX PIPE_BUF).
	MaxLineBytes = 4096
	// maxRangesPerLine keeps a line under MaxLineBytes: a range is at most about 42 bytes in JSON.
	maxRangesPerLine = 80

	// UnknownGen is the generation of records written when the stream's identity could not be read.
	UnknownGen = "?"
)

// Kinds of record.
const (
	Presented = "presented"
	Refused   = "refused"
	Duplicate = "duplicate"
	Channel   = "channel"
	snapshot  = "snapshot"
)

// Record is one line.
type Record struct {
	Gen   string      `json:"gen"`
	T     time.Time   `json:"t"`
	Kind  string      `json:"kind"`
	Seqs  [][2]uint64 `json:"seqs,omitempty"`
	DupOf []uint64    `json:"dup_of,omitempty"`

	// Snapshot fields: the folded generation.
	First time.Time   `json:"first,omitempty"`
	P     [][2]uint64 `json:"p,omitempty"`
	R     [][2]uint64 `json:"r,omitempty"`
	C     [][2]uint64 `json:"c,omitempty"`
	D     [][2]uint64 `json:"d,omitempty"`  // duplicates known to repeat something shown
	Du    [][]uint64  `json:"du,omitempty"` // other duplicates: [seq, target...]
	Torn  int         `json:"torn,omitempty"`
}

// Ranges is a sorted set of disjoint, non-adjacent inclusive ranges.
type Ranges [][2]uint64

// Add inserts lo..hi.
func (r *Ranges) Add(lo, hi uint64) {
	if hi < lo {
		return
	}
	s := *r
	if n := len(s); n == 0 || lo > s[n-1][1]+1 && s[n-1][1] != ^uint64(0) {
		*r = append(s, [2]uint64{lo, hi})
		return
	}
	i := sort.Search(len(s), func(i int) bool { return s[i][1]+1 >= lo || s[i][1] == ^uint64(0) })
	j := i
	for j < len(s) && s[j][0] <= hi+1 {
		lo, hi = min(lo, s[j][0]), max(hi, s[j][1])
		j++
	}
	out := append(append(Ranges{}, s[:i]...), [2]uint64{lo, hi})
	*r = append(out, s[j:]...)
}

// Has reports whether seq is in the set.
func (r Ranges) Has(seq uint64) bool {
	i := sort.Search(len(r), func(i int) bool { return r[i][1] >= seq })
	return i < len(r) && r[i][0] <= seq
}

// Count is how many sequences the set holds.
func (r Ranges) Count() uint64 {
	var n uint64
	for _, x := range r {
		n += x[1] - x[0] + 1
	}
	return n
}

// CountIn is how many sequences of lo..hi the set holds.
func (r Ranges) CountIn(lo, hi uint64) uint64 {
	var n uint64
	for _, x := range r {
		a, b := max(x[0], lo), min(x[1], hi)
		if a <= b {
			n += b - a + 1
		}
	}
	return n
}

// Min is the lowest sequence, false when empty.
func (r Ranges) Min() (uint64, bool) {
	if len(r) == 0 {
		return 0, false
	}
	return r[0][0], true
}

// Minus is the sequences of r that are in none of others.
func (r Ranges) Minus(others ...Ranges) Ranges {
	cur := append(Ranges{}, r...)
	for _, o := range others {
		var next Ranges
		for _, x := range cur {
			lo := x[0]
			done := false
			for _, y := range o {
				if y[1] < lo {
					continue
				}
				if y[0] > x[1] {
					break
				}
				if y[0] > lo {
					next = append(next, [2]uint64{lo, y[0] - 1})
				}
				if y[1] >= x[1] {
					done = true
					break
				}
				lo = y[1] + 1
			}
			if !done {
				next = append(next, [2]uint64{lo, x[1]})
			}
		}
		cur = next
	}
	return cur
}

func union(rs ...Ranges) Ranges {
	var out Ranges
	for _, r := range rs {
		for _, x := range r {
			out.Add(x[0], x[1])
		}
	}
	return out
}

// Gen is the folded record of one stream generation.
type Gen struct {
	P, R, C Ranges              // presented, refused, channel-acked
	D       Ranges              // duplicates known to repeat something shown
	Dups    map[uint64][]uint64 // other duplicates by sequence, with the sequences they repeat when known
	First   time.Time
}

func (g *Gen) dups(seq uint64) []uint64 { return g.Dups[seq] }

// resolved is the duplicate sequences whose every named original is presented or channel-acked.
func (g *Gen) resolved() Ranges {
	var out Ranges
	for seq, of := range g.Dups {
		if len(of) == 0 {
			continue
		}
		ok := true
		for _, t := range of {
			if !g.P.Has(t) && !g.C.Has(t) {
				ok = false
			}
		}
		if ok {
			out.Add(seq, seq)
		}
	}
	return out
}

// Accounted is every sequence the ledger can explain: presented, refused, channel-acked, or a duplicate of one that was.
// A duplicate that names no original, or an original the ledger does not hold, explains nothing: a loss must not look
// like a duplicate.
func (g *Gen) Accounted() Ranges { return union(g.P, g.R, g.C, g.D, g.resolved()) }

// Base is the lowest sequence the ledger covers for this generation; below it nothing is claimed. False when empty.
func (g *Gen) Base() (uint64, bool) {
	var b uint64
	found := false
	for _, r := range []Ranges{g.P, g.R, g.C, g.D} {
		if m, ok := r.Min(); ok && (!found || m < b) {
			b, found = m, true
		}
	}
	for seq := range g.Dups {
		if !found || seq < b {
			b, found = seq, true
		}
	}
	return b, found
}

// Lost is the sequences from Base up to firstSeq-1 the ledger does not account for: the stream no longer holds them,
// so they can never be delivered or recorded now. Before is how many evicted sequences lie below Base, which the
// ledger says nothing about.
func (g *Gen) Lost(firstSeq uint64) (lost, before uint64) {
	b, ok := g.Base()
	if !ok || firstSeq <= 1 {
		if firstSeq > 1 {
			return 0, firstSeq - 1
		}
		return 0, 0
	}
	before = min(b, firstSeq) - 1
	if firstSeq <= b {
		return 0, before
	}
	hi := firstSeq - 1
	return (hi - b + 1) - g.Accounted().CountIn(b, hi), before
}

// Counts are the sequences of the generation by their strongest record: presented, else refused, else channel-acked,
// else duplicate.
func (g *Gen) Counts() (presented, refused, duplicate, channel uint64) {
	presented = g.P.Count()
	refused = g.R.Minus(g.P).Count()
	channel = g.C.Minus(g.P, g.R).Count()
	d := union(g.D)
	for seq := range g.Dups {
		d.Add(seq, seq)
	}
	duplicate = d.Minus(g.P, g.R, g.C).Count()
	return
}

// State is the whole ledger folded in memory.
type State struct {
	Gens  map[string]*Gen
	First time.Time // the time of the earliest record
	Torn  int       // lines that could not be parsed
	Lines int
}

func (s *State) gen(id string) *Gen {
	g := s.Gens[id]
	if g == nil {
		g = &Gen{Dups: map[uint64][]uint64{}}
		s.Gens[id] = g
	}
	return g
}

func (s *State) apply(r Record) {
	g := s.gen(r.Gen)
	first := r.T
	switch r.Kind {
	case Presented, Refused, Channel:
		dst := map[string]*Ranges{Presented: &g.P, Refused: &g.R, Channel: &g.C}[r.Kind]
		for _, x := range r.Seqs {
			dst.Add(x[0], x[1])
		}
	case Duplicate:
		for _, x := range r.Seqs {
			for q := x[0]; q <= x[1] && q-x[0] < 1<<20; q++ {
				g.Dups[q] = union1(g.Dups[q], r.DupOf)
			}
		}
	case snapshot:
		first = r.First
		for _, p := range []struct {
			dst *Ranges
			src [][2]uint64
		}{{&g.P, r.P}, {&g.R, r.R}, {&g.C, r.C}, {&g.D, r.D}} {
			for _, x := range p.src {
				p.dst.Add(x[0], x[1])
			}
		}
		for _, d := range r.Du {
			if len(d) > 0 {
				g.Dups[d[0]] = union1(g.Dups[d[0]], d[1:])
			}
		}
		s.Torn += r.Torn
	}
	if !first.IsZero() && (g.First.IsZero() || first.Before(g.First)) {
		g.First = first
	}
	if !first.IsZero() && (s.First.IsZero() || first.Before(s.First)) {
		s.First = first
	}
}

func union1(a, b []uint64) []uint64 {
	out := append([]uint64(nil), a...)
	for _, x := range b {
		found := false
		for _, y := range out {
			if x == y {
				found = true
			}
		}
		if !found {
			out = append(out, x)
		}
	}
	return out
}

// Read folds the ledger at path. A missing file is an empty ledger. A line that does not parse (a torn last write) is
// skipped and counted, never fatal.
func Read(path string) (State, error) {
	st := State{Gens: map[string]*Gen{}}
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	defer f.Close()
	rd := bufio.NewReader(f)
	for {
		line, err := rd.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var r Record
			if json.Unmarshal(line, &r) != nil || r.Kind == "" || (r.Kind != snapshot && len(r.Seqs) == 0) {
				st.Torn++
			} else {
				st.Lines++
				st.apply(r)
			}
		}
		if err != nil {
			break
		}
	}
	return st, nil
}

// Total is the counts summed over every generation.
func (s State) Total() (presented, refused, duplicate, channel uint64) {
	for _, g := range s.Gens {
		p, r, d, c := g.Counts()
		presented, refused, duplicate, channel = presented+p, refused+r, duplicate+d, channel+c
	}
	return
}

// Ledger appends to one file. The zero value and nil are inert (every method returns nil).
type Ledger struct {
	path string
	mu   sync.Mutex
	// tail is true once the file's end is known to be a line end, so a torn last line from a crash is closed once.
	tail bool
	skip int64 // size below which compaction is not tried again (it failed at that size)
}

// New is the ledger kept at path.
func New(path string) *Ledger { return &Ledger{path: path} }

// Path is the file's path.
func (l *Ledger) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

// Note appends one record of kind for seqs of generation gen and syncs it, one line per MaxLineBytes of ranges.
func (l *Ledger) Note(gen, kind string, seqs ...uint64) error {
	return l.write(Records(gen, kind, time.Now(), seqs, nil))
}

// NoteDuplicate records seq as acked without being shown again; of is the sequence it repeats, 0 when unknown.
func (l *Ledger) NoteDuplicate(gen string, seq, of uint64) error {
	var dup []uint64
	if of != 0 {
		dup = []uint64{of}
	}
	return l.write(Records(gen, Duplicate, time.Now(), []uint64{seq}, dup))
}

// Records is the lines of one pass: seqs sorted and collapsed to ranges, split so each line stays short.
func Records(gen, kind string, t time.Time, seqs []uint64, dupOf []uint64) []Record {
	s := append([]uint64(nil), seqs...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	var rs Ranges
	for _, q := range s {
		rs.Add(q, q)
	}
	var out []Record
	for len(rs) > 0 {
		n := min(len(rs), maxRangesPerLine)
		out = append(out, Record{Gen: gen, T: t.UTC().Truncate(time.Second), Kind: kind, Seqs: append([][2]uint64(nil), rs[:n]...), DupOf: dupOf})
		rs = rs[n:]
	}
	return out
}

func (l *Ledger) write(recs []Record) error {
	if l == nil || l.path == "" || len(recs) == 0 {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(l.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if !l.tail {
		if err := closeTornTail(f, l.path); err != nil {
			return err
		}
		l.tail = true
	}
	for _, r := range recs {
		b, err := json.Marshal(r)
		if err != nil {
			return err
		}
		if _, err := f.Write(append(b, '\n')); err != nil {
			l.tail = false
			return err
		}
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if fi, err := f.Stat(); err == nil && fi.Size() > compactAt && fi.Size() > l.skip {
		if err := compact(l.path); err != nil {
			l.skip = fi.Size() * 2 // do not retry a failing compaction on every pass
		}
		l.tail = false
	}
	return nil
}

// closeTornTail ends a last line a crash left without its newline, so the next record starts on a line of its own.
func closeTornTail(f *os.File, path string) error {
	rf, err := os.Open(path)
	if err != nil {
		return err
	}
	defer rf.Close()
	fi, err := rf.Stat()
	if err != nil || fi.Size() == 0 {
		return err
	}
	var last [1]byte
	if _, err := rf.ReadAt(last[:], fi.Size()-1); err != nil {
		return err
	}
	if last[0] == '\n' {
		return nil
	}
	_, err = f.Write([]byte{'\n'})
	return err
}

// compact rewrites path as one snapshot line per generation, through a temporary file and a rename. Counts are kept.
func compact(path string) error {
	st, err := Read(path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ledger-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // a no-op once renamed
	err = tmp.Chmod(0o600)
	ids := make([]string, 0, len(st.Gens))
	for id := range st.Gens {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for i, id := range ids {
		if err != nil {
			break
		}
		r := Snapshot(id, st.Gens[id])
		if i == 0 {
			r.Torn = st.Torn
		}
		var b []byte
		if b, err = json.Marshal(r); err == nil {
			_, err = tmp.Write(append(b, '\n'))
		}
	}
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}

// Snapshot is the folded generation as one record; duplicates whose originals are known to have been shown fold into D.
func Snapshot(id string, g *Gen) Record {
	r := Record{Gen: id, T: time.Now().UTC().Truncate(time.Second), Kind: snapshot, First: g.First,
		P: g.P, R: g.R, C: g.C, D: union(g.D, g.resolved())}
	var seqs []uint64
	res := g.resolved()
	for seq := range g.Dups {
		if !res.Has(seq) {
			seqs = append(seqs, seq)
		}
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	for _, q := range seqs {
		r.Du = append(r.Du, append([]uint64{q}, g.dups(q)...))
	}
	return r
}

// Summary is what status reports of a ledger.
type Summary struct {
	Presented, Refused, Duplicate, Channel uint64
	First                                  time.Time
	Torn                                   int
	Empty                                  bool
}

// Summarize is the counts of the whole ledger.
func (s State) Summarize() Summary {
	p, r, d, c := s.Total()
	return Summary{Presented: p, Refused: r, Duplicate: d, Channel: c, First: s.First, Torn: s.Torn, Empty: len(s.Gens) == 0}
}

// Lead is the first part of the delivery line of status.
func (s Summary) Lead() string {
	if s.Empty {
		return "delivery: nothing presented yet (the local ledger is empty)"
	}
	since := "an unknown time"
	if !s.First.IsZero() {
		since = s.First.UTC().Format(time.RFC3339)
	}
	out := fmt.Sprintf("delivery: %d presented, %d refused, %d duplicate, %d channel-acked (not proven) since %s", s.Presented, s.Refused, s.Duplicate, s.Channel, since)
	return out
}

// Note is the closing sentence of the delivery line: what presented means, and what the ledger cannot say.
const Note = "presented means handed to the host's input, not that the model read it; the ledger is local to this machine"

// Text words the delivery line: lead, then loss (already worded), then the note and anything the ledger itself reports.
func (s Summary) Text(loss string, writeErr string) string {
	out := s.Lead()
	if loss != "" {
		out += "; " + loss
	}
	if s.Torn > 0 {
		out += "; " + strconv.Itoa(s.Torn) + " unreadable ledger line(s) skipped"
	}
	if writeErr != "" {
		out += "; LEDGER WRITE FAILED: " + strings.TrimSpace(writeErr) + " (mail is still acked)"
	}
	return out + ". " + Note
}

// Unreadable words the delivery line for a ledger file that cannot be read.
func Unreadable(err error, writeErr string) string {
	out := fmt.Sprintf("delivery: the presented ledger cannot be read: %v", err)
	if writeErr != "" {
		out += "; LEDGER WRITE FAILED: " + strings.TrimSpace(writeErr) + " (mail is still acked)"
	}
	return out
}

// StreamState is what status knows of one inbox stream: the ledger's record of its current generation (nil when there is
// none) and the stream's first sequence, below which mail is gone for good.
type StreamState struct {
	Gen      *Gen
	FirstSeq uint64
}

// LossText words the loss part of the delivery line from the streams as they are now. With no stream state at all it
// says the loss was not checked and claims nothing.
func LossText(streams []StreamState) string {
	if len(streams) == 0 {
		return "loss: not checked (the inbox could not be read)"
	}
	var lost, before uint64
	for _, s := range streams {
		g := s.Gen
		if g == nil {
			g = &Gen{} // no record for this generation: everything evicted is below the ledger
		}
		l, b := g.Lost(s.FirstSeq)
		lost, before = lost+l, before+b
	}
	out := "loss: none known"
	if lost > 0 {
		out = fmt.Sprintf("loss: at least %d unproven (evicted from the inbox with no record that they were handed to the host)", lost)
	}
	if before > 0 {
		out += fmt.Sprintf("; at least %d more may be lost before the ledger started", before)
	}
	return out
}
