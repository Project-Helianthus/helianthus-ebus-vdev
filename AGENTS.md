# AGENTS.md

## Purpose and boundaries

`helianthus-ebus-vdev` is a virtual eBUS device emulator. It owns virtual
device implementations, slave-response logic, frame handling, data-source
adapters, configuration, and the `vdev` binary. The initial device is a VR90
room controller.

Do not add low-level eBUS transport primitives, registry/schema ownership,
gateway API ownership, or master-side bus operations. Preserve bounded timing,
collision detection, and last-known-good device data on partial source failures
where the owning contract permits it.

## Workflow

1. Reconcile `origin/main`, local changes, related issues, branches, PRs,
   reviews, and checks before work.
2. Use one scoped issue, a branch named `issue/<number>-<slug>` from current
   `main`, and one linked PR.
3. Keep changes narrow; add focused tests when behavior changes. Use RED-first
   evidence where practical for protocol, timing, persistence, recovery, or
   safety behavior.
4. Run `gofmt -w $(git ls-files '*.go')`, `GOWORK=off go vet ./...`,
   `GOWORK=off go build ./...`, and `GOWORK=off go test -race -count=1 ./...`
   before pushing. State commands and results in the PR, then obtain fresh
   review for the full PR head.
5. Do not merge without green applicable checks, resolved blocking findings,
   and any required public documentation. Stop at the requested boundary.

`ORCHESTRATOR` and `CO_PILOT` are portable reasoning roles. Use a co-pilot for
planning, bounded implementation, adversarial review, or a second opinion; do
not spend that role on routine reads, searches, polling, or shell inspection.
If it is unavailable, continue with an independent fresh review when the risk
justifies it.

## Safety, privacy, and documentation

Emulation can respond on a real bus. Do not run it against a live adapter,
install it, alter credentials, or make any device-affecting change without
explicit operator confirmation at action time. Keep discovery and responses
bounded; avoid destructive state replacement on partial read failures.
Never commit credentials, personal identifiers, network coordinates, device
fingerprints, or private/raw captures; use sanitized, minimal fixtures.

Protocol or externally visible emulator behavior changes need proportionate
conformance evidence and public documentation in
[`helianthus-docs-ebus`](https://github.com/Project-Helianthus/helianthus-docs-ebus).
Keep device-specific claims evidence-bound and mark unsupported behavior as
unknown rather than inferred.
