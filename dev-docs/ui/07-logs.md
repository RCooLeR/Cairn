# UI 07 — Logs Page

Standalone multi-scope log viewer; same LogViewer component embeds in project/container pages.

## 1. Layout

```text
┌ Scope: [All ▾] [project chips…] [service chips…] [container multiselect ▾]   ┐
│ [Search…___________] [Level ▾] [⏸ Pause] [⤓ Follow] [🕐 TS] [⤒ Export] [✕]   │
├──────────────────────────────────────────────────────────────────────────────┤
│ 12:04:11.123  api-1     | INFO  Server started on :3000                      │
│ 12:04:11.500  db-1      | LOG   database system is ready                     │
│ …virtualized list…                                                           │
└──────────────────────────────────────────────────────────────────────────────┘
```

## 2. Elements & behavior

- **Scope picker:** All / Project / Service / Container; switching restarts stream (`StartLogStream`).
- **Line anatomy:** timestamp (toggle), source chip (stable color per container, click → filter to it), level tag (colored, heuristic — tooltip "detected"), text (ANSI colors rendered, wrapped toggle).
- **Follow:** auto-scroll pinned to bottom; any manual scroll-up unpins (shows "↓ N new lines" pill, click repins).
- **Pause:** stream continues into buffer; banner "Paused — 1 204 new lines [Resume]".
- **Search:** debounced highlight (all matches, count, n/N navigation); filters dropdown option "hide non-matching".
- **Level filter:** multi-chips ERROR/WARN/INFO/DEBUG/unknown.
- **Skip marker:** backpressure drops render as `— 312 lines skipped (UI lagging) —` divider ([modules/06 §2]).
- **Export:** modal (current filters, range tail/all-buffer, format .log/.jsonl) → `ExportLogs` → toast with [Open folder].
- **Stderr** lines: subtle red left border.
- Selection/copy: native text selection; ⌘/Ctrl-C copies plain text without UI chrome.

## 3. Performance contract

Virtualized (only visible rows in DOM); ring buffer 50 000 lines; sustains 5 000 lines/s at ≥30 fps ([06-testing.md §7]); search on buffer ≤ 100 ms.

## 4. Empty/error

No scope selected → EmptyState "Pick a project, service, or container". Stream error → inline error with retry. Container exited mid-stream → divider "— container exited (code 1) —", stream stays for others.

## 5. Tests

E2E perf with `big-logs`; pause/resume integrity (no loss below threshold); follow pin/unpin; search nav; export file content matches filters; ANSI rendering snapshot.
