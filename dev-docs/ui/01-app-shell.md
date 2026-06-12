# UI 01 — App Shell & Navigation

## 1. Layout

```text
┌─────┬──────────────────────────────────────────────┐
│ S   │  Page header (title, page-level actions)     │
│ i   │                                              │
│ d   │  Page content (router outlet)                │
│ e   │                                              │
│ b   │                                              │
│ a   ├──────────────────────────────────────────────┤
│ r   │  (status strip lives in sidebar footer)      │
└─────┴──────────────────────────────────────────────┘
```
Min window 1100×700; sidebar 220px (collapsible to 56px icon rail, persisted).

## 2. Sidebar

Items (fixed order): Overview(Dashboard), Projects, Containers, Images, Volumes, Networks, Logs, Terminal, Updates, Settings. Each: icon + label + optional count/badge — Projects (running count), Containers (running count), Updates (actionable update count, yellow), Terminals (open sessions). Active item: accent left bar + filled background. Logo top: `assets/cairn-logo.png` expanded, `assets/cairn-icon.png` when collapsed ([ui/00 §0]); collapse toggle bottom.

## 3. Sidebar footer status strip

```text
● Docker Engine   Running        ← StatusDot + provider display name + state
  ctx: colima     v27.1          ← context + version, truncated, tooltip full
  [↻]                            ← quick restart-backend (confirm)
```
States: Running(green) / Starting(pulse) / Stopped(gray, "Start" button) / Error(red, opens repair view). Click → provider panel in Settings.

## 4. Global elements

- **Title bar:** native window chrome (Wails default per OS); app menu minimal (About, Check for updates, Quit).
- **Global banner** (below header, full width): docker disconnected ("Docker is not reachable — [Repair] [Retry]"), provider degraded, app update available. One banner max; priority: error > warn > info.
- **Toast stack:** bottom-right, max 3 visible.
- **Notification center:** bell icon in page header right side; unread count; popover list (level icon, title, body, time); "mark all read"; items deep-link (e.g. update applied → Updates history).

## 5. Degraded modes (normative)

| State | Behavior |
|---|---|
| No provider configured | All pages redirect-affordance to Onboarding; sidebar disabled except Settings |
| Docker stopped | Pages render cached data with gray "stale" watermark + banner with Start/Repair actions; mutations disabled |
| Provider repair needed | Banner → repair panel listing ProviderProblems with `RepairHint` + one-click fix where Recoverable |

## 6. Command palette (Ctrl/Cmd-K)

Sections: Navigation (pages, projects by name, containers by name), Actions (context-aware: "Start project X", "Open terminal in Y"), Commands (cheatsheet entries — `Runnable` safe ones execute after preview toast; others copy). Fuzzy match; recent items first; `Esc` closes. Risk-labeled rows for command entries.

## 7. Routing

`/dashboard /projects /projects/:id/:tab? /containers /containers/:id/:tab? /images /volumes /networks /logs /terminal /updates /settings/:section?` — deep-linkable; unknown id → toast + list redirect. Page state (filters, sort, search) persisted per page in session.

## 8. Tests

E2E: navigation across all routes; banner priority logic; palette navigation + safe-command run + destructive command copy-only; degraded-mode rendering with daemon stopped; sidebar counts match backend.
