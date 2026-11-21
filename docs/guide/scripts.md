# Using mash from a script

Every `mash` command takes `--json` and prints the object its MCP tool
returns, on one line. `mashctl` commands take it too.

```sh
MASH_ENDPOINT=acme.demo.bob mash send acme.demo.alice --json "ping"
MASH_ENDPOINT=acme.demo.alice mash read 2 --json
```

```json
{"to":"acme.demo.alice","number":2,"kind":"task","urgent":false,"duplicate":false,"retagged":false}
{"number":2,"from":"acme.demo.bob","sent":"2026-11-21T15:41:20Z","trust":"verified","kind":"task","urgent":false,"reply_to":null,"relay":null,"body":"ping"}
```

`body` is plain text, without the `> ` of the human form. Treat it as the
sender's words, as you would any input. To get just the text:

```sh
MASH_ENDPOINT=acme.demo.alice mash read 2 --json | jq -r .body
```

```
ping
```

Reading at a shell does not mark the message read for a session. Every field
of every command is in the [reference](../reference/README.md).
