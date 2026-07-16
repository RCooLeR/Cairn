# Miscellaneous smaller findings & cleared areas

## X-1 · LOW — grab-bag (each verified in source, none urgent)

1. **Terminal data uses a lossy bus** — `internal/bus/bus.go:145-161` `deliverDropOldest` silently
   drops the oldest event when a subscriber buffer is full. For `terminal:data` (buffer 4096 in
   `shell/app.go:451`) a dropped chunk means bytes vanish mid-escape-sequence — a pathological
   `yes`-style firehose can visually corrupt the xterm until a repaint. If corruption is ever reported,
   this is where it comes from; consider coalescing terminal data instead of dropping.
2. **CLI shim arg forwarding via PowerShell `$args`** — `internal/services/docker_cli_shim_windows.go`
   (`shimScript`, lines ~150-157): arguments containing embedded double quotes are mangled by
   PowerShell 5.1's native-arg quoting before they reach `wsl.exe` (e.g.
   `docker run -e 'A=he said "hi"'`). Spaces survive; embedded quotes don't. Consider a small .exe or
   a cmd shim with `%*` passthrough if fidelity matters.
3. **`wslEnableScript`** (`internal/providers/windows_wsl.go:1135-1143`): `& $wsl --status *> $null`
   then `if ($LASTEXITCODE -eq 0)` — if `Get-Command wsl.exe` succeeds but wsl.exe exits nonzero for a
   *transient* reason (e.g. the notorious UTF-16 pipe hiccup), the script proceeds to `--install
   --no-distribution`, which is harmless but slow. Fine; noted for awareness.
4. **`DetectAll` returns `joined` error even when every provider succeeded but one `SaveStatus`
   failed** (`internal/providers/manager.go:134-148`) — callers treating error as fatal will discard
   the successful statuses map they were also handed. Callers currently tolerate it; keep in mind.
5. **`MapPathToBackend` drive-relative paths** (`internal/providers/windows_wsl.go:579-587`):
   `C:foo` (no slash — CWD-relative on drive C) maps to `/mnt/c/foo`, which is wrong but essentially
   unreachable through the UI. Cosmetic.
6. **`Store` reader/writer split** (`internal/store/store.go:112-147`) is correct and deliberate
   (1-conn writer + WAL + busy_timeout=5000 + NumCPU readers). No action.
7. **Compose detector** (`internal/compose/detector.go:93-116`): `refreshContainers`' cache fallback
   branch condition `len(records) > 0 || len(summaries) == 0` is redundant (summaries is always empty
   when `d.Docker == nil`) — dead condition, harmless.

## Cleared / reviewed-clean areas

- `internal/metrics/math.go` — counter-reset clamps, NaN/Inf guards, cgroup v1+v2 memory handling:
  clean.
- `internal/bus` — subscribe/unsubscribe/close lifecycle is race-free (subscription map under mutex,
  per-subscriber close-once via unsubscribe guard); ctx-driven unsubscribe goroutine per subscription
  is bounded by subscription count.
- `internal/terminal/manager.go` — see [05-observability.md](05-observability.md).
- `internal/shell/app.go` — service wiring order, tray/quit interplay (`quitRequested` atomics), and
  notification bridge (`wasDisconnected` replace-not-stack logic) are all sound. `OnShutdown` ordering
  (cancel → StopAll → close bus → close DB) is right, subject to S-3's uncancellable deploy caveat.
- `internal/shell/runtime.go` — rebind sequence (write-lock swap → old handles stopped after unlock) is
  correct and avoids holding the service lock during teardown; per-provider tuning
  (`DisableStreamingStats`/`StatsConcurrency=1`/longer intervals for WSL) is coherent.
- `internal/providers/stdio_conn.go` / `stdio_tracker.go` — close-once semantics, first-read WSL error
  sniffing (UTF-16 heuristic), forced-kill accounting: correct. (See W-4 for one soft note.)
- `internal/docker` mappers/math spot checks — nothing filed.

## Areas NOT covered in this pass (be explicit rather than silent)

- `internal/lineage`, `internal/security`, `internal/dockerbridge`, `internal/store` repositories
  beyond the core (projects/updates/metrics/settings/lineage/object_cache SQL), `internal/compose`
  parse.go internals.
- Frontend: AgentPage (~2.2k lines), TerminalPage (~1.6k lines), DataTable, overview components (see
  [06-frontend.md](06-frontend.md); App.tsx itself *was* fully read).

The original review agents for these scopes hit a session usage limit before producing findings
(re-runs succeeded for App.tsx and updates/backups/registry — see 06 and 08). Re-running the remaining
scopes is the highest-value follow-up after fixing the HIGHs; the terminal page (xterm lifecycle,
resize, reconnect) and the security package carry the highest residual risk.
