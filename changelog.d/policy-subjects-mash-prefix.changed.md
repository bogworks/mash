The policy view moves from `agents.policy.<range>` to `mash.policy.<range>`, and the issue service from `agents.issue.<range>.<owner>` to `mash.issue.<range>.<owner>`, off SAP's reserved `agents` prefix
([ADR 0129](docs/decisions/0129-the-policy-view-and-the-issue-service-move-off-saps-agents-prefix-to-mash-policy-and-mash-issue.md)).
Both old subjects are still answered for this release. Upgrade in this order: upgrade `mashctl` and restart `policy serve` and `issue serve`;
then upgrade the endpoints; then, on a go, run the next apply on each adopted account. That apply pushes the new template once, which closes every
sender's connection once and reissues no credential. A resubmission of the list set in force is enough, and `mashctl policy diff` shows the push.
Move each wrangler's permission from `agents.issue.<range>.<owner>` to `mash.issue.<range>.<owner>` at any time in this release.
