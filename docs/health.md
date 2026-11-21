# Project health

This page says, plainly, how far you can trust each part of mash today, and how we know. It is a dated snapshot, not a promise: it describes one commit on one day, and it is only as current as the "Checked at" line at the bottom.

There is no score and no percentage. Each of eight areas gets one of three ratings:

- **Verified**: something runs and shows it. A CI job, a build attestation or a recorded test, which you can open and read.
- **Documented**: a document says it, but nothing that runs or was recorded shows it yet.
- **Missing**: neither.

"Verified" covers only what the evidence link shows, and the last column lists what is not covered. mash is pre-1.0; a good rating here is not an audit and not a claim that mash is production ready.

| Area | Rating | Evidence | What is missing |
|---|---|---|---|
| Installability | Documented | [install.md](install.md), [release.md](release.md), [v0.5.6 release](https://github.com/bogworks/mash/releases/tag/v0.5.6) | The README still shows the v0.5.5 install and says `mashctl` is not a release asset. No recorded run of the install steps on a clean machine against the published release. |
| Compatibility | Documented | [compatibility.md](compatibility.md) | Only Linux tests gate a merge or a release; the macOS test job is advisory. nats-server 2.15 and Claude Code 2.1.312 are the versions tests and receipts ran on; others are expected to work, not tested. WSL 2 is untested and native Windows is unsupported. |
| Reliability | Verified | [CI run on this commit](https://github.com/bogworks/mash/actions/runs/44910607059), [live-run records for 0.5.5](evidence/2026-11-17-0-5-5-live-runs/README.md), [known limits](operations.md#durability-and-sync_interval) | The live records are for 0.5.5; none was made for 0.5.6. A host crash can lose up to about 2 minutes of mail already reported as sent (a stated limit, not fixed). No load or soak results. |
| Security and supply chain | Verified | [Verify your download](security/verify-your-download.md), [release attestations](https://github.com/bogworks/mash/attestations), [security workflow run](https://github.com/bogworks/mash/actions/runs/44910606997), [SECURITY.md](../SECURITY.md) | What is verified: every v0.5.6 binary, script and `SHA256SUMS` carries a build provenance attestation from the release workflow at the tag, which anyone can check with `gh attestation verify`; vulnerability scans (govulncheck, CodeQL) run on every push and weekly; the release build is made twice and compared. Not covered: tags are annotated, not signed (the attestation is the proof); the release runs on the maintainers' own runners, so the build level is SLSA 2, not 3; no independent audit; no OpenSSF Scorecard result. |
| Documentation | Documented | [README](../README.md), [install.md](install.md), [operations.md](operations.md), [architecture.md](architecture.md) | The README is behind the release (names v0.5.5). Examples in the docs are not run by CI. Some pages still describe the releases before 0.5.6. |
| Maintenance | Documented | [CHANGELOG.md](../CHANGELOG.md), [SECURITY.md](../SECURITY.md#supported-versions) | Only the latest release is supported, and the changelog says 0.5.5 stays the version for deployments already running. No measured response time for issues or reports; the only commitment is acknowledging a security report within 5 business days. |
| Governance | Documented | [CONTRIBUTING.md](../CONTRIBUTING.md), [CODE_OF_CONDUCT.md](../CODE_OF_CONDUCT.md), [issue forms](https://github.com/bogworks/mash/tree/v0.5.6/.github/ISSUE_TEMPLATE) | One human maintainer: nearly every commit is by one person, and review and release depend on them. No governance or maintainers file and no code-owners file. |
| Licensing | Documented | [LICENSE](../LICENSE) (Apache-2.0) | No dependency licence report, and no NOTICE file. Each release binary has a CycloneDX software bill of materials listing its dependencies. |

## Reading the table

- Evidence for a Verified row is a run you can open. Evidence for a Documented row is the document that makes the claim.
- This page can be wrong in either direction. If a link does not show what the row says, that is a bug in this page; please open an issue.
- Security reports go to [SECURITY.md](../SECURITY.md), not to issues. How to check a download is in [verify-your-download.md](security/verify-your-download.md).

Checked at commit dd696e9ee8f688cef6fe1102410aa43d731f567b (v0.5.6) on 2026-11-21.
