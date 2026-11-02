package policy

import (
	"slices"
	"sort"
	"strings"

	"github.com/bogworks/mash/internal/admin"
)

// MaxTagsWarn is the number of tags on one user JWT above which Check warns: the user's JWT travels in the CONNECT
// line, which the default server caps at 4096 bytes, and about 150 tags fill it (see UserJWTLimit).
const MaxTagsWarn = 150

// plan is the file resolved against its zones: for every ordered pair of zones whether the sending zone's key grants
// the receiving zone as a wildcard, which users carry which exception tags, and each sender's decided recipients. It
// needs no owner.
type plan struct {
	zones      []string            // sorted
	members    map[string][]string // zone -> endpoint names, sorted
	zone       map[string]string   // endpoint -> zone
	wildcards  map[string][]string // sending zone -> receiving zones its key grants whole, sorted
	denyZone   map[string]bool     // sending zone -> its key carries {{tag(deny)}} templates
	tags       map[string][]string // endpoint -> sorted tags (allow:, deny:)
	recipients map[string][]string // endpoint -> decided recipients, sorted
}

// plan decides every ordered pair once and chooses the encoding of each zone pair (Zs -> Zr):
//
//   - every pair allowed: the zone key carries the wildcard for Zr and no user a tag;
//   - none allowed: nothing;
//   - mixed: either (a) no wildcard and each sender an allow tag per allowed recipient (one allow:Zr.* when it may
//     mail the whole zone), or (b) the wildcard and each sender a deny tag per denied recipient (one deny:Zr.* when
//     it may mail nobody in Zr). The wildcard belongs to the key and so to every sender of Zs: (b) is chosen when it
//     needs fewer tags in total over all senders of Zs, (a) on a tie.
//
// A zone with any (b) pair needs deny templates on its key, and nats-server refuses a user without a deny tag then,
// so every user of that zone carries at least admin.NoDenyTag.
func (f *File) plan() plan {
	names := f.names()
	p := plan{
		members: map[string][]string{}, zone: map[string]string{}, wildcards: map[string][]string{},
		denyZone: map[string]bool{}, tags: map[string][]string{}, recipients: map[string][]string{},
	}
	idx := make(map[string]int, len(names))
	for i, n := range names {
		idx[n] = i
		z := f.zoneOf(n)
		p.zone[n] = z
		p.members[z] = append(p.members[z], n)
	}
	for z := range p.members {
		p.zones = append(p.zones, z)
	}
	sort.Strings(p.zones)

	m := f.matrix(names)
	for i, from := range names {
		rec := []string{}
		for j, to := range names {
			if m.allowed(i, j) {
				rec = append(rec, to)
			}
		}
		p.recipients[from] = rec
	}

	for _, zs := range p.zones {
		S := p.members[zs]
		for _, zr := range p.zones {
			R := p.members[zr]
			allowedPer := make([]int, len(S)) // allowed recipients in zr, per sender
			total := 0
			for si, s := range S {
				for _, r := range R {
					if m.allowed(idx[s], idx[r]) {
						allowedPer[si]++
					}
				}
				total += allowedPer[si]
			}
			if total == 0 {
				continue
			}
			if total == len(S)*len(R) {
				p.wildcards[zs] = append(p.wildcards[zs], zr)
				continue
			}
			costA, costB := 0, 0
			for _, na := range allowedPer {
				costA += tagCost(na, len(R))
				costB += tagCost(len(R)-na, len(R))
			}
			useDeny := costB < costA
			kind := admin.AllowTag
			if useDeny {
				kind = admin.DenyTag
				p.wildcards[zs] = append(p.wildcards[zs], zr)
				p.denyZone[zs] = true
			}
			for _, s := range S {
				// The recipients the tags name: the allowed ones under (a), the denied ones under (b).
				var named []string
				for _, r := range R {
					if m.allowed(idx[s], idx[r]) != useDeny {
						named = append(named, r)
					}
				}
				switch {
				case len(named) == 0:
				case len(named) == len(R) && len(R) > 1:
					p.tags[s] = append(p.tags[s], kind+":"+zr+".*")
				default:
					for _, r := range named {
						p.tags[s] = append(p.tags[s], kind+":"+zr+"."+r)
					}
				}
			}
		}
	}
	for _, z := range p.zones {
		for _, n := range p.members[z] {
			if p.denyZone[z] && !slices.ContainsFunc(p.tags[n], func(t string) bool { return strings.HasPrefix(t, admin.DenyTag+":") }) {
				p.tags[n] = append(p.tags[n], admin.NoDenyTag)
			}
			if p.tags[n] == nil {
				p.tags[n] = []string{}
			}
			slices.Sort(p.tags[n])
		}
	}
	return p
}

// tagCost is the tags one sender needs to name k of the n recipients of a zone: none for none, one wildcard tag for
// all, else one per recipient.
func tagCost(k, n int) int {
	switch k {
	case 0:
		return 0
	case n:
		return 1
	}
	return k
}
