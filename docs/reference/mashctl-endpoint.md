Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# mashctl endpoint

Create, change and remove endpoints.

Release: 0.5.9. Group: Endpoints.

## Usage

```text
mashctl endpoint <command> [options]
```

The global options of `mashctl` also apply: see
[mashctl](mashctl.md#global-options).

## Exit codes

| Code | Meaning |
| --- | --- |
| 0 | It worked. |
| 1 | It failed; one line on stderr says why. |
| 2 | The command line was wrong; nothing was attempted. |

## MCP tool

None. Administration. There are no MCP tools for mashctl.

## Commands

| Command | Description |
| --- | --- |
| [`mashctl endpoint create`](mashctl-endpoint-create.md) | Create an endpoint and its credentials. |
| [`mashctl endpoint delete`](mashctl-endpoint-delete.md) | Delete an endpoint, its inbox and its credentials. The address can never be used again. |
| [`mashctl endpoint list`](mashctl-endpoint-list.md) | List endpoints. |
| [`mashctl endpoint show`](mashctl-endpoint-show.md) | Show one endpoint. |
| [`mashctl endpoint set`](mashctl-endpoint-set.md) | Set a value on an endpoint's record. |
| [`mashctl endpoint unset`](mashctl-endpoint-unset.md) | Remove a value from an endpoint's record. |
| [`mashctl endpoint suspend`](mashctl-endpoint-suspend.md) | Refuse new mail to an endpoint. Its stored mail and its credentials are untouched. |
| [`mashctl endpoint resume`](mashctl-endpoint-resume.md) | Undo suspend. |
| [`mashctl endpoint reissue`](mashctl-endpoint-reissue.md) | Give an endpoint new credentials and cancel the old ones. |
| [`mashctl endpoint inbox`](mashctl-endpoint-inbox.md) | List what is in an endpoint's inbox, without showing any message's text. |
| [`mashctl endpoint permissions`](mashctl-endpoint-permissions.md) | Print the NATS permissions an endpoint's user needs, for someone who runs their own NATS. |
