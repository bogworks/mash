package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/bogworks/mash/internal/refgen"
)

func main() {
	specDir := flag.String("spec", "spec", "folder holding the spec JSON files")
	outDir := flag.String("out", "docs/reference", "folder to write the pages into")
	flag.Parse()
	if err := refgen.Run(*specDir, *outDir); err != nil {
		fmt.Fprintln(os.Stderr, "refgen:", err)
		os.Exit(1)
	}
}
