package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/sap"
)

const whoUsage = "usage: mash who [--long] [--json] [--names] [--match TEXT] [--name SUB] [--owner O] [--role SUB] [--label K[=V]]... [--cap C]... [--online|--offline]"

// whoOutput is how `mash who` renders its result.
type whoOutput struct {
	Long, JSON, Names bool
	// Activity, when non-nil, adds the state column (text) or the activity object (JSON): what each online endpoint
	// reports about itself, by directory key. An online endpoint missing from it did not answer in time.
	Activity map[string]endpoint.PeerState
}

// ParseWhoFlags reads the arguments after `who`. A flag that takes a value accepts "--flag v" and "--flag=v".
func ParseWhoFlags(args []string) (endpoint.WhoFilter, whoOutput, error) {
	var f endpoint.WhoFilter
	var o whoOutput
	bad := func(format string, a ...any) (endpoint.WhoFilter, whoOutput, error) {
		return endpoint.WhoFilter{}, whoOutput{}, fmt.Errorf("%s\nmash who: %s", whoUsage, fmt.Sprintf(format, a...))
	}
	for i := 0; i < len(args); i++ {
		name, val, hasVal := strings.Cut(args[i], "=")
		switch name {
		case "--long", "--json", "--names", "--online", "--offline":
			if hasVal {
				return bad("%s takes no value", name)
			}
			switch name {
			case "--long":
				o.Long = true
			case "--json":
				o.JSON = true
			case "--names":
				o.Names = true
			case "--online":
				f.Online = true
			case "--offline":
				f.Offline = true
			}
		case "--match", "--name", "--owner", "--role", "--label", "--cap":
			if !hasVal {
				if i+1 >= len(args) {
					return bad("%s needs a value", name)
				}
				i++
				val = args[i]
			}
			switch name {
			case "--match":
				f.Match = val
			case "--name":
				f.Name = val
			case "--owner":
				f.Owner = val
			case "--role":
				f.Role = val
			case "--label":
				f.Labels = append(f.Labels, val)
			case "--cap":
				f.Caps = append(f.Caps, val)
			}
		default:
			return bad("unknown argument %q", args[i])
		}
	}
	if o.JSON && o.Names {
		return bad("--json and --names are exclusive")
	}
	if err := f.Validate(); err != nil {
		return bad("%v", err)
	}
	return f, o, nil
}

// whoNames renders one name per line.
func whoNames(ps []endpoint.Presence) string {
	names := make([]string, len(ps))
	for i, p := range ps {
		names[i] = config.Clean(p.Record.KeyOrName())
	}
	return strings.Join(names, "\n")
}

// whoLease and whoEntry are the stable --json schema (docs/operations.md).
type whoLease struct {
	Host    string `json:"host,omitempty"`
	Version string `json:"version,omitempty"`
}

type whoEntry struct {
	Name        string            `json:"name"`
	Identity    string            `json:"identity,omitempty"` // additive (ADR 0081): the canonical agent.owner.name, for a three-token endpoint only
	Owner       string            `json:"owner"`
	Online      bool              `json:"online"`
	LastSeen    string            `json:"last_seen,omitempty"`
	Role        string            `json:"role,omitempty"`
	Host        string            `json:"host,omitempty"`
	Note        string            `json:"note,omitempty"`
	Description string            `json:"description,omitempty"`
	Caps        []string          `json:"caps,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Lease       *whoLease         `json:"lease,omitempty"`
	Alias       *whoAlias         `json:"alias,omitempty"` // additive (ADR 0076): present only for a record that carries an alias
	// Activity is what the peer says it is doing, present only when it reported a state; ActivityTrust is then "claimed"
	// (the report is the peer's own and unsigned).
	Activity      *whoActivity `json:"activity,omitempty"`
	ActivityTrust string       `json:"activity_trust,omitempty"`
	// Delivery and DeliveryReason are the peer's own mash delivery-health report (the reply's delivery object: ok,
	// degraded, holding, and a reason class when not ok), present only when it reported known values. Additive.
	Delivery       string `json:"delivery,omitempty"`
	DeliveryReason string `json:"delivery_reason,omitempty"`
}

// whoActivity is the peer's reported state; kind only with blocked.
type whoActivity struct {
	State string `json:"state"`
	Kind  string `json:"kind,omitempty"`
}

// whoStateCell is the state column of one entry: "-" offline or not reported, "?" no answer in time or refused.
func whoStateCell(p endpoint.Presence, act map[string]endpoint.PeerState) string {
	if !p.Online {
		return "-"
	}
	st, ok := act[p.Record.KeyOrName()]
	if !ok {
		return "?"
	}
	return st.Display()
}

// whoDeliveryCell is the delivery column of one entry: "?" no answer in time or refused (or an online endpoint missing
// from act), "-" offline or answered without a delivery report, else the report. Never "ok" unless the peer said so.
func whoDeliveryCell(p endpoint.Presence, act map[string]endpoint.PeerState) string {
	if !p.Online {
		return "-"
	}
	st, ok := act[p.Record.KeyOrName()]
	if !ok {
		return "?"
	}
	return st.DeliveryDisplay()
}

// whoAlias is the legacy identity a new-form record answers to.
type whoAlias struct {
	Owner string `json:"owner"`
	Name  string `json:"name"`
}

// whoJSON renders the presences as a JSON array (`[]` when none). Map keys encode sorted. lease is added only when
// long is set and a live lease is known.
func whoJSON(ps []endpoint.Presence, long bool, live map[string]endpoint.LeaseInfo) string {
	return whoJSONAct(ps, long, live, nil)
}

// whoJSONAct is whoJSON with the activity of the online endpoints in act (nil: none asked).
func whoJSONAct(ps []endpoint.Presence, long bool, live map[string]endpoint.LeaseInfo, act map[string]endpoint.PeerState) string {
	out := make([]whoEntry, 0, len(ps))
	for _, p := range ps {
		r := p.Record
		e := whoEntry{Name: r.KeyOrName(), Owner: r.Owner, Online: p.Online, Role: r.Role, Host: r.Host, Note: r.Note, Description: r.Description}
		if d := sap.Display(r.KeyOrName()); d != e.Name {
			e.Identity = d // a legacy entry has none: its name is already what people read
		}
		if len(r.Caps) > 0 {
			e.Caps = r.Caps
		}
		if len(r.Labels) > 0 {
			e.Labels = r.Labels
		}
		if !p.LastSeen.IsZero() {
			e.LastSeen = p.LastSeen.UTC().Format(time.RFC3339)
		}
		if r.Alias != nil {
			e.Alias = &whoAlias{Owner: r.Alias.Owner, Name: r.Alias.Name}
		}
		if li, ok := live[r.KeyOrName()]; ok && long {
			e.Lease = &whoLease{Host: li.Host, Version: li.Version}
		}
		if st, ok := act[r.KeyOrName()]; ok && p.Online && st.Outcome == endpoint.PeerReported {
			e.Activity = &whoActivity{State: st.State, Kind: st.Kind}
			e.ActivityTrust = "claimed"
		}
		if st, ok := act[r.KeyOrName()]; ok && p.Online && st.Delivery != "" {
			e.Delivery, e.DeliveryReason = st.Delivery, st.DeliveryReason
		}
		out = append(out, e)
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	return string(b)
}

// whoRender renders an already filtered list. filtered says a filter was given: then an empty text result is empty,
// and the "no endpoints" text is only for an empty directory.
func whoRender(ps []endpoint.Presence, o whoOutput, filtered bool, live map[string]endpoint.LeaseInfo) string {
	switch {
	case o.JSON:
		return whoJSONAct(ps, o.Long, live, o.Activity)
	case o.Names:
		return whoNames(ps)
	case len(ps) == 0 && filtered:
		return ""
	}
	return whoTextAct(ps, o.Long, live, o.Activity)
}

// whoActivityBudget is the one overall time the state column may take, however many endpoints are online.
const whoActivityBudget = 2 * time.Second

// whoActivityOf asks every online endpoint of ps what it is doing, in parallel (PeerActivity holds the limit of 8 in
// flight), within one overall whoActivityBudget. The result is never nil; an online endpoint that is not in it did not
// answer in time and shows "?".
func whoActivityOf(ctx context.Context, ep *endpoint.Endpoint, ps []endpoint.Presence) map[string]endpoint.PeerState {
	act := map[string]endpoint.PeerState{}
	ctx, cancel := context.WithTimeout(ctx, whoActivityBudget)
	defer cancel()
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, p := range ps {
		if !p.Online {
			continue
		}
		key := p.Record.KeyOrName()
		wg.Add(1)
		go func() {
			defer wg.Done()
			st := ep.PeerActivity(ctx, key)
			if ctx.Err() != nil && st.Outcome == endpoint.PeerNoAnswer {
				return // the budget ran out: not back
			}
			mu.Lock()
			act[key] = st
			mu.Unlock()
		}()
	}
	wg.Wait()
	return act
}
