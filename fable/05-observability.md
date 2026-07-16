# Logs, metrics, terminal, provider manager

Files: `internal/logsvc/*`, `internal/metrics/*`, `internal/terminal/manager.go`,
`internal/providers/manager.go`

These packages are in good shape overall: consistent injected clocks, per-session lifecycles with
timeouts, careful teardown (`waitForClosed` patterns), NaN/reset-guarded math (`metrics/math.go` is
clean — counter resets clamp to 0, cgroup v2 `inactive_file` handled). Findings:

---

<a name="m-1"></a>
## M-1 · MEDIUM — provider auto-fallback silently overwrites the user's saved provider choice

**File:** `internal/providers/manager.go:463-487`

```go
func (m *Manager) updateActiveAfterDetect(...) {
	saved, _ := m.settings.GetString(ctx, "provider.active_id")
	if saved != "" {
		if status, ok := statuses[saved]; ok && status.Healthy {
			m.setActiveBestEffort(ctx, saved)
			return
		}
	}
	for _, id := range m.providerIDsSnapshot() {
		if status, ok := statuses[id]; ok && status.Healthy {
			m.setActiveBestEffort(ctx, id)      // ← persists the fallback over the user's choice
			return
		}
	}
}
```

`setActiveBestEffort` **persists** `provider.active_id`. If the user's chosen provider is unhealthy for
one detect cycle (WSL restarting, docker briefly down) and any other provider (e.g. an added "existing
context") is healthy, the user's choice is permanently replaced — when the original recovers, `saved`
now points at the fallback and the code stays there. The explicit `SetActiveProvider` choice is
supposed to be durable.

**Fix:** keep two notions — `provider.active_id` (user intent, only written by `SetActiveProvider`) and
the in-memory effective/fallback provider. On detect, prefer intent when healthy; fall back in memory
only, without persisting.

---

<a name="l-1"></a>
## L-1 · MEDIUM — followed project with zero containers spams `logs:error`

**File:** `internal/logsvc/manager.go:497-501` with `:253-256`

`watchObjects` re-resolves containers on every `objects:changed` event; `resolveContainers` returns
`NotFound("No log containers matched the request")` when a project currently has zero containers. For a
follow stream on a project the user just stopped, **every** subsequent object change in the whole engine
publishes another `logs:error` to the stream — error toast/banner spam until the user closes the view.

**Fix:** in the watch path, treat "no containers matched" as a benign empty set (keep watching), e.g.
give `resolveContainers` an `allowEmpty` mode for watch/rescan calls.

---

<a name="l-2"></a>
## L-2 · LOW — `stop()` racing `attach()` can leak a blocked reader for the stream's life

**File:** `internal/logsvc/manager.go:412-437, 454-464`

Producer goroutine: `ContainerLogs(s.ctx, ...)` → `s.addReader(key, reader)`. If `stop()` runs its
`closeReaders()` in the window after `ContainerLogs` returned but before `addReader`, the new reader is
missed; the producer then blocks in `ReadDockerLogStream` on an idle follow stream that nothing will
close (ctx cancellation doesn't unblock a blocked `Read`; the deferred `reader.Close` only runs when the
producer exits — circular). `stop()` then reports "log stream readers did not stop within 5s" and leaks
the goroutine + connection (which also pins a `MaxConnsPerHost` slot — see D-3).

**Fix:** after `addReader`, re-check `s.ctx.Err() != nil` and close/return immediately; that closes the
race window.

---

<a name="m-2"></a>
## M-2 · LOW — stats semaphore is held for a stream's entire lifetime (latent)

**File:** `internal/metrics/manager.go:325-343`

`streamContainer` acquires a `statsSemaphore` slot and holds it until the stream ends. Today this is
harmless because the only wiring that sets `StatsConcurrency` (WSL, `shell/runtime.go:345-350`) also
sets `DisableStreamingStats`, so streams never run under a semaphore. But the API invites a config
where `StatsConcurrency=N` with streaming enabled → the first N containers monopolize all slots forever
and every other container gets **no metrics at all** (both the streaming and one-shot paths acquire
before doing anything). Guard it: acquire per-sample in the fallback path only (already the case) and
either document that `StatsConcurrency` implies one-shot mode or enforce it in `applyOptions`.

<a name="m-3"></a>
## M-3 · LOW — "uptime" derives from `CreatedAt`

**File:** `internal/metrics/manager.go:452-458` — `uptime = raw.Read.Sub(summary.CreatedAt)`. For a
restarted container this reports total age since creation, not time since start (docker's own uptime
uses `StartedAt`, which the cache even stores via `containerRecordFromInspect`). Displayed uptimes are
wrong after any restart.

<a name="m-4"></a>
## M-4 · LOW — install-plan bookkeeping leaks / blocks retry

**Files:** `internal/providers/manager.go:299-323`, `internal/providers/windows_wsl.go:361-392`

`Manager.ApplyInstall` deletes its plan record *before* executing; if a step fails, a retry with the
same planID reports "Install plan expired" at the manager even though the provider still holds the
plan — and the provider-side `installPlans[planID]` entry is only deleted when the **last step
succeeds**, so failed plans accumulate until app restart. Delete the manager record only after success
(or restore it on failure), and TTL-expire provider-side plans.

---

## Terminal manager — reviewed, no defects worth filing

`internal/terminal/manager.go`: correct `finishOnce` teardown; `context.WithoutCancel` for
resize/inspect after the opening request ends (deliberate and right); session cap enforced under lock;
pump publishes then finishes on read error with exec exit-code inspection. Two informational notes:
`WriteTerminal` takes no per-session write lock (xterm input is serialized by the single frontend
caller, and PTY/hijacked writers tolerate interleaving — fine today); `containerUser` treats a failed
`id -u` probe as "root iff requested user string looks like root" — sensible fallback.

## Metrics manager — additional notes (fine as-is)

- `gpuMetrics` can run duplicate probes when multiple sessions tick during an expired cache window
  (stampede is bounded by session count and 5s TTL; nvidia-smi is cheap enough).
- `GetDashboardMetrics` calls `gpuMetrics` + `ContainerProcessPIDs` per running container per uncached
  probe — the dominant cost on big hosts; acceptable at desktop scale.
- `flush()` re-queues failed batches with sort+trim caps (`maxPendingPersistSamples`) — solid.
- `refreshDockerInfo` caches `onlineCPUs` once per runtime; a provider rebind builds a new manager, so
  stale CPU counts don't cross backends.
