// Package plugin embeds the Claude Code plugin (the files beside this one) so `mash plugin export` can write the
// exact plugin of this build. This Go file is not part of the plugin; Claude Code's loader ignores it.
package plugin

import "embed"

// Files holds the plugin as it is laid out in a plugin directory.
//
//go:embed .claude-plugin/plugin.json .mcp.json hooks/hooks.json scripts/mash
var Files embed.FS

// PiFiles holds the Pi extension. It is separate from Files so the Claude plugin's file set, and what
// `mash plugin export <dir>` writes, do not change.
//
//go:embed pi/mash.ts
var PiFiles embed.FS
