package policy

import (
	"encoding/json"
	"slices"
	"strings"
)

// Matrix renders who may mail whom as a text table: one row per sender, one column per recipient, "+" where the
// sender may address the recipient and "-" where it may not. Endpoints are in name order; the output is
// deterministic.
func (c *Compiled) Matrix() string {
	if len(c.Endpoints) == 0 {
		return "(no endpoints)\n"
	}
	head := "from \\ to"
	w := len(head)
	for _, e := range c.Endpoints {
		w = max(w, len(e.Name))
	}
	var b strings.Builder
	pad := func(s string, n int) {
		b.WriteString(s)
		b.WriteString(strings.Repeat(" ", n-len(s)))
	}
	pad(head, w)
	for _, e := range c.Endpoints {
		b.WriteString("  ")
		b.WriteString(e.Name)
	}
	b.WriteString("\n")
	for _, from := range c.Endpoints {
		pad(from.Name, w)
		for _, to := range c.Endpoints {
			mark := "-"
			if _, ok := slices.BinarySearch(from.Recipients, to.Name); ok {
				mark = "+"
			}
			b.WriteString("  ")
			pad(mark, len(to.Name))
		}
		b.WriteString("\n")
	}
	b.WriteString("\n+ may mail, prompt and probe; - may not\n")
	lines := strings.Split(b.String(), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return strings.Join(lines, "\n")
}

type jsonDoc struct {
	Owner     string         `json:"owner"`
	Default   string         `json:"default"`
	Endpoints []jsonEndpoint `json:"endpoints"`
}

type jsonEndpoint struct {
	Name          string            `json:"name"`
	Labels        map[string]string `json:"labels"`
	Recipients    []string          `json:"recipients"`
	MayMailNobody bool              `json:"may_mail_nobody"`
}

// JSON is the machine-readable form for `show --json`: owner, default and, per endpoint, name, labels, recipients
// and may_mail_nobody. Keys are in a fixed order, maps are sorted, the output is indented and ends in a newline.
// It carries nothing of the JWT.
func (c *Compiled) JSON() ([]byte, error) {
	d := jsonDoc{Owner: c.Owner, Default: c.Default, Endpoints: make([]jsonEndpoint, len(c.Endpoints))}
	for i, e := range c.Endpoints {
		labels := e.Labels
		if labels == nil {
			labels = map[string]string{}
		}
		rec := e.Recipients
		if rec == nil {
			rec = []string{}
		}
		d.Endpoints[i] = jsonEndpoint{Name: e.Name, Labels: labels, Recipients: rec, MayMailNobody: len(rec) == 0}
	}
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
