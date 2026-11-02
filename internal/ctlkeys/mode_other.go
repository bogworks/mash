//go:build !unix

package ctlkeys

import "os"

// tooOpen is false where file modes carry no owner/group/other bits.
func tooOpen(os.FileMode) bool { return false }
