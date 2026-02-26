# AGENTS

This repository is part of the **Helianthus Multi-Protocol HVAC Gateway Platform**.

## Dual-AI Operating Model

All development follows the dual-AI orchestrator protocol defined in the workspace-root [`AGENTS.md`](../AGENTS.md):

- **Orchestrator:** Claude Code — orchestration, hard dev (complexity 7–10), angry tester, deep consultant
- **Co-Pilot:** Codex — adversarial planning, easy dev (complexity 1–6), code review, second opinions
- Phases: Adversarial Planning → Smart Routing → Dual Code Review
- Hard rules: one issue/PR per repo, squash+merge only, doc-gate, transport-gate, MCP-first

See the root AGENTS.md for the full protocol, routing tables, system prompts, and invariants.

---

## Repo-Specific Rules

These instructions apply to the entire repository.

1. Keep changes targeted to the requested issue.
2. Prefer small, composable packages under `internal/`.
3. Always run `gofmt ./...` before finishing.
4. Validate with `go build ./...` and `go test -race ./...` before opening a PR.
5. React (emoji) to every review comment and reply with status when actioned.
6. Keep repository terminology inclusive and consistent.
7. If a change modifies externally visible behavior, update `helianthus-docs-ebus` alongside the code change (doc-gate).
8. Transport/protocol changes require a full 88-case runtime matrix pass, unless explicitly overridden by owner approval.

## Repository Purpose

`helianthus-ebus-vdev` is a **virtual eBUS device emulator**. It emulates slave devices on the eBUS network (starting with VR90 room thermostat), connecting to the bus via an ENH adapter and receiving real sensor data from the gateway's GraphQL API.

## Dependencies

- `helianthus-ebusgo` — transport, protocol, emulation, types
- No dependency on `helianthus-ebusreg` or `helianthus-ebusgateway`

## Architecture

```
helianthus-ebus-vdev
├── cmd/vdev/          # binary entry point
└── internal/
    ├── bus/           # slave responder, frame reader
    ├── device/        # device interface + device implementations
    ├── datasource/    # data source interface + implementations (GraphQL, etc.)
    └── config/        # configuration loading
```
