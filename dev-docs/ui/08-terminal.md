# UI 08 — Terminal Page

xterm.js sessions + cheatsheet side panel. Reference: `ui-ideas (1).png` bottom-right pane.

## 1. Layout

```text
┌ Tabs: [⌂ host] [⬢ Ubuntu] [⌗ apps] [▣ api-1 (root)] [+▾]          │ Cheatsheet ▸ ┐
├────────────────────────────────────────────────────────────────────┼──────────────┤
│                                                                    │ [Search…]    │
│   xterm.js viewport                                                │ ▾ Containers │
│                                                                    │  docker ps   │
│                                                                    │  …           │
├────────────────────────────────────────────────────────────────────┤ ▾ Compose    │
│ status: zsh · ~/stacks/apps · ctx colima · 80×24                   │ ▾ Recent     │
└────────────────────────────────────────────────────────────────────┴──────────────┘
```

## 2. Sessions

- **[+▾] menu:** New host terminal · New backend terminal · New project terminal (project picker) · New container terminal (container picker → shell dropdown from detection, user field, workdir field).
- Tab anatomy: kind icon, title (container/project name or shell), **root badge (red)** when uid 0, close ✕ (confirm if foreground process suspected — Enter-pressed-recently heuristic skipped; simple confirm always for container sessions).
- Sessions persist across navigation; max 16 (toast when full). Closing page ≠ closing sessions.
- Status bar: shell, cwd (if known), context, dimensions; updates on resize.
- Resize: viewport-driven, debounced 100 ms → `ResizeTerminal`.
- Copy/paste: standard shortcuts; paste with newline(s) into **container/root** sessions → inline confirm popover showing pasted content (bracketed-paste guard).
- Container session header strip (first line area): `api-1 · /bin/bash · root ⚠ · /app` per [05-security.md §6].

## 3. Cheatsheet panel (collapsible, 280px)

- Search across command+description.
- Categories (accordion): Containers, Images, Compose, Volumes, Networks, Logs, Exec, Stats/Debug, Cleanup.
- Entry row: command (mono), description, **risk label chip** (Safe green / Confirm yellow / Destructive orange / Dangerous red), [copy], [▶ run] only when `Runnable && risk==safe` — runs in the **active session** (types + Enter after a 1 s ghost preview the user can Esc).
- Placeholders `<container>` auto-filled from active container session context; unfilled → focus inline edit before copy/run.
- **Recent commands** section: last 20 from `command_history` (source=terminal), click to re-insert.
- Saved snippets: P2 (hidden in v1).

## 4. Tests

E2E: open each session kind; echo round-trip; resize correctness (`stty size`); root badge; paste-guard popover; run-safe-command ghost flow; dangerous commands have no run button; recent list updates.
