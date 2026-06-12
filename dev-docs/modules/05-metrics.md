# Module 05 — Metrics Collector

`internal/metrics`

## 1. Collected signals

Per container: CPU %, memory used/limit, network RX/TX (cumulative), block read/write (cumulative), PIDs, restart count, health, uptime. Derived: per-second rates from cumulative counters; project/service aggregates (sum mem/rates, sum CPU%); host-level: Docker disk usage (images/containers/volumes/build cache) from `system df` (cached 5 min).

## 2. Sampling strategy

- Source: Engine API `ContainerStats(stream=true)` — one stream per running container, supervised; fallback one-shot polling if a stream errors repeatedly.
- Adaptive cadence: containers visible in UI (frontend registers visibility scope via `StartStatsStream(scope)`) sample at 2 s; background containers at 10 s; paused/exited not sampled.
- CPU% computed Docker-CLI-compatible: `(cpuDelta/systemDelta) * onlineCPUs * 100`.
- Rates: `(counterNow - counterPrev) / dt`; counter resets (container restart) → clamp to 0, never negative.

## 3. Aggregation & delivery

- In-memory ring of latest samples per container (for instant page loads).
- Bus topic `stats:sample` coalesced to latest-per-container per 1 s for the UI.
- Project aggregates computed on publish (group by cached project mapping).
- Top-N (CPU/memory) computed in backend for dashboard (avoids shipping all samples).

## 4. Persistence & retention

Writes batched: one INSERT batch per 10 s with `resolution='raw'`. Hourly retention job:
```text
raw    > 60 min  → downsample to 1m rows (avg in base columns, bucket max in *_max columns), delete raw
1m     > 24 h    → downsample to 15m (same avg/max scheme)
15m    > 7 d     → delete
```
Downsampling implemented as INSERT-SELECT with `strftime` bucketing; the job is idempotent and crash-safe (transactional per bucket window). DB growth bound verified by test.

History queries (`GetProjectMetrics/GetContainerMetrics`) pick resolution by requested range: ≤ 1 h → raw, ≤ 24 h → 1m, else 15m; response is `SeriesBundle{cpu[], mem[], netRx[], netTx[], blockR[], blockW[]}` with aligned timestamps.

## 5. Edge cases

Containers without memory limit → limit NULL, UI shows used only. cgroup v1 vs v2 stat shape differences handled by SDK but tested explicitly. Very short-lived containers may produce zero samples — acceptable. Provider switch flushes in-memory state and restarts streams.

## 6. Tests

Unit: CPU/rate math fixtures (incl. counter reset, zero dt), downsample bucketing, resolution selection. Integration: values within ±10 % of `docker stats` over 60 s; 100-container seed → sampler CPU overhead < 5 % of one core; retention bounds row counts.
