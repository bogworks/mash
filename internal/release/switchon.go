package release

// SwitchOnVersion is the switch-on release of the 0.6.0 SDK adoption (REQ-019). 0.6.0 is the SDK move, so it is version
// 0.6.0. From this version the release gate requires the upgrade-proof receipts (AT-011 and AT-012) and refuses a
// candidate that has none; below it the receipts are checked only when the maintainer gives them.
const SwitchOnVersion = "0.6.0"

// RequiresProofs reports whether the release gate requires the upgrade proofs for version (N.N.N): true when the
// version is at or above SwitchOnVersion by numeric comparison (0.10.0 is above 0.6.0, 0.5.10 is below). A version
// that does not parse as N.N.N is not held to it here; the callers refuse such a version earlier.
func RequiresProofs(version string) bool {
	v, ok := versionKey(version)
	if !ok {
		return false
	}
	s, _ := versionKey(SwitchOnVersion)
	return !less(v, s)
}
