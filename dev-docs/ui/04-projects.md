# UI 04 — Projects & Project Detail

Compose projects are the primary management unit. Reference: `ui-ideas (4).png` (grid), `(3).png` (detail).

## 1. Projects page

Header: title, search, FilterChips [All · Running · Stopped · Partial · Unhealthy · Updates available · High CPU · Recently changed], sort (name/activity/CPU), view toggle grid|list (persisted), [+ Import Project].

## 2. Project card (grid)

```text
┌──────────────────────────────────────────┐
│ ⬢ apps                    ● Running   ⋮  │
│ 5 / 5 services · healthy                 │
│ CPU 4.2%  RAM 612 MiB  ⌁ 1.2 MB/s        │
│ ───── sparkline (CPU 60s) ─────          │
│ [2 image · 1 base · 1 rebuild]  :80 :443 │
│ ▶ Start ■ Stop ↻ Restart ⌲ Logs ⌨ Term   │
└──────────────────────────────────────────┘
```
Elements: status dot+word; services running/total + health word (worst-of); live CPU/RAM/net; sparkline; **update badges** (per [ui/00 §6], counts; click → project Updates tab); published ports (click = copy host:port, ctrl-click = open browser http://localhost:port); footer icon actions (visible on hover in grid; primary action swaps Start/Stop by state). Kebab: Redeploy, Pull images, Check updates, Open compose file, Open folder, Down…, Down + volumes… (danger group), Remove from list.
List view: same data as table rows (Name, Status, Services, Health, CPU, RAM, Net, Updates, Ports, Last changed, Workdir, actions).

Card states: workdir-missing → amber warning icon + tooltip + "Re-link folder…" in kebab; inactive imported project → grayed with [Start].

## 3. Import flow

Modal: folder picker (native dialog) → detected compose files listed (checkboxes if multiple) → validation result panel (`E_COMPOSE_INVALID` shows CLI output) → project name preview → [Import]. WSL `/mnt/*` path → performance note banner.

## 4. Project detail — header & tabs

Header: back link, name + status, workdir (click=open folder), context chip, last-updated; actions: ▶/■, Restart, Redeploy (plan modal), Pull, Check updates, Open terminal, kebab (Down…, Down+volumes…, Export logs, Remove from list).

Tabs: **Overview · Services · Containers · Logs · Stats · Compose · Environment · Volumes · Networks · Updates · Backups · Events**.

## 5. Tabs (each)

- **Overview:** health summary chips; service cards mini-grid (name, image, status, health, CPU/RAM, ports, per-service ▶■↻⌲⌨); resource chart (1 h); exposed ports panel (service → host:port links); recent logs pane (50 lines, follow); update status card (badges + [Plan update]).
- **Services:** table Name · Image (+`build:` chip for built services) · Replicas · Status · Health · CPU · RAM · Ports · actions(Start/Stop/Restart/Logs/Terminal/Update if badge). Built services show lineage popover on image chip (base image, confidence).
- **Containers:** standard containers table ([ui/05]) pre-filtered to project.
- **Logs:** embedded LogViewer scoped to project, service filter chips ([ui/07] behavior).
- **Stats:** ChartPanels CPU/Mem/Net/Block per project + per-service stacked toggle; range picker; top services table.
- **Compose:** file list (multiple -f order); Monaco read-only raw view per file; tab "Resolved" = `docker compose config` output; validation banner (valid green / errors listed); env files sub-list rendered with values **masked** when key matches secret pattern (reveal per-value eye toggle, copy disabled for masked). v1 read-only notice with "editing arrives in v1.1".
- **Environment:** resolved env per service (KeyValueGrid, same masking rules).
- **Volumes / Networks:** project-scoped subsets of [ui/06] tables.
- **Updates:** grouped plan view — sections "Pull & recreate", "Rebuild & redeploy", "Manual attention" with per-service rows (current image, base image, digests local→remote, confidence chip); [Check now]; [Update service] per row; [Update project…] opens mixed-plan modal ([ui/09 §4]).
- **Backups:** backups of this project's volumes (table: volume, file, size, created, result) + [Backup volume…]; restore via row action (plan modal).
- **Events:** audit + docker events filtered to project, reverse-chron.

## 6. Interactions & errors

All actions → command preview (toast for safe, modal for confirm+). Project gone mid-view (`E_NOT_FOUND`) → toast + redirect to list. Tab state in URL. Empty states per tab (e.g. Updates: "All images up to date ✓").

## 7. Tests

E2E: card grid live-updates on external `docker compose up`; every tab renders for `build-multistage` and `mixed-updates`; ports open/copy; masked env values never appear in DOM dumps; updates tab grouping matches planner output exactly.
