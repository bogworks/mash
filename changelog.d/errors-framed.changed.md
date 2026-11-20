An error before a command runs (a bad option, an unknown command, a configuration or endpoint that cannot be used) is now
one `error:` line with a `hint:` line on stderr, as the command's own errors are. With `--json` it is `{"error","hint"}`
on stdout and nothing on stderr. The exit codes are 1 for a failure and 2 for a usage error. `mash send` with no message and no `--file` stops
with `no message text arrived on standard input` when nothing arrives before the command's deadline (`cli_timeout`).
