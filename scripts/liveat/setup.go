package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// The live setup is the policy script scripts/mash-local-nats-setup.sh (a NATS server in operator mode with TLS and
// JetStream) followed by `mashctl init --config <dir>/mash-setup.yaml`. These are the names of the files the script
// writes in its --dir; the harness no longer imports them from the product.
const (
	setupScriptDefault = "scripts/mash-local-nats-setup.sh"

	fileCACert     = "ca.pem"
	fileCAKey      = "ca.key"
	fileServerKey  = "server.key"
	fileServerConf = "server.conf"
	fileOperator   = "operator.jwt"
	fileServerPID  = "server.pid"
	fileSetupYAML  = "mash-setup.yaml"
	fileAdminCreds = "creds/mashadmin.creds"
	fileSysCreds   = "creds/sys.creds"
	fileOperatorNK = "keys/operator.nk"
	fileAccountNK  = "keys/account.nk"
)

// secretFiles are the files of the script's directory whose lines are searched for in all captured output.
var secretFiles = []struct{ label, file string }{
	{"ca key", fileCAKey},
	{"server key", fileServerKey},
	{"operator signing seed", fileOperatorNK},
	{"account identity seed", fileAccountNK},
	{"admin creds", fileAdminCreds},
	{"system creds", fileSysCreds},
}

var portLineRE = regexp.MustCompile(`(?m)^port: (\d+)(?:\s|$)`)

// parseSetupPort reads the `port: N` line the script prints (it may be followed by a note in parentheses).
func parseSetupPort(out string) (int, error) {
	m := portLineRE.FindStringSubmatch(out)
	if m == nil {
		return 0, fmt.Errorf("the setup script printed no `port: N` line")
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n < 1 || n > 65535 {
		return 0, fmt.Errorf("the setup script printed an unusable port %q", m[1])
	}
	return n, nil
}

// parsePIDFile reads the pid the script records in server.pid (one line, digits).
func parsePIDFile(b []byte) (int, error) {
	line, _, _ := strings.Cut(string(b), "\n")
	line = strings.TrimSpace(line)
	pid, err := strconv.Atoi(line)
	if err != nil || pid <= 1 {
		return 0, fmt.Errorf("server.pid does not hold a process id")
	}
	return pid, nil
}

// yamlQuote double-quotes s for a YAML scalar.
func yamlQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// SetupFile is the content of a setup file for `mashctl init --config`: paths and addresses, never secrets.
type SetupFile struct {
	Servers                                                     []string
	CA, Owner, AdminCreds, SystemCreds, OperatorKey, AccountKey string
}

// Render writes the YAML in the order the script writes its own mash-setup.yaml.
func (f SetupFile) Render() string {
	var q []string
	for _, s := range f.Servers {
		q = append(q, yamlQuote(s))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "servers: [%s]\n", strings.Join(q, ", "))
	fmt.Fprintf(&b, "ca: %s\nowner: %s\n", yamlQuote(f.CA), f.Owner)
	fmt.Fprintf(&b, "admin_creds: %s\nsystem_creds: %s\n", yamlQuote(f.AdminCreds), yamlQuote(f.SystemCreds))
	fmt.Fprintf(&b, "operator_key: %s\naccount_key: %s\n", yamlQuote(f.OperatorKey), yamlQuote(f.AccountKey))
	return b.String()
}

// errSetupOnHost is why the setup script is never run on the machine the harness itself runs on: the script makes keys,
// credentials and a NATS server, and tests run only in containers or VMs that the test makes and destroys.
const errSetupOnHost = "setup runs only inside a node container: the setup script is never run on the test host (AT-001 to AT-003 start their own sandbox container; AT-005 runs it in M1; --hosts local cannot run it)"

// containerEnvFile is the file podman puts in every container it makes.
const containerEnvFile = "/run/.containerenv"

// insideContainer reports whether this process runs in a podman container. Tests replace it.
var insideContainer = func() bool {
	_, err := os.Stat(containerEnvFile)
	return inContainerSignals(err == nil, os.Getenv(envInsideRevision))
}

// inContainerSignals: /run/.containerenv is also in toolbox and distrobox, so the sandbox's own variable (the revision
// it starts the run with) is required too.
func inContainerSignals(containerEnv bool, revision string) bool {
	return containerEnv && revision != ""
}

// scriptPath is the setup script: the Run's SetupScript, or the default under the checkout.
func (r *Run) scriptPath() string {
	if r.SetupScript != "" {
		return r.SetupScript
	}
	return filepath.Join(r.Repo, setupScriptDefault)
}

// setupArgs is the command line of one script call (run with bash, so the file needs no execute bit).
func (r *Run) setupArgs(dir string, more ...string) []string {
	return append([]string{"bash", r.scriptPath(), "--dir", dir}, more...)
}
