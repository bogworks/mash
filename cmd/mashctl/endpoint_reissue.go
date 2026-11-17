package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/nats-io/jwt/v2"

	"github.com/bogworks/mash/internal/admin"
	"github.com/bogworks/mash/internal/config"
	"github.com/bogworks/mash/internal/ctl"
	"github.com/bogworks/mash/internal/enroll"
	"github.com/bogworks/mash/internal/sap"
)

const endpointReissueUsage = `usage: mashctl endpoint reissue ADDRESS (--public-key FILE | --make-key) [--jwt-out FILE]
                                [--servers N] [--sys-creds FILE] [--dry-run] [--json]
  ADDRESS is alice/dev/design. The old key is revoked on every server, then the directory record names the new key.
  --public-key FILE  sign the new public key the endpoint made; the JWT is written to --jwt-out
  --make-key         make the key on this host and replace <config dir>/<address>.creds
  The bulk forms (--all, --changed) are not available: run this once per endpoint.
  exit codes: 0 done or planned, 1 refused, 2 usage, 6 error
Example:
  mashctl endpoint reissue alice/dev/design --public-key design.pub
`

// runEndpointReissue is `mashctl endpoint reissue`.
func runEndpointReissue(args []string, env func(string) string, lookup config.Lookup, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprint(stdout, endpointReissueUsage)
		return 0
	}
	var c epCommon
	var positional string
	usage := func(msg string) int {
		fmt.Fprintf(stderr, "mashctl: endpoint reissue: %s\n%s", msg, endpointReissueUsage)
		return 2
	}
	for i := 0; i < len(args); i++ {
		flag, inline, hasInline := splitFlag(args, i)
		get := func() (string, error) { return flagValue(args, &i, flag, inline, hasInline) }
		if ok, err := c.take(flag, get); ok {
			if err != nil {
				return usage(err.Error())
			}
			continue
		}
		switch {
		case flag == "--all" || flag == "--changed" || flag == "--yes":
			return usage("the bulk forms (--all, --changed) are not available; run endpoint reissue ADDRESS once per endpoint")
		case strings.HasPrefix(flag, "-"):
			return usage("unknown " + config.RejectedArg(flag))
		case positional != "":
			return usage("one endpoint address only")
		default:
			positional = flag
		}
	}
	doc := epDoc{Schema: endpointSchema, Outcome: "refused"}
	var addr sap.Address
	refuse := func(code int, ref *epRefusal) int {
		doc.Refusal = ref
		if code == 2 {
			doc.Outcome = "error"
		}
		return epReport("reissue", doc, nil, code, stdout, stderr, c.jsonOut)
	}
	if positional == "" {
		return refuse(2, &epRefusal{"address_missing", "no endpoint address: write alice/dev/design"})
	}
	addr, err := parseEndpointRef(positional)
	if err != nil {
		return refuse(2, &epRefusal{"name_invalid", err.Error()})
	}
	doc.Endpoint = epEndpointOf(addr)
	switch {
	case c.makeKey && c.publicKey != "":
		return refuse(2, &epRefusal{"key_both", "give one of --public-key FILE and --make-key, not both"})
	case !c.makeKey && c.publicKey == "":
		return refuse(2, &epRefusal{"key_missing", "a reissue needs a new key: write --public-key FILE (the endpoint's new public key, from mash key create) or --make-key (make the key on this host)"})
	}
	servers, err := parseServers(env)
	if err != nil {
		return refuse(2, &epRefusal{"servers_invalid", err.Error()})
	}
	cfg, err := loadCtlConfig(env, lookup, true)
	if err != nil {
		return epErr(doc, "reissue", "config", err.Error(), c.jsonOut, stdout, stderr)
	}

	var pub, outPath string
	var outFile *epFile
	var ref *epRefusal
	if c.makeKey {
		p, err := credsFileFor(cfg, addr)
		if err != nil {
			return refuse(1, &epRefusal{"no_config_dir", "no config directory to write the creds file in; set MASH_CONFIG_DIR"})
		}
		outPath = p
		doc.Creds = &epFile{Path: p}
		outFile = doc.Creds
	} else {
		if pub, ref = epReadPublicKey(c.publicKey); ref != nil {
			return refuse(1, ref)
		}
		outPath = c.jwtOut
		display := c.jwtOut
		if outPath == "" {
			outPath = addr.String() + ".jwt"
			display = "./" + outPath
		}
		if ref := epPathFree(outPath); ref != nil {
			return refuse(1, ref)
		}
		doc.JWT = &epFile{Path: display}
		outFile = doc.JWT
	}
	keys, acct, err := epAccountOf(cfg, env)
	if err != nil {
		return refuse(1, &epRefusal{"no_account_key", err.Error()})
	}
	announceTarget(cfg, env, stderr, nil)
	adm, ctx, closeAdmin, err := ctl.OpenAdmin("mashctl", cfg)
	if err != nil {
		return epErr(doc, "reissue", "server", admin.RewordUnavailable(err.Error())+"; nothing was changed", c.jsonOut, stdout, stderr)
	}
	defer closeAdmin()

	burned, err := adm.IsBurned(ctx, addr)
	if err != nil {
		return epErr(doc, "reissue", "server", admin.RewordUnavailable(err.Error()), c.jsonOut, stdout, stderr)
	}
	if burned {
		return refuse(1, &epRefusal{enroll.CodeKeyRetired, fmt.Sprintf("endpoint %s was retired and its address is never reassigned", addr)})
	}
	switch _, _, err := adm.UserOf(ctx, addr); {
	case errors.Is(err, admin.ErrNoRecord):
		return refuse(1, &epRefusal{"endpoint_missing", fmt.Sprintf("endpoint %s does not exist; create it with: mashctl endpoint create %s", addr, epShortcut(addr))})
	case err != nil:
		return epErr(doc, "reissue", "server", admin.RewordUnavailable(err.Error()), c.jsonOut, stdout, stderr)
	}

	var step epStep
	if c.makeKey {
		step = epStep{Sender: "endpoint", Step: "session", Action: "restart the sessions of this endpoint so they read the new creds",
			Address: addr.String(), Purpose: "use", Detail: "creds file " + outFile.Path}
	} else {
		step = epNextCredential(addr, outFile.Path)
	}
	doc.NextSteps = []epStep{step}
	lines := func(w io.Writer) {
		fmt.Fprintf(w, "  address  %s\n", addr)
		if c.makeKey {
			fmt.Fprintf(w, "  creds    %s\n", outFile.Path)
			fmt.Fprintln(w, "next: restart the sessions of this endpoint so they read the new creds")
		} else {
			fmt.Fprintf(w, "  jwt      %s\n", outFile.Path)
			fmt.Fprintln(w, "next: on the endpoint's machine, run: "+step.Detail)
		}
	}
	if c.dryRun {
		doc.Outcome = "planned"
		return epReport("reissue", doc, func(w io.Writer) {
			fmt.Fprintf(w, "would reissue endpoint %s\n", epShortcut(addr))
			lines(w)
		}, 0, stdout, stderr, c.jsonOut)
	}

	if err := checkServerSet(ctx, cfg, env, servers); err != nil {
		return epErr(doc, "reissue", "servers", fmt.Sprintf("%v; %s", err, ctl.NotRevoked), c.jsonOut, stdout, stderr)
	}
	deps := enroll.Deps{Keys: keys, Admin: adm, Account: acct,
		Revoke: func(ctx context.Context, u string) error {
			var out bytes.Buffer
			err := revokeUsers(ctx, cfg, env, servers, []string{u}, &out, nil)
			stderr.Write(out.Bytes())
			return err
		}}
	res, err := enroll.Reissue(ctx, deps, addr, pub)
	if err != nil {
		if r, ok := epRefusalOf(err); ok {
			return refuse(1, r)
		}
		return epErr(doc, "reissue", "enroll", admin.RewordUnavailable(err.Error())+"; run the same command again to finish it", c.jsonOut, stdout, stderr)
	}
	defer clear(res.Seed)
	doc.UserPublicKey = res.PublicKey

	var werr error
	if c.makeKey {
		creds, ferr := jwt.FormatUserConfig(res.JWT, res.Seed)
		if ferr != nil {
			werr = errors.New("the creds could not be formatted")
		} else {
			werr = epReplaceCreds(outPath, creds)
			clear(creds)
		}
	} else {
		werr = epWriteNew(outPath, []byte(res.JWT+"\n"))
	}
	if werr != nil {
		finish := "run the same command with --public-key again to write the JWT"
		if c.makeKey {
			finish = fmt.Sprintf("the new key is lost; run: mashctl endpoint reissue %s --make-key", epShortcut(addr))
		}
		return epErr(doc, "reissue", "write", fmt.Sprintf("endpoint %s was reissued but %s was not written (%v); %s", addr, outPath, werr, finish), c.jsonOut, stdout, stderr)
	}
	outFile.Written = true
	doc.Outcome = "reissued"
	return epReport("reissue", doc, func(w io.Writer) {
		fmt.Fprintf(w, "reissued endpoint %s\n", epShortcut(addr))
		lines(w)
	}, 0, stdout, stderr, c.jsonOut)
}
