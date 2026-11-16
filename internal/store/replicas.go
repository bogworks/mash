package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/bogworks/mash/internal/sap"
)

// Replicas decides the replica count of mash-owned streams and buckets (ADR 0141): setting is the replicas setting
// ("auto", "1" or "3"; empty is auto) and servers the declared server count (0 when none is declared). auto is 3 when
// at least 3 servers are declared and 1 otherwise; 3 needs at least 3 declared servers; every other value is refused,
// 5 included (NATS allows it, mash has not tested it and does not offer it). It is the only place the number is decided.
func Replicas(setting string, servers int) (int, error) {
	switch s := strings.ToLower(strings.TrimSpace(setting)); s {
	case "", "auto":
		if servers >= 3 {
			return 3, nil
		}
		return 1, nil
	case "1":
		return 1, nil
	case "3":
		if servers < 3 {
			return 0, fmt.Errorf("mash: replicas 3 needs at least 3 servers and %s are declared: declare them (config.json \"servers\" or MASH_SERVERS), or use replicas auto or 1", serversText(servers))
		}
		return 3, nil
	default:
		extra := ""
		if s == "5" {
			extra = " (5 is allowed by NATS but mash has not tested it and does not offer it)"
		}
		return 0, fmt.Errorf("mash: replicas %q is not supported%s: the values are auto, 1 and 3", setting, extra)
	}
}

func serversText(n int) string {
	if n < 1 {
		return "none"
	}
	return strconv.Itoa(n)
}

// replicaCount is the replica count of a stream configuration: 0 (unset) is 1.
func replicaCount(c jetstream.StreamConfig) int {
	if c.Replicas < 1 {
		return 1
	}
	return c.Replicas
}

// Step is one line of RaiseReplicas's progress: an object (a stream name) and what became of it. Action is "skip"
// (already at or above the target, or not present), "raise" (the update was made), "wait" (waiting for the new
// replicas, Lag is the largest lag seen) or "done" (every replica reports current).
type Step struct {
	Object   string
	Action   string
	From, To int
	Lag      uint64
	Note     string
}

// RaiseWait and RaisePoll bound how long RaiseReplicas waits for the replicas of one object to be current, and how
// often it asks. They are variables so tests need not wait for the full time.
var (
	RaiseWait = 60 * time.Second
	RaisePoll = 250 * time.Millisecond
)

// raiseOrder is the mash-owned objects with a fixed name, in the order they are raised. The policy ledger is not
// listed: this tree creates no such bucket.
var raiseOrder = []string{
	"KV_" + DirectoryBucket,
	LeaseStreamName,
	IssuedStreamName,
	AdoptionStreamName,
	"KV_" + RetiredBucket,
}

// RaiseReplicas raises every mash-owned object to target replicas, in place and while it serves (ADR 0141): the
// directory, lease, issue-record, adoption and retired-name buckets in that order, then every inbox stream. For each
// object it reads the stream's configuration; at or above target it does nothing; below it, it updates the stream
// with only Replicas changed (every other field is written back as read; the lease bucket keeps rollup off, and no
// key of any bucket is written), then waits, up to RaiseWait, until every replica reports current before the next
// object. progress, when not nil, hears each step.
//
// It is idempotent and resumable (a partial run leaves R1 and R3 objects side by side, which is safe), never lowers,
// and refuses a target mash does not support or above the declared server count (servers). Before it changes
// anything it checks that the cluster can place target replicas, and changes nothing when it cannot.
func RaiseReplicas(ctx context.Context, js jetstream.JetStream, target, servers int, progress func(Step)) error {
	if progress == nil {
		progress = func(Step) {}
	}
	if _, err := Replicas(strconv.Itoa(target), servers); err != nil {
		return fmt.Errorf("%w (the target may not exceed the declared server count %d)", err, servers)
	}
	objects, err := raiseObjects(ctx, js)
	if err != nil {
		return err
	}
	if err := checkPlacement(ctx, js, target); err != nil {
		return err
	}
	for _, name := range objects {
		if err := raiseOne(ctx, js, name, target, progress); err != nil {
			return err
		}
	}
	return nil
}

// raiseObjects is the fixed objects (those that exist) followed by every inbox stream, sorted by the server's order.
func raiseObjects(ctx context.Context, js jetstream.JetStream) ([]string, error) {
	have := map[string]bool{}
	var inboxes []string
	names := js.StreamNames(ctx)
	for n := range names.Name() {
		have[n] = true
		if strings.HasPrefix(n, sap.InboxStream("")) {
			inboxes = append(inboxes, n)
		}
	}
	if err := names.Err(); err != nil {
		return nil, fmt.Errorf("mash: list streams: %w", err)
	}
	var out []string
	for _, n := range raiseOrder {
		if have[n] {
			out = append(out, n)
		}
	}
	sort.Strings(inboxes)
	return append(out, inboxes...), nil
}

// probeStreamPrefix names the short-lived memory stream checkPlacement makes. It holds nothing and is deleted at once.
const probeStreamPrefix = "mash-replica-probe-"

// PlacementWait bounds how long checkPlacement waits for the probe's replicas to be current. It is a variable so
// tests need not wait for the full time.
var PlacementWait = 10 * time.Second

// checkPlacement makes a throwaway memory stream of target replicas, waits briefly until every replica is current,
// and deletes it. A cluster that cannot place it (too few JetStream nodes up, or no room), or places it with a server
// that is offline (JetStream keeps a down server as a peer and would raise to it), refuses, and RaiseReplicas has then
// changed nothing. A refusal of the probe that is not about placement (the connection's rights) does not stop the
// raise: the first update is itself refused or waits when the cluster cannot place it.
func checkPlacement(ctx context.Context, js jetstream.JetStream, target int) error {
	if target < 2 {
		return nil
	}
	name := probeStreamPrefix + strconv.FormatInt(time.Now().UnixNano(), 36)
	s, err := js.CreateStream(ctx, jetstream.StreamConfig{
		Name: name, Subjects: []string{"mash.probe." + name}, Storage: jetstream.MemoryStorage,
		Replicas: target, MaxMsgs: 1, MaxBytes: 1 << 20, Discard: jetstream.DiscardOld,
	})
	if err != nil {
		var ae *jetstream.APIError
		if errors.As(err, &ae) && (ae.ErrorCode == 10005 || ae.ErrorCode == 10023 || ae.ErrorCode == 10074) { // no peers, insufficient resources, not clustered
			return fmt.Errorf("mash: the cluster cannot place %d replicas now (%s): bring every JetStream server up, or check they have room; nothing was changed", target, ae.Description)
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return fmt.Errorf("mash: could not check the cluster can place %d replicas: %w; nothing was changed", target, err)
		}
		if strings.Contains(strings.ToLower(err.Error()), "insufficient") || strings.Contains(strings.ToLower(err.Error()), "no suitable peers") {
			return fmt.Errorf("mash: the cluster cannot place %d replicas now (%v): bring every JetStream server up, or check they have room; nothing was changed", target, err)
		}
		return nil
	}
	defer func() { _ = js.DeleteStream(ctx, name) }()
	deadline := time.Now().Add(PlacementWait)
	for {
		info, err := s.Info(ctx)
		if err != nil {
			return fmt.Errorf("mash: could not check the cluster can place %d replicas: %w; nothing was changed", target, err)
		}
		_, ok, why := replicasCurrent(info, target)
		if ok {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("mash: the cluster cannot serve %d replicas now (%s): a server is down or not caught up; bring every JetStream server up and run it again; nothing was changed", target, why)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(RaisePoll):
		}
	}
}

// raiseOne raises one stream.
func raiseOne(ctx context.Context, js jetstream.JetStream, name string, target int, progress func(Step)) error {
	s, err := js.Stream(ctx, name)
	if err != nil {
		if errors.Is(err, jetstream.ErrStreamNotFound) {
			progress(Step{Object: name, Action: "skip", To: target, Note: "not present"})
			return nil
		}
		return fmt.Errorf("mash: %s: %w", name, err)
	}
	info, err := s.Info(ctx)
	if err != nil {
		return fmt.Errorf("mash: %s: %w", name, err)
	}
	cfg := info.Config
	from := replicaCount(cfg)
	if from >= target {
		progress(Step{Object: name, Action: "skip", From: from, To: target, Note: fmt.Sprintf("already %d replica(s)", from)})
		return nil
	}
	if name == LeaseStreamName && cfg.AllowRollup {
		return ErrLeaseBucketRollup // the lease bucket is hardened by init; raising does not turn it on or off
	}
	cfg.Replicas = target // the one change; everything else is written back as read
	if _, err := js.UpdateStream(ctx, cfg); err != nil {
		return fmt.Errorf("mash: raise %s to %d replicas: %w", name, target, err)
	}
	progress(Step{Object: name, Action: "raise", From: from, To: target})
	if err := waitCurrent(ctx, s, name, target, progress); err != nil {
		return err
	}
	if name == LeaseStreamName {
		return VerifyLeaseBucket(ctx, js)
	}
	return nil
}

// waitCurrent waits until the stream has target-1 followers, none offline and all current. It is bounded by RaiseWait.
func waitCurrent(ctx context.Context, s jetstream.Stream, name string, target int, progress func(Step)) error {
	deadline := time.Now().Add(RaiseWait)
	var last string
	for {
		info, err := s.Info(ctx)
		if err != nil {
			return fmt.Errorf("mash: %s: %w", name, err)
		}
		lag, ok, why := replicasCurrent(info, target)
		if ok {
			progress(Step{Object: name, Action: "done", From: target, To: target})
			return nil
		}
		if why != last {
			progress(Step{Object: name, Action: "wait", To: target, Lag: lag, Note: why})
			last = why
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("mash: %s did not reach %d current replicas in %s (%s); the raise is safe to run again", name, target, RaiseWait, why)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(RaisePoll):
		}
	}
}

// replicasCurrent reports whether the stream's info shows target-1 followers that are all current, with the largest
// lag and a sentence for what is still missing.
func replicasCurrent(info *jetstream.StreamInfo, target int) (lag uint64, ok bool, why string) {
	if info.Cluster == nil {
		return 0, false, "no cluster information yet"
	}
	followers := info.Cluster.Replicas
	cur := 0
	for _, p := range followers {
		if p.Lag > lag {
			lag = p.Lag
		}
		if p.Current && !p.Offline {
			cur++
		}
	}
	if len(followers) >= target-1 && cur >= target-1 {
		return lag, true, ""
	}
	return lag, false, fmt.Sprintf("%d of %d replicas current, largest lag %d", cur+1, target, lag)
}

// ObjectReplicas is one mash-owned stream (a bucket's stream or an inbox) and its replica count.
type ObjectReplicas struct {
	Object   string
	Replicas int
}

// ReplicaState reads the replica count of every mash-owned object that exists, in RaiseReplicas's order. It changes
// nothing.
func ReplicaState(ctx context.Context, js jetstream.JetStream) ([]ObjectReplicas, error) {
	objects, err := raiseObjects(ctx, js)
	if err != nil {
		return nil, err
	}
	out := make([]ObjectReplicas, 0, len(objects))
	for _, n := range objects {
		s, err := js.Stream(ctx, n)
		if errors.Is(err, jetstream.ErrStreamNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("mash: %s: %w", n, err)
		}
		info, err := s.Info(ctx)
		if err != nil {
			return nil, fmt.Errorf("mash: %s: %w", n, err)
		}
		out = append(out, ObjectReplicas{Object: n, Replicas: replicaCount(info.Config)})
	}
	return out, nil
}

// ReplicaShortfallText is the one finding audit and issued check give when the configured replica count is want
// and the objects in short have fewer; "" when short is empty. It names the way out in words (the command is named
// elsewhere).
func ReplicaShortfallText(want int, short []string) string {
	if len(short) == 0 || want < 2 {
		return ""
	}
	list := short
	more := ""
	if len(list) > 3 {
		list, more = list[:3], fmt.Sprintf(" and %d more", len(short)-3)
	}
	return fmt.Sprintf("%d mash-owned object(s) have fewer than %d replicas (%s%s): one server down would stop writes; raise the replica count of mash-owned state to %d", len(short), want, strings.Join(list, ", "), more, want)
}
