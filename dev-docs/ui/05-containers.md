# UI 05 — Containers & Container Detail

Reference: `ui-ideas (1).png` (table + detail split with logs/terminal).

## 1. Containers page

Header: search (name/image/ID), FilterChips [All · Running · Stopped · Paused · Unhealthy · Ungrouped], project filter dropdown, columns menu.

Table (virtualized, multi-select): **Name** (+compose service sublabel) · **Status** (dot+word+uptime) · **Project** (link) · **Service** · **Image** (truncated, tooltip full) · **Ports** (chips, click-copy) · **CPU** (value+spark) · **Memory** (used[/limit]) · **Net I/O** (rx/tx rate) · **Health** · **Restarts** (red when >3 in 10 min) · actions.

Row actions: Logs · Terminal · ▶/■ (state-aware) · ↻ · kebab(Inspect, Rename…, Kill…, Pause/Unpause, Remove…, Copy ID). Rename modal validates name charset/uniqueness; Compose-managed containers get a note that Compose may recreate under the original name. Bulk bar on selection: Start · Stop · Restart · Remove stopped… (confirm lists names). Row click → detail. Live updates via `objects:changed` + `stats:sample`.

## 2. Container detail

Header: name, status, project/service breadcrumbs, image; actions ▶■↻, Logs, Terminal, kebab (Kill…, Pause, Remove…, Inspect JSON, Copy ID).

Tabs: **Overview · Logs · Terminal · Stats · Inspect · Files(v1.1 placeholder hidden)**.

### Overview tab
- Facts grid: ID, image (+image ID link), created, started, uptime, restart count, **restart policy**, command, user, platform.
- State card: status, health (+ last 5 healthcheck results with output expanders), exit code if exited.
- Ports table (host↔container, protocol, open-in-browser for http-ish).
- Mounts table (volume/bind, source→target, RW/RO; volume names link to Volumes page).
- Networks table (network, IP, aliases).
- Env vars (masked per secret pattern, eye toggle).
- Labels (collapsible, compose labels highlighted).

### Image Lineage card (Overview, when lineage exists — normative content)
```text
Image Lineage                                Confidence: High ⓘ
Running image     cairn/apps-api:local       sha256:71ab…
Built from        node:20-alpine
Dockerfile        ./api/Dockerfile (target: runner)
Base @ build      sha256:aaa1…   [copy]
Base remote now   sha256:bbb2…   [copy]
Status            ⚠ Base image update available
→ Recommended: Rebuild & redeploy service    [Plan rebuild…]
```
Unknown case: `Base image: Unknown — third-party registry image, no base metadata found.` Confidence ⓘ tooltip explains source ([modules/10 §5]).

### Other tabs
- **Logs:** embedded LogViewer (container scope).
- **Terminal:** embedded session (shell select dropdown from `DetectContainerShells`, user field, root badge).
- **Stats:** CPU/Mem/Net/Block charts + PIDs; range picker.
- **Inspect:** raw JSON tree (collapsible, search, copy path/value).

## 3. Confirmations

Kill → needs_confirmation. Remove running/force → destructive modal (effects: "container will be deleted; anonymous volumes removed if checked"). All per [05-security.md §2].

## 4. Tests

E2E: filters/search at seed scale <100 ms; bulk stop of 10; lineage card golden-matches backend for `build-simple` container; masked env; healthcheck history renders on `healthcheck-fail`.
