# UI 03 — Dashboard (Overview)

Purpose: situational awareness in < 3 seconds. Reference mockup: `ui-ideas (2).png`.

## 1. Layout

```text
Row A  Engine status hero card | counts strip (Projects · Containers run/stop · Images · Volumes · Disk)
Row B  Resource usage panel (CPU | Memory | Network tabs, live chart)  | Projects mini-list (right rail)
Row C  Container status donut + recent containers table              | Logs peek + Updates available card (right rail)
Row D  Top processes/containers table | Recent Docker events
```
Right rail (320px) on ≥1280px; stacks below on narrow.

## 2. Elements

- **Engine hero:** big StatusDot + "Docker Engine — Running", provider name, context, version; actions: Start/Stop/Restart (confirm), Open terminal. Stopped state turns hero gray with prominent [Start Docker].
- **Counts strip:** each count clickable → respective page (pre-filtered: e.g. stopped count → Containers?status=exited). Disk usage shows total + reclaimable ("64.2 GB · 12.1 GB reclaimable → [Clean up]" → prune wizard modal with per-category checkboxes and typed-name for volumes).
- **Resource panel:** tabs CPU/Memory/Network; live area chart (300 pts), per-project stacked toggle; range picker 5m/1h/24h.
- **Projects mini-list:** top 5 by activity: name, StatusDot, services x/y, update badges; click → project detail; [View all].
- **Container donut:** running/stopped/unhealthy/paused; legend clickable (filters Containers page).
- **Recent containers:** 6 rows: name, project, status, CPU spark, mem, uptime; row click → container detail.
- **Updates card:** "4 updates available · 1 rebuild needed" + top 3 rows (project/service, badge) + [Check now] (spinner + progress) + [Open Updates].
- **Logs peek:** last 8 lines across all containers (level-colored), click → Logs page.
- **Recent events:** Docker events feed (object icon, action, name, relative time), max 10.
- **Quick actions** (header right): Open terminal · Check updates · Import project · Prune… · Restart Docker.

## 3. Data & refresh

Initial: `GetDashboardMetrics` (one call). Live: `stats:sample` (charts/sparks), `objects:changed` (counts/donut, debounced 500 ms), `logs:lines` (peek stream, tail 8), events feed. Page hidden → streams stopped.

## 4. Empty/degraded

No containers at all: friendly hero "Run your first container" + [Import project] [Open terminal] + counts zeroed. Docker stopped: cached values watermarked "stale", charts paused flat.

## 5. Tests

E2E: render < 1.5 s at seed scale; counts match daemon; donut filter deep-links; cleanup wizard requires confirmations; live chart updates with `big-logs` running.
