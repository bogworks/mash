# The credential commands for mashctl (design, 0.5.5)

2026-11-16. Status: design; the code is #703. It is the command line over core's `internal/credential` (#698, ADR 0136, held, stacked on #688). Standalone first (ADR 0131): everything here works with one NATS account, the keys `mashctl` already holds, and no other service.

## 1. What the commands do

Core's primitive is "public key up, credential down". The endpoint makes its own key; `mashctl` signs a JWT for the public key with the owner group's scoped key; the endpoint puts its seed and the JWT together. `mashctl` never sees a seed. `credential.Issue` and `credential.Show` do the work. This design adds the verbs, the text and JSON forms, the exit codes, and the sentences that tell a person what to run next.

Four verbs, two sides:

| Where it runs | Verb | Does |
|---|---|---|
| the endpoint's host | `mash credential key --out FILE` | makes a user key; writes the seed to FILE (mode 0600, refuses to overwrite); prints the public key |
| the host with the keys | `mashctl credential issue --address owner.name --key PUB` | signs a credential for PUB; writes the JWT to a file or prints it |
| the endpoint's host | `mash credential install --seed FILE --jwt FILE --out CREDS` | makes the creds file from the endpoint's seed and the JWT (`credential.Assemble`) |
| the host with the keys | `mashctl credential show PUB` | the issue record and history of that key, whether the account JWT revokes it, whether it is connected now |

`key` and `install` need no NATS and no keys, so they run on a host that has only the `mash` binary. A fifth verb, `mashctl credential revoke PUB`, revokes a key by public key.

## 2. The forms

```
mash credential key --out ep.seed
  public key: UABC...XYZ
  seed written to ep.seed (mode 0600); it never leaves this host
  next: on the host with the keys, run
    mashctl credential issue --address acme.build --key UABC...XYZ --out ep.jwt

mashctl credential issue --address acme.build --key UABC...XYZ --out ep.jwt
  issued: acme.build, key UABC...XYZ, generation 4
  tags: ep:acme.build ...
  jwt written to ep.jwt
  next: copy ep.jwt to the endpoint's host, then run
    mash credential install --seed ep.seed --jwt ep.jwt --out acme.build.creds

mashctl credential show UABC...XYZ
  key: UABC...XYZ   address: acme.build   revoked: no   connected: yes
  history: issued generation 4 at 2026-11-16T09:12:00Z (issue)
```

- **Input.** `issue` takes `--address`, `--key`, `--endpoint` (defaults to the address's endpoint in the directory; refused with the flag named when there is none or several) and `--purpose issue|rotation` (default issue). Or `--request FILE` / `--request -`: the JSON object `credential.Parse` reads. `--request` conflicts with the other four flags.
- **Output.** Text by default. `--json` prints core's `Result` as one JSON object on stdout (outcome, address, public_key, jwt, tags, generation, issued_at, refusal) and nothing else on stdout; text goes to stderr only when `--json` is not set. `--out FILE` writes the JWT (mode 0600) and the text result omits the `jwt` field.
- **A seed is never accepted.** A seed in `--key`, `--request` or stdin is refused with core's `seed_in_request` and is not echoed; the text says the key should be treated as exposed.
- **Server flags.** `issue` and `show` take `--servers N` and `--sys-creds FILE` like `add` (#695), and read the same environment.
- **Exit codes** follow ADR 0134 and `policydirect`: issued/shown 0, refused 1 (the result's `refusal.code` says why), error 6, usage 2. `show` of a key with no record is 1 (`not_found`).
- **`--help`** on every verb prints the usage and changes nothing (#697).

## 3. next_steps, as exact commands

`policydirect.Result.next_steps` (core, #688) lists, for each sender a policy touches, a step: first credential, new credential, rotate-and-revoke, revoke. A command that applies a policy prints each step as the command to run, with what only the endpoint knows left as a named placeholder:

```
next steps (4):
  acme.build  first credential   run on acme.build's host: mash credential key --out ep.seed
              then, where the keys are: mashctl credential issue --address acme.build --key <PUBLIC KEY from the line above>
  acme.test   rotate and revoke  run on acme.test's host: mash credential key --out ep.seed
              then: mashctl credential issue --address acme.test --key <NEW KEY> --purpose rotation
              then: mashctl credential revoke <OLD KEY>
```

The text is built from `NextStep{Sender, Step, Action, Address, Purpose, Detail}` by one pure function, `RenderNextSteps`, tested with golden files. In JSON the steps stay data (core's shape); a wrapper builds its own commands. The text names no host; "the endpoint's host" is the host that runs that sender.

## 4. Order of use

Section 8 gives the order for new endpoints and for rotation, step by step. The seed never travels on this path; with `mashctl add` it does, and under a list set in force `add` and `reissue` refuse (#702).

## 5. What it does not do

Write the endpoint host's `config.json`, expire a credential, take a signed request, or change `add`. It does not create the policy bucket or choose a first policy: with no list set in force `issue` refuses `no_list_set`.

## 6. Tests

- Unit: flag parsing, `--request` conflicts, seed refusal in each input, `RenderNextSteps` goldens, exit-code table.
- A table test (the one from #697) covers `--help` on the four verbs.
- Live, two servers: key on host B, issue on host A, install on B, connect; `show` finds the key; rotation issues a second key; a key bound to another address is refused. Seeds appear in no output (grep the captured output).

## 7. Decisions

- **Where the endpoint verbs live.** `mashctl` is not on the endpoint's host (ADR 0131). `key` and `install` are `mash credential key|install`: no NATS, no operator keys. `mashctl credential key|install` also exist for a single host, one shared implementation (`internal/credential`).
- **Revoke.** `mashctl credential revoke PUB [--servers N] [--sys-creds FILE] [--json]` revokes a key by public key. Outcome words and exit codes follow ADR 0134 (revoked and unchanged 0, refused 1, partial 5, error 6, usage 2). A key with no issue record is refused `not_found` unless a server already revokes it.
- **`--endpoint`.** Defaults to the endpoint of the address, found in the directory (a legacy `<name>` record is governed in place, #702). If the directory has none or more than one, refuse and name the flag.
- **Output.** `issue` prints the JWT on stdout by default (no seed in it); `--out FILE` writes it with mode 0600 and refuses an existing file without `--force`. Nothing that holds a seed goes to stdout: `key` writes the seed to its file and prints only the public key, and `install` writes the creds file itself.
- **`show`.** Takes an address (every key at it) or a public key.

## 8. The story, end to end

This builds on #702: a legacy endpoint is governed in place, and `add` and `reissue` refuse while a list set is in force. Three endpoints, `x`, `y` and `z`, and the policy "x may mail y".

| Step | Where | Command | Blocked on |
|---|---|---|---|
| 1 | keys' host | `mashctl add x`, `add y`, `add z`: the directory record and inbox (and a first creds file, which step 5 retires) | nothing |
| 2 | keys' host | apply the policy (`policy apply`) | **the first policy: the policy commands wait on the answers to #681; until they land this step and everything after it cannot run** |
| 3 | each endpoint's host | `mash credential key --out ep.seed` prints the public key | nothing (works today) |
| 4 | keys' host, once per endpoint | `mashctl credential issue --address owner.x --key UX... --out x.jwt`; copy the JWT over | step 2 (with no list set in force, issue refuses `no_list_set`) |
| 5 | each endpoint's host | `mash credential install --seed ep.seed --jwt x.jwt --out CREDS`; restart the session | step 4 |
| 6 | keys' host | `mashctl credential revoke UX_OLD` for the key `add` made; the policy's next steps name this step | step 5 (revoke after the restart, never before) |
| 7 | | x mails y: delivered. z mails y: refused, no list entry allows it | steps 2 to 6 |

Rotation of an endpoint: new key on its host, `issue --purpose rotation`, `install`, restart the session, then `revoke OLD`. The order is restart, then revoke, so the endpoint is never without a valid credential.

One allow entry grants mail, prompt, status and heartbeat together. Whether an allow can grant status alone is an open question with Randy; this design assumes it cannot.

## 9. Where things go on the endpoint's host

- **Creds file.** `install --out` takes any path. The path `mash` reads by default is `<config dir>/<name>.creds` for an endpoint with `MASH_NAME=x` (a legacy endpoint), and `<config dir>/<agent_owner_name>.creds` (the flat identity) for a three-token one; `MASH_CREDS` (or `creds` in `config.json`) names another path. `install` requires `--out`; pass the default path above, or any path and set `MASH_CREDS` to it.
- **The rest of the endpoint's config.** Nothing in this change writes `config.json`. The endpoint's host needs `url` (the NATS servers), `owner`, and `tls_ca` when the server uses a private CA; the operator sets them by hand or with the environment (`MASH_URL`, `MASH_OWNER`, `MASH_TLS_CA`). A command that writes it is out of scope here (section 5).
- **The seed.** `ep.seed` stays on the endpoint's host with mode 0600. It is read only by `install`. After `install` the seed also sits inside the creds file; the seed file can be deleted.

