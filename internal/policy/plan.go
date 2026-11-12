package policy

// recipients decides every ordered pair of declared endpoints once and returns, per sender, the declared endpoints it
// may mail, sorted (never nil: empty means none). It needs no owner. Nothing in the compile turns the decisions into
// server permissions (ADR 0091 removed the zone, the only unit the compile could enforce them on); they are what
// `show`, `explain` and the tests report.
func (f *File) recipients() map[string][]string {
	names := f.names()
	out := make(map[string][]string, len(names))
	m := f.matrix(names)
	for i, from := range names {
		rec := []string{}
		for j, to := range names {
			if m.allowed(i, j) {
				rec = append(rec, to)
			}
		}
		out[from] = rec
	}
	return out
}
