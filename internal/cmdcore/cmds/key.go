package cmds

import (
	"context"
	"errors"
	"io/fs"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/cmdcore"
	"github.com/bogworks/mash/internal/keycred"
)

// KeyDefaultOut is where mash key create writes the seed when --out is not given: mash.seed in the current folder.
const KeyDefaultOut = "mash.seed"

// KeyParent is the specification's description of the key command, which holds key create.
const KeyParent = "Make your own key pair, for when the seed must never leave this machine."

// KeyCreateParams are the arguments of mash key create. It has no MCP tool.
type KeyCreateParams struct {
	Out string `json:"out,omitempty" flag:"--out" value:"path" desc:"Where to write the seed. The public key goes beside it with .pub added. Default: mash.seed in the current folder."`
}

// KeyCreateResult is the two files made: Seed is the seed file's path and PublicKey the public key file's path, as
// given. Neither holds the key itself.
type KeyCreateResult struct {
	Seed      string `json:"seed"`
	PublicKey string `json:"public_key"`
}

// Text is "Created <seed> and <public key file>"
func (r KeyCreateResult) Text(cmdcore.Opts) string {
	return "Created " + r.Seed + " and " + r.PublicKey
}

// KeyCreate is the key create command: a new user seed (mode 0600) and its public key beside it. It reads no
// configuration and needs no endpoint.
var KeyCreate = cmdcore.Command[KeyCreateParams, KeyCreateResult]{
	Name:        "key create",
	Group:       "Setup",
	NoMCP:       true,
	Summary:     "Make a seed and its public key.",
	Description: "Make a seed and its public key.",
	Examples:    []string{"mash key create --out bob.seed"},
	Run: func(_ context.Context, _ *cmdcore.Env, p KeyCreateParams) (KeyCreateResult, error) {
		out := p.Out
		if out == "" {
			out = KeyDefaultOut
		}
		pub := keycred.PublicPathFor(out)
		res, err := keycred.CreateKey(keycred.KeyOptions{Out: out, PubOut: pub})
		if res.Outcome == keycred.Refused {
			return KeyCreateResult{}, cliframe.Failf("%s already exists; nothing was written", res.Existing)
		}
		if err != nil {
			var pe *fs.PathError
			if errors.As(err, &pe) {
				return KeyCreateResult{}, cliframe.Failf("cannot write %s: %v", pe.Path, pe.Err)
			}
			return KeyCreateResult{}, cliframe.Failf("cannot write %s: %v", out, err)
		}
		return KeyCreateResult{Seed: out, PublicKey: pub}, nil
	},
}
