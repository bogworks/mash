package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/keycred"
)

const keyUsage = `usage: mash key create --out FILE [--pub-out FILE] [--json] [--dry-run]

mash key makes a user key pair. It needs no server, no config file and no account key.

  create   write a new seed to FILE (mode 0600) and its public key to FILE.pub, or to --pub-out

Example:
  mash key create --out design.seed
`

const keyCreateUsage = `usage: mash key create --out FILE [--pub-out FILE] [--json] [--dry-run]

Make a user key pair. The seed goes to FILE (mode 0600); the public key goes to FILE with a trailing .seed replaced
by .pub (FILE.pub if FILE does not end in .seed), or to --pub-out. It makes a missing parent directory (mode 0700) and refuses to overwrite either file. It prints the
public key and both paths, never the seed.

  --out FILE       the seed file (required)
  --pub-out FILE   the public key file
  --json           one JSON document (schema mash-key@1)
  --dry-run        write nothing; say what would be written

Example:
  mash key create --out design.seed
`

const credentialUsage = `usage: mash credential create --seed FILE --jwt FILE [--out CREDS] [--force] [--json] [--dry-run]

mash credential builds a credentials file. It needs no server, no config file and no account key.

  create   join a seed and the user JWT issued for its public key into a credentials file

It checks that the JWT is a user JWT with a valid self-signature that names the seed's key; it does not check the
issuing account or the expiry.

Example:
  mash credential create --seed design.seed --jwt design.jwt
`

const credentialCreateUsage = `usage: mash credential create --seed FILE --jwt FILE [--out CREDS] [--force] [--json] [--dry-run]

Join a user seed and the user JWT issued for its public key into a credentials file (mode 0600). It refuses if the JWT
was issued for a different key (key_mismatch) and refuses to replace an existing file unless --force is given. It
prints the credentials path and the public key, never the seed or the JWT.

It checks the structure of the JWT, its self-signature, and that it is a user JWT naming the seed's key; it does not
check the issuing account or the expiry.

  --seed FILE   the seed file (required)
  --jwt FILE    the user JWT file (required)
  --out CREDS   the credentials file; default is the config directory plus the JWT's name and .creds
  --force       replace an existing credentials file
  --json        one JSON document (schema mash-credential@1)
  --dry-run     write nothing; say what would be written

Example:
  mash credential create --seed design.seed --jwt design.jwt --out design.creds
`

// keycredUsage is the help text of `mash key` and `mash credential` at the level args names (args[0] is the verb).
func keycredUsage(args []string) string {
	sub := ""
	if len(args) > 1 && !strings.HasPrefix(args[1], "-") {
		sub = args[1]
	}
	switch {
	case args[0] == "key" && sub == "create":
		return keyCreateUsage
	case args[0] == "key":
		return keyUsage
	case sub == "create":
		return credentialCreateUsage
	}
	return credentialUsage
}

// keycredHelpFirst is the index of the argument that may ask for help for `mash key` and `mash credential`.
func keycredHelpFirst(args []string) int {
	if len(args) > 1 && !strings.HasPrefix(args[1], "-") {
		return 2
	}
	return 1
}

// splitFlag splits --name=value.
func splitFlag(a string) (name, inline string, has bool) {
	if strings.HasPrefix(a, "--") {
		return strings.Cut(a, "=")
	}
	return a, "", false
}

// keycredCmd runs `mash key` and `mash credential`. args[0] is the verb. It reads no config.
func keycredCmd(args []string, env func(string) string, stdout, stderr io.Writer) int {
	verb := args[0]
	usage := keyUsage
	if verb == "credential" {
		usage = credentialUsage
	}
	if len(args) < 2 {
		fmt.Fprintf(stderr, "mash %s: needs a subcommand: create\n%s", verb, usage)
		return 2
	}
	var found *keycredVerb
	var names []string
	for i, v := range keycredVerbTable {
		if v.noun != verb {
			continue
		}
		names = append(names, v.name)
		if v.name == args[1] {
			found = &keycredVerbTable[i]
		}
	}
	if found == nil {
		fmt.Fprintf(stderr, "mash %s: unknown subcommand %q; the only one is %s (try: mash %s --help)\n", verb, args[1], strings.Join(names, ", "), verb)
		return 2
	}
	return found.run(args[2:], env, stdout, stderr)
}

// keycredVerb is one command of the key and credential nouns. The dispatch and the command-tree test both read
// keycredVerbTable, so a new verb is dispatched and tested from one entry.
type keycredVerb struct {
	noun    string
	name    string
	usage   string // the help text of `mash NOUN NAME`
	run     func(rest []string, env func(string) string, stdout, stderr io.Writer) int
	changes bool // it writes files, so it has --dry-run
}

var keycredVerbTable = []keycredVerb{
	{noun: "key", name: "create", usage: keyCreateUsage, changes: true,
		run: func(rest []string, _ func(string) string, stdout, stderr io.Writer) int {
			return keyCreateCmd(rest, stdout, stderr)
		}},
	{noun: "credential", name: "create", usage: credentialCreateUsage, changes: true,
		run: func(rest []string, env func(string) string, stdout, stderr io.Writer) int {
			return credentialCreateCmd(rest, env, stdout, stderr)
		}},
}

type keycredFlags struct {
	vals           map[string]string
	json, dry, frc bool
}

// parseKeycred reads the flags in valued (take a value) and the booleans --json, --dry-run and, when force, --force.
func parseKeycred(verb string, args []string, valued []string, force bool) (keycredFlags, error) {
	f := keycredFlags{vals: map[string]string{}}
	for i := 0; i < len(args); i++ {
		name, inline, has := splitFlag(args[i])
		switch {
		case name == "--json" && !has:
			f.json = true
		case name == "--dry-run" && !has:
			f.dry = true
		case name == "--force" && force && !has:
			f.frc = true
		default:
			ok := false
			for _, v := range valued {
				if name == v {
					ok = true
				}
			}
			if !ok {
				return f, fmt.Errorf("mash %s create: unexpected %s (try: mash %s create --help)", verb, config.RejectedArg(args[i]), verb)
			}
			v, err := flagValue(args, &i, name, inline, has)
			if err != nil {
				return f, fmt.Errorf("mash %s create: %w", verb, err)
			}
			f.vals[name] = v
		}
	}
	return f, nil
}

func missing(verb string, f keycredFlags, names ...string) error {
	for _, n := range names {
		if f.vals[n] == "" {
			return fmt.Errorf("mash %s create: %s is required (try: mash %s create --help)", verb, n, verb)
		}
	}
	return nil
}

func writeJSON(w io.Writer, v any) {
	b, _ := json.Marshal(v)
	fmt.Fprintf(w, "%s\n", b)
}

type keyDoc struct {
	Schema     string           `json:"schema"`
	Outcome    string           `json:"outcome"`
	PublicKey  string           `json:"public_key"`
	SeedPath   string           `json:"seed_path"`
	PublicPath string           `json:"public_path"`
	Refusal    *keycred.Refusal `json:"refusal"`
}

func keyCreateCmd(args []string, stdout, stderr io.Writer) int {
	f, err := parseKeycred("key", args, []string{"--out", "--pub-out"}, false)
	if err == nil {
		err = missing("key", f, "--out")
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	res, err := keycred.CreateKey(keycred.KeyOptions{Out: f.vals["--out"], PubOut: f.vals["--pub-out"], DryRun: f.dry})
	if err != nil && res.SeedPath == "" { // bad arguments, before anything ran
		fmt.Fprintf(stderr, "mash key create: %v (try: mash key create --help)\n", err)
		return 2
	}
	if err != nil {
		res.Outcome = keycred.Errored
		res.Refusal = &keycred.Refusal{Code: "io_error", Detail: err.Error()}
	}
	if f.json {
		writeJSON(stdout, keyDoc{"mash-key@1", res.Outcome, res.PublicKey, res.SeedPath, res.PublicPath, res.Refusal})
	}
	switch res.Outcome {
	case keycred.Refused:
		if !f.json {
			fmt.Fprintf(stderr, "mash key create: refused (%s): %s\nnext: pick another --out, or move the existing file\n", res.Refusal.Code, res.Refusal.Detail)
		}
		return 1
	case keycred.Errored:
		if !f.json {
			fmt.Fprintf(stderr, "mash key create: %v\nnext: check the directory exists and is writable, then run it again\n", err)
		}
		return 6
	}
	if f.json {
		return 0
	}
	head := "created key"
	if res.Outcome == keycred.Planned {
		head = "would create key (dry run, nothing written)"
	}
	fmt.Fprintf(stdout, "%s\n  public key  %s\n  seed        %s\n  public file %s\nnext: send %s to the host that runs mashctl, then: mashctl endpoint create ADDRESS --public-key %s\n",
		head, res.PublicKey, res.SeedPath, res.PublicPath, res.PublicPath, res.PublicPath)
	return 0
}

type credDoc struct {
	Schema    string           `json:"schema"`
	Outcome   string           `json:"outcome"`
	PublicKey string           `json:"public_key"`
	CredsPath string           `json:"creds_path"`
	Name      string           `json:"name"`
	Refusal   *keycred.Refusal `json:"refusal"`
}

func credentialCreateCmd(args []string, env func(string) string, stdout, stderr io.Writer) int {
	f, err := parseKeycred("credential", args, []string{"--seed", "--jwt", "--out"}, true)
	if err == nil {
		err = missing("credential", f, "--seed", "--jwt")
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	// With no HOME the config directory may be empty, and a run with no --out is then refused: it names --out.
	res, err := keycred.CreateCredentials(keycred.CredOptions{
		Seed: f.vals["--seed"], JWT: f.vals["--jwt"], Out: f.vals["--out"],
		ConfigDir: config.ConfigDir(env, env("HOME")), Force: f.frc, DryRun: f.dry,
	})
	if errors.Is(err, keycred.ErrNeedOut) {
		fmt.Fprintf(stderr, "mash credential create: %v (try: mash credential create --help)\n", err)
		return 2
	}
	if err != nil {
		res.Outcome = keycred.Errored
		res.Refusal = &keycred.Refusal{Code: "io_error", Detail: err.Error()}
	}
	if f.json {
		writeJSON(stdout, credDoc{"mash-credential@1", res.Outcome, res.PublicKey, res.CredsPath, res.Name, res.Refusal})
	}
	switch res.Outcome {
	case keycred.Refused:
		if !f.json {
			next := "check that the seed and the JWT belong together"
			if res.Refusal.Code == keycred.CodeFileExists {
				next = "pass --force to replace it, or choose another --out"
			}
			fmt.Fprintf(stderr, "mash credential create: refused (%s): %s\nnext: %s\n", res.Refusal.Code, res.Refusal.Detail, next)
		}
		return 1
	case keycred.Errored:
		if !f.json {
			fmt.Fprintf(stderr, "mash credential create: %v\nnext: check the paths and permissions, then run it again\n", err)
		}
		return 6
	}
	if f.json {
		return 0
	}
	head := "created credentials"
	if res.Outcome == keycred.Planned {
		head = "would create credentials (dry run, nothing written)"
	}
	fmt.Fprintf(stdout, "%s\n  public key  %s\n  creds       %s\nnext: mash status\n", head, res.PublicKey, res.CredsPath)
	return 0
}
