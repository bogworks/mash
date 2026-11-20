`mash status` is now one line: `acme.demo.bob: ready, offline` (or `ready, online` while an agent session holds the
endpoint open), or `acme.demo.bob: not ready: <why>` with `hint: mash doctor` on stderr and exit 1. It now connects to the
server and signs in, so it says whether mail works instead of listing settings. With several endpoints installed and none
chosen it stops with `error: more than one endpoint is installed here: ...` (exit 2); name one with `--endpoint`. With none installed it stops with `error: no endpoint is installed here` and `hint: mash join <file>` (exit 2). The
checks the old status listed moved to the new `mash doctor` (and an MCP `doctor` tool): one row each for config, creds,
server, sign-in and refused mail (a row that cannot run is `skip`), then a count and, for the first problem with a fix, a hint. It exits 1 when it finds a
problem. The version is in `mash version` and the mail count in `mash inbox`; the channel, plugin, hook-delivery, clock,
relay and name-rule rows are gone. Both commands take `--json`, which prints the same object as the matching MCP tool.
