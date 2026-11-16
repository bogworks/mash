# The credential commands for mashctl (design, 0.5.5)

Lane ctl, 2026-11-16. For lead. Status: a design for questions, no code. It is the command line over core's `internal/credential` (#698, ADR 0136, held, stacked on #688). Standalone first (ADR 0131): everything here works with one NATS account, the keys `mashctl` already holds, and no other service.

## 1. What the commands do

Core's primitive is "public key up, credential down". The endpoint makes its own key; `mashctl` signs a JWT for the public key with the owner group's scoped key; the endpoint puts its seed and the JWT together. `mashctl` never sees a seed. `credential.Issue` and `credential.Show` do the work. This design adds the verbs, the text and JSON forms, the exit codes, and the sentences that tell a person what to run next.

Four verbs, two sides:

| Where it runs | Verb | Does |
|---|---|---|
| the endpoint's host | `mashctl credential key --out FILE` | makes a user key; writes the seed to FILE (mode 0600, refuses to overwrite); prints the public key |
| the host with the keys | `mashctl credential issue --address owner.name --key PUB` | signs a credential for PUB; writes the JWT to a file or prints it |
| the endpoint's host | `mashctl credential install --seed FILE --jwt FILE --out CREDS` | makes the creds file from the endpoint's seed and the JWT (`credential.Assemble`) |
| the host with the keys | `mashctl credential show PUB` | the issue record and history of that key, whether the account JWT revokes it, whether it is connected now |

`key` and `install` need no NATS and no keys, so they run on a host that has only the `mashctl` binary. (0.5.5 ships `mashctl` in the release assets for this.)

## 2. The forms

```
mashctl credential key --out ep.seed
  public key: UABC...XYZ
  seed written to ep.seed (mode 0600); it never leaves this host
  next: on the host with the keys, run
    mashctl credential issue --address acme.build --key UABC...XYZ --out ep.jwt

mashctl credential issue --address acme.build --key UABC...XYZ --out ep.jwt
  issued: acme.build, key UABC...XYZ, generation 4
  tags: ep:acme.build ...
  jwt written to ep.jwt
  next: copy ep.jwt to the endpoint's host, then run
    mashctl credential install --seed ep.seed --jwt ep.jwt --out acme.build.creds

mashctl credential show UABC...XYZ
  key: UABC...XYZ   address: acme.build   revoked: no   connected: yes
  history: issued generation 4 at 2026-11-16T09:12:00Z (issue)
```

- **Input.** `issue` takes `--address`, `--key`, `--endpoint` (defaults to the endpoint the address names; see O3) and `--purpose issue|rotation` (default issue). Or `--request FILE` / `--request -`: the JSON object `credential.Parse` reads. `--request` conflicts with the other four flags.
- **Output.** Text by default. `--json` prints core's `Result` as one JSON object on stdout (outcome, address, public_key, jwt, tags, generation, issued_at, refusal) and nothing else on stdout; text goes to stderr only when `--json` is not set. `--out FILE` writes the JWT (mode 0644, it is not secret) and the result omits the `jwt` field in text mode.
- **A seed is never accepted.** A seed in `--key`, `--request` or stdin is refused with core's `seed_in_request` and is not echoed; the text says the key should be treated as exposed.
- **Server flags.** `issue` and `show` take `--servers N` and `--sys-creds FILE` like `add` (#695), and read the same environment.
- **Exit codes** follow ADR 0134 and `policydirect`: issued/shown 0, refused 1 (the result's `refusal.code` says why), error 6, usage 2. `show` of a key with no record is 1 (`not_found`).
- **`--help`** on every verb prints the usage and changes nothing (#697).

## 3. next_steps, as exact commands

`policydirect.Result.next_steps` (core, #688) lists, for each sender a policy touches, a step: first credential, new credential, rotate-and-revoke, revoke. A command that applies a policy prints each step as the command to run, with what only the endpoint knows left as a named placeholder:

```
next steps (4):
  acme.build  first credential   run on acme.build's host: mashctl credential key --out ep.seed
              then, where the keys are: mashctl credential issue --address acme.build --key <PUBLIC KEY from the line above>
  acme.test   rotate and revoke  run on acme.test's host: mashctl credential key --out ep.seed
              then: mashctl credential issue --address acme.test --key <NEW KEY> --purpose rotation
              then: revoke the old key (see O2)
```

The text is built from `NextStep{Sender, Step, Action, Address, Purpose, Detail}` by one pure function, `RenderNextSteps`, tested with golden files. In JSON the steps stay data (core's shape); a wrapper builds its own commands. The text names no host; "the endpoint's host" is the host that runs that sender.

## 4. Order for a new three-part agent

1. Host with the keys: `mashctl add` is not used. Apply the policy (0.5.5 `policy apply`), which prints the next steps.
2. Endpoint's host: `credential key`.
3. Keys' host: `credential issue`; copy the JWT over (any channel; it is not secret).
4. Endpoint's host: `credential install`.

`add` and `reissue` stay as they are (core's note: they stay until ctl and Randy decide). The seed travels with `add`; it does not with this path.

## 5. What it does not do

Revoke a key by public key as a verb (O2), expire a credential, take a signed request, or change `add`. It does not create the policy bucket or choose a first policy: with no list set in force `issue` refuses `no_list_set`.

## 6. Tests

- Unit: flag parsing, `--request` conflicts, seed refusal in each input, `RenderNextSteps` goldens, exit-code table.
- A table test (the one from #697) covers `--help` on the four verbs.
- Live, two servers: key on host B, issue on host A, install on B, connect; `show` finds the key; rotation issues a second key; a key bound to another address is refused. Seeds appear in no output (grep the captured output).

## 7. Open points for Randy or lead

- **O1.** The endpoint-side verbs (`key`, `install`) live in `mashctl credential`, not in `mash`. `mash` is what a session runs; `mashctl` is the admin tool but is also the binary that will be in the release assets. Choice: both binaries carry them (one package), or only mashctl.
- **O2.** Revoking the old key after a rotation: core says the existing revoke path (ADRs 0067, 0114) does it by public key, but there is no verb that takes a public key alone (`retire` takes a name). Add `mashctl credential revoke PUB` (small, uses `revokeUsers`), or leave it to `retire`?
- **O3.** `--endpoint`: required by core's `Request`. Default to `agent.<owner>.<name>` from the address, or require it? The default saves a flag; the agent token is a fixed word.
- **O4.** Where `issue` writes: stdout vs `--out`. A JWT is not secret, but `--out` makes the copy step a file copy. Keep both.
- **O5.** `credential show` takes a public key. A person usually has a name. Accept `--address owner.name` too and list every key of that address?
- **O6.** The text of the next steps names `mashctl credential key` on "the endpoint's host". That assumes the operator can run `mashctl` there. If not (a host with only `mash`), O1 decides it.
