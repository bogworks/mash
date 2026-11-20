`mash who` filters again, by what an endpoint does: `--role <text>` keeps endpoints whose role contains the text (any
case), `--capability <name>` those that have that capability, and `--label <name>` or `--label <name=value>` those with
that label (or that exact value). `--capability` and `--label` can be given more than once, and all must match; they
combine with the prefix and `--online` or `--offline`. The `who` tool takes the same `role`, `capability` and `label`
(for the last two, a string or a list of strings), for example `mash who --capability review --online`.
