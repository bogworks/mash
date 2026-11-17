package main

import (
	"fmt"
	"strings"
	"sync"
)

// Assertion is one line of a scenario: what was expected (Name) and what was seen.
type Assertion struct {
	Name   string
	Pass   bool
	Detail string
}

// Checks collects the assertions of one scenario.
type Checks struct {
	mu   sync.Mutex
	list []Assertion
}

// Check records an assertion and reports whether it held. Detail is what was observed (an exit code, a refusal code);
// it is kept in the record, so it must never hold a seed or a JWT.
func (c *Checks) Check(name string, cond bool, detail string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.list = append(c.list, Assertion{Name: name, Pass: cond, Detail: detail})
	return cond
}

// All returns a copy of the assertions in the order they were made.
func (c *Checks) All() []Assertion {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Assertion(nil), c.list...)
}

// Failed counts the assertions that did not hold.
func (c *Checks) Failed() int {
	n := 0
	for _, a := range c.All() {
		if !a.Pass {
			n++
		}
	}
	return n
}

// Table is the assertions as a text table, one per line.
func (c *Checks) Table() string {
	var b strings.Builder
	for _, a := range c.All() {
		res := "PASS"
		if !a.Pass {
			res = "FAIL"
		}
		fmt.Fprintf(&b, "%s  %s", res, a.Name)
		if a.Detail != "" {
			fmt.Fprintf(&b, "  [%s]", a.Detail)
		}
		b.WriteByte('\n')
	}
	return b.String()
}
