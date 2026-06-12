# Cairn — Development Documentation

**Status:** Authoritative. This `dev-docs/` set supersedes `codex-docs/` for implementation. `codex-docs/` remains as historical concept input.

**Product:** Cairn — a cross-platform, Compose-first Docker management desktop app for Windows, macOS, and Linux. A clean Docker Desktop alternative that installs, detects, configures, and manages existing Docker-compatible backends. It never implements its own container runtime.

**Locked decisions (do not reopen without explicit owner approval):**

| Decision | Value |
|---|---|
| Desktop shell | Wails **v3** (alpha-track; API-stable; see [02-architecture.md §3](02-architecture.md)) |
| Backend language | Go **1.26.4** (pinned via `go.mod` `toolchain go1.26.4`; CI enforces exact version; upgrades only at phase boundaries) |
| Frontend | React 18+ + TypeScript (strict) + Vite + Tailwind CSS |
| Local store | SQLite (modernc.org/sqlite, CGO-free) |
| Docker control | Docker Engine API (Go SDK) for objects; official `docker compose` CLI for Compose lifecycle |
| Terminal UI | xterm.js; Compose/file viewer: Monaco (read-only in v1) |
| Frontend state | zustand |
| Charts | Recharts (single chart library) |
| Registry accounts | Docker Hub / GHCR / custom login via `docker login` delegation; image tag & push in v1 |
| First platform | Linux native provider, then Windows WSL, then macOS Colima |
| v1 scope | Full v1: Milestones/Phases 0–10 in [07-development-plan.md](07-development-plan.md) |

## Document map

Read top-level docs in order; module and UI docs are reference material consumed per-phase.

| Doc | Contents |
|---|---|
| [01-product-spec.md](01-product-spec.md) | Product definition, principles, personas, feature priorities, acceptance criteria, non-goals |
| [02-architecture.md](02-architecture.md) | System architecture, Wails v3 integration, process/concurrency model, event flow, error model, repo layout |
| [03-data-model.md](03-data-model.md) | Full SQLite schema, migrations, retention, cache semantics |
| [04-api-contracts.md](04-api-contracts.md) | All backend services, DTOs, event names/payloads, error codes — the frontend↔backend contract |
| [05-security.md](05-security.md) | Threat model, destructive-action policy, credentials, audit |
| [06-testing.md](06-testing.md) | Test strategy, fixtures, sample projects, CI matrix, release checklist |
| [07-development-plan.md](07-development-plan.md) | **The build plan.** Phase-by-phase, step-by-step, with tests and exit criteria per step |
| [08-packaging-release.md](08-packaging-release.md) | Installers, signing, updates of Cairn itself, release process |
| `modules/01…13` | Per-module design: responsibilities, internal design, algorithms, edge cases, test requirements |
| `ui/00…10` | Design system + per-screen spec: every element, state, and interaction |

### Backend modules (`modules/`)

1. [App core & event bus](modules/01-app-core.md)
2. [Platform providers](modules/02-providers.md)
3. [Docker client](modules/03-docker-client.md)
4. [Compose wrapper & project detector](modules/04-compose.md)
5. [Metrics collector](modules/05-metrics.md)
6. [Log streaming](modules/06-logs.md)
7. [Terminal manager](modules/07-terminal.md)
8. [Registry client](modules/08-registry.md)
9. [Update system](modules/09-updates.md)
10. [Image lineage](modules/10-image-lineage.md)
11. [Volume backups](modules/11-backups.md)
12. [SQLite store](modules/12-store.md)
13. [Security & audit](modules/13-security-audit.md)

### UI specs (`ui/`)

0. [Design system](ui/00-design-system.md)
1. [App shell & navigation](ui/01-app-shell.md)
2. [Onboarding & provider setup](ui/02-onboarding.md)
3. [Dashboard](ui/03-dashboard.md)
4. [Projects & project detail](ui/04-projects.md)
5. [Containers & container detail](ui/05-containers.md)
6. [Images / Volumes / Networks](ui/06-images-volumes-networks.md)
7. [Logs](ui/07-logs.md)
8. [Terminal](ui/08-terminal.md)
9. [Updates](ui/09-updates.md)
10. [Settings](ui/10-settings.md)

## Development environment (Windows)

All Cairn development and testing on Windows uses a **dedicated WSL distro named `cairn-dev`** — never Docker Desktop's distros (`docker-desktop*`) and never a distro with Docker Desktop WSL integration enabled. This keeps the developer's existing Docker Desktop projects fully isolated.

```powershell
wsl --install Ubuntu-26.04 --name cairn-dev
```

Inside `cairn-dev`: enable systemd (`/etc/wsl.conf` → `[boot] systemd=true`, then `wsl --shutdown`), install Docker Engine + Compose + Buildx from the official apt repo, `usermod -aG docker $USER`. Disable Docker Desktop → Settings → Resources → WSL Integration for `cairn-dev`. Verify: `wsl -d cairn-dev -- docker run --rm hello-world`.

When running/testing Cairn against this distro, set `windows.wsl_distro = cairn-dev` (the product default for end users remains `Ubuntu`). Beware shared host ports: Docker Desktop stacks and `cairn-dev` stacks both publish to Windows localhost. See [modules/02-providers.md §5.1] for the detection/coexistence rules.

## Conventions used across all docs

- **MUST / SHOULD / MAY** follow RFC 2119 meaning.
- "Provider" = platform backend adapter (WSL, Linux native, Colima, existing context, remote SSH).
- "Project" = Docker Compose project. "Service" = Compose service. Containers always map to at most one project/service via Compose labels.
- Update kinds: `service_image` (pull & recreate) vs `base_image` (rebuild & redeploy). Image-lineage terminology follows [modules/10-image-lineage.md](modules/10-image-lineage.md).
- Every backend mutation goes through a **command plan** (preview → confirm → execute → audit). No silent destructive operations, ever.
- IDs: projects use `providerID + "/" + composeProjectName`; sessions/backups use UUIDv7.
- All timestamps stored UTC, ISO-8601.

## Glossary

| Term | Meaning |
|---|---|
| Backend (OS) | The Docker-capable environment a provider manages (WSL distro, native daemon, Colima VM) |
| Backend (app) | The Go side of the Wails app |
| Command plan | Ordered list of real shell/API commands shown to the user before execution |
| Lineage | Mapping from running container → service image → Dockerfile → base image(s) + digests |
| Rebuild required | A locally built service whose Dockerfile base image digest changed upstream |
| Pinned digest | Image referenced as `name@sha256:…`; never flagged as a normal update |
