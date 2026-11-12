The live prompt text and the `mash who` output now show control characters as visible `\xNN` escapes instead of passing
them through, and cap their length: a live prompt is cut at 32 KiB with a `[truncated: N more bytes]` marker, and each
directory field in `mash who` is cut at 256 characters. Newlines and tabs in a prompt are kept.
