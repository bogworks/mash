package issue

import (
	"container/heap"
	"time"
)

// nonceItem is one reserved nonce in the expiry heap.
type nonceItem struct {
	nonce string
	exp   time.Time
	idx   int
}

type nonceHeap []*nonceItem

func (h nonceHeap) Len() int           { return len(h) }
func (h nonceHeap) Less(i, j int) bool { return h[i].exp.Before(h[j].exp) }
func (h nonceHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i]; h[i].idx = i; h[j].idx = j }
func (h *nonceHeap) Push(x any)        { it := x.(*nonceItem); it.idx = len(*h); *h = append(*h, it) }
func (h *nonceHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return it
}

// nonceSet is the replay set: nonces ordered by expiry in a min-heap with an index by nonce, so a take, a release and
// the drop of an expired nonce are each O(log n) and only expired entries are ever visited. It is not safe for
// concurrent use; the Issuer guards it with its own mutex, which it holds for these calls only.
type nonceSet struct {
	byNonce map[string]*nonceItem
	h       nonceHeap
}

// take reserves nonce until exp. It reports the refusal code when the nonce is held (CodeReplay) or the set is full
// of unexpired nonces (CodeBusy); a full set is refused without a scan.
func (s *nonceSet) take(nonce string, exp, now time.Time) string {
	if s.byNonce == nil {
		s.byNonce = map[string]*nonceItem{}
	}
	for len(s.h) > 0 && !s.h[0].exp.After(now) {
		it := heap.Pop(&s.h).(*nonceItem)
		delete(s.byNonce, it.nonce)
	}
	if _, seen := s.byNonce[nonce]; seen {
		return CodeReplay
	}
	if len(s.byNonce) >= MaxNonces {
		return CodeBusy
	}
	it := &nonceItem{nonce: nonce, exp: exp}
	s.byNonce[nonce] = it
	heap.Push(&s.h, it)
	return ""
}

// release frees nonce, which a request reserved and then was refused for: only a nonce that went with a returned
// credential stays used.
func (s *nonceSet) release(nonce string) {
	if it, ok := s.byNonce[nonce]; ok {
		heap.Remove(&s.h, it.idx)
		delete(s.byNonce, nonce)
	}
}

func (s *nonceSet) len() int { return len(s.byNonce) }
