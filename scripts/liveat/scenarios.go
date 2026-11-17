package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// The CLI forms these scenarios use that this build may not have yet (contract 0.5.5, REQ-003, REQ-004, REQ-010):
//
//	mash key create --out FILE                      (writes FILE, mode 0600, and FILE with .seed replaced by .pub)
//	mashctl endpoint create O/G/N --public-key FILE [--jwt-out FILE] [--json]
//	mashctl endpoint list --json
//	mashctl endpoint delete O/G/N
//	mash credential create --seed FILE --jwt FILE [--out CREDS]
//
// A command that is unknown fails its assertion with that fact; nothing is stubbed.

// ep is the files of one endpoint made the contract's way.
type ep struct{ seed, pub, jwt, creds string }

// provision makes an endpoint the way JRN-001 does: the key on the endpoint's machine (keyHome), the JWT signed on
// the mashctl host (ctl), carried by copying the JWT file only, and the creds file assembled on keyHome. creds "" is
// the default path; noCreds skips the assembly.
func (a *AT) provision(ctl, keyHome Home, owner, group, name, creds string, noCreds bool) (ep, bool) {
	who := owner + "/" + group + "/" + name
	e := ep{seed: filepath.Join(keyHome.Dir, name+".seed")}
	e.pub = strings.TrimSuffix(e.seed, ".seed") + ".pub"
	ctlJWT := filepath.Join(ctl.Dir, owner+"."+group+"."+name+".jwt")
	e.jwt = ctlJWT
	if ctl.Dir != keyHome.Dir {
		e.jwt = filepath.Join(keyHome.Dir, owner+"."+group+"."+name+".jwt")
	}
	e.creds = creds
	ok := true
	r := a.Mash("key-create-"+name, keyHome, a.Conn(), "key", "create", "--out", e.seed)
	ok = a.C.Check("mash key create makes "+name+"'s key on its own machine", r.Code == 0, exitDetail(r)) && ok
	a.Watch("seed "+name, e.seed)
	a.checkMode(name+" seed file is mode 0600", e.seed)
	r = a.Ctl("endpoint-create-"+name, ctl, a.Conn(), "endpoint", "create", who, "--public-key", e.pub, "--jwt-out", ctlJWT)
	ok = a.C.Check("mashctl endpoint create "+who+" --public-key signs for the endpoint's own key", r.Code == 0, exitDetail(r)) && ok
	a.Watch("jwt "+name, ctlJWT)
	a.checkMode(name+" JWT file is mode 0600", ctlJWT)
	if ctl.Dir != keyHome.Dir {
		if b, err := os.ReadFile(ctlJWT); err == nil {
			_ = os.WriteFile(e.jwt, b, 0o600) // the operator carries the JWT file; the seed never moves
		}
	}
	if noCreds || !ok {
		return e, ok
	}
	args := []string{"credential", "create", "--seed", e.seed, "--jwt", e.jwt}
	if creds != "" {
		args = append(args, "--out", creds)
	}
	r = a.Mash("credential-create-"+name, keyHome, a.Conn(), args...)
	ok = a.C.Check("mash credential create assembles "+name+"'s creds file on its machine", r.Code == 0, exitDetail(r)) && ok
	return e, ok
}

func (a *AT) checkMode(name, path string) {
	st, err := os.Stat(path)
	a.C.Check(name, err == nil && st.Mode().Perm() == 0o600, modeDetail(st, err))
}

func modeDetail(st os.FileInfo, err error) string {
	if err != nil {
		return "not found"
	}
	return fmt.Sprintf("mode %04o", st.Mode().Perm())
}

// whoEntry is one element of `mash who --json`.
type whoEntry struct {
	Identity string
	Agent    string
	Owner    string
	Group    string
	Instance string
	Online   bool
}

// who reads `mash who --json` as the endpoint with environment extra.
func (a *AT) who(h Home, extra []string) ([]whoEntry, Result) {
	r := a.Mash("who", h, extra, "who", "--json")
	v, err := parseJSON(r.Stdout)
	if err != nil {
		return nil, r
	}
	var out []whoEntry
	for _, o := range findObjects(v, "identity") {
		s := func(k string) string { x, _ := o[k].(string); return x }
		on, _ := o["online"].(bool)
		out = append(out, whoEntry{s("identity"), s("agent"), s("owner"), s("group"), s("instance"), on})
	}
	return out, r
}

// waitOnline polls who until every identity is online.
func (a *AT) waitOnline(h Home, extra []string, ids ...string) bool {
	deadline := time.Now().Add(30 * time.Second)
	for {
		es, _ := a.who(h, extra)
		n := 0
		for _, e := range es {
			if e.Online && slices.Contains(ids, e.Identity) {
				n++
			}
		}
		if n == len(ids) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(500 * time.Millisecond)
	}
}

var sentRE = regexp.MustCompile(`sent #(\d+)`)

// mailVerified sends text from one endpoint to another with the CLI and reads it back from the recipient's inbox with
// the recipient's own credentials: it must arrive, carry the text and be labelled [verified].
func (a *AT) mailVerified(name string, h Home, from, to []string, toAddr, text string) {
	r := a.Mash("send-"+name, h, from, "send", toAddr, text)
	a.C.Check("mail "+name+": mash send succeeds", r.Code == 0, exitDetail(r))
	m := sentRE.FindStringSubmatch(r.Stdout)
	if m == nil {
		a.C.Check("mail "+name+": send names the message number", false, "no '#N' in the output")
		return
	}
	deadline := time.Now().Add(20 * time.Second)
	var rd Result
	for {
		rd = a.Mash("read-"+name, h, to, "read", m[1])
		if rd.Code == 0 && strings.Contains(rd.Stdout, text) || time.Now().After(deadline) {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	a.C.Check("mail "+name+": the recipient reads the text", rd.Code == 0 && strings.Contains(rd.Stdout, text), exitDetail(rd))
	a.C.Check("mail "+name+": the recipient sees it labelled verified", strings.Contains(rd.Stdout, "verified"), "")
}

// permissions asserts that `mash admin permissions O.G.I` prints REQ-005's set.
func (a *AT) permissions(h Home, owner, group, name string) {
	addr := owner + "." + group + "." + name
	r := a.Mash("permissions-"+name, h, a.Conn(), "admin", "permissions", addr)
	a.C.Check("mash admin permissions "+addr+" succeeds", r.Code == 0, exitDetail(r))
	v, err := parseJSON(r.Stdout)
	if err != nil {
		a.C.Check("mash admin permissions prints the permission set as JSON", false, err.Error())
		return
	}
	pub, sub, deny := permissionLists(v)
	fails, owners := checkPermissions(owner, group, name, pub, sub, deny)
	a.C.Check("REQ-005: the credential's publish and subscribe set is exactly the contract's", len(fails) == 0, strings.Join(fails, "; "))
	a.C.Check("INV-002: no line names another owner or a wildcard in the owner slot", len(owners) == 0, strings.Join(owners, "; "))
}

// issuedPermissions decodes the user JWT in path (a bare JWT file or a creds file) in memory and applies the same
// contract comparison to the permissions it carries. The token and payload are never recorded.
func (a *AT) issuedPermissions(path, owner, group, name string) {
	const check = "REQ-005: the issued credential's publish and subscribe set is exactly the contract's (decoded from the JWT)"
	b, err := os.ReadFile(path)
	if err != nil {
		a.C.Check(check, false, "the JWT file could not be read")
		return
	}
	pub, sub, deny, claimName, err := decodeIssued(string(b))
	if err != nil {
		a.C.Check(check, false, err.Error())
		return
	}
	fails, _ := checkPermissions(owner, group, name, pub, sub, deny)
	if want := owner + "." + group + "." + name; claimName != want {
		fails = append(fails, "the JWT name claim is not the dotted address "+want)
	}
	a.C.Check(check, len(fails) == 0, strings.Join(fails, "; "))
}

// decodeIssued finds the JWT in text and returns its allow lists, deny entries (tagged like permissionLists) and
// name claim. Errors never include token or payload content.
func decodeIssued(text string) (pub, sub, deny []string, name string, err error) {
	tok := jwtRE.FindString(text)
	if tok == "" {
		return nil, nil, nil, "", fmt.Errorf("no JWT found in the file")
	}
	raw, derr := base64.RawURLEncoding.DecodeString(strings.Split(tok, ".")[1])
	if derr != nil {
		return nil, nil, nil, "", fmt.Errorf("the JWT payload is not base64url")
	}
	var v struct {
		Name string `json:"name"`
		NATS struct {
			Pub struct{ Allow, Deny []string } `json:"pub"`
			Sub struct{ Allow, Deny []string } `json:"sub"`
		} `json:"nats"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return nil, nil, nil, "", fmt.Errorf("the JWT payload is not JSON")
	}
	for _, d := range v.NATS.Pub.Deny {
		deny = append(deny, "publish "+d)
	}
	for _, d := range v.NATS.Sub.Deny {
		deny = append(deny, "subscribe "+d)
	}
	return v.NATS.Pub.Allow, v.NATS.Sub.Allow, deny, v.Name, nil
}

// permissionLists reads the allow lists and any deny entries from the printed permission JSON.
func permissionLists(v any) (pub, sub, deny []string) {
	strs := func(x any) []string {
		var out []string
		if al, ok := x.([]any); ok {
			for _, e := range al {
				if s, ok := e.(string); ok {
					out = append(out, s)
				}
			}
		}
		return out
	}
	m, _ := v.(map[string]any)
	for _, side := range []struct {
		key string
		dst *[]string
		tag string
	}{{"pub", &pub, "publish "}, {"sub", &sub, "subscribe "}} {
		sm, _ := m[side.key].(map[string]any)
		*side.dst = strs(sm["allow"])
		for _, d := range strs(sm["deny"]) {
			deny = append(deny, side.tag+d)
		}
	}
	return pub, sub, deny
}

// expectedPermissions is REQ-005's complete set for O.G.I, written out from the contract.
func expectedPermissions(owner, group, name string) (pub, sub []string) {
	f := owner + "_" + group + "_" + name
	pub = []string{
		"agents.mail." + owner + ".*.*", "agents.prompt." + owner + ".*.*", "agents.status." + owner + ".*.*",
		"agents.hb." + owner + "." + group + "." + name,
		"_INBOX.>", "$SRV.>", "$JS.API.INFO",
		"$JS.API.STREAM.INFO.inbox-" + f,
		"$JS.API.CONSUMER.INFO.inbox-" + f + ".deliver",
		"$JS.API.CONSUMER.MSG.NEXT.inbox-" + f + ".deliver",
		"$JS.ACK.inbox-" + f + ".deliver.>",
		"$JS.API.DIRECT.GET.inbox-" + f,
		"$JS.API.DIRECT.GET.inbox-" + f + ".>",
		"$JS.API.STREAM.INFO.KV_mash-directory",
		"$JS.API.DIRECT.GET.KV_mash-directory.>",
		"$JS.API.STREAM.INFO.KV_mash-leases",
		"$KV.mash-leases." + f,
		"$JS.API.DIRECT.GET.KV_mash-leases.$KV.mash-leases." + f,
	}
	sub = []string{"_INBOX." + f + ".>", "agents.prompt." + owner + "." + group + "." + name,
		"agents.status." + owner + "." + group + "." + name, "$SRV.>", "agents.hb." + owner + ".*.*"}
	return pub, sub
}

// diffSet reports the lines of want absent from got and the lines of got not in want.
func diffSet(side string, got, want []string) (missing, unexpected []string) {
	for _, w := range want {
		if !slices.Contains(got, w) {
			missing = append(missing, side+" "+w)
		}
	}
	for _, g := range got {
		if !slices.Contains(want, g) {
			unexpected = append(unexpected, side+" "+g)
		}
	}
	return missing, unexpected
}

// checkPermissions compares the complete printed sets to the contract's. fails covers set differences and
// deny entries; owners covers every agents. line that does not carry the owner literally in the owner slot.
func checkPermissions(owner, group, name string, pub, sub, deny []string) (fails, owners []string) {
	wp, ws := expectedPermissions(owner, group, name)
	var missing, unexpected []string
	for _, side := range []struct {
		n         string
		got, want []string
	}{{"publish", pub, wp}, {"subscribe", sub, ws}} {
		m, u := diffSet(side.n, side.got, side.want)
		missing = append(missing, m...)
		unexpected = append(unexpected, u...)
	}
	if len(missing) > 0 {
		fails = append(fails, "missing: "+strings.Join(missing, ", "))
	}
	if len(unexpected) > 0 {
		fails = append(fails, "unexpected: "+strings.Join(unexpected, ", "))
	}
	if len(deny) > 0 {
		fails = append(fails, "deny entries: "+strings.Join(deny, ", "))
	}
	for _, s := range append(slices.Clone(pub), sub...) {
		if !strings.HasPrefix(s, "agents.") {
			continue
		}
		t := strings.Split(s, ".")
		if s == "agents.>" || strings.HasSuffix(s, ".>") || len(t) < 4 || t[2] != owner {
			owners = append(owners, s)
		}
	}
	return fails, owners
}

var authRE = regexp.MustCompile(`(?i)authori[sz]ation|not authori[sz]ed|revoked`)

// AT001 is the solo developer, standalone.
var AT001 = Scenario{
	ID: "at001",
	Scenario: "AT-001, the solo developer, standalone: a fresh scratch home; mashctl init --local --owner alice --start; for design and review, " +
		"mash key create, mashctl endpoint create alice/dev/<name> --public-key, mash credential create; two sessions (mash serve) with " +
		"MASH_OWNER=alice MASH_GROUP=dev MASH_NAME=design|review; mail each way; endpoint list --json; service metadata (mash who --json); " +
		"mash admin permissions alice.dev.design; mashctl endpoint delete alice/dev/review, then the review session's connection, a reconnect and mash status.",
	Body: func(a *AT) {
		h, err := a.Home("home")
		if !a.C.Check("a fresh scratch home is made", err == nil, "") || !a.StartServer(h) {
			return
		}
		for _, n := range []string{"design", "review"} {
			if _, ok := a.provision(h, h, "alice", "dev", n, "", false); !ok {
				return
			}
		}
		var procs = map[string]*Proc{}
		envs := map[string][]string{}
		for _, n := range []string{"design", "review"} {
			p, env, err := a.StartSession(h, "dev", n)
			detail := ""
			if err != nil {
				detail = redact(err.Error())
			}
			if !a.C.Check("the "+n+" session starts with MASH_GROUP=dev and completes the MCP handshake", err == nil, detail) {
				return
			}
			procs[n], envs[n] = p, env
		}
		a.C.Check("both sessions are online", a.waitOnline(h, envs["design"], "alice.dev.design", "alice.dev.review"), "")

		a.mailVerified("design-to-review", h, envs["design"], envs["review"], "alice.dev.review", "hello review from design")
		a.mailVerified("review-to-design", h, envs["review"], envs["design"], "alice.dev.design", "hello design from review")

		r := a.Ctl("endpoint-list", h, a.Conn(), "endpoint", "list", "--json")
		a.C.Check("mashctl endpoint list --json succeeds", r.Code == 0, exitDetail(r))
		groups := map[string]string{}
		if v, err := parseJSON(r.Stdout); err == nil {
			for _, o := range findObjects(v, "group") {
				inst, _ := o["instance"].(string)
				g, _ := o["group"].(string)
				groups[inst] = g
			}
		}
		a.C.Check("endpoint list --json shows design and review, each with group dev", groups["design"] == "dev" && groups["review"] == "dev", fmt.Sprintf("groups seen: %v", groups))

		es, _ := a.who(h, envs["design"])
		for _, n := range []string{"design", "review"} {
			var got *whoEntry
			for i := range es {
				if es[i].Identity == "alice."+"dev."+n {
					got = &es[i]
				}
			}
			ok := got != nil && got.Agent == "claude-code" && got.Owner == "alice" && got.Group == "dev" && got.Instance == n
			d := "not listed"
			if got != nil {
				d = fmt.Sprintf("agent %q owner %q group %q instance %q", got.Agent, got.Owner, got.Group, got.Instance)
			}
			a.C.Check("REQ-006: the service metadata of "+n+" shows agent claude-code and its owner, group and instance", ok, d)
		}

		a.permissions(h, "alice", "dev", "design")
		a.issuedPermissions(filepath.Join(h.Dir, "alice.dev.design.jwt"), "alice", "dev", "design")

		// endpoint delete retires the creds file in the config directory, so the revoked key is kept at a private path first.
		revokedCreds := filepath.Join(a.Dir, "review-before-delete.creds")
		kept := false
		if b, err := os.ReadFile(filepath.Join(h.Config, "alice.dev.review.creds")); err == nil {
			kept = os.WriteFile(revokedCreds, b, 0o600) == nil
			a.Watch("jwt review-creds", revokedCreds)
			a.Watch("seed review-creds", revokedCreds)
		}
		a.C.Check("review's creds file is copied to a private path before the delete", kept, "")
		revEnv := append(slices.Clone(envs["review"]), "MASH_CREDS="+revokedCreds)
		r = a.Ctl("endpoint-delete-review", h, a.Conn(), "endpoint", "delete", "alice/dev/review")
		a.C.Check("mashctl endpoint delete alice/dev/review succeeds", r.Code == 0, exitDetail(r))
		deadline := time.Now().Add(30 * time.Second)
		closed := false
		for !closed && time.Now().Before(deadline) {
			p := procs["review"]
			closed = p.Exited() || authRE.MatchString(p.Stderr()+p.Stdout())
			if !closed {
				time.Sleep(300 * time.Millisecond)
			}
		}
		a.C.Check("REQ-011: the review session's connection is closed (its serve exits or reports the authorization failure)", closed, "")
		r = a.Mash("reconnect-review", h, revEnv, "inbox")
		a.C.Check("REQ-011: a reconnect by the review key is refused as not authorized", r.Code != 0 && authRE.MatchString(r.Text()), exitDetail(r))
		r = a.Mash("status-review", h, revEnv, "status")
		a.C.Check("REQ-011: mash status says why (an authorization failure)", authRE.MatchString(r.Text()), exitDetail(r))
	},
}

// AT002 is the key crosses no machine.
var AT002 = Scenario{
	ID: "at002",
	Scenario: "AT-002, the key crosses no machine: two scratch homes, A (mashctl host) and B (endpoint). B runs mash key create; A runs " +
		"mashctl endpoint create alice/dev/design --public-key with B's public key file; B runs mash credential create and connects; the endpoint " +
		"mails a peer. All captured output and A's home are searched for B's seed, and all captured output for the JWT.",
	Body: func(a *AT) {
		ha, err1 := a.Home("home-a")
		hb, err2 := a.Home("home-b")
		if !a.C.Check("two scratch homes are made", err1 == nil && err2 == nil, "") || !a.StartServer(ha) {
			return
		}
		e, ok := a.provision(ha, hb, "alice", "dev", "design", "", false)
		if !ok {
			return
		}
		if _, ok := a.provision(ha, hb, "alice", "dev", "review", "", true); !ok { // the peer: a record and an inbox, no session
			return
		}
		p, env, err := a.StartSession(hb, "dev", "design")
		d := ""
		if err != nil {
			d = redact(err.Error())
		}
		if !a.C.Check("B's design session connects with the creds file it assembled", err == nil, d) {
			return
		}
		a.C.Check("B's design session is online", a.waitOnline(hb, env, "alice.dev.design"), "")
		r := a.Mash("send-to-peer", hb, env, "send", "alice.dev.review", "mail from the endpoint on B")
		a.C.Check("the endpoint on B mails its peer", r.Code == 0, exitDetail(r))
		r = a.Ctl("inbox-inspect-peer", ha, a.Conn(), "inbox", "inspect", "alice.dev.review", "--json")
		a.C.Check("A sees the mail in the peer's inbox (sent by alice.dev.design)", r.Code == 0 && (strings.Contains(r.Stdout, "alice.dev.design") || strings.Contains(r.Stdout, "alice_dev_design")), exitDetail(r))
		_ = p

		seed, jwtText := a.secrets["seed design"], a.secrets["jwt design"]
		a.C.Check("the seed and the JWT of B's design were read for the search", len(seed) > 0 && len(jwtText) > 0, "")
		hits, err := scanFor(ha.Dir, map[string][]byte{"B's seed": seed})
		a.C.Check("a search of A's whole home for B's seed finds nothing", err == nil && len(hits) == 0, hitFiles(hits))
		hits, err = scanFor(a.srvDir, map[string][]byte{"B's seed": seed})
		a.C.Check("a search of the server directory for B's seed finds nothing", err == nil && len(hits) == 0, hitFiles(hits))
		hits, err = scanFor(a.R.Out, map[string][]byte{"B's seed": seed})
		a.C.Check("a search of all captured output for B's seed finds nothing", err == nil && len(a.ownLeaks(hits)) == 0, hitFiles(hits))
		hits, err = scanFor(a.R.Out, map[string][]byte{"B's JWT": jwtText})
		a.C.Check("no captured output contains the JWT", err == nil && len(a.ownLeaks(hits)) == 0, hitFiles(hits))
		_ = e
	},
}

func hitFiles(h []Leak) string {
	var s []string
	for _, l := range h {
		s = append(s, l.File)
	}
	return strings.Join(s, ", ")
}
