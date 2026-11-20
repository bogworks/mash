The MCP `send` tool's parameters are now `address`, `message`, `kind`, `urgent`, `reply_to`, `relay` and `id`; they were
`to`, `text`, `in_reply_to` and `via` (and the old names are refused). Its receipt text is `Mail sent (number 42).`, and the same
object is in the result's structured content (`mash send --json` prints it).
`mash send` takes the message as one argument (quote it), or `--file`, or standard input, and has `--reply-to`, `--relay` and
`--id`. `mash version` prints one line, `mash v<version> (<commit>)`, with the first 7 characters of the commit, and the
MCP `version` tool prints the same line. `mash <verb> --json` on a verb that has no JSON form is now a usage error (exit 2).
`mash send --help` and `mash version --help` show the new generated layout.
