Generated from spec/*.json by go run ./scripts/refgen. Do not edit.

# Planned, not yet released

What is planned and not yet released. None of it is in 0.6.1.

## Changes

- mash prompt becomes mash sap prompt. And the MCP tool reply moves with it.
- A mesh holds many owners. mashctl owner create, list and delete arrive. Mail
  between owners becomes a designed case, under policy.
- Policy. Planned, with no release named. Who may send to whom. mash ships
  open; narrowing is a choice.
- Asking to join. An endpoint can ask to join and be approved by an
  administrator or by policy.
- The MCP revision and how a session is woken. Planned: a study of the MCP
  Events extension as the way to wake a session. Channels stay until then.

## Commands

### `mash sap`

Plain Synadia Agent Protocol operations, for experts. Experimental.

Planned: later. Group: Raw SAP.

#### Usage

```text
mash sap [options]
```

The global options of `mash` also apply: see [mash](mash.md#global-options).

#### Exit codes

| Code | Meaning |
| --- | --- |
| 0 | It worked. |
| 1 | It failed; one line on stderr says why. |
| 2 | The command line was wrong; nothing was attempted. |

#### Output

##### Planned

Stream: stdout. Exit code: 0.

```text
(mash sap prompt, mash sap reply and what else SAP offers,
 still subject to mash's policy)
```

#### MCP tool

None. To be decided with the design.

#### What it replaces

In 0.5.5, this did not exist, and it does not exist in 0.5.6. Planned: mash
prompt and the reply tool move here, which is a breaking change.

### `mashctl owner`

Create, list and remove owners.

Planned: later. Group: Owners.

#### Usage

```text
mashctl owner [options]
```

The global options of `mashctl` also apply: see
[mashctl](mashctl.md#global-options).

#### Exit codes

| Code | Meaning |
| --- | --- |
| 0 | It worked. |
| 1 | It failed; one line on stderr says why. |
| 2 | The command line was wrong; nothing was attempted. |

#### Output

##### Planned

Stream: stdout. Exit code: 0.

```text
(mashctl owner create, list and delete)
```

#### MCP tool

None. Administration. There are no MCP tools for mashctl.

#### What it replaces

In 0.5.5, this did not exist. In 0.5.6 a mesh has the one owner named at
mashctl init, and nothing in the grammar assumes one. Several owners per mesh
are planned.
