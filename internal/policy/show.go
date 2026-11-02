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
	b.WriteString("\n+ may mail; - may not\n")
	lines := strings.Split(b.String(), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return strings.Join(lines, "\n")
}

type jsonDoc struct {
	Owner        string         `json:"owner"`
	Default      string         `json:"default"`
	SubjectLabel string         `json:"subject_label"`
	Zones        []jsonZone     `json:"zones"`
	Endpoints    []jsonEndpoint `json:"endpoints"`
}

type jsonZone struct {
	Name      string   `json:"name"`
	Wildcards []string `json:"wildcards"`
	Endpoints []string `json:"endpoints"`
}

type jsonEndpoint struct {
	Name          string            `json:"name"`
	Zone          string            `json:"zone"`
	Labels        map[string]string `json:"labels"`
	Recipients    []string          `json:"recipients"`
	MayMailNobody bool              `json:"may_mail_nobody"`
	Tags          []string          `json:"tags"`
}

// JSON is the machine-readable form for `show --json`: owner, default, subject_label, the zones (name, the zones
// its key grants whole, endpoints) and, per endpoint, name, zone, labels, recipients, may_mail_nobody and the tags
// of its user. Keys are in a fixed order, maps are sorted, the output is indented and ends in a newline.
// It carries nothing of the JWT.
func (c *Compiled) JSON() ([]byte, error) {
	d := jsonDoc{Owner: c.Owner, Default: c.Default, SubjectLabel: c.SubjectLabel,
		Zones: make([]jsonZone, len(c.Zones)), Endpoints: make([]jsonEndpoint, len(c.Endpoints))}
	for i, z := range c.Zones {
		d.Zones[i] = jsonZone{Name: z.Name, Wildcards: nonNil(z.Wildcards), Endpoints: nonNil(z.Endpoints)}
	}
	for i, e := range c.Endpoints {
		labels := e.Labels
		if labels == nil {
			labels = map[string]string{}
		}
		rec := e.Recipients
		if rec == nil {
			rec = []string{}
		}
		d.Endpoints[i] = jsonEndpoint{Name: e.Name, Zone: e.Zone, Labels: labels, Recipients: rec, MayMailNobody: len(rec) == 0, Tags: nonNil(e.Tags)}
	}
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
