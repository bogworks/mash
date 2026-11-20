package main

import (
	"context"
	"fmt"
	"io"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/cmdcore"
	"github.com/bogworks/mash/internal/cmdcore/cmds"
)

// keyHelp is `mash key --help`: key only holds key create, so its help is the description and that one usage line.
func keyHelp() string {
	return cmdcore.ParentHelp(helpFrame, cmds.KeyParent, []string{"mash key create [options]"})
}

// keyCmd runs `mash key ...`. args[0] is "key"; the only command under it is create, which is on the command table.
// It reads no configuration: it needs no endpoint, no config file and no account key.
func keyCmd(args []string, stdin io.Reader, stdout, stderr io.Writer, global cliframe.Global) int {
	if len(args) == 1 {
		return cliframe.Report(stdout, stderr, cliframe.Usagef("mash key needs a command: create").WithHint("mash key --help"), global.JSON)
	}
	switch args[1] {
	case "-h", "--help":
		fmt.Fprint(stdout, keyHelp())
		return 0
	case "create":
		return newCommands(func(context.Context) (*cmdcore.Env, error) { return &cmdcore.Env{}, nil }).
			RunCLI("key create", args[2:], stdin, stdout, stderr, global)
	}
	return cliframe.Report(stdout, stderr, cliframe.Usagef("mash key has no command %s", args[1]).WithHint("mash key --help"), global.JSON)
}
