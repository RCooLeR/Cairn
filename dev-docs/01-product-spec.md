# 01 — Product Specification

## 1. Product definition

Cairn is a desktop application for managing Docker environments on Windows, macOS, and Linux.

```text
Cairn = installer + dashboard + Compose manager + terminal + charts + updater
Cairn != container runtime, VM runtime, Kubernetes platform, registry server
```

Cairn manages: containers, Compose projects, images, volumes, networks, logs, stats, terminals, image updates (including base-image updates for locally built services), and Docker/Compose installation state.

Cairn uses:
- the **Docker Engine API** (Go SDK) for low-level object management, streams, events;
- the official **`docker compose` CLI** for Compose lifecycle (`up`, `down`, `pull`, `build`, `config`, `ps`, `logs`). Compose is never reimplemented in v1.

## 2. Target users

| Persona | Needs |
|---|---|
| Developer (daily Compose user) | Fast project start/stop, logs, terminals, config visibility, no Docker Desktop license/footprint |
| Home-lab / self-hoster | Update visibility for many stacks, safe one-click updates, volume backups, low resource use |
| DevOps engineer | Transparent commands, audit log, remote contexts, trustworthy destructive-action handling |

## 3. Core principles (product invariants)

1. **Compose-first.** Projects and services are the primary unit, not raw containers. Containers auto-group via `com.docker.compose.*` labels.
2. **Transparent actions.** Every major action shows the exact command(s) before execution.
3. **Safe by default.** Destructive actions require confirmation; the worst ones require typing the target name. No auto-updates by default. No Docker TCP exposure by default.
4. **No lock-in.** Closing Cairn leaves a fully standard Docker/Compose environment. No proprietary project format.
5. **Honest uncertainty.** Lineage/update results carry confidence levels; unknown base images are stated as unknown, never guessed.

## 4. Platform strategy

| Platform | Default backend | Cairn responsibility |
|---|---|---|
| Windows | Ubuntu on WSL2 + official Docker packages | Detect/install WSL+Ubuntu, install Docker inside, manage via WSL, map paths |
| Linux | Native Docker Engine (Ubuntu/Debian first) | Detect/install official packages, manage local daemon, permission guidance |
| macOS | Colima (default) or existing context | Detect/install Homebrew→docker CLI→Colima, manage Colima lifecycle |
| Any OS | Existing Docker context | Connect to whatever the user already has (Docker Desktop, OrbStack, Rancher Desktop, …) |
| Any OS | Remote Docker over SSH (post-MVP within v1) | Docker contexts over SSH |

Docker contexts are supported from the start; one client, many daemons.

## 5. Feature priorities

```text
P0 = first usable MVP   P1 = required for v1   P2 = post-v1   P3 = future
```

### P0 — first usable MVP
- Docker connection: daemon/version/compose detection, provider health, current context.
- Containers: list (status, image, ports, uptime, project/service), start/stop/restart, logs, terminal, inspect JSON.
- Images: list (repo/tag/ID/size/created, used-by), pull, remove with confirmation.
- Volumes: list (driver, created, usage), inspect, delete with confirmation.
- Networks: list (driver, subnet, connected containers), inspect.
- Compose grouping: project/service grouping from labels, project cards, service list.
- Basic dashboard: running/stopped/unhealthy counts, project/image/volume counts, disk usage.

### P1 — v1 product
- Compose project management: start/stop/restart/redeploy/pull, project logs, project terminal, config viewer, import project folder.
- Docker Desktop parity: **run-image wizard** (create & run container from an image: name, ports, env, volumes, network, restart policy), **create volume / create network** forms, container rename, restart-policy display, save image to / load image from `.tar`, Docker Hub search in the Pull modal.
- Registry publishing: registry account login (Docker Hub/GHCR/custom via `docker login` delegation), image tag, image push with progress.
- Stats & charts: live CPU/memory/network, top containers, project aggregates.
- Updates: service-image checks, base-image checks for built services, separate badges, pull/recreate flow, rebuild/redeploy flow, mixed project plan, post-update health monitoring, history, ignore.
- Image lineage: per container/service/project; Dockerfile FROM parsing incl. multi-stage; base digest history; confidence levels; honest unknowns.
- Terminals: host/backend/container terminals, command cheatsheet, command palette, risk labels.
- Providers: Linux native, Windows WSL Ubuntu, macOS Colima, existing context.
- Settings, audit log, notifications (in-app), installers.

### P2 — post-v1 (v1.1 roadmap)
- **Per-service auto-update opt-in:** scheduled Watchtower-style updates, off by default, with health watch + auto-rollback, per-service granularity (builds on the v1 update engine).
- **Container/volume file browser + `docker cp`:** browse filesystems, upload/download files (completes the deferred volume browser).
- **Ops quality bundle:** port-conflict detection before project start (names the conflicting process), diagnostics bundle export for bug reports, scheduled prune.
- Volume backup/restore UI polish, network topology view, remote SSH provider UI, tray app, desktop notifications, disk cleanup wizard, Compose YAML editor, .env editor, log export presets, saved snippets, standalone image-build UI.

> Note: volume **backup/restore engine** is built in v1 (Phase 9) because the update flow offers "backup volumes first"; the full management UI is P2.

### P3 — future
Notification integrations (Telegram / ntfy / webhooks for update-available, unhealthy, update-applied events), Kubernetes view, registry browser, team mode, cloud sync, plugin system, alerting, automation rules, vulnerability scanning, multi-host aggregate dashboard, i18n (Ukrainian first).

## 6. Explicit non-goals for v1

Custom container runtime; custom VM runtime; Kubernetes dashboard; Docker Scout clone; enterprise policy management; Windows containers; registry server; multi-user/server mode; team collaboration; cloud sync; AI assistant features.

## 7. v1 acceptance criteria

**Windows:** fresh machine with WSL support → Cairn installs/verifies Ubuntu+Docker in WSL; Compose projects inside WSL work; full object management.
**Linux:** detects native Docker; can install Docker on Ubuntu/Debian; manages local Docker without Docker Desktop.
**macOS:** detects or installs Colima path; uses selected context; full object management.
**All platforms:** project dashboard works; label grouping correct; logs stream without UI freeze; container terminal works; stats charts live; one-click service update works end-to-end (incl. health watch); base-image rebuild update works; destructive actions always confirmed.

Performance targets (see [06-testing.md §7](06-testing.md)): usable with 100 containers / 500 images / 200 volumes; dashboard initial render < 1.5 s after daemon ping; log viewer sustains ≥ 5 000 lines/s without dropping frames below 30 fps.

## 8. Brand

Name: **Cairn** (stacked stones = stacked services; a marker that guides).
Tagline: "A clean Compose-first Docker manager for Windows, macOS, and Linux."
Visual identity: dark-first, calm, technical, teal/green accent — see [ui/00-design-system.md](ui/00-design-system.md) and the mockups in `codex-docs/ui-ideas/`.
