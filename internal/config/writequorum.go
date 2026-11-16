package config

import (
	"fmt"
	"strconv"
	"strings"
)

// WriteQuorum decides how many servers must answer before `mashctl add`, `reissue` and `retire` read the account JWT
// or push a change to it (ADR 0142). setting is the write_quorum setting ("auto", or a whole number; empty is auto) and
// declared the declared server count (0 when none is declared).
//
// auto is a majority (declared/2 + 1) when 3 or more servers are declared and every server otherwise (1 of 1, 2 of 2).
// A number must be at least 1 and at most the declared count; with 3 or more declared servers it must also be a
// majority, because two disjoint groups smaller than a majority could each take a write and the next read would miss
// one of them. The result is 0 when auto meets no declared count: the caller then keeps asking every server it can see.
func WriteQuorum(setting string, declared int) (int, error) {
	s := strings.ToLower(strings.TrimSpace(setting))
	if s == "" || s == "auto" {
		if declared >= 3 {
			return declared/2 + 1, nil
		}
		return max(declared, 0), nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("mash: write_quorum %q is not supported: the values are auto or a whole number of servers, 1 or more", setting)
	}
	switch {
	case declared < 1:
		return 0, fmt.Errorf("mash: write_quorum %d needs the number of servers declared (config.json \"servers\" or MASH_SERVERS), or use write_quorum auto", n)
	case n > declared:
		return 0, fmt.Errorf("mash: write_quorum %d is more than the %d declared servers: no write could reach it; lower it or declare the servers", n, declared)
	case declared >= 3 && n < declared/2+1:
		return 0, fmt.Errorf("mash: write_quorum %d is below a majority (%d) of the %d declared servers: two groups of servers could each take a write and neither would see the other's, so a revocation could be lost; use %d or more, or auto", n, declared/2+1, declared, declared/2+1)
	}
	return n, nil
}

// EffectiveWriteQuorum is WriteQuorum of this configuration's setting and declared server count.
func (c Config) EffectiveWriteQuorum() (int, error) { return WriteQuorum(c.WriteQuorum, c.Servers) }
