package endpoint

import "github.com/bogworks/mash/internal/sap"

// mustSubj is a's subjects for a test address the SDK builder accepts; it panics otherwise.
func mustSubj(a sap.Address) sap.Subjects {
	s, err := a.Subjects()
	if err != nil {
		panic(err)
	}
	return s
}
