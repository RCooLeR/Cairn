# Docker client & object watching

Files: `internal/docker/client.go`, `internal/docker/objects.go`

The connection layer implements a deliberate grace period: ping failures publish `docker:reconnecting`
and only escalate to `docker:disconnected` after `failureThreshold` (3) failed recovery attempts
(`client.go:335-378`). The findings below are places where *other* paths defeat that design or starve
the transport.

---

<a name="d-1"></a>
## D-1 · HIGH — WSL object poll loop exits permanently on first error and bypasses the grace period

**File:** `internal/docker/objects.go:421-439` (and its call site `:364-366`)

```go
func (c *Client) objectPollLoop(ctx context.Context) {
	...
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := c.Reconcile(ctx); err != nil {
				c.disconnect(mapDockerError("poll Docker objects", err))
				return                                  // ← loop never restarts
			}
		}
	}
}
```

and in `objectEventLoop`:

```go
if c.usesProcessBackedTransport() {
	c.objectPollLoop(ctx)
	return                                          // ← whole event loop ends with it
}
```

Two defects in one:

1. **Any single transient error** (one 10s unary timeout while the engine is busy, one blip during a
   `wsl --shutdown`) calls `c.disconnect(...)` directly — publishing the `docker:disconnected` banner
   *immediately*, with none of the reconnecting/grace behavior the health loop was built for. This
   re-introduces exactly the "disconnected flash on transient blip" the stabilization work
   (`fix: stabilize WSL runtime...`) removed.
2. After that first error the poll loop — and with it the entire `objectEventLoop` goroutine — returns
   and is **never restarted** for the life of the runtime (it is only spawned once per
   `RebindProvider`). Even after the health loop reconnects, object polling is gone until the user
   switches providers or restarts the app.

**Fix:** on `Reconcile` error, don't disconnect and don't return — log, continue the ticker (the health
loop already owns disconnect detection with proper grace). If a hard failure signal is desired, count
consecutive failures and route through the same threshold logic as `handleConnectionLoss`. Also make
`objectEventLoop` re-evaluate `usesProcessBackedTransport()` per iteration instead of `return`ing, so a
provider whose transport mode changes after reconnect keeps a watcher.

---

<a name="d-2"></a>
## D-2 · HIGH — in WSL poll mode, nothing ever publishes `objects:changed` for external changes

**Files:** `internal/docker/objects.go:291-306` (`Reconcile`), `:421` (poll loop), consumers:
`internal/portforward/manager.go:173`, `internal/logsvc/manager.go:488`, frontend listeners.

In event-stream mode, Docker events → `objectChangePublisher` → `bus.TopicObjectsChanged`. In
**poll mode (every WSL setup)**, the poll loop only calls `Reconcile`, which refreshes the SQLite
object cache and *publishes nothing*. A grep over the tree confirms the only `objects:changed`
publishers are Cairn-initiated actions (`lifecycle.go:108`, `create.go:758-766`, project service,
backups, updates executor).

Concrete consequences on WSL (the flagship platform):

- A container started/stopped **outside Cairn** (`docker run` in a terminal, a crash-exit, compose from
  a shell) never refreshes the UI — the frontend refreshes inventory only on `objects:changed`,
  `docker:connected`, `provider:changed`, and user actions (no polling interval exists in `App.tsx`).
- `logsvc` project-scope follow sessions attach new containers from `objects:changed`
  (`manager.go:484-507`); on WSL a `docker compose up` scale-out from a shell never attaches → missing
  logs in a live stream.
- The port-forward manager's prompt-reconcile never fires; only its own 5s ticker saves it (works, but
  the subscription is dead weight on WSL).

**Fix:** make `Reconcile` (or the poll loop) diff the new snapshot against the previous one (the object
cache already stores the prior state — cheap ID/state comparison) and publish
`ObjectsChangedPayload{Kind: ...}` per kind that changed. Even a coarse "publish container-kind change
when the container list hash differs" restores UI liveness with one event per 60s poll at worst. Also
consider dropping `defaultReconcileEvery` (60s) to something WSL-friendly (10–15s) since it is the only
change-detection mechanism there.

---

<a name="d-3"></a>
## D-3 · HIGH — `MaxConnsPerHost: 4` starves the API when more than 4 follow-streams are open (WSL)

**File:** `internal/docker/client.go:456-466`

```go
func processBackedHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			MaxConnsPerHost:     4,
			...
```

For the process-backed (WSL stdio) transport every connection is a `wsl.exe … dial-stdio` child, so a
cap is sensible — but `MaxConnsPerHost` counts **active** connections, and follow-mode
`ContainerLogs(follow=true)` responses hold their connection for the stream's lifetime (they are plain
HTTP responses, unlike exec-attach which hijacks *outside* the pool via the dialer). Per net/http
semantics, when the limit is reached **new dials block**.

Concrete failure: open the Logs view for a compose project with ≥5 services (logsvc attaches one
follow-reader per container, `internal/logsvc/manager.go:395-437`). The 5th reader blocks in the
transport queue; every subsequent unary call (`ContainerList`, `Ping`, …) also blocks until its 10s
context deadline → the UI throws timeout errors, the health-loop ping fails, and the client enters the
reconnecting path — all while Docker itself is healthy. Metrics one-shots (already serialized by
`StatsConcurrency: 1`) contend for the same 4 slots.

**Fix:** don't let long-lived streams share the unary pool cap:
- simplest: raise/remove `MaxConnsPerHost` (keep `MaxIdleConns` small; `IdleConnTimeout: 10s` already
  reaps idle wsl.exe children — the process count is bounded by actual concurrent streams, which the
  app already limits elsewhere), or
- give streams their own `http.Client` (separate `dockerclient` instance for logs/stats) so unary calls
  always have headroom.
The stdio diagnostics panel added on this branch will make the effect visible either way.

---

<a name="d-4"></a>
## D-4 · MEDIUM — events-stream error triggers an instant `disconnected` banner (non-WSL path)

**File:** `internal/docker/objects.go:403-412`

```go
case err, ok := <-errs:
	...
	if err != nil {
		c.disconnect(mapDockerError("watch Docker events", err))
		streamOK = false
	}
```

On native/Colima transports, the `Events` stream errors the moment the daemon restarts (EOF) — this
calls `disconnect` directly, publishing `docker:disconnected` with **zero grace**, racing (and beating)
the health loop's calm reconnecting flow. The loop itself recovers (backoff + resubscribe), so the only
defect is the premature banner/notification — but that is precisely the UX the grace period exists to
prevent, and a daemon restart is its canonical trigger.

**Fix:** on stream error, don't `disconnect`; clear the connection (or just let `ensureConnected`
re-dial next iteration) and let the health loop decide disconnected-ness through its threshold. Publish
nothing from here.

---

## D-5 · LOW — cache-write failures fail the user's read

`ListContainers`/`GetContainer`/etc. return an error if `saveContainers` (SQLite cache write) fails
(`objects.go:70-73`), so a transient `SQLITE_BUSY` beyond the busy timeout turns a *successful* Docker
read into a user-visible failure. The cache is an optimization; log and return the data instead.

## D-6 · INFO

- `isDockerConflictMessage` (`client.go:503-508`) matches any error containing "is already in use" —
  broad, occasionally misclassifies unrelated errors as Conflict. Cosmetic.
- `handleConnectionLoss` retries forever with capped backoff and no jitter; two Cairn instances against
  one broken backend sync their retries. Harmless at this scale.
- `Connect` publishes `docker:connected` on every successful (re)connect including the lazy
  `ensureConnected` path — consumers treat it as a refresh trigger, so this is fine, just noisy.
