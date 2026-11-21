// Command mash is a messaging mesh for agents: a Claude Code channel server
// (mash serve), and a CLI for people and scripts.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"regexp"
	"runtime/debug"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/bogworks/mash/internal/cliframe"
	"github.com/bogworks/mash/internal/cmdcore"
	"github.com/bogworks/mash/internal/cmdcore/cmds"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/endpoint"
	"github.com/bogworks/mash/internal/host"
)

// helpIntro is the first line of the tool's own help, `mash --help` and `mash help`.
const helpIntro = "Trusted mail between agents. Speaks as one endpoint."

// helpTail is what follows the commands: how an endpoint is addressed, the global options and the configuration.
const helpTail = `An endpoint is addressed owner.group.name: MASH_ENDPOINT (or config.json
"endpoint", or --endpoint). With none set, the one endpoint whose creds file
(owner.group.name.creds, mode 0600) is in the config directory is used; with
several, name one.
A global option may stand anywhere in the command. A flag beats its environment
variable, which beats config.json.
Configuration: config.json {"servers","endpoint","ca"} in $MASH_CONFIG_DIR
(default ~/.config/mash, or $XDG_CONFIG_HOME/mash), overridden by MASH_SERVER
(commas between several), MASH_ENDPOINT, MASH_CA, MASH_CREDS, MASH_ADMIN_CREDS,
MASH_HOST (see docs/install.md).
State (spool, hook.log): $MASH_STATE_DIR (default $XDG_STATE_HOME/mash, else
~/.local/state/mash).
Slow link: "open_timeout" (default 20s) and "call_timeout" (default 15s), or
MASH_OPEN_TIMEOUT and MASH_CALL_TIMEOUT.
Also tunable: lease_retry, transient_budget, transient_max, cli_timeout,
admin_timeout (MASH_LEASE_RETRY, MASH_TRANSIENT_BUDGET, MASH_TRANSIENT_MAX,
MASH_CLI_TIMEOUT, MASH_ADMIN_TIMEOUT).
`

// helpCol is the column the commands' one-line descriptions start at.
const helpCol = 47

// helpGroups are the headings of the tool's own help, in order (the specification's x-group).
var helpGroups = []string{"Everyday", "Setup", "Integration", "Raw SAP"}

// helpLayout is the commands under each heading in the order of the specification. A command on the command table is
// listed from the table (its synopsis and description are generated); one that is not yet is listed from handCommands.
var helpLayout = map[string][]string{
	"Everyday":    {"send", "inbox", "read", "who", "status", "doctor", "version"},
	"Setup":       {"join", "key"},
	"Integration": {"serve", "hook"},
	"Raw SAP":     {"prompt"},
}

// handHelp is the commands that are not on the command table yet; their texts are the specification's. Their
// --help is printed in the generated layout by cmdcore.StaticHelp.
var handHelp = map[string]cmdcore.StaticCommand{
	"serve": {
		Name:        "serve",
		Description: "Run as an MCP server for an agent host. Started by the host, not by hand.",
		Usage:       "mash serve [options]",
		Examples:    []string{"mash serve"},
		Options: [][2]string{
			{"--wake-count <n>", "Wake the session when more than this many messages are waiting. Default 15; 0 turns it off."},
			{"--wake-age <duration>", "Wake the session when the oldest waiting message is older than this. Default 1h; 0 turns it off."},
			{"--accept-unverified", "Deliver mail that is not verified, with its trust label. Default: off; such mail is refused."},
		},
	},
	"hook": {
		Name:        "hook",
		Description: "Print mail waiting for this session, in the form the agent host expects. Run by the host after each tool call and with each prompt.",
		Usage:       "mash hook",
		Examples:    []string{"mash hook"},
	},
	"prompt": {
		Name:        "prompt",
		Description: "Ask an endpoint a live question and wait for its answer. Not stored: if the recipient is not there, it fails.",
		Usage:       "mash prompt <address> <text>",
		Examples:    []string{`mash prompt acme.demo.alice "are you there?"`},
		Arguments:   [][2]string{{"address", "Who to ask."}, {"text", "The question."}},
	},
}

// handCommands are the synopsis and one-line description of the commands that are not on the command table yet.
var handCommands = map[string][2]string{
	"key":    {"mash key create [options]", cmds.KeyParent},
	"serve":  {handHelp["serve"].Usage, handHelp["serve"].Description},
	"hook":   {handHelp["hook"].Usage, handHelp["hook"].Description},
	"prompt": {handHelp["prompt"].Usage, handHelp["prompt"].Description},
}

// usageFor is the tool's own help for the table r: the commands listed by group, each with its one-line description.
// The commands on the table come from it (their group, synopsis and description); the rest are hand-written. A command on
// the table that the layout does not name is listed last under its own group.
func usageFor(r *cmdcore.Registry) string {
	var b strings.Builder
	b.WriteString(helpIntro + "\n")
	for _, g := range helpGroups {
		b.WriteString("\n" + g + ":\n")
		names := helpLayout[g]
		for _, n := range r.Groups(g) {
			if !slices.Contains(names, n) && !strings.Contains(n, " ") { // key create is listed as its parent, key
				names = append(slices.Clone(names), n)
			}
		}
		for _, n := range names {
			if _, ok := r.Lookup(n); ok {
				b.WriteString(r.ListLine(n, helpCol) + "\n")
			} else if h, ok := handCommands[n]; ok {
				b.WriteString(cmdcore.ListLine(h[0], h[1], helpCol) + "\n")
			}
		}
	}
	return b.String() + "\n" + cmdcore.GlobalOptions(helpFrame) + "\n\n" + helpTail
}

// usage is the help of the command table.
var usage = usageFor(newCommands(nil))

func main() {
	// A session that is killed (Claude Code exiting, a closed terminal) shuts
	// down cleanly, so serve releases its lease instead of leaving it to expire.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT)
	code := runLookup(ctx, os.Args[1:], os.Getenv, os.LookupEnv, os.Stdin, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// run is main with its environment injected, for tests.
func run(args []string, env func(string) string, stdin io.Reader, stdout, stderr io.Writer) int {
	return runCtx(context.Background(), args, env, stdin, stdout, stderr)
}

// runCtx is runLookup for callers that only have an env func (tests): a variable is present iff it is not empty.
func runCtx(ctx context.Context, args []string, env func(string) string, stdin io.Reader, stdout, stderr io.Writer) int {
	return runLookup(ctx, args, env, config.LookupFromFunc(env), stdin, stdout, stderr)
}

// commit is set by -ldflags "-X main.commit=<sha>"; otherwise the VCS revision stamped by go build is used.
var commit string

// readBuildInfo is debug.ReadBuildInfo, replaceable in tests.
var readBuildInfo = debug.ReadBuildInfo

// resolveVersion is the version this binary reports: the one a release build stamped into endpoint.Version by
// ldflag (anything but the default "dev"), else the module version a `go install …@v0.1.0` build carries, else endpoint.DevVersion ("0.0.0-dev").
func resolveVersion(configured string, bi *debug.BuildInfo) string {
	if configured != "" && configured != endpoint.DevVersion {
		return configured
	}
	// Only a clean tag counts. Since Go 1.24 a plain `go build` in a checkout stamps a pseudo-version
	// (v0.0.0-<time>-<sha>, with +dirty for a modified tree) into Main.Version: that is a development build.
	if bi != nil && releaseTag.MatchString(bi.Main.Version) && !pseudoVersion.MatchString(bi.Main.Version) {
		return strings.TrimPrefix(bi.Main.Version, "v")
	}
	return endpoint.DevVersion
}

// pseudoVersion is Go's untagged-commit version tail: -<yyyymmddhhmmss>-<12 hex> (also after a dot, as in v0.1.1-0.<...>).
var pseudoVersion = regexp.MustCompile(`[-.]\d{14}-[0-9a-f]{12}$`)

// releaseTag is vN.N.N with an optional -prerelease that is not a pseudo-version's timestamp-and-sha tail.
var releaseTag = regexp.MustCompile(`^v\d+\.\d+\.\d+(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)

// versionLine is "mash v<version> (<commit>)", as `mash version` prints it: the commit is its first 7 characters
// ("unknown" when the build has none). It names the commit so a pinned build can be checked.
func versionLine(version, ldflags string, bi *debug.BuildInfo) string {
	return fmt.Sprintf("mash v%s (%s)", version, shortCommit(buildRevision(ldflags, bi)))
}

// buildRevision is the commit this binary was built from: the ldflag, else the VCS revision go build stamped (with
// "+dirty" for a modified tree), else "".
func buildRevision(ldflags string, bi *debug.BuildInfo) string {
	return endpoint.BuildRevision(ldflags, bi)
}

// helpTopic is the verbs that are not on the table and have help of their own: `mash help VERB` prints their
// `mash VERB --help`.
var helpTopic = map[string]bool{"serve": true, "hook": true, "key": true, "prompt": true}

// noJSONForm is the verbs that have no --json form: a --json given to one is a usage error.
var noJSONForm = map[string]bool{"prompt": true, "serve": true, "hook": true}

// stdinWait is how long a message read from standard input has to arrive: 15 minutes, as the send help says. It is its
// own limit, not cli_timeout: a run can take up to twice cli_timeout (this wait, then the command's own deadline).
const stdinWait = 15 * time.Minute

// stdinLimit is stdinWait; a variable only so a test can shorten it.
var stdinLimit = stdinWait

// runLookup is main with its environment injected. lookup must agree with env; it is what config.Load reads.
func runLookup(ctx context.Context, args []string, env func(string) string, lookup config.Lookup, stdin io.Reader, stdout, stderr io.Writer) int {
	// Resolved before anything dispatches, so `version`, the MCP server and the SAP service all report one string.
	// Written only when it changes, which a process does once: later commands (and the tests, which run many in
	// one process beside live sessions that read it) only read.
	bi, _ := readBuildInfo()
	if v := resolveVersion(endpoint.CurrentVersion(), bi); v != endpoint.CurrentVersion() {
		endpoint.SetVersion(v)
	}
	if c := buildRevision(commit, bi); c != endpoint.CurrentCommit() {
		endpoint.SetCommit(c)
	}
	// The encryption override is the very first thing checked, before any argument is read (bare `mash`, a bad flag,
	// help and version included): a removed name or a bad value is refused (exit 2), and while the override is on
	// every invocation prints the warning on stderr, once. A hook never blocks the session: it says the refusal on
	// stderr, exits 0, and reads and writes nothing.
	if skipErr := config.CheckSkipEncryption(lookup, config.HomeDir(env)); skipErr != nil {
		if len(args) > 0 && args[0] == "hook" {
			fmt.Fprintf(stderr, "mash hook: %s\n", skipErr.Msg())
			return 0
		}
		return cliframe.Report(stdout, stderr, cliframe.Usagef("%s", skipErr.Msg()).WithHint(skipErr.Hint()), slices.Contains(args, "--json"))
	}
	config.WarnSkipEncryption(lookup, stderr)
	// An error before a command runs is one framed error (cliframe.Report): "error: MSG" and a hint on stderr, or with
	// --json {"error","hint"} on stdout and nothing on stderr. The code is the error's own (1 failure, 2 usage).
	if len(args) == 0 {
		return cliframe.Report(stdout, stderr, cliframe.Usagef("mash needs a command").WithHint("mash --help"), false)
	}
	// The global options (--endpoint, --creds, --server, --ca, --config-dir, --detail, --json, --no-color) are taken out
	// of the arguments wherever they stand, before any verb runs; a flag shadows its environment variable.
	rawArgs := args
	global, args, gerr := cliframe.ExtractOwning(cliframe.Spec{Tool: "mash", CredsEnv: "MASH_CREDS", CredsKey: "creds"}, args, nil)
	if gerr != nil {
		// The options did not parse, so --json is looked for by hand.
		return cliframe.Report(stdout, stderr, cliframe.Usagef("%s", strings.TrimPrefix(gerr.Error(), "mash: ")).WithHint("mash --help"), slices.Contains(rawArgs, "--json"))
	}
	if len(args) == 0 {
		return cliframe.Report(stdout, stderr, cliframe.Usagef("mash needs a command").WithHint("mash --help"), global.JSON)
	}
	env, lookup = global.Shadow(cliframe.Spec{Tool: "mash", CredsEnv: "MASH_CREDS", CredsKey: "creds"}, env, lookup)
	home := config.HomeDir(env)
	// A folder mash 0.5.5 left is named for what it is, before any command (help included) runs.
	if code, refused := cliframe.RefuseLegacy055(stdout, stderr, env, home, global.JSON && args[0] != "hook"); refused {
		if args[0] == "hook" { // a hook never blocks a session: the two lines on stderr, exit 0, no stdin, no spool
			return 0
		}
		return code
	}
	// The refusal was made (and the warning printed) before the arguments were read; a --config-dir flag can point at a
	// file with the removed key, so that file is checked again here, refusal only.
	if skipErr := config.CheckSkipEncryption(lookup, config.HomeDir(env)); skipErr != nil {
		if args[0] == "hook" { // a hook never blocks a session: the refusal on stderr, exit 0
			fmt.Fprintf(stderr, "mash hook: %s\n", skipErr.Msg())
			return 0
		}
		return cliframe.Report(stdout, stderr, cliframe.Usagef("%s", skipErr.Msg()).WithHint(skipErr.Hint()), global.JSON)
	}
	var serr error
	if args, env, lookup, serr = serveFlags(args, env, lookup); serr != nil {
		return cliframe.Report(stdout, stderr, serr, global.JSON)
	}
	if args[0] == "help" { // `mash help` prints what `mash --help` does; `mash help send` what `mash send --help` does
		if len(args) == 1 {
			args = []string{"--help"}
		} else if _, known := helpTopic[args[1]]; !known && !commandVerb(args[1]) {
			return cliframe.Report(stdout, stderr, cliframe.Usagef("mash has no command %s", args[1]).WithHint("mash help"), global.JSON)
		} else if args[1] == "key" && len(args) > 2 && args[2] == "create" {
			args = []string{"key", "create", "--help"}
		} else {
			args = []string{args[1], "--help"}
		}
	}
	// A help option before the command is the same as after it: `mash -h version` is `mash version --help`.
	if (args[0] == "-h" || args[0] == "--help") && len(args) > 1 {
		_, hand := handHelp[args[1]]
		if _, topic := helpTopic[args[1]]; hand || topic || commandVerb(args[1]) { // the help option goes right after the command, before any "--"
			args = append([]string{args[1], "--help"}, args[2:]...)
		}
	}
	if u, ok := newCommands(nil).HelpFor(args[0], args[1:]); ok { // the generated help of the commands on the table
		fmt.Fprint(stdout, u)
		return 0
	}
	if args[0] == "key" && len(args) > 1 && (args[1] == "-h" || args[1] == "--help") { // before any dispatch: help changes nothing
		fmt.Fprint(stdout, keyHelp())
		return 0
	}
	if u, ok := helpUsage(args); ok { // before any dispatch, config load or write: help changes nothing
		fmt.Fprint(stdout, u)
		return 0
	}
	if global.JSON && noJSONForm[args[0]] { // nothing is attempted
		// With --json every error is {"error","hint"} on stdout and nothing on stderr (rules.json).
		return cliframe.Report(stdout, stderr, cliframe.Usagef("mash %s has no --json form", args[0]).WithHint("mash "+args[0]+" --help"), true)
	}
	if args[0] == "serve" && stdinIsTerminal(stdin) { // run by hand: nothing is attempted, no config, no connection
		return cliframe.Report(stdout, stderr, cliframe.Usagef("mash serve is started by an agent host, not by hand").WithHint("mash serve --help"), global.JSON)
	}
	if args[0] == "serve" && len(args) > 1 { // serve's one option, --accept-unverified, is out of args: anything left is wrong
		msg := "unknown option " + args[1]
		if !strings.HasPrefix(args[1], "-") {
			msg = "serve takes no argument " + args[1]
		}
		return cliframe.Report(stdout, stderr, cliframe.Usagef("%s", msg).WithHint("mash serve --help"), global.JSON)
	}
	if args[0] == "version" { // needs no endpoint, so before config
		mk := func(context.Context) (*cmdcore.Env, error) {
			return &cmdcore.Env{Svc: buildVersion{pluginRoot: env("MASH_PLUGIN_ROOT")}}, nil
		}
		return newCommands(mk).RunCLI("version", args[1:], stdin, stdout, stderr, global)
	}
	if args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(stdout, usage)
		return 0
	}
	if args[0] == "key" { // before config: it needs no endpoint, config file or account key
		return keyCmd(args, stdin, stdout, stderr, global)
	}
	if args[0] == "join" { // before config: it makes the configuration, so it reads none
		mk := func(context.Context) (*cmdcore.Env, error) {
			return &cmdcore.Env{Svc: joinSvc{env: env, home: home}}, nil
		}
		return newCommands(mk).RunCLI("join", args[1:], stdin, stdout, stderr, global)
	}
	jsonOut := global.JSON
	load := config.Load
	if args[0] == "serve" || args[0] == "hook" {
		load = config.LoadNamedOnly // a session never picks an endpoint for itself
	}
	cfg, err := load(lookup, home)
	// A session named without a group has no address and no default: it stops at once naming MASH_ENDPOINT.
	// A hook never fails the session (REQ-002 applies to the session itself, which serve stops), so it says so on
	// stderr and exits 0; status and doctor report it.
	report := args[0] == "status" || args[0] == "doctor" // they report what is wrong with the configuration, they do not fail on it
	if errors.Is(err, config.ErrGroupMissing) && !report {
		if args[0] == "hook" {
			fmt.Fprintf(stderr, "mash hook: %s: %v\n", config.CodeGroupMissing, err)
			return 0
		}
		return cliframe.Report(stdout, stderr, cliframe.Usagef("%s: %s", config.CodeGroupMissing, strings.TrimPrefix(err.Error(), "mash: ")).WithHint("mash doctor"), jsonOut)
	}
	var adapter host.Adapter
	if args[0] == "hook" || args[0] == "serve" {
		// An unknown host is refused loudly, never read as claude: a hook that ran with the wrong adapter would
		// drain mail into the wrong place.
		var herr error
		if adapter, herr = host.Select(cfg.Host); herr != nil {
			if args[0] == "hook" { // as it was: the hook's own line, nothing framed
				fmt.Fprintln(stderr, herr)
				return 1
			}
			return cliframe.Report(stdout, stderr, cliframe.Failf("%s", strings.TrimPrefix(herr.Error(), "mash: ")).WithHint("mash doctor"), jsonOut)
		}
	}
	if args[0] == "hook" { // never fails the session: a bad config is a silent no-op
		return hook(cfg, err, adapter, env, stdin, stdout, stderr)
	}
	// A command of the table parses its arguments before it needs the endpoint, so a bad option is a usage error, with
	// no endpoint chosen or installed, and not the configuration's error: the error waits in the environment.
	var cfgFail error
	var several *config.SeveralEndpointsError
	switch {
	case errors.As(err, &several) && slices.Contains([]string{"send", "who", "inbox", "read", "prompt"}, args[0]):
		cfgFail = cmdcore.NoEndpoint(cmdcore.SeveralInstalled, args[0], several.Addresses)
	case err != nil && args[0] != "serve" && !report:
		cfgFail = cliframe.Failf("%s", strings.TrimPrefix(err.Error(), "mash: ")).WithHint("mash doctor")
	}
	var skipErr *config.SkipEncryptionError
	if errors.As(err, &skipErr) && args[0] != "hook" && !report { // the removed names or a bad value: a configuration error, exit 2
		return cliframe.Report(stdout, stderr, cliframe.Usagef("%s", skipErr.Msg()).WithHint("mash doctor"), jsonOut)
	}
	if cfgFail != nil {
		if slices.Contains([]string{"send", "who", "inbox", "read"}, args[0]) {
			mk := func(context.Context) (*cmdcore.Env, error) { return nil, cfgFail }
			return newCommands(mk).RunCLI(args[0], args[1:], stdin, stdout, stderr, global)
		}
		return cliframe.Report(stdout, stderr, cfgFail, jsonOut)
	}
	// A plain URL with no MASH_DANGEROUSLY_SKIP_NATS_ENCRYPTION=1 is refused before anything is connected (REQ-014): serve stops at session
	// start and the commands that connect stop, exit 2, with the refusal on stderr.
	if err == nil && !cfg.Inert() {
		if _, perr := endpoint.EndpointTLS(cfg); perr != nil && !report { // a report says it as a row
			return cliframe.Report(stdout, stderr, cliframe.Usagef("%s", strings.TrimPrefix(perr.Error(), "mash: ")).WithHint("mash doctor"), jsonOut)
		}
	}
	switch args[0] {
	case "serve":
		if cfg.WakeErr != nil && err == nil { // the wake settings are serve's: a bad one stops it, no other command minds
			return cliframe.Report(stdout, stderr, cliframe.Usagef("%s", strings.TrimPrefix(cfg.WakeErr.Error(), "mash: ")).WithHint("mash serve --help"), jsonOut)
		}
		return serve(ctx, cfg, err, adapter, env("MASH_PLUGIN_ROOT"), stdin, stdout, stderr)
	case "send":
		mk := func(context.Context) (*cmdcore.Env, error) {
			// A message read from standard input has stdinLimit to arrive, whatever cli_timeout is.
			return &cmdcore.Env{Svc: &cliSvc{cfg: cfg, stderr: stderr}, StdinTimeout: stdinLimit}, nil
		}
		return newCommands(mk).RunCLI("send", args[1:], stdin, stdout, stderr, global)
	case "inbox", "read", "who", "status", "doctor":
		mk := func(context.Context) (*cmdcore.Env, error) {
			return &cmdcore.Env{Svc: &cliSvc{cfg: cfg, cfgErr: err, stderr: stderr}}, nil
		}
		return newCommands(mk).RunCLI(args[0], args[1:], stdin, stdout, stderr, global)
	case "prompt":
		return cli(cfg, args, stdin, stdout, stderr)
	}
	return cliframe.Report(stdout, stderr, cliframe.Usagef("mash has no command %s", args[0]).WithHint("mash --help"), global.JSON)
}

// stdinIsTerminal reports whether r is a terminal: a file whose mode is a character device. An agent host gives serve
// a pipe; a person at a keyboard gives it a terminal. A variable so a test can force either answer.
var stdinIsTerminal = func(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
