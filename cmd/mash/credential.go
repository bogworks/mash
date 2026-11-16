package main

import (
	"fmt"
	"io"

	"github.com/bogworks/mash/internal/credential"
)

// credentialUsage is the usage of `mash credential`: the two verbs that run on the endpoint's host, with no NATS
// connection, no environment and no operator keys.
const credentialUsage = `usage: mash credential key --out FILE
       mash credential install --seed FILE --jwt FILE --out CREDS [--force]

  key      make a user key; the seed goes to FILE (mode 0600, never over an existing file), the public key is printed
  install  make the creds file from the seed and the JWT mashctl signed for its public key
`

// credentialCmd is `mash credential`; it runs before the configuration is read.
func credentialCmd(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, credentialUsage)
		return 2
	}
	switch args[0] {
	case "key":
		return credential.KeyCmd("mash", args[1:], stdout, stderr)
	case "install":
		return credential.InstallCmd("mash", args[1:], stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, credentialUsage)
		return 0
	}
	fmt.Fprintf(stderr, "mash credential: unknown verb\n%s", credentialUsage)
	return 2
}
