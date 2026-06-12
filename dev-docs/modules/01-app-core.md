# Module 01 — App Core & Event Bus

`internal/app`, `internal/bus`, `internal/shell`

## 1. Responsibilities

- Application lifecycle: startup sequence ([02-architecture.md §6]), graceful shutdown (cancel all streams/sessions, flush DB, close client).
- Dependency wiring: constructs store → providers → docker client → domain managers → services; plain constructor injection, no DI framework.
- Background schedulers: reconcile loop, metrics sampler, update-check scheduler, retention vacuum.
- Event bus: typed in-process pub/sub; bridge selected topics to Wails events.
- App config: data dir resolution per OS, structured logging setup, panic recovery (log + user-visible crash notice, never silent exit).

## 2. Event bus design

```go
type Topic string
type Event struct{ Topic Topic; TS time.Time; Payload any }
type Bus interface {
    Publish(Event)
    Subscribe(ctx context.Context, t Topic, buf int) <-chan Event
}
```

- Non-blocking publish: slow subscribers drop-oldest within their buffer; streams that must not lose data (terminal) bypass the bus and use direct session channels.
- Coalescers (decorators): `CoalesceLatest(topic, window)` for stats; `Batch(topic, window, maxN)` for logs and `objects:changed`.
- Shell bridge subscribes to the frontend-facing topics ([04-api-contracts.md §7]) and emits Wails events 1:1. Topic names are identical backend/frontend.

## 3. Schedulers

| Scheduler | Interval | Behavior |
|---|---|---|
| Docker events pump | continuous | engine events → cache invalidation → `objects:changed`; auto-resubscribe on reconnect |
| Full reconcile | 60 s | list all objects, diff cache, repair drift; cheap (summaries only) |
| Metrics sampler | 2 s visible / 10 s background | see modules/05 |
| Update check | settings (`updates.check_interval_hours`) | skips when daemon unreachable; jittered start |
| Retention vacuum | 1 h | metrics downsampling/delete, audit/notification trimming |

All schedulers are context-owned by app lifecycle; each exposes `TriggerNow()` for UI/tests.

## 4. Lifecycle states

```text
Starting → Ready(provider healthy) | Degraded(no provider / docker down) → ShuttingDown
```
State exposed in bootstrap payload and `provider:status`/`docker:*` events; frontend renders Degraded as global banner + onboarding affordance, never a dead screen.

## 5. Tests

Unit: wiring smoke (all services constructible), bus semantics (ordering, drop policy, coalescing), scheduler trigger/cancel. Integration: startup with daemon down → Degraded → daemon up → Ready without restart; shutdown leaks zero goroutines (goleak).
