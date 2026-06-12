# 07 — Development Plan

Phase-by-phase, step-by-step plan from empty repo to released v1.0. Each step lists **Do**, **Test**, and each phase ends with an **Exit gate** — do not start the next phase until the gate is green. References point to the normative docs.

Principles: Linux-first (no WSL/VM layer while building the core); vertical slices (every phase ends with something visibly working); tests land in the same step as the code; UI built against real backend from Phase 2 onward.

Estimated sizes assume 1–2 engineers; treat as relative weights, not promises.

---

## Phase 0 — Foundations (repo, shell, store, CI) — ~1 week

**0.0 Dev environment (Windows machines)**
- Do: set up the dedicated `cairn-dev` WSL distro per [README.md "Development environment (Windows)"] — custom-named Ubuntu LTS, systemd on, official Docker Engine inside, Docker Desktop WSL integration disabled for it. All local Docker integration testing on Windows targets `cairn-dev`, never Docker Desktop.
- Test: `wsl -d cairn-dev -- docker run --rm hello-world` succeeds; `docker context ls` on Windows still shows `desktop-linux` untouched.

**0.1 Repo & toolchain**
- Do: init repo per [02-architecture.md §4]; Go module with `go 1.26` + `toolchain go1.26.4` (pinned, README locked decisions); Wails v3 (pinned) app skeleton with one window loading Vite React app; Tailwind; ESLint/Prettier; golangci-lint; Taskfile.
- Test: `wails3 dev` hot-reloads both sides; `go version` in CI asserts go1.26.4 exactly; lint passes; CI workflow runs lint+unit on 3 OS runners.

**0.2 SQLite store + migrations**
- Do: `internal/store` per [modules/12-store.md]; migration runner; migration 0001 with full schema from [03-data-model.md]; settings repo with typed accessors + defaults ([03 §7]).
- Test: unit: migrate empty DB → schema matches; idempotent re-run; settings round-trip; WAL/foreign-keys pragmas asserted.

**0.3 Event bus + error model + logging**
- Do: `internal/bus` (typed topics, coalescing helpers); `AppError` with codes ([04 §8]); zerolog-style structured app logging to rotating file.
- Test: unit: pub/sub ordering, coalescing window, unsubscribe-on-cancel; error wrap/unwrap preserves code.

**0.4 Wails services skeleton + bindings**
- Do: `internal/services` empty structs for all services in [04-api-contracts.md]; register in shell; generate TS bindings; frontend `api/` wrappers + `useEvent` hook; zustand store skeletons.
- Test: contract: bindings generation is clean in CI (diff = fail); frontend calls `AppVersion()` and renders it.

**0.5 Design system base**
- Do: tokens, theme, core components per [ui/00-design-system.md] (Button, Badge, Card, Table, Modal, Tabs, EmptyState, Toast, Tooltip, StatusDot); app shell layout with sidebar/status bar placeholders ([ui/01-app-shell.md]).
- Test: Vitest component tests; Storybook (or Ladle) builds; dark theme renders.

**Exit gate 0:** app launches on all 3 OS in dev; frontend↔backend call works; DB migrates; CI green.

---

## Phase 1 — Docker read-only core on Linux — ~2 weeks

**1.1 Linux native provider (detect only)**
- Do: `providers/linux_native` Detect(): docker CLI, socket reachability, permission check, compose/buildx versions, systemd presence ([modules/02-providers.md §4]); provider manager with active-provider selection; persistence to `providers` table.
- Test: unit with fake exec; integration on CI dockerd: status fields correct; permission-denied path returns problem `PERM_SOCKET` with repair hints.

**1.2 Docker client connect + ping + info**
- Do: `internal/docker` client from provider DockerHost ([modules/03-docker-client.md]); Ping/Info/Version/DiskUsage; `docker:connected/disconnected` events; reconnect loop with backoff.
- Test: integration: ping; stop dockerd → disconnected event → start → reconnect.

**1.3 Object listing (containers/images/volumes/networks)**
- Do: list+inspect wrappers with DTO mapping ([04 §2]); cache writes; Docker events subscription → `objects:changed` (coalesced); 60 s reconcile.
- Test: integration: seeded objects match `docker` CLI output; create/remove container externally → event within 1 s; cache reconciles.

**1.4 Read-only UI pages**
- Do: Containers/Images/Volumes/Networks tables per [ui/05], [ui/06] (columns, sorting, filtering, search, empty states); Inspect viewer (raw JSON, collapsible); status bar shows provider/docker state.
- Test: E2E: lists render seeded daemon; search/filter < 100 ms @ seed scale; empty states correct with no objects.

**1.5 Container lifecycle actions**
- Do: start/stop/restart/kill + bulk; command-plan pipeline `internal/security` (risk mapping [05 §2], plan/apply, audit writes); confirmation modal component.
- Test: integration: actions verified against daemon; kill requires confirmation; audit rows written; unit: risk mapper table-driven.

**1.6 Run-image wizard + object creation parity**
- Do: RunImage (create+start with validation), RenameContainer, CreateVolume, CreateNetwork, SaveImage/LoadImage, SearchHub ([modules/03 §6a]); UI: run wizard ([ui/06 §1a]), create-volume/network modals, rename, save/load modals, Hub search in Pull modal.
- Test: integration: run wizard creates container with ports/env/volumes matching inspect; port-conflict validation; rename round-trip; volume/network create+inspect; save→load tar round-trip preserves image ID; E2E: full wizard journey, Hub-search-offline fallback.

**Exit gate 1:** Cairn on Linux shows live Docker state, controls containers, and can run/create objects; auditing works; all P0 object features pass.

---

## Phase 2 — Compose-first (projects) — ~2 weeks

**2.1 Compose CLI wrapper**
- Do: `internal/compose` exec via provider (argv arrays, workdir, env), version detect, `config/ps/ls` JSON parsing ([modules/04-compose.md §2-3]).
- Test: unit: parser fixtures for compose v2.2x outputs; integration: all testdata projects parse.

**2.2 Project detector**
- Do: grouping from labels + `compose ls` + imported folders; project/service ID scheme; persistence; reconcile rules (label project wins; imported project matched by normalized name) ([modules/04 §4]).
- Test: unit: grouping fixtures incl. orphan containers, name collisions across providers; integration: testdata projects detected with correct services.

**2.3 Projects page + project cards**
- Do: [ui/04-projects.md §2-3]: cards (status, services x/y, health, CPU/RAM placeholders, ports, workdir), filters, search, Import Project flow (folder picker → validate → `ImportProject`).
- Test: E2E: import `app-db` → card appears; filters work; invalid folder → `E_COMPOSE_INVALID` inline error.

**2.4 Project lifecycle actions**
- Do: start/stop/restart/pull (safe, with preview); PlanRedeploy/PlanDown(+volumes) through plan pipeline; correct workdir execution; progress via `job:progress`.
- Test: integration: lifecycle on testdata projects; `down --volumes` requires typed name; commands recorded in audit; workdir-missing → `E_WORKDIR_MISSING` with re-link flow.

**2.5 Project detail page (Overview/Services/Containers/Compose tabs)**
- Do: [ui/04 §4-6]: overview (health summary, ports, service cards), services tab, containers tab (filtered containers table reuse), compose tab (raw files + resolved `config` output in Monaco read-only + validation state + env files).
- Test: E2E: tabs render for `build-multistage`; resolved config matches CLI; validation error path on broken yaml fixture.

**Exit gate 2:** Compose projects are first-class: detected, imported, controlled, inspected. MVP-2 scope done.

---

## Phase 3 — Logs, stats, terminals — ~2–3 weeks

**3.1 Log streaming backend**
- Do: `internal/logsvc` ([modules/06-logs.md]): container stream (stdout/stderr demux), project/service multiplex with per-source colors, ring buffers, batching (≤ 50 ms/200 lines), backpressure, level heuristics, export.
- Test: unit: demux/batch/ring; integration: `big-logs` sustains 5 000 lines/s without drop; cancel cleans goroutines (leak check).

**3.2 Logs UI**
- Do: [ui/07-logs.md]: virtualized viewer, follow/pause, search (debounced, highlight), level & container filters, timestamps toggle, export; embedded log panes on container/project pages.
- Test: E2E perf: 30 fps at full rate; search responsive; pause buffers and resumes correctly.

**3.3 Stats collection + charts**
- Do: `internal/metrics` ([modules/05-metrics.md]): adaptive sampling, rate computation from cumulative counters, aggregation to project level, SQLite persistence + retention downsampling; `stats:sample` stream.
- Test: unit: rate calc, downsample math; integration: values within tolerance of `docker stats`; retention vacuum bounds table.

**3.4 Charts UI + dashboard v1**
- Do: chart components (line, area, donut, sparkline) per [ui/00 §7]; Dashboard per [ui/03-dashboard.md] (status row, counts, CPU/mem/network charts, top containers, disk usage, recent events, quick actions); project cards get live sparklines.
- Test: E2E: dashboard < 1.5 s; charts update live; top-N matches docker stats ordering.

**3.5 Terminal backend + UI**
- Do: `internal/terminal` ([modules/07-terminal.md]): PTY host/backend shells (creack/pty; ConPTY later), container exec sessions via Docker API with TTY, resize, shell detection; sessions registry. UI: xterm.js page with tabs, side cheatsheet panel ([ui/08-terminal.md]); cheatsheet content + risk labels; command palette (Ctrl/Cmd-K) with navigation + safe commands ([ui/01 §6]).
- Test: integration: open shell in alpine & distroless-ish containers (shell detection fallback), resize, exit code propagation; E2E: type → output round-trip; root badge shown; palette opens pages and runs safe commands only.

**Exit gate 3:** Daily-driver usable on Linux: logs, charts, terminals, dashboard. MVP-1+2+3-core complete. Soak test 4 h with streams open: no leaks.

---

## Phase 4 — Hardening I + permission flows — ~1 week

**4.1** Linux permission options UX (sudo per-action / group with warning / rootless) per [05 §5], [ui/02 §6]; provider repair hints rendering; docker-stopped degraded mode on every page.
**4.2** Error-state audit across UI: every page renders all `AppError` codes correctly (storybook matrix); global banner; notification center ([ui/01 §7]).
**4.3** Perf pass at seed scale ([06 §7] targets); fix hot paths; goroutine/pprof review.
- Test: chaos script (random daemon stop/start, container churn) 1 h → no crash, UI consistent.

**Exit gate 4:** Linux experience is robust under failure; performance targets met.

---

## Phase 5 — Windows WSL provider — ~2–3 weeks

**5.1 WSL detection & exec layer**
- Do: `providers/windows_wsl` ([modules/02 §5]): wsl.exe presence/version, distro enumeration (handle UTF-16LE output; exclude `docker-desktop*`; custom-named distros first-class), Ubuntu detection, WSL2 check, systemd check; command exec via `wsl.exe -d <distro> --`; path mapping (`C:\…` ↔ `/mnt/c/…`, UNC, spaces). Local development/testing uses the `cairn-dev` distro (step 0.0); set `windows.wsl_distro = cairn-dev` in dev builds' settings.
- Test: unit: output parsers (fixtures for wsl.exe quirks), path mapper edge cases; manual matrix on Win11 VM (WSL absent/present etc. per [06 §2]).

**5.2 Install flow inside Ubuntu**
- Do: guided install plan (apt repo, docker-ce + cli + containerd + buildx + compose plugins, enable systemd if needed, service enable/start) as CommandPlan steps with progress; Docker-group-in-WSL handling; restart-WSL guidance.
- Test: scripted VM run from clean Ubuntu → working docker; failure injection (no network, no systemd) → correct problems + repair hints.

**5.3 Docker connection through WSL**
- Do: DockerHost via WSL socket strategy (TCP on localhost disabled; use `npipe`-less approach: docker context over WSL interop or socket proxying as designed in [modules/02 §5.4]); compose exec through provider; terminal: PowerShell host + `wsl.exe` backend shells (ConPTY).
- Test: full integration suite (Phases 1–3 tests) re-run against WSL provider; path-heavy project under `/mnt/c` shows perf warning ([ui/02 §4]).

**5.4 Windows UX**
- Do: onboarding branch for Windows ([ui/02 §3]); WSL settings ([ui/10 §4]); performance-path recommendation surfaces.
- Test: E2E smoke on Windows runner/VM; fresh-machine manual checklist.

**Exit gate 5:** v1 acceptance criteria "Windows" block passes on a clean Win11 VM.

---

## Phase 6 — macOS Colima provider — ~1–2 weeks

**6.1** `providers/macos_colima` ([modules/02 §6]): brew/docker CLI/colima detection; colima start/stop/status/profile; context selection; resource settings (CPU/RAM/disk) via colima flags.
**6.2** Existing-context provider (all OS): context list/ping/select; works with Docker Desktop/OrbStack/Rancher.
**6.3** macOS onboarding branch + settings; zsh host terminal.
- Test: manual matrix ([06 §2] macOS rows); integration suite against Colima on macOS runner; context-switch E2E (Colima ↔ Desktop) without restart.

**Exit gate 6:** v1 acceptance "macOS" block passes; existing-context provider passes on all OS.

---

## Phase 7 — Volume backup/restore engine — ~1 week

> Sequenced before updates because the update flow's "backup volumes first" option depends on it.

**7.1** `internal/backups` ([modules/11-backups.md]): backup via helper container (`tar.gz` + json sidecar), restore (into existing/new/duplicate), safety checks (running-container warnings, typed-name overwrite), backup directory setting, ListBackups.
**7.2** Minimal v1 UI: volume row actions Backup/Restore + Backups list in project detail tab; full browser deferred to P2.
- Test: integration round-trip backup/restore on `app-db` postgres volume with data verification (checksum); restore-overwrite requires typed name.

**Exit gate 7:** volume backup/restore verified with real data; engine callable as a plan step.

---

## Phase 8 — Registry, updates & image lineage — ~3–4 weeks (the differentiator)

**8.1 Registry client + accounts**
- Do: `internal/registry` ([modules/08-registry.md]): ref parsing, token auth (anonymous + credential-helper), HEAD manifest digest for tag+platform, multi-arch index resolution, rate-limit/backoff, per-registry concurrency caps, result cache (TTL 1 h). Account management ([modules/08 §4a]): login/logout via provider-exec `docker login --password-stdin`, account listing from backend config.json, TestAuth, plaintext-store warning.
- Test: unit vs httptest server (200/301/401/404/429, index vs manifest); integration vs local `registry:2`; auth cases [06-testing.md §5] 12 & 14.

**8.1a Image tag & push**
- Do: TagImage/PushImage in docker client ([modules/03 §6]) with push progress events; Tag/Push modals + Registries settings section ([ui/06 §1], [ui/10 §4a]); push through plan pipeline (needs_confirmation); redaction coverage.
- Test: [06-testing.md §5] case 13 (tag→push round-trip against authed local registry); E2E not-logged-in → login → push journey.

**8.2 Dockerfile parser + lineage discovery**
- Do: `internal/lineage` ([modules/10-image-lineage.md]): FROM parser (stages, AS names, ARG substitution, platform flag, scratch, stage-ref FROMs), final-stage resolution incl. `build.target`; discovery pipeline: compose build config → Dockerfile → OCI annotations → cairn labels; confidence assignment; persistence (`image_lineage`, `base_image_refs`); cairn labels applied on Cairn-triggered builds.
- Test: unit: ≥ 30 Dockerfile fixtures (multi-stage, ARG-in-FROM, comments, line continuations, lowercase from, scratch, digest-pinned FROM); golden lineage for all testdata projects ([06 §5] cases 6–8).

**8.3 Update checker**
- Do: `internal/updates` checker: service-image digest compare; base-image digest compare via lineage; status machine ([04 §6]); special cases (latest warning, pinned, local-only, auth, rate-limit); scheduler + manual; ignore list; persistence; badges data in ProjectSummary.
- Test: [06 §5] cases 1–8, 11 fully automated with mock registry.

**8.4 Update planner + executor + health watch + rollback**
- Do: PlanServiceUpdate/PlanProjectUpdate (ordered mixed plans); executor through plan pipeline with `job:progress`; pre-update snapshot (image IDs, digests, compose config, dockerfile hash); volume-backup option wired to the Phase 7 engine; health watcher (state/healthcheck/restart-loop/log-scan, 60 s); auto-rollback rules; update_history.
- Test: [06 §5] cases 9–10; integration: end-to-end update on seeded old digest incl. backup-first option; rebuild flow on `build-simple`; failed health → rollback → history `rolled_back`.

**8.5 Updates UI + lineage UI**
- Do: Updates page ([ui/09-updates.md]): table, filters (update type/status), check-now, per-row + bulk actions, confirmation modal with digests/commands/options, history view; project-card badges ([ui/04 §2]); project Updates tab grouped by action ([ui/04 §6]); container Image Lineage card ([ui/05 §4]).
- Test: E2E journeys 3–4 ([06 §8]); badge counts match backend; unknown-base wording exact per [modules/10 §7].

**Exit gate 8:** all 14 update/lineage/registry cases ([06 §5]) green; one-click update + rebuild + tag/push work on all 3 platforms.

---

## Phase 9 — Settings, audit, notifications, cheatsheet polish — ~1 week

**9.1** Settings page complete ([ui/10-settings.md]) wired to settings keys ([03 §7]) incl. provider-specific groups and Registries section polish; theme switch; update interval.
**9.2** Audit log viewer + filters; notification center; in-app update-available notice ([08 §5]).
**9.3** Cheatsheet content finalized (≥ 60 commands, categories, risk labels, placeholders, runnable subset) ([modules/07 §6]).
- Test: settings round-trip E2E; audit filter correctness; cheatsheet risk labels reviewed against [05 §2].

**Exit gate 9:** every sidebar page is functionally complete.

---

## Phase 10 — v1 polish & release — ~2–3 weeks

**10.1** Onboarding final pass: all branches (fresh/partial/complete installs per OS), health-check screen, setup result summary ([ui/02]).
**10.2** Empty/error/loading states sweep on all pages; copy review; keyboard navigation & focus order; basic a11y (labels, contrast per [ui/00 §9]).
**10.3** Packaging ([08]): NSIS, AppImage+deb, signed/notarized dmg; self-version-check; uninstall cleanliness.
**10.4** Full test matrix execution ([06 §2]); 24 h soak; performance re-verification; security review against [05] checklists.
**10.5** Docs: user quickstart, provider troubleshooting guide; release notes.
- Test: **v1 release checklist [06 §9] — every box.**

**Exit gate 10 = v1.0 release.**

---

## Post-v1 (v1.1 planning seeds, not in scope)

Committed v1.1 roadmap ([01-product-spec.md] P2): per-service auto-update opt-in (scheduled, health-watched, auto-rollback), container/volume file browser + `docker cp`, ops quality bundle (port-conflict detection before project start, diagnostics bundle export, scheduled prune). Plus: Compose YAML/.env editing (guarded), tray app + desktop notifications, remote SSH provider UI, disk cleanup wizard, network topology graph, standalone image-build UI, rpm/brew-cask, ARM64 Windows, auto-self-update, opt-in crash reporting. P3 horizon includes notification integrations (Telegram/ntfy/webhooks).

## Cross-phase rules

1. Every step's tests merge with the step. Red CI blocks the phase.
2. Binding regeneration + contract check on every PR.
3. New destructive operations must land with risk-mapping entry + confirmation test in the same PR.
4. Provider work is always validated against the full Phase 1–3 integration suite, not provider-specific smoke tests only.
5. Any deviation from dev-docs requires updating the doc in the same PR (docs are the contract).
