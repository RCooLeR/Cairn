# Module 06 — Log Streaming

`internal/logsvc`

## 1. Responsibilities

Container/service/project/all-scope log streams; stdout/stderr demultiplexing; multiplexed multi-source streams with stable source identity; tail/since/follow; batching and backpressure; level heuristics; pagination for non-follow reads; export.

## 2. Stream architecture

```text
ContainerLogs(follow) ──┐
ContainerLogs(follow) ──┼─► merger (per stream session) ─► ring buffer (50k lines)
ContainerLogs(follow) ──┘            │
                                     └─► batcher (≤50ms / ≤200 lines) ─► event logs:lines{streamID}
```

- Source: Engine API `ContainerLogs` with `follow, timestamps=true, tail=N` (Docker's stdcopy demux for non-TTY containers; TTY containers are single-stream).
- Project/service scope: resolve member containers from cache; containers that start later in the project are attached dynamically (subscribed to `objects:changed`).
- Each line: `LogLine{TS, ContainerID, ContainerName, Service, Stream(stdout|stderr), Level?, Text}`. Order within a source preserved; across sources merged by timestamp with 200 ms tolerance window.
- Level heuristic (display only, never filtering truth): regex pass for `ERROR|WARN|INFO|DEBUG|FATAL` tokens and JSON `"level":` fields; unknown → none.
- Backpressure: if frontend lags (event queue full), drop-oldest in ring; UI shows "N lines skipped" marker line.

## 3. Non-follow reads & export

`FetchLogPage`: bounded `ContainerLogs(tail/since/until)` merged and paginated (cursor = last ts+source). `ExportLogs`: same pipeline to file (`.log` plain or `.jsonl`), path returned; respects current filters.

## 4. Session lifecycle

Sessions registered with ID; cancelled by `StopStream`, page navigation (frontend calls stop on unmount), docker disconnect (auto-restart on reconnect if session still open), or app shutdown. Leak-checked in tests.

## 5. Tests

Unit: stdcopy demux fixtures, merger ordering/tolerance, ring drop accounting, level heuristics corpus, batch flush timing. Integration: `big-logs` 5 000 lines/s sustained 60 s — zero loss below backpressure threshold, correct skip markers above; dynamic container join mid-stream; goleak on session churn.
