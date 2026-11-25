package main

import (
	"math"
	"testing"
)

func TestBoundedIntHoldsAtTheLargestInt(t *testing.T) {
	for _, c := range []struct {
		in   uint64
		want int
	}{{0, 0}, {7, 7}, {math.MaxInt, math.MaxInt}, {uint64(math.MaxInt) + 1, math.MaxInt}, {math.MaxUint64, math.MaxInt}} {
		if got := boundedInt(c.in); got != c.want || got < 0 {
			t.Errorf("boundedInt(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}
