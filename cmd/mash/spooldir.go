package main

import (
	"errors"
	"fmt"
	"io"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
)

// spoolDirCmd is `mash spool-dir`: it prints the directory serve spools this endpoint's mail in, so a host extension
// that watches the spool (hosts/pi/mash.ts) does not rebuild the path from the state directory, owner and name. It
// connects to nothing and creates nothing.
func spoolDirCmd(cfg config.Config, stdout, stderr io.Writer) int {
	if cfg.Inert() {
		fmt.Fprintln(stderr, errors.New("mash spool-dir: no endpoint name (set MASH_NAME)"))
		return 1
	}
	dir, err := endpoint.SpoolDir(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "mash spool-dir: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, dir)
	return 0
}
