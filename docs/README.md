# mash documentation

Start with the [README](../README.md). This index is for version 0.5.6. What changed in each release: [CHANGELOG](../CHANGELOG.md).

## Get started

- [Quick start](guide/quickstart.md): two Claude Code sessions mailing each other
- [By hand](guide/by-hand.md): the same, typed yourself
- [Join from another machine](guide/join.md) and [use mash from a script](guide/scripts.md)
- Set up with an agent: the `mash-setup` plugin ([setup skill](../mash-setup/skills/setup/SKILL.md), [day-2 skill](../mash-setup/skills/day-2/SKILL.md))
- [Install](install.md): binaries, plugin, verify a download, configuration, launch

## Understand mash

- [The stack](guide/stack.md): an agent on plain SAP beside an agent on mash, layer by layer
- [mash and SAP](sap.md), and the short version, [mash versus SAP](guide/mash-vs-sap.md)
- [Glossary](reference/glossary.md)
- [How mash compares](comparison.md)
- [FAQ](faq.md)

## Use mash

- [Command reference](reference/README.md): `mash`, `mashctl`, the MCP tools and the rules (generated from `spec/`)
- [Troubleshooting](troubleshooting.md)

## Run a mesh (operators)

- [Operating mash](operations.md): `mashctl init --config`, the setup file, endpoints, limits
- [Local NATS setup script](reference/setup.md)
- [mashctl guide](mashctl.md) and [mashctl doctor](doctor.md): explanations; the syntax is in the reference
- [TLS, clusters and a second mashctl machine](tls-and-clusters.md)
- [Name rule file](name-rule.md)
- [Synadia Cloud](synadia.md): not supported
- [Coming in 0.6.0](reference/coming-in-0-6-0.md)

## Security

- [Security policy](../SECURITY.md)
- [Threat model](security/threat-model.md): a dated review, as of commit 7ebf0ab

## Contribute

- [Contributing](../CONTRIBUTING.md) and [change-log fragments](../changelog.d/README.md)
- [Architecture](architecture.md)
- [Decisions (ADRs)](decisions/INDEX.md)
- [Releasing](release.md)
- [Review gate](review/in-session-loop.md)

## Internal records (not user documentation)

These are working records of the project's own process. They are kept for the history and are not kept up to date.

- [Original v0.1 design](specs/2026-10-21-mash-design.md)
- [Policy engine design, not shipped](policy-enforcement.md)
- Work contracts: [docs/contracts/](contracts/) and [contracts/](../contracts/)
- Evidence and live-run receipts: [evidence/](evidence/), [receipts/](../receipts/), [scripts/liveat/README.md](../scripts/liveat/README.md)
- Release-note drafts: [release-notes/](release-notes/)
- [Demo recorded on 0.3.0](demo/README.md) (historical)
- Repository settings: [branch protection](branch-protection.md)
- Release-tool data: `release-cutover`, `allowed_signers`, `skipped-releases.txt`
