# Module 07 — Terminal Manager

`internal/terminal`

## 1. Session kinds

| Kind | Transport | Spawn |
|---|---|---|
| host | local PTY | provider `HostShellCommand` (PowerShell / bash·zsh·fish / zsh) |
| backend | local PTY | provider `BackendShellCommand` (`wsl.exe -d U`, `colima ssh`, native shell) |
| project | local PTY | backend shell with `cd <mapped workdir>` + `COMPOSE_*` env |
| container | Docker exec hijack | exec TTY via docker client (modules/03 §5) |

## 2. PTY layer

- Linux/macOS: `creack/pty`. Windows: ConPTY (via `aymanbagabas/go-pty` or equivalent maintained lib — pinned at Phase 3).
- Session: `{ID, Kind, argv, env, cwd, ptyFD/hijackConn, cols, rows, startedAt}`. Output pumped raw (base64) to `terminal:data{sessionID}` — bypasses bus coalescing (lossless, ordered). Input via `WriteTerminal`. Resize via PTY ioctl / exec resize API.
- Exit: process/exec end → `terminal:closed{sessionID, exitCode}`; sessions also closed on app quit (SIGHUP to children).
- Limits: max 16 concurrent sessions; output rate naturally bounded by xterm.js consumption — no server-side throttle, but a 1 MiB in-flight cap pauses reads until frontend acks (Wails event ack via `WriteTerminal` keepalive — implement as simple high-water pause).

## 3. Container session specifics

Shell detection per modules/03 §5 with user override; options: user, workdir, env. Session info exposes `IsRoot` (uid 0 check via `id -u` probe) → UI root badge. If no shell found → `E_NOT_FOUND` with hint ("distroless image — try docker debug-style tooling", manual command copy).

## 4. Frontend contract

xterm.js owns rendering, scrollback (10 000 lines), copy/paste, search addon. Frontend sends input verbatim (incl. control sequences), backend never interprets. One terminal page with tab strip + sessions persist across page navigation until closed ([ui/08-terminal.md]).

## 5. Cheatsheet & palette data

`GetCheatsheet()` serves the curated command list: ≥ 60 entries across categories (containers, images, compose, volumes, networks, logs, exec, stats/debug, cleanup), each `{Command, Description, Risk, Placeholders[], Runnable}`. Placeholders (`<container>`, `<service>`…) are filled by UI context (current selection). `Runnable=true` only for `safe` commands; others copy-only or route through plan pipeline. Risk labels must match [05-security.md §2].

## 6. Tests

Unit: session registry, high-water pause logic, placeholder filling. Integration: host/backend/container sessions echo round-trip; resize reflects in `stty size`; bash-less container fallback; 16-session limit; goleak. E2E: typing latency subjectively < 50 ms (measured echo round-trip < 100 ms in test).
