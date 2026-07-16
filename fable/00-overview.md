# Cairn — Deep Code Review (fable)

**Date:** 2026-07-03 · **Tree:** branch `feat/port-forwarding-ui` (working tree incl. uncommitted changes)
**Build state:** `go build ./...` ✅ · `go vet ./...` ✅ · `npx tsc --noEmit` (frontend) ✅

This review dug into the code hands-on. Where behavior depended on external systems (wsl.exe argument
handling, systemd/polkit inside WSL), claims were **verified empirically** on this machine with probe
programs replicating the app's exact `exec.Command` invocations — several plausible-looking "bugs"
were *disproved* that way and are listed at the bottom of [01-providers-wsl.md](01-providers-wsl.md)
so they don't get re-reported later.

## Findings index (by severity)

| ID | Sev | Subsystem | Title | File |
|----|-----|-----------|-------|------|
| [W-1](01-providers-wsl.md#w-1) | **HIGH** | providers/WSL | `Start`/`Stop`/`Restart` run `systemctl` without `-u root` → always fail (verified) | `internal/providers/windows_wsl.go:394` |
| [D-1](03-docker-client.md#d-1) | **HIGH** | docker | WSL poll loop exits permanently on first error and bypasses the disconnect grace period | `internal/docker/objects.go:421` |
| [D-2](03-docker-client.md#d-2) | **HIGH** | docker | WSL poll mode never publishes `objects:changed` → stale UI / log streams miss new containers | `internal/docker/objects.go:421` |
| [D-3](03-docker-client.md#d-3) | **HIGH** | docker | `MaxConnsPerHost: 4` starves the whole API when >4 follow log streams are open (WSL) | `internal/docker/client.go:456` |
| [S-1](04-services.md#s-1) | **HIGH** | services | PATH shim rewrites `HKCU\Environment\Path` as `REG_SZ`, breaking `%VAR%` entries (verified in source) | `internal/services/docker_cli_shim_windows.go:174` |
| [P-1](02-port-forwarding.md#p-1) | **HIGH** | portforward | Enabled toggle is never persisted; rebind/restart force-enables forwarding | `internal/shell/runtime.go:170` |
| [F-4](06-frontend.md#f-4) | **HIGH** | frontend | Updates "Check now" button spins and stays disabled forever after the first check | `frontend/src/App.tsx:2056` |
| [B-1](08-updates-backups.md#b-1) | **HIGH** | updates | Multi-arch images always report a false "update available" (index vs platform digest) | `internal/updates/manager.go:610` |
| [B-2](08-updates-backups.md#b-2) | **HIGH** | backups | Volume restore always fails on WSL — helper-script `$` expanded by intermediate shell (verified) | `internal/backups/manager.go:852` |
| [B-3](08-updates-backups.md#b-3) | **HIGH** | backups | Non-overwrite restore TOCTOU can silently destroy an existing volume's data | `internal/backups/manager.go:522` |
| [P-2](02-port-forwarding.md#p-2) | **HIGH** | portforward | UDP relay dies permanently on Windows `WSAECONNRESET` from a stray ICMP | `internal/portforward/udp.go:53` |
| [P-3](02-port-forwarding.md#p-3) | MEDIUM | portforward | `StopAll` races an in-flight reconcile → leaked bound host port | `internal/portforward/manager.go:76` |
| [P-4](02-port-forwarding.md#p-4) | MEDIUM | portforward | Failed (`error`) forwards are never retried until container churn | `internal/portforward/manager.go:228` |
| [P-5](02-port-forwarding.md#p-5) | MEDIUM | portforward | Loopback-published ports (`-p 127.0.0.1:x:y`) relay to the distro eth0 IP → dead forward | `internal/portforward/manager.go:301` |
| [P-6](02-port-forwarding.md#p-6) | LOW | portforward | TCP accept-loop death leaves forward listed "active"; IPv6 `::1` publishes mirrored as IPv4 | `internal/portforward/manager.go:276` |
| [S-2](04-services.md#s-2) | MEDIUM | services | `redactText` erases whole lines (wrong submatch index) → agent redrafts drop env vars | `internal/services/agent_service.go:2122` |
| [S-3](04-services.md#s-3) | MEDIUM | services | Imported-project background deploy holds runtime read-lock, uncancellable → app-wide freeze | `internal/services/project_service.go:1310` |
| [S-4](04-services.md#s-4) | MEDIUM | services | `agent.max_context_lines` never truncates (counts JSON blobs as one line) | `internal/services/agent_service.go:1033` |
| [S-5](04-services.md#s-5) | MEDIUM | services | PATH edit never broadcasts `WM_SETTINGCHANGE` → "open a new shell" repair hint doesn't work | `internal/services/docker_cli_shim_windows.go:185` |
| [D-4](03-docker-client.md#d-4) | MEDIUM | docker | Events-stream error triggers instant `disconnected` banner, bypassing the grace period | `internal/docker/objects.go:409` |
| [M-1](05-observability.md#m-1) | MEDIUM | providers | Provider auto-fallback silently overwrites the user's saved provider choice | `internal/providers/manager.go:463` |
| [L-1](05-observability.md#l-1) | MEDIUM | logsvc | Followed project with zero containers spams `logs:error` on every objects-changed event | `internal/logsvc/manager.go:497` |
| [W-2](01-providers-wsl.md#w-2) | MEDIUM | providers/WSL | `hostname -I` backend-IP pick breaks under WSL mirrored networking / multi-IP | `internal/providers/windows_wsl.go:465` |
| [S-6](04-services.md#s-6) | LOW | services | `ApplyFileEdit` create-mode skips the conflict check → silent overwrite | `internal/services/agent_service.go:637` |
| [S-7](04-services.md#s-7) | LOW | services | `DraftProjectFile` reads unbounded file into the LLM prompt | `internal/services/agent_service.go:527` |
| [L-2](05-observability.md#l-2) | LOW | logsvc | `stop()` racing `attach()` can leak a reader goroutine for the stream's life | `internal/logsvc/manager.go:427` |
| [M-2](05-observability.md#m-2) | LOW | metrics | Stats semaphore is held for a stream's lifetime (latent starvation if config changes) | `internal/metrics/manager.go:325` |
| [M-3](05-observability.md#m-3) | LOW | metrics | "Uptime" is computed from `CreatedAt`, not `StartedAt` | `internal/metrics/manager.go:452` |
| [M-4](05-observability.md#m-4) | LOW | providers | Install-plan records leak on failure; retry after mid-plan failure reports "plan expired" | `internal/providers/manager.go:299` |
| [F-3](06-frontend.md#f-3) | MEDIUM | frontend | Removed containers' stats never pruned → inflated project metrics after churn | `frontend/src/App.tsx:18929` |
| [F-5](06-frontend.md#f-5) | MEDIUM | frontend | Failed update/rollback job renders as green success box; backend error discarded | `frontend/src/App.tsx:2120` |
| [F-6](06-frontend.md#f-6) | MEDIUM | frontend | Inspect modal reopens after close / shows wrong item on out-of-order responses | `frontend/src/App.tsx:4655` |
| [F-7](06-frontend.md#f-7) | MEDIUM | frontend | Hub search re-triggers on selection; in-flight responses never invalidated | `frontend/src/App.tsx:2153` |
| [F-1](06-frontend.md#f-1) | LOW | frontend | PortForwardingPanel stays hidden after provider becomes WSL until remount | `frontend/src/components/settings/PortForwardingPanel.tsx:58` |
| [F-8..F-12](06-frontend.md#f-8) | LOW | frontend | Dead controls (log-export Range, chart 5m/1h/24h), dismissed update notice resurrected, debounced refresh dropped, stale network-detail merge | `frontend/src/App.tsx`, `hooks/useDebouncedRuntimeEvent.ts` |
| [B-4](08-updates-backups.md#b-4) | MEDIUM | backups | Interrupted restore leaves a stash that permanently blocks later restores of that volume | `internal/backups/manager.go:852` |
| [B-5](08-updates-backups.md#b-5) | MEDIUM | registry | `Login` deletes the working inline credential before the new login is validated | `internal/registry/auth.go:35` |
| [B-6](08-updates-backups.md#b-6) | MEDIUM | updates | Background update scheduler dies permanently on transient error or disable→re-enable | `internal/updates/manager.go:487` |
| [B-7](08-updates-backups.md#b-7) | MEDIUM | updates | Update bookkeeping runs on cancelled job ctx → history rows stuck in "started" | `internal/updates/executor.go:707` |
| [B-8..B-12](08-updates-backups.md#b-8) | LOW | upd/bak/reg | Delete-order strands artifacts; plans consumed before validation; rollback erases `new_*` history columns; stray volume blocks retry; `sh` assumed on Windows host for existing contexts | various |
| [U-1](08-updates-backups.md) | LOW | updates | Automatic-rollback compose output published with empty job ID — invisible in job panel | `internal/updates/executor.go:703` |
| [X-1](07-misc.md) | LOW | misc | Smaller items: bus drop-oldest vs terminal data, `parseWSLListVerbose` NAME-prefix skip, `wslEnableScript` `*> $null` + `exit $LASTEXITCODE`, shim arg quoting via PowerShell `$args`, `truncateSingleLine` UTF-8 split, `isDockerConflictMessage` over-matching | various |

## Coverage map

| Area | Depth |
|------|-------|
| `internal/providers` (WSL, stdio, exec, manager, apt; linux/colima lifecycle) | **Full read + empirical probes** |
| `internal/portforward`, `internal/logsvc`, `internal/metrics`, `internal/terminal` | **Full read** |
| `internal/docker` (client.go, objects.go, math paths) | **Full read** (create/exec/files/lifecycle/logs/stats/prune/mappers: spot checks only) |
| `internal/shell` (app.go, runtime.go) | **Full read** |
| `internal/services` (all files) | **Full read by review agent** + independent source verification of top findings |
| `internal/bus`, `internal/store` (core/open/DSN), `internal/compose` (detector core) | Targeted read |
| `internal/updates`, `internal/backups`, `internal/registry` | **Full read by review agent** + empirical WSL/docker verification; executor core also read directly |
| `frontend/src/App.tsx` (~20k lines) + `hooks/` + stores | **Full read by review agent** (all ~19.9k lines, cross-checked against Go backend) |
| `frontend` settings/port-forward UI (this branch's changes) | **Full read of diff + panel** |
| `internal/lineage`, `internal/security`, `internal/dockerbridge`, AgentPage/TerminalPage frontend | **Not covered this pass** (review agents for these scopes hit a session usage limit) |

## Fix-first list

1. **B-3** — the only *data-destruction* finding: re-check volume existence at restore-apply time.
   Small fix, removes the one path where Cairn can silently delete real user data.
2. **W-1** — add `-u root` to the WSL `systemctl start|stop|restart docker` calls (one line each); the
   engine Start/Stop/Restart buttons currently cannot work on a stock Ubuntu WSL install.
3. **B-2** — escape `$` for the WSL provider in the backup/restore helper path; volume restore is
   currently 100% broken on the flagship provider (fails safely, but unusable).
4. **B-1** — compare local digest against index *or* platform digest; until then update checks are
   wrong for essentially every official image and "apply" loops forever.
5. **P-1** — persist the port-forward toggle (settings key read in `RebindProvider`); right now a user
   who disables forwarding gets it silently re-enabled on every restart.
6. **D-1/D-2** — make the WSL object poll loop survive errors, route failures through the same
   grace-period path as the health loop, and publish `objects:changed` when a poll detects a diff;
   this restores the "stabilize WSL runtime" behavior the recent commits aimed for.
7. **F-4** — clear `updateCheckJobID` on completion; one-line fix for a button that bricks itself.
