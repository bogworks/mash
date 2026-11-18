# Run record at001: 20261118T190226Z-478df28f

- Run id: 20261118T190226Z-478df28f
- Revision: e26332cdd55342538791c3088c85cc5ca2b09c59 (clean)

## Environment

- linux/amd64
- go: go1.27.4
- nats-server: v2.15.1 (built from the go.mod pin v2.15.1)
- scratch (relative to the run directory): at001/home

## Scenario

AT-001, the solo developer, standalone: a fresh scratch home; mashctl init --local --owner alice --start; for design and review, mash key create, mashctl endpoint create alice/dev/<name> --public-key, mash credential create; two sessions started through the plugin launcher and hooks, as Claude Code starts the plugin (sh plugin/scripts/mash serve with the env of plugin/.mcp.json, MASH_BIN set to the built binary, no download; MASH_OWNER=alice MASH_GROUP=dev MASH_NAME=design|review); mail each way through the sessions' MCP send tool, delivered by the hook commands of plugin/hooks/hooks.json (UserPromptSubmit and PostToolUse, run under sh -c with the hook JSON on stdin) with the sender's verified trust label, cross-checked with the MCP inbox and read tools; endpoint list --json; service metadata (mash who --json); mash admin permissions alice.dev.design; mashctl endpoint delete alice/dev/review, then the review session's connection, a reconnect and mash status, both through the plugin launcher. Not driven: a real Claude Code session (the harness plays Claude Code's two roles, starting the MCP server and running the hooks with their JSON, and no model is involved).

## Expected

- a fresh scratch home is made
- mashctl init --local --owner alice --start brings up one local server
- REQ-014: the quick start runs with no override: the environment of init, serve and the ctl commands holds no MASH_INSECURE_PLAINTEXT, and the URL is tls://
- mash key create makes design's key on its own machine
- design seed file is mode 0600
- mashctl endpoint create alice/dev/design --public-key signs for the endpoint's own key
- design JWT file is mode 0600
- mash credential create assembles design's creds file on its machine
- mash key create makes review's key on its own machine
- review seed file is mode 0600
- mashctl endpoint create alice/dev/review --public-key signs for the endpoint's own key
- review JWT file is mode 0600
- mash credential create assembles review's creds file on its machine
- plugin/.mcp.json and plugin/hooks/hooks.json declare the launcher serve with MASH_SPOOL_FROM_START=1 and MASH_PLUGIN_ROOT, and the UserPromptSubmit and PostToolUse hook commands
- the design session starts through the plugin launcher (sh plugin/scripts/mash serve) with MASH_GROUP=dev and completes the MCP handshake
- the design session's process, started by the launcher, is the harness's built mash (the launcher resolved MASH_BIN)
- the review session starts through the plugin launcher (sh plugin/scripts/mash serve) with MASH_GROUP=dev and completes the MCP handshake
- the review session's process, started by the launcher, is the harness's built mash (the launcher resolved MASH_BIN)
- both sessions are online
- REQ-014: mash status for a session says tls: OK (no override set)
- mail design-to-review (UserPromptSubmit): the design session's MCP send tool succeeds
- mail design-to-review (UserPromptSubmit): the UserPromptSubmit hook command from hooks.json exits 0
- mail design-to-review (UserPromptSubmit): the UserPromptSubmit hook shows it as mash mail context with the text
- mail design-to-review (UserPromptSubmit): the hook shows the sender's verified trust label (alice.dev.design [verified])
- mail design-to-review (UserPromptSubmit): cross-check, the recipient's MCP inbox tool lists it from alice.dev.design as verified
- mail design-to-review (UserPromptSubmit): cross-check, the recipient's MCP read tool shows the text, verified
- mail review-to-design (PostToolUse): the review session's MCP send tool succeeds
- mail review-to-design (PostToolUse): the PostToolUse hook command from hooks.json exits 0
- mail review-to-design (PostToolUse): the PostToolUse hook shows it as mash mail context with the text
- mail review-to-design (PostToolUse): the hook shows the sender's verified trust label (alice.dev.review [verified])
- mail review-to-design (PostToolUse): cross-check, the recipient's MCP inbox tool lists it from alice.dev.review as verified
- mail review-to-design (PostToolUse): cross-check, the recipient's MCP read tool shows the text, verified
- mail design-to-review (PostToolUse): the design session's MCP send tool succeeds
- mail design-to-review (PostToolUse): the PostToolUse hook command from hooks.json exits 0
- mail design-to-review (PostToolUse): the PostToolUse hook shows it as mash mail context with the text
- mail design-to-review (PostToolUse): the hook shows the sender's verified trust label (alice.dev.design [verified])
- mail design-to-review (PostToolUse): cross-check, the recipient's MCP inbox tool lists it from alice.dev.design as verified
- mail design-to-review (PostToolUse): cross-check, the recipient's MCP read tool shows the text, verified
- mail review-to-design (UserPromptSubmit): the review session's MCP send tool succeeds
- mail review-to-design (UserPromptSubmit): the UserPromptSubmit hook command from hooks.json exits 0
- mail review-to-design (UserPromptSubmit): the UserPromptSubmit hook shows it as mash mail context with the text
- mail review-to-design (UserPromptSubmit): the hook shows the sender's verified trust label (alice.dev.review [verified])
- mail review-to-design (UserPromptSubmit): cross-check, the recipient's MCP inbox tool lists it from alice.dev.review as verified
- mail review-to-design (UserPromptSubmit): cross-check, the recipient's MCP read tool shows the text, verified
- mashctl endpoint list --json succeeds
- endpoint list --json shows design and review, each with group dev
- REQ-006: the service metadata of design shows agent claude-code and its owner, group and instance
- REQ-006: the service metadata of review shows agent claude-code and its owner, group and instance
- mash admin permissions alice.dev.design succeeds
- REQ-005: the credential's publish and subscribe set is exactly the contract's
- INV-002: no line names another owner or a wildcard in the owner slot
- REQ-005: the issued credential's publish and subscribe set is exactly the contract's (decoded from the JWT)
- review's creds file is copied to a private path before the delete
- mashctl endpoint delete alice/dev/review succeeds
- REQ-011: the review session's connection is closed (its serve exits or reports the authorization failure)
- REQ-011: a reconnect by the review key through the plugin launcher is refused as not authorized
- REQ-011: mash status through the plugin launcher says why (an authorization failure)
- no download or install happened: the launcher resolved MASH_BIN (no launcher message in serve stderr or hook.log, no bin directory in CLAUDE_PLUGIN_DATA) and hook.log holds no seed or JWT
- INV-001: no command output holds a seed or a JWT
- INV-001: no command output holds a key or JWT this scenario made
- INV-001: every command's output was captured completely (no capture or read error)

## Observed

- pass: a fresh scratch home is made
- pass: mashctl init --local --owner alice --start brings up one local server (exit 0)
- pass: REQ-014: the quick start runs with no override: the environment of init, serve and the ctl commands holds no MASH_INSECURE_PLAINTEXT, and the URL is tls://
- pass: mash key create makes design's key on its own machine (exit 0)
- pass: design seed file is mode 0600 (mode 0600)
- pass: mashctl endpoint create alice/dev/design --public-key signs for the endpoint's own key (exit 0)
- pass: design JWT file is mode 0600 (mode 0600)
- pass: mash credential create assembles design's creds file on its machine (exit 0)
- pass: mash key create makes review's key on its own machine (exit 0)
- pass: review seed file is mode 0600 (mode 0600)
- pass: mashctl endpoint create alice/dev/review --public-key signs for the endpoint's own key (exit 0)
- pass: review JWT file is mode 0600 (mode 0600)
- pass: mash credential create assembles review's creds file on its machine (exit 0)
- pass: plugin/.mcp.json and plugin/hooks/hooks.json declare the launcher serve with MASH_SPOOL_FROM_START=1 and MASH_PLUGIN_ROOT, and the UserPromptSubmit and PostToolUse hook commands
- pass: the design session starts through the plugin launcher (sh plugin/scripts/mash serve) with MASH_GROUP=dev and completes the MCP handshake
- pass: the design session's process, started by the launcher, is the harness's built mash (the launcher resolved MASH_BIN)
- pass: the review session starts through the plugin launcher (sh plugin/scripts/mash serve) with MASH_GROUP=dev and completes the MCP handshake
- pass: the review session's process, started by the launcher, is the harness's built mash (the launcher resolved MASH_BIN)
- pass: both sessions are online
- pass: REQ-014: mash status for a session says tls: OK (no override set) (config: OK - url tls://127.0.0.1:20000, owner alice)
- pass: mail design-to-review (UserPromptSubmit): the design session's MCP send tool succeeds (sent #1 to alice.dev.review)
- pass: mail design-to-review (UserPromptSubmit): the UserPromptSubmit hook command from hooks.json exits 0 (exit 0)
- pass: mail design-to-review (UserPromptSubmit): the UserPromptSubmit hook shows it as mash mail context with the text
- pass: mail design-to-review (UserPromptSubmit): the hook shows the sender's verified trust label (alice.dev.design [verified])
- pass: mail design-to-review (UserPromptSubmit): cross-check, the recipient's MCP inbox tool lists it from alice.dev.design as verified
- pass: mail design-to-review (UserPromptSubmit): cross-check, the recipient's MCP read tool shows the text, verified
- pass: mail review-to-design (PostToolUse): the review session's MCP send tool succeeds (sent #1 to alice.dev.design)
- pass: mail review-to-design (PostToolUse): the PostToolUse hook command from hooks.json exits 0 (exit 0)
- pass: mail review-to-design (PostToolUse): the PostToolUse hook shows it as mash mail context with the text
- pass: mail review-to-design (PostToolUse): the hook shows the sender's verified trust label (alice.dev.review [verified])
- pass: mail review-to-design (PostToolUse): cross-check, the recipient's MCP inbox tool lists it from alice.dev.review as verified
- pass: mail review-to-design (PostToolUse): cross-check, the recipient's MCP read tool shows the text, verified
- pass: mail design-to-review (PostToolUse): the design session's MCP send tool succeeds (sent #2 to alice.dev.review)
- pass: mail design-to-review (PostToolUse): the PostToolUse hook command from hooks.json exits 0 (exit 0)
- pass: mail design-to-review (PostToolUse): the PostToolUse hook shows it as mash mail context with the text
- pass: mail design-to-review (PostToolUse): the hook shows the sender's verified trust label (alice.dev.design [verified])
- pass: mail design-to-review (PostToolUse): cross-check, the recipient's MCP inbox tool lists it from alice.dev.design as verified
- pass: mail design-to-review (PostToolUse): cross-check, the recipient's MCP read tool shows the text, verified
- pass: mail review-to-design (UserPromptSubmit): the review session's MCP send tool succeeds (sent #2 to alice.dev.design)
- pass: mail review-to-design (UserPromptSubmit): the UserPromptSubmit hook command from hooks.json exits 0 (exit 0)
- pass: mail review-to-design (UserPromptSubmit): the UserPromptSubmit hook shows it as mash mail context with the text
- pass: mail review-to-design (UserPromptSubmit): the hook shows the sender's verified trust label (alice.dev.review [verified])
- pass: mail review-to-design (UserPromptSubmit): cross-check, the recipient's MCP inbox tool lists it from alice.dev.review as verified
- pass: mail review-to-design (UserPromptSubmit): cross-check, the recipient's MCP read tool shows the text, verified
- pass: mashctl endpoint list --json succeeds (exit 0)
- pass: endpoint list --json shows design and review, each with group dev (groups seen: map[design:dev review:dev])
- pass: REQ-006: the service metadata of design shows agent claude-code and its owner, group and instance (agent "claude-code" owner "alice" group "dev" instance "design")
- pass: REQ-006: the service metadata of review shows agent claude-code and its owner, group and instance (agent "claude-code" owner "alice" group "dev" instance "review")
- pass: mash admin permissions alice.dev.design succeeds (exit 0)
- pass: REQ-005: the credential's publish and subscribe set is exactly the contract's
- pass: INV-002: no line names another owner or a wildcard in the owner slot
- pass: REQ-005: the issued credential's publish and subscribe set is exactly the contract's (decoded from the JWT)
- pass: review's creds file is copied to a private path before the delete
- pass: mashctl endpoint delete alice/dev/review succeeds (exit 0)
- pass: REQ-011: the review session's connection is closed (its serve exits or reports the authorization failure)
- pass: REQ-011: a reconnect by the review key through the plugin launcher is refused as not authorized (exit 1)
- pass: REQ-011: mash status through the plugin launcher says why (an authorization failure) (exit 0)
- pass: no download or install happened: the launcher resolved MASH_BIN (no launcher message in serve stderr or hook.log, no bin directory in CLAUDE_PLUGIN_DATA) and hook.log holds no seed or JWT
- pass: INV-001: no command output holds a seed or a JWT
- pass: INV-001: no command output holds a key or JWT this scenario made
- pass: INV-001: every command's output was captured completely (no capture or read error)

## Result

PASS
