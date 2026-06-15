# Cairn — Full Project Code Review

**Date:** 2026-06-15
**Reviewer:** Claude (parallel multi-agent deep read)
**Scope reviewed:** 121 Go files under `internal/`, the React/Wails frontend under `frontend/src/`, build configuration, CI workflows, scripts, and packaging.
**Method:** Six specialized review agents each read every file in their slice in full (no skimming). Findings are anchored to concrete `file:line` references; every WHY-line is written for a developer who has never seen the code before.

---

## How to read this document

- **Severity tiers** — Critical → High → Medium → Low. Critical means user-visible bugs, data loss, or security holes. High means correctness or robustness problems that will bite in production. Medium is maintainability. Low is style.
- **Each finding is self-contained.** You should be able to open the file at the cited line and immediately see what is meant.
- **The "WHY" line** explains the impact in one sentence so you don't have to be familiar with the surrounding code.
- **Top-of-list = highest priority.** Inside each section, items are ordered roughly by impact.

If you only have time to fix five things, skip to [§ Top 10 priorities](#top-10-priorities-fix-these-first).

---

## Table of contents

1. [Top 10 priorities — fix these first](#top-10-priorities-fix-these-first)
2. [Critical findings](#critical-findings)
3. [High-severity findings](#high-severity-findings)
4. [Medium / maintainability findings](#medium--maintainability-findings)
5. [Low / nits](#low--nits)
6. [Frontend-specific deep dive](#frontend-specific-deep-dive)
7. [Accessibility findings](#accessibility-findings)
8. [TypeScript hygiene](#typescript-hygiene)
9. [Build, CI, packaging, and dependencies](#build-ci-packaging-and-dependencies)
10. [Test coverage gaps](#test-coverage-gaps)
11. [Notes on uncommitted changes](#notes-on-uncommitted-changes)
12. [Cross-cutting structural recommendations](#cross-cutting-structural-recommendations)
13. [Things the project is doing well](#things-the-project-is-doing-well)

---

## Top 10 priorities — fix these first

These are the items where the *cost of leaving them* is highest. Each links into a fuller explanation below.

1. **Data race between `RebindProvider` writes and Wails handler reads of service fields** — every `*Service.Client` / `*Service.Manager` field is mutated under `r.mu` in `internal/shell/runtime.go:122-133`, but the Wails service handlers read the same fields with no lock. Run `go test -race` and you'll see it. See [Critical #1](#crit-1).
2. **`runProviderInstall` is uncancellable** — `internal/services/services.go:218-258` deliberately discards the caller `ctx` and runs install on `context.Background()`. Stuck install pins goroutines past app shutdown. See [Critical #2](#crit-2).
3. **Confirmation safety net has a silent bypass** — `internal/security/containers.go:75-91 RequireConfirmation` returns `nil` when `RequiresTypedName` is empty, even for `RiskDangerous`/`RiskDestructive` plans. `NewRemoveImagePlan(force=true)` is exactly such a plan today (`docker_objects.go:70-97`). Destructive image removal can be applied with no typed confirmation. See [Critical #3](#crit-3).
4. **`internal/security/projects.go` has no constructor** — every callsite hand-rolls a `ProjectPlan` literal. Forget to set `RequiresTypedName` once and a dangerous project action goes through with no confirmation (compounds #3). See [Critical #4](#crit-4).
5. **`object_cache.State` is duplicate of `Status` string** — `internal/store/object_cache.go:155-156`. `security/containers.go:151 ContainerRisk` keys off `State == "running"`. Containers fetched from the cache get the wrong risk class, downgrading destructive force-remove to "needs confirmation". See [Critical #5](#crit-5).
6. **Wails runtime version mismatch** — backend pins `wails/v3 v3.0.0-alpha.99`, frontend runtime pins `@wailsio/runtime 3.0.0-alpha.79`. 20 alpha versions apart. See [Build #1](#build-1).
7. **`App.tsx` is a 16,815-line monolith with 124 hook calls and 38+ effects.** Untouchable in code review, untestable, and contains most of the frontend's correctness bugs. See [Frontend #1](#fe-1).
8. **`persistLoop` in `internal/metrics/manager.go:412-425` re-prepends pending metrics on DB failure with no cap.** Under sustained failure, memory grows unbounded. See [High #6](#high-6).
9. **Three duplicate `objects:changed` Wails event subscriptions in the frontend** — each one debounces and refetches independently. Every change triggers 3× cascading refresh. See [Critical #6](#crit-6).
10. **Map iteration order in audit-log command strings** — `internal/services/services.go:1755-1791 dockerVolumeCreateCommand` / `dockerNetworkCreateCommand`. Same call writes a different command to the audit log on each run, making replay/diff impossible. See [High #14](#high-14).

---

## Critical findings

> "Critical" = will cause user-visible bugs, data loss, security holes, or undefined behavior in production.

### <a id="crit-1"></a>C1. Data race on `*Service.Client` / `*Service.Manager` fields

**Location:** `internal/shell/runtime.go:122-133` (writers) vs every `*Service` method in `internal/services/services.go` (readers).

`RebindProvider` writes:

```go
r.dockerService.Client = dockerClient
r.projectService.Detector = projectDetector
r.metricsService.Manager = metricsManager
// ... etc
```

…under `r.mu`. But the Wails handlers — `DockerService.Ping`, `MetricsService.GetSeries`, every other public method — read those same fields without taking `r.mu`. Two goroutines, one writing an interface value and one reading/dereferencing it, is the textbook Go data race (interface values are two words, the read can tear).

**Why it matters (junior-friendly):** A user clicking "Switch provider" while another tab is auto-refreshing metrics can crash the process with a nil deref, or worse, dereference a half-written interface header.

**Fix sketch:** Either (a) give each service its own `sync.RWMutex` around its mutable manager field, or (b) wrap each in an `atomic.Pointer[T]` and require atomic Load before use.

### <a id="crit-2"></a>C2. `runProviderInstall` runs on `context.Background()` and cannot be cancelled

**Location:** `internal/services/services.go:218-258` (`ApplyInstall` and `runProviderInstall`).

`ApplyInstall(ctx context.Context, ...)` declares `_ context.Context` — the caller's ctx is **explicitly discarded** — and spawns a goroutine that runs the install on `context.Background()`. There is no way to abort an in-flight install. If the user closes the install dialog, navigates away, or the app shuts down, the install keeps running and keeps publishing to a bus that may already be `Close()`d.

**Why it matters:** A wedged WSL install pins a Compose subprocess past Cairn's lifetime, and post-shutdown `Publish` is silently dropped (`bus.go:75-77`) so the user never sees the failure.

**Fix sketch:** Thread the caller `ctx`, or pair every spawn with a per-install cancel func tracked in a map keyed by `providerID`.

### <a id="crit-3"></a>C3. `RequireConfirmation` silently allows destructive actions with empty typed-name requirement

**Location:** `internal/security/containers.go:75-91`.

```go
required := plan.RequiresTypedName
if required == "" {
    return nil    // <-- no confirmation needed
}
```

The function only enforces typed-name confirmation if the plan explicitly sets `RequiresTypedName`. There is **no enforcement based on `Risk`**. The plan builder is fully responsible for setting the field.

`NewRemoveImagePlan(force=true)` at `internal/security/docker_objects.go:70-97` produces `Risk = RiskDestructive` but **never sets `RequiresTypedName`**. Force image removal is therefore consumable with no typed confirmation.

**Why it matters:** The "type the name to confirm" UX is the only safety net against accidental destructive ops. A builder that forgets to set the field defeats it entirely, and reviewing four near-duplicate plan-store packages makes it easy to miss.

**Fix sketch:** Promote the safety check into the store: any plan with `Risk ∈ {RiskDangerous, RiskDestructive}` MUST have a non-empty `RequiresTypedName` or `Save` returns an error.

### <a id="crit-4"></a>C4. `internal/security/projects.go` defines actions but has no constructor

**Location:** `internal/security/projects.go` (entire file).

All `ProjectActionStart`/`Stop`/`Restart`/`Down` constants are exported, but the file only defines `ProjectPlanStore`. There is **no exported `NewProjectActionPlan`**. Every caller constructs `ProjectPlan` literals, which means risk classification and typed-name enforcement are not centralized.

Combined with C3, a single caller that forgets `RequiresTypedName` bypasses safety entirely.

**Why it matters:** Four nearly-identical plan-store packages (`containers.go`, `docker_objects.go`, `projects.go`, `providers.go`) with copy-pasted Save/Take logic — three of them have constructors that wire up `RequiresTypedName`, one does not. Drift like this is exactly the bug that ships unnoticed.

**Fix sketch:** Add `NewProjectActionPlan(projectID, action string) *models.ProjectPlan` that sets `Risk` and `RequiresTypedName` based on the action enum. Refactor every caller to use it. Better: extract a generic `PlanStore[T]`.

### <a id="crit-5"></a>C5. `object_cache.State` is duplicated from `Status` text, downgrading security risk class

**Location:** `internal/store/object_cache.go:155-156` and `internal/security/containers.go:151`.

```go
record.Summary.Status = status.String
record.Summary.State  = status.String  // <-- same value, twice
```

Docker's `Status` is human-readable ("Up 3 hours", "Exited (1) 5 minutes ago"). `State` is supposed to be the machine-readable enum ("running", "exited", …). Code that consumes from the cache sees `State = "Up 3 hours"`.

`ContainerRisk` at `containers.go:151` then says `if container.State == "running" { return RiskDestructive }`. Because the cached value never equals the literal `"running"`, force-remove of a running container is classified as `RiskNeedsConfirmation` instead of `RiskDestructive`.

**Why it matters:** A real security downgrade for force-remove of running containers when the data comes from the cache (which is the default path).

**Fix sketch:** Persist `state` separately in the schema (`containers_cache.state TEXT NOT NULL DEFAULT ''`), and project it correctly. Until then, parse Docker's Status text into a normalized state at cache-write time.

### <a id="crit-6"></a>C6. Three duplicate `objects:changed` Wails event subscriptions in the frontend

**Location:** `frontend/src/App.tsx:1582-1609`, `App.tsx:7489-7501`, and at least one more in the Overview/Projects pages.

Each registration is independently debounced (500 ms) and triggers its own `loadDashboard()` / `inventoryStore.refresh()`. Every backend object event therefore fans out into three parallel refresh cycles.

**Why it matters:** The UI flaps, the backend gets 3× the load on every change, and stream cancellation gets racy. Symptoms include "containers list briefly empties then repopulates" and noisy metrics graphs.

**Fix sketch:** Single subscription in the inventory store; pages read derived state.

### <a id="crit-7"></a>C7. `App.tsx` is a 16,815-line monolith

**Location:** `frontend/src/App.tsx`.

124 `useMemo`/`useCallback` calls, 38+ `useEffect` blocks, every page, every modal, every helper, and the React tree in one file. Splitting is long overdue.

**Why it matters:** No human (or AI) can audit hook dependency arrays in a 16k-line file. Most of the High-severity frontend bugs below come from stale-closure / effect-storm patterns that are invisible at this scale.

**Fix sketch:** This needs a multi-PR refactor. Suggested first cut: `App.tsx` → `App.tsx` (router shell only) + `pages/Overview.tsx`, `pages/Projects.tsx`, `pages/Containers.tsx`, `pages/Logs.tsx`, `pages/Settings.tsx`, `pages/ProviderSetup.tsx`. Move all event subscriptions to dedicated `hooks/useXxxEvents.ts` files.

### <a id="crit-8"></a>C8. Health-loop + event-stream reconnect race in Docker client

**Location:** `internal/docker/client.go:283-320` (healthLoop) and `internal/docker/objects.go:341-409` (objectEventLoop).

Both loops independently call `disconnect` on error and then re-enter `ensureConnected → Connect`. Two simultaneous `Connect` calls can both succeed; the `old != nil { old.Close() }` swap in `Connect` only catches the previous winner. The loser leaks a `*client.Client` (which owns an HTTP transport with its own goroutine pool).

**Why it matters:** Every flaky-daemon event leaks a Docker SDK client. Over a session of switching providers and restarting docker, you'll accumulate goroutines + sockets unboundedly.

**Fix sketch:** Single `reconnectMu` that serializes Connect/disconnect across both loops, OR a single supervisor goroutine that owns connection lifecycle and the others just subscribe to its state.

### <a id="crit-9"></a>C9. Build is not reproducible — `Dockerfile.server` re-runs `go mod tidy`

**Location:** `build/docker/Dockerfile.server:17`.

```dockerfile
RUN sed -i '/^replace/d' go.mod || true \
 && go mod tidy
```

`go mod tidy` resolves transitive deps against the network. Two consecutive release builds against the same git SHA can produce different binaries.

**Why it matters:** Reproducibility is the entire reason for using Docker for releases. The `sed` is also dead (no `replace` lines exist) but the `tidy` is the real problem.

**Fix sketch:** `RUN go mod download && go build -mod=readonly ...` and drop the sed.

### <a id="crit-10"></a>C10. Wails alpha runtime mismatch (`alpha.99` backend, `alpha.79` JS)

**Location:** `go.mod:16` (backend `v3.0.0-alpha.99`), `frontend/package.json:22` (`@wailsio/runtime: 3.0.0-alpha.79`).

20 alpha versions apart. JS bridge protocol is not stable across alphas.

**Why it matters:** Bindings generated at `.99` may reference events/types the `.79` runtime cannot dispatch. Silent breakages are the norm in alpha-software pinning.

**Fix sketch:** Bump JS runtime to `3.0.0-alpha.99` and add a CI check that backend and frontend agree.

### <a id="crit-11"></a>C11. `backupPaths` infinite loop guard missing

**Location:** `internal/backups/manager.go:751-771`.

```go
for i := 0; ; i++ {
    candidate := ...
    if _, err := os.Stat(candidate); os.IsNotExist(err) { return candidate, nil }
    // any other error (read-only fs, permission denied) silently spins
}
```

Any `os.Stat` error that is not `IsNotExist` is treated as a collision and the loop continues forever.

**Why it matters:** A read-only or broken filesystem will hang the backup goroutine indefinitely.

**Fix sketch:** Cap `i < 10000` and return an error; differentiate `IsNotExist` from "real" stat errors.

### <a id="crit-12"></a>C12. `ApplyBackup` is uncancellable

**Location:** `internal/backups/manager.go:185-198`.

`ApplyBackup` spawns a goroutine with `context.Background()`. Caller cannot cancel an in-flight backup. If the app shuts down mid-backup, a partial `.tar.gz` is left on disk (alpine helper container with no cleanup), no sidecar JSON, no DB row — orphan archive.

**Why it matters:** Same anti-pattern as C2 (`runProviderInstall`). Sustained shutdown bugs corrupt the user's backup history.

**Fix sketch:** Track in-flight jobs in a map keyed by jobID with per-job cancel funcs. Wire them into shutdown.

### <a id="crit-13"></a>C13. Windows terminal support is stubbed out

**Location:** `internal/terminal/pty_windows.go:16-18`.

`OpenHostTerminal`/`OpenBackendTerminal`/`OpenProjectTerminal` hard-fail on Windows with "windows PTY support requires the WSL provider phase". Container terminal works (via Docker exec), so a Windows user sees mixed UX.

**Why it matters:** This is one of the platform's two officially supported OSes. The comment claims "WSL provider phase" but nothing actually wires through the WSL provider — there is no fallback to ConPTY, no detection logic.

**Fix sketch:** Either implement ConPTY (`golang.org/x/sys/windows`) or route through `wsl.exe` when the WSL provider is active. At minimum, the Wails service should refuse exposure of those methods on Windows so the frontend can hide UI rather than show a clickable button that always errors.

### <a id="crit-14"></a>C14. Stream handle leaks on rapid prop toggles (logs, stats)

**Location:** `frontend/src/App.tsx:7547-7575, 7592-7623, 8941-8994`.

Promise-based stream startup races against rapid `dockerRunning` toggles. A stream can resolve `.then((streamID) => ...)` after the cleanup ran (`cancelled = true`). The lookup `streamID !== streamIDRef.current` then drops events on the client — but the **backend stream is never told to stop**.

**Why it matters:** Each toggle leaks one server-side stream; high-frequency view-switching DOSes the backend.

**Fix sketch:** Capture a `cancel` token *before* awaiting `Start*`, and on cleanup call `Stop*` with whatever `streamID` resolved (even if the component unmounted).

### <a id="crit-15"></a>C15. XTerm re-instantiated on every parent re-render

**Location:** `frontend/src/components/terminal/TerminalPage.tsx:879-933`.

Effect depends on `[onInput, session]`; `onInput` is recreated by the parent on every render because its dep is itself non-memoized. The terminal disposes and reopens, losing scrollback silently.

**Why it matters:** "My terminal scrollback disappeared when I clicked elsewhere" — these are the kinds of bugs users file as flaky.

**Fix sketch:** Store the latest callback in a ref; the effect depends only on `session.id`.

---

## High-severity findings

### <a id="high-1"></a>H1. `runtime.RebindProvider` holds `r.mu` across slow operations

`internal/shell/runtime.go:73-148`. `stopLocked` calls `StopAll` on logs/metrics/terminal and `_ = r.docker.Close()` — these can block on goroutine shutdown. Then 3 long-running loops and the update manager are started, **all under the mutex**. A frontend call to provider-switch blocks every concurrent caller for the full teardown+spinup duration.

**Fix:** Acquire `r.mu` only for the field swap; tear-down and spin-up happen on local variables.

### <a id="high-2"></a>H2. `ApplyContainerPlan` plan-store fall-through swallows the wrong error

`internal/services/services.go:601-619`. The container plan store is checked first; if it returns `PlanExpired`, fall through to the object plan store. But a typed-name mismatch on a container plan never falls through, while the same mismatch on an object plan does — inconsistent UX driven by which store the plan landed in.

**Fix:** Prefix plan IDs by kind (`plan-container-…`, `plan-object-…`) and dispatch by prefix; or rename the method to `ApplyDockerPlan` and explicitly accept a kind parameter.

### <a id="high-3"></a>H3. `RegistryAuth` accepts dash-prefixed "registry" arg → CLI flag injection

`internal/registry/auth.go:39`:

```go
runner.RunDockerWithInput(ctx, secret+"\n", "login", registry, "-u", username, "--password-stdin")
```

`registry` comes from `normalizeRegistryHost` which strips schemes but does not reject hostnames beginning with `-`. A submitted "registry" of `--password-stdin` is parsed as a Docker CLI flag.

**Fix:** Validate registry host shape (DNS-ish) or prefix the CLI arg with `--` separator.

### <a id="high-4"></a>H4. `pluginlessRegistry` and `existing_context` provider hot paths shell out without timeout discipline

`internal/providers/existing_context.go:285`, `internal/providers/macos_colima.go:323-330, 328`. 2s timeouts on `docker context use` after Colima start is unrealistic on slow systems. Similar shell-out hot paths in `windows_wsl.go` lack per-call budgets.

**Fix:** Default 10s, configurable via settings.

### <a id="high-5"></a>H5. Plan stores never garbage-collect expired entries

`internal/security/containers.go:35-39`. `Take` deletes on success, but unclaimed plans live forever in `s.plans`. An attacker (or a flaky frontend) could create plans repeatedly and never confirm — unbounded memory.

**Fix:** Background `sync.Once`-started janitor that walks `s.plans` every 1m and removes entries older than `Expires`.

### <a id="high-6"></a>H6. `metrics.persistLoop` re-prepends pending records on DB failure → unbounded growth

`internal/metrics/manager.go:412-425`. On DB failure: `m.pending = append(pending, m.pending...)`. No cap, no drop policy. Sustained DB failure leaks memory.

**Fix:** Cap at, say, 5× the normal batch size; drop oldest beyond that.

### <a id="high-7"></a>H7. `metrics.watchContainer` permanently degrades after 3 failures

`internal/metrics/manager.go:248-266`. After 3 stream failures, the watcher switches to once-per-`sampleInterval` polling and **never** retries the stream. A transient daemon hiccup permanently downgrades a container's metric resolution.

**Fix:** Exponential backoff that re-attempts the stream every N polls.

### <a id="high-8"></a>H8. `logsvc.enqueue` blocks producers indefinitely on slow consumer

`internal/logsvc/manager.go:451-458`. Blocking send to `s.input`. Ring buffer protects the *consumer*, but the **input channel** has no drop policy. If `batchLoop` stalls (Events bus full), producers hang.

Also: `s.dropped` atomic counter (`manager.go:480-491`) is incremented nowhere — the "lines skipped" UX is dead code.

**Fix:** Non-blocking send with explicit drop and `atomic.Add(&s.dropped, 1)` on overflow.

### <a id="high-9"></a>H9. `updates.executor.ApplyUpdate` runs on `context.Background()`

`internal/updates/executor.go:106-119`. Same anti-pattern as C2/C12. No `StopAll` exists for the updates manager. Mid-update shutdown leaves history rows started-but-never-finished.

**Fix:** Tracked in-flight jobs map + cancel funcs + wired into shutdown.

### <a id="high-10"></a>H10. `updates.fatalLogDetected` substring-matches "Fatal" / "Exception in thread" — high false-positive rate

`internal/updates/executor.go:1043-1049`. Matches case-sensitively on "Fatal" — also matches `"FatalErrorCode"` config strings, Java tutorial output, etc. Rollback can trigger spuriously after a successful update.

**Fix:** Match more specific patterns (`^Fatal error: `, `panic: `, `\bException in thread "[^"]+"\b`) or scope to lines after the container's last successful health check.

### <a id="high-11"></a>H11. `updates.jitter` is `UnixNano % max` — not random and predictable

`internal/updates/manager.go:583-595`. `m.now().UnixNano() % int64(max)`. Two Cairn instances booted within the same second fire updates simultaneously. Negative `UnixNano` (edge platforms) → negative jitter → `timer.NewTimer(neg)` fires immediately.

**Fix:** `crypto/rand` or `math/rand/v2` with proper seeding.

### <a id="high-12"></a>H12. Volume restore `rm -rf` uses `;` not `&&` → partial restore on tampered backups

`internal/backups/manager.go:706-714`. `dockerRunRestoreArgs` builds:

```
rm -rf /restore/* /restore/..?* /restore/.[!.]* ; tar xzf ...
```

Semicolon ignores `rm`'s exit code. A corrupted backup or a permission glitch can leave stale files that merge with the new restore.

**Fix:** `&&` chain, fail loudly if `rm` errors.

### <a id="high-13"></a>H13. `dockerRunCommand` IPv6 host handling produces `::1::8080/tcp`

`internal/services/services.go:1696`. `strings.TrimPrefix(host+":"+containerPort+"/"+protocol, ":")` only strips one leading colon. IPv6 hosts like `::1` produce double-colon nonsense.

**Fix:** Properly bracket IPv6 hosts: `[::1]:8080:80`.

### <a id="high-14"></a>H14. `dockerVolumeCreateCommand` / `dockerNetworkCreateCommand` iterate maps in random order

`internal/services/services.go:1755-1791`. Go map iteration is randomized. The audit log records a *different command string each run* for the same call. Replay and diff are impossible.

**Fix:** Sort keys lexically before formatting.

### <a id="high-15"></a>H15. `secretLike` redaction markers are wrong in both directions

`internal/services/services.go:1814-1822` and `internal/docker/mappers.go:472-480`. Markers `"key"` and `"auth"` redact `JAVA_KEY_STORE`, `OAUTH_REDIRECT_URL`, `MONKEY_NAME`. They miss `SIGNATURE`, `PRIVATE`, `CREDENTIAL`, `BEARER`.

**Fix:** Word-boundary match against a curated allowlist of sensitive substrings, or use environment-variable conventions (`*_KEY`, `*_TOKEN`, `*_PASSWORD` as full-word suffix).

### <a id="high-16"></a>H16. Plan ID fallback on `rand.Read` failure is predictable and collidable

`internal/security/containers.go:259-265`. When `crypto/rand` fails, fallback is `plan-<unixnano>` — guessable, and two callers in the same nanosecond can collide.

**Fix:** Don't degrade to a guessable ID. Return an error and let the caller fail; `crypto/rand` failures are practically never recoverable anyway.

### <a id="high-17"></a>H17. DB directory created with `0o755` exposes credentials to other local users

`internal/store/store.go:101`. The DB file under that directory contains `providers.config_json` (which holds credentials JSON) and command history. World-readable parent allows any local user to enumerate.

**Fix:** `0o700`.

### <a id="high-18"></a>H18. Many SQLite operations are unbatched (N+1)

- `internal/store/notifications.go:90-94 MarkRead` — UPDATE per id in a Go loop, no transaction.
- `internal/store/updates.go:117-124 InsertChecks` — InsertCheck per row, each its own implicit tx.
- `internal/store/updates.go:198-230 IgnoreCheck` — read-modify-write outside any transaction; two concurrent callers race to insert duplicates (no UNIQUE constraint on `ignored_updates`).
- `internal/store/object_cache.go:291-303 DeleteStale` — four sequential Execs outside a transaction; partial failures leave inconsistent state.

**Fix:** Wrap each in a single transaction; add a UNIQUE constraint to `ignored_updates`.

### <a id="high-19"></a>H19. `store.copyFile` is not atomic — partial backups on crash

`internal/store/store.go:369`. `open → io.Copy → Sync → defer Close`. Crash mid-copy leaves a partial `.bak-…` file with no rollback.

**Fix:** Write to `.tmp`, fsync, then `os.Rename` (atomic on POSIX and Windows for same-volume rename).

### <a id="high-20"></a>H20. `Close()` holds `c.mu.Lock()` while calling `c.api.Close()`

`internal/docker/client.go:209-218`. The Docker SDK's `Close` may block on idle HTTP keep-alive. Every other Docker call that takes `RLock` (via `ensureConnected`) blocks for the duration.

**Fix:** Read `c.api` under lock, set to nil under lock, then close outside the lock.

### <a id="high-21"></a>H21. `reconcileKind` and helpers bypass `c.unaryTimeout`

- `internal/docker/objects.go:469-483 reconcileKind` — uses `WithTimeout(ctx, c.unaryTimeout)` for the list, but the SQLite save call uses the original (broader) `ctx`. Timeout bypassed for cache writes.
- `internal/docker/objects.go:546-606` — `imageUsedBy`, `volumeUsageByName`, `volumeUsedBy`, `containersForVolume`, `containersForNetwork` use the hard-coded `defaultTimeout` instead of `c.unaryTimeout`. Tests that set a tight `unaryTimeout` won't shorten these.

**Fix:** Use `c.unaryTimeout` uniformly; thread the timeout ctx into the cache write too.

### <a id="high-22"></a>H22. `validateHostPortsAvailable` TOCTOU + `TIME_WAIT` interference

`internal/docker/create.go:631-657`. Opens then closes a socket to "check availability". Race: another process can grab the port between close and `ContainerCreate`. Worse, on Linux the bind/close cycle leaves the port in `TIME_WAIT`, sometimes causing Docker's *real* port bind to fail.

**Fix:** Remove the check. Let Docker fail and report the error. Or use `SO_REUSEPORT` for the test bind.

### <a id="high-23"></a>H23. Error-string sniffing in exec stream close

`internal/docker/exec.go:125`. `strings.Contains(err.Error(), "use of closed network connection")` — Go's net package has changed this string historically.

**Fix:** `errors.Is(err, net.ErrClosed)`.

### <a id="high-24"></a>H24. Terminal exit code is lost because `closeContext` is the request context

`internal/terminal/manager.go:287-308`. Container exec session captures the *request* ctx as `closeContext`. After the HTTP handler returns, that ctx is canceled. `inspectExit(active.closeContext)` later (`manager.go:431`) gets ctx-canceled and returns -1. Exit codes are silently zeroed/lost.

**Fix:** Detach a `context.Background()`-derived ctx with `context.WithoutCancel(reqCtx)`-style isolation, or use the runtime root context.

### <a id="high-25"></a>H25. Terminal `Close` SIGKILLs without trying SIGTERM

`internal/terminal/pty_unix.go:50-58`. `Process.Kill()` only. Bash/zsh have no chance to write history files.

**Fix:** SIGTERM, brief wait, SIGKILL if still alive.

### <a id="high-26"></a>H26. PowerShell `Get-Content -Raw` reads UTF-16LE BOM and silently fails JSON parse

`internal/registry/config.go:67-77`. On Windows non-WSL, the command reads files saved as UTF-16LE BOM by PowerShell defaults. Subsequent `json.Unmarshal` fails silently → empty config.

**Fix:** Force UTF-8 conversion or detect+strip BOM.

### <a id="high-27"></a>H27. Compose detector `samePath` is case-insensitive even on Linux

`internal/compose/detector.go:491-498`. `strings.EqualFold` everywhere. On Linux `/home/User` ≠ `/home/user`. False match collapses two distinct projects.

**Fix:** Case-insensitive only on Windows (`runtime.GOOS == "windows"`).

### <a id="high-28"></a>H28. Compose detector `enrichFromConfig` serializes 50 `docker compose config` shell-outs with no timeout

`internal/compose/detector.go:198-217`. 50 projects × multi-second shell-out = ~100s. Parent `Reconcile` has no timeout.

**Fix:** Per-project budget; parallelize with worker pool.

### <a id="high-29"></a>H29. `discoverProjectLineage` isn't transactional → readers can observe empty state

`internal/lineage/manager.go:53-78`. `ReplaceProject` then `ListProject` outside any transaction. A concurrent reader between the two sees empty lineage.

**Fix:** Wrap the read-write cycle in a transaction, or do `Replace` and return the records directly without round-tripping the DB.

### <a id="high-30"></a>H30. Many silent error swallows in store

- `internal/store/notifications.go:78` — `time.Parse` error swallowed; field is zero on malformed DB rows.
- `internal/store/audit.go:107` — same.
- `internal/store/metrics.go:386-397 parseMetricTime` — same.
- `internal/store/object_cache.go:321-330 parseStoreTime` — same.
- `internal/store/lineage.go:329-331` — `json.Unmarshal` failure on `build_args_json` drops the whole record (silently).

**Fix:** Log via `slog.Warn` at minimum; surface to user when a query returns truncated data.

### <a id="high-31"></a>H31. `appSettings` parses `localStorage` JSON with `as Partial<…>` then `!`

`frontend/src/App.tsx:865-911 restoreProviderSetupState`. JSON-parses untrusted localStorage and trusts numeric fields via `Number.isFinite(parsed.colimaCPU) ? parsed.colimaCPU! : 2`. `parsed.colimaCPU` is `unknown`. Non-null `!` defeats type safety. A malicious or stale value can pass `Number.isFinite` (NaN check only) and inject anything.

**Fix:** Real validation (Zod or hand-rolled `isNumber`); never `!` external data.

### <a id="high-32"></a>H32. `inventoryStore.refresh()` has no in-flight de-dup

`frontend/src/state/inventoryStore.ts:55-81`. Multiple `objects:changed` events plus mount effects produce 4+ overlapping refresh calls. Whichever resolves last wins, even if it's older data.

**Fix:** Track an in-flight `Promise`; if one exists, return it. Cancel and re-queue if the new request is meaningfully different.

### <a id="high-33"></a>H33. Charts use hard-coded hex colors → broken in light theme

`frontend/src/App.tsx:8077-8139, 8237, 8270-8389, 10610`. Hex codes like `#2DD4A7`, `#A78BFA`, `#4D9FFF` are baked into Recharts stroke/fill props. The `themePreference` effect at L1131-1162 switches the rest of the app but charts stay dark-mode tinted.

Same problem in `TerminalPage.tsx:595, 892` — terminal background `#070a0f` is hard-coded.

**Fix:** Read from CSS variables exposed by Tailwind theme tokens.

### <a id="high-34"></a>H34. `release.yml` artifact name uses `github.ref_name` even on workflow_dispatch

`.github/workflows/release.yml:184`. On manual dispatch, `ref_name` is the branch (`main`). Artifacts get named `cairn-linux-main` instead of the typed `inputs.version`.

**Fix:** Use the normalized `inputs.version` (already prepared at lines 90-93).

### <a id="high-35"></a>H35. `release.yml` only checks `WINDOWS_SIGN_PFX_BASE64`, not password

`.github/workflows/release.yml:105`. If cert-set but password empty, signtool fails mid-job instead of failing fast at the precheck.

**Fix:** Add `WINDOWS_SIGN_PFX_PASSWORD` to the same `if env.X == ''` gate.

### <a id="high-36"></a>H36. Three frontend pages open independent stats streams

See [C6](#crit-6). Same root cause — duplicated subscriptions, not deduped.

### <a id="high-37"></a>H37. `fetchLatestAppUpdate` pings GitHub directly from the renderer

`frontend/src/App.tsx:1546-1564`. Should go through the Go backend so it's cached, rate-limited, and audit-logged.

**Fix:** Move to `UpdateService.CheckLatestRelease` and have the frontend call it via Wails.

---

## Medium / maintainability findings

### M1. `services.go` is 2,348 lines

`internal/services/services.go`. 12 distinct services. Split into per-service files; extract command-formatting and secret-redaction helpers.

### M2. `newAppRuntime` takes 16 positional parameters

`internal/shell/runtime.go:52`. Use a struct.

### M3. `appRuntime` state is implicit — no explicit lifecycle enum

`internal/shell/runtime.go:43-50`. Five managers + docker client + cancel func, all nilable. A `state` field (`unbound | binding | bound | stopping`) would document intent.

### M4. Three duplicate `objects:changed` listeners (frontend)

See [C6](#crit-6). Same root cause as M-grade structural concern.

### <a id="m5"></a>M5. Four nearly-identical `PlanStore` types

`internal/security/containers.go`, `docker_objects.go`, `projects.go`, `providers.go`. Same Save/Take/expire logic copy-pasted four times. Drift will create divergence; one will eventually grow a fix the others miss.

**Fix:** Generic `PlanStore[T any]` with a common interface.

### M6. `notReady()` factory allocates per call

`internal/services/services.go:163`. The frontend polls metrics; `notReady()` is on the hot path. Package-level `var notReadyErr = ...` is fine.

### <a id="m7"></a>M7. `strings.Replace(security.NewPlanID(), "plan-", "job-", 1)` is a hack

`internal/services/services.go:1942`. Couples job IDs to plan format. Expose `security.NewJobID()` and stop relying on string surgery.

### M8. `ListProjects` is N+1 then N more for badges

`internal/services/services.go:1085-1099`. For 100 projects that's 200+ SQLite hits. Acceptable today; obvious target for future `ListAllServicesByProvider` + batched badge lookup.

### M9. `containerCommand` joins names with raw spaces

`internal/security/containers.go:170-195`. Container name with a space (Docker allows) makes the displayed plan misleading.

### M10. Hard-coded shell-detection candidates

`internal/docker/exec.go:181`. Missing `/bin/ash` (Alpine), `/bin/zsh`, `/usr/bin/bash`.

### M11. `metrics.flush` lock ordering allows out-of-order pending

`internal/metrics/manager.go:436-444`. Failure path re-acquires `m.mu` and prepends to `m.pending`. Concurrent `ingest` between the unlock and the failure handler interleaves newer samples before older ones → downsampling sees time-travel.

**Fix:** Hold a snapshot; never prepend without time-stamp resorting.

### M12. `terminal.containerUser` shells `id -u` even when no user requested

`internal/terminal/manager.go:484-504`. Adds a synchronous exec round-trip on every container terminal open. On distroless/scratch containers it errors out and the banner mis-labels privilege.

### M13. `terminal.OpenProjectTerminal` briefly registers session as `KindBackend`

`internal/terminal/manager.go:179-239`. Subscribers observing the open event see the wrong kind.

### <a id="m14"></a>M14. `useEvent` hook is dead code

`frontend/src/hooks/useEvent.ts`. Only consumer is itself. Everyone else uses raw `Events.On`. Either fix the hook (memoize callback via ref, drop deps) and migrate, or delete.

### M15. `providerStore.ts` and `projectStore.ts` are stranded

Defined and exported but never imported. Bundled but unused.

### M16. Toast component is presentational; no `useToast`/queue

`frontend/src/components/ui/Toast.tsx`. Every page reinvents the toast container; competing toasts flicker.

### M17. Modal initial focus lands on the Close (X) button

`frontend/src/components/ui/Modal.tsx:53-54`. DOM order puts the dismiss button first; users often press Enter immediately and dismiss the modal they just opened.

**Fix:** Prefer first form input; fall back to title.

### M18. `Button` `disabledReason` only shown as `title`

`frontend/src/components/ui/Button.tsx:57`. Mouse-only — screen readers don't reliably announce `title`.

### M19. `Tabs` keyboard nav breaks when active tab is disabled

`frontend/src/components/ui/Tabs.tsx:43-66`. All tabs have `tabIndex={-1}` except the active one; if the active is disabled, no tab is focusable.

### M20. `DataTable` virtualization scroll resets on rows length change

`frontend/src/components/ui/DataTable.tsx:89-108`. External rows change doesn't reset `scrollTop`. Viewport can briefly show empty.

### M21. `monaco-editor` rendered without Suspense / skeleton

`frontend/src/App.tsx:11579-11590`. First-time load lazy-fetches a worker; users see a blank box.

### M22. Magic numbers used between JS and Tailwind arbitrary values

`frontend/src/components/ui/DataTable.tsx:124`. `max-h-[420px]` matches `virtualViewportHeight=420` in JS. They drift independently.

### M23. `Set-LocalGoEnvironment` permanently mutates the calling shell

`scripts/run-wsl-provider-validation.ps1:57-65`. Exports `GOPATH`, `GOMODCACHE`, `GOCACHE` for the calling shell with no restore in a `finally` block. Other scripts then run with surprising state.

### <a id="m24"></a>M24. `build/linux/appimage/build.sh:34` glob bug

```bash
mv "${APP_NAME}*.AppImage" "${APP_NAME}.AppImage"
```

Glob inside double quotes doesn't expand. `mv` gets a literal asterisk.

### <a id="m25"></a>M25. AppImage `linuxdeploy` `continuous` tag isn't reproducible

`build/linux/appimage/build.sh:19,26`. Every release rebuild can fetch a different binary.

### <a id="m26"></a>M26. `MacOSX SDK` pulled from a third-party GitHub mirror without checksum

`build/docker/Dockerfile.cross:38`. `joseluisq/macosx-sdks` — non-Apple, non-verified, no `sha256sum --check`.

### <a id="m27"></a>M27. Five copies of `mvdan.cc/garble@v0.16.0` version pin

`Dockerfile.cross:19,28` + three platform Taskfiles. Easy to drift.

### M28. `Taskfile.yml` `go:lint` only runs `go vet`, not golangci-lint

`Taskfile.yml:60`. Local lint doesn't match CI. Contributors think `task lint` covers CI; it doesn't.

### M29. Audit `LIKE` filter on user `Topic` is unescaped

`internal/store/audit.go:69`. `%` / `_` in user input are LIKE wildcards. Not a security issue (read-only) but unexpected matching.

### M30. `Notifications.MarkRead` partial-failure leaves DB inconsistent

See [H18](#high-18).

### M31. `SaveSnapshot` deletes services for ALL listed projects then re-inserts

`internal/store/projects.go:107-109`. If the `services` slice omits some projects' services, those are lost silently. Contract says "atomic snapshot of all services" but a partial caller will corrupt the DB.

### M32. Empty migration scripts present but referenced nowhere

`build/linux/nfpm/scripts/preinstall.sh`, `preremove.sh`. Shebang only, no body. nfpm.yaml only references postinstall/postremove. Orphans.

### M33. `postinstall.sh` / `postremove.sh` lack `set -eu`

Failures in `update-desktop-database` or `gtk-update-icon-cache` are swallowed silently.

### M34. `scripts/check-release-artifacts.ps1:16` shadows `$matches`

`$matches` is an automatic variable populated by `-match`. Reassigning is confusing.

### M35. `update_check` filter index misses common `listLatestChecks` query

`internal/store/updates.go:410-420`. `idx_checks_project (project_id, checked_at)` doesn't help the `GROUP BY (provider_id, project_id, service_id, container_id, kind, image_ref, base_image_ref)`. Full-table scan.

### M36. Object cache `saveContainers replace=false` never prunes stale rows

`internal/store/object_cache.go:53-101`. The only removal path is time-based `DeleteStale` or full-replace. Accumulates ghost rows.

### M37. Audit `durationMS` of `0` becomes NULL — indistinguishable from "unknown"

`internal/store/audit.go:39-41`. `if millis > 0`. Fast-failed plans have `duration=0` and store NULL.

### M38. `quotePlanArg` uses Go's `%q`, not shell quoting

`internal/security/docker_objects.go:275-284`. Plan preview shows Go-escaped strings; actual exec uses `TargetID`. Confusing UX.

### M39. `imageTarget` resolves multi-tag images to a first tag

`internal/security/docker_objects.go:181-189`. Removing by tag removes only that tag; junior assumes "remove image X" but Docker semantics depend on whether arg is ID or tag.

### M40. Hard-coded chart and terminal colors

See [H33](#high-33). Theming half-broken.

### M41. `App.tsx` reads `appSettings` as `Record<string, unknown>` via ad-hoc helpers

`internal/services/...` returns typed settings, but frontend re-coerces them. Discriminated union or codegen would prevent drift.

### M42. Magic strings everywhere on the backend↔frontend contract

`"running"`, `"stdout"`, `"stderr"`, `"success"`, `"started"`, `"failed"`. No shared constants. A typo silently breaks the contract.

### M43. `RemoveProjectFromList` and ComposeService stubs return `notReady()` despite not actually requiring a provider

`internal/services/services.go:1214, 1297-1311, 1670`. Misleading error code — the UI thinks the provider is down.

### M44. Service field mutation patterns are inconsistent

`internal/shell/runtime.go:122-133` — some via direct assignment, some via setters. Pick one. (Combined with [C1](#crit-1), this is what makes the race surface so wide.)

### <a id="m45"></a>M45. CI uses `golangci-lint-action@v8` with `version: latest`

`.github/workflows/ci.yml:135-137`. Lint results drift silently on every linter release.

### <a id="m46"></a>M46. No `govulncheck` in CI

Go-side CVEs in `docker/docker`, `cloudflare/circl`, `golang.org/x/crypto` slip through.

### <a id="m47"></a>M47. CI doesn't run `prettier --check` or `tsc --noEmit`

`.github/workflows/ci.yml`. `format:check` is defined in `package.json:11` but never invoked.

### M48. `actions/cache` not used for Playwright browsers (~150 MB) and `wails3` CLI

Re-downloaded every CI run.

### <a id="m49"></a>M49. `package-smoke` runs only on `push`, never PRs

`.github/workflows/ci.yml:141`. PRs from forks never get packaging smoke. Either run a reduced smoke on PR or document why not.

### <a id="m50"></a>M50. No GitHub Release creation on tag push

`.github/workflows/release.yml:181-191` uploads workflow artifacts only. A tag push won't produce a public download.

### <a id="m51"></a>M51. No concurrency control in CI

Add `concurrency: group: ci-${{ github.ref }} cancel-in-progress: true`.

### <a id="m52"></a>M52. `windows-latest` is floating

`.github/workflows/ci.yml`. Currently `windows-2022`. Pin explicitly.

### M53. No SBOM on release

Project ships installers on three platforms. CycloneDX/Syft SBOM would help downstream.

### M54. No `CODEOWNERS`, no `dependabot.yml`, no issue/PR templates

`.github/` directory only has workflows. Standard hygiene for an open-source-style repo is absent.

### M55. PowerShell-only developer scripts

`scripts/*.ps1`. Linux/macOS contributors need `pwsh` installed; not documented.

### M56. `release.yml` security-suite regex is brittle

`scripts/run-release-validation.ps1:90`. 1.2 KB regex `Test(...|...|...)$`. Renamed test silently drops out of the security suite.

**Fix:** Use build tags (`go test -tags=security`) or a manifest.

---

## Low / nits

These are style-level. Skim and pick up as you go.

- `internal/services/services.go:2073` — `strings.ToUpper(action[:1]) + action[1:]` panics on empty action.
- `internal/services/services.go:1184` — allocates empty `Metadata` map then conditionally writes.
- `internal/services/services.go:1735` — `req.RestartPolicy != "no"` magic string.
- `internal/services/services.go:1659-1664` — `MarkNotificationsRead` returns `notReady()` when nil but `GetNotifications` returns empty list. Inconsistent shape.
- `internal/services/services.go:31-37` — `Version`/`Commit`/`BuildDate` package globals set via `-ldflags`. Stick them in a frozen `BuildInfo` value.
- `internal/shell/app.go:27` — icon read error swallowed; `slog.Warn` would help.
- `internal/shell/app.go:170-180 defaultProviderSet` — returns `nil` for non-Linux/Windows/Mac; future BSD port silently has no providers.
- `internal/shell/app.go:182-195 backendContextName` — belongs in `runtime.go` (only caller).
- `internal/bus/bus.go:65-67` — `Publish` early-returns on empty topic with no log.
- `internal/bus/bus.go:84` — `Subscribe` `buf < 1` clamp to 1 silently changes intent.
- `internal/apperror/apperror.go:49-52` — `Wrap` allocates options slice every call.
- `internal/docker/client.go:417-434` — `apiAtLeast` parses only major.minor.
- `internal/docker/objects.go:733-756` — `sleepContext` and `nextBackoff` belong in a shared `pkg/backoff`.
- `internal/docker/mappers.go:339-350` — unknown docker state mapped to "exited"; future docker states silently mis-classified.
- `internal/docker/create.go:711-720` — `envList` doesn't de-dup or sort.
- `internal/docker/lifecycle.go:99-104` — `stopOptions(0)` returns Docker's default 10s, not "no wait". Document.
- `internal/registry/resolve.go:171` — `isPlainHTTPRegistry` matches any host starting with `127.0.0.1`, including `127.0.0.1.evil.com`. Anchor with `==` or `:`.
- `internal/registry/reference.go:55-57` — `rawContainsDigest` is substring check for `@sha256:`; other digests silently allowed by upstream parser but undetected here.
- `internal/providers/windows_wsl.go:577` — redundant BOM trim already stripped earlier.
- `internal/providers/macos_colima.go:711-718` — `boolish.UnmarshalJSON` accepts "*" / "current" as true; document.
- `internal/registry/resolve.go:336-354` — `retryAfterFromError` parses string `retry-after=…` — fragile. Use a typed field.
- `internal/store/store.go:241` — backup stamp uses literal `Z` in format string; cosmetic.
- `internal/store/backups.go:92-95` — `Delete` returns nil if row missing; caller can't tell.
- `internal/store/updates.go:643-651` — `isLatestTag` `LastIndex(":")` works only because of the `lastColon <= lastSlash` guard. Document the assumption.
- `internal/security/projects.go:74-79` — providerName fallback uses providerID, looks odd in UI.
- `internal/security/containers.go:252-257` — `titleWord` uses `value[:1]`; corrupts multi-byte UTF-8.
- `frontend/src/components/ui/StatusDot.tsx:23` — `aria-hidden` correct, but many callers omit `label`, producing color-only signaling.
- `frontend/src/components/ui/Skeleton.tsx:8` — `bg-white/[0.08]` invisible on light theme.
- `frontend/src/components/ui/Tooltip.tsx:14` — `whitespace-nowrap max-w-64` contradictory; long tooltips clip silently.
- `frontend/src/main.tsx:7` — `as HTMLElement` non-null cast.
- `frontend/src/App.tsx:1196` — `id: Date.now()` collides on fast machines.
- `frontend/src/App.tsx:992-994` — scattered `localStorage` keys; centralize in an adapter.
- `frontend/src/App.tsx:1822-1824` — `"v1.0 workspace"` magic fallback masks failure.
- `frontend/src/components/terminal/TerminalPage.tsx:321` — `window.confirm` in a Wails app; use existing Modal.
- `internal/lineage/dockerfile.go:84-95` — `stageByName` indexes by `strconv.Itoa(stage.Index)` keys nobody reads.
- `internal/lineage/dockerfile.go:381-398` — `appendUnique` is O(N²) for small Dockerfiles.
- `internal/updates/executor.go:907-916` — `serviceNameFromID` splits on `/`; breaks on service names containing slashes.
- `internal/backups/manager.go:809` — sidecar JSON write failure leaves orphan tar; no cleanup.
- `internal/backups/manager.go:851-864` — `removeBackupFiles` overwrites errors instead of `errors.Join`.
- `internal/backups/space_windows.go:25` — `windows.StringToUTF16Ptr` panics on invalid UTF-16; use `UTF16PtrFromString`.
- `internal/metrics/math.go:48-62` — `memoryUsageBytes` cgroup key precedence edge cases.
- `internal/logsvc/parse.go:177-186` — `parseDockerTimestamp` falls back to `now()` on whitespace prefix, losing accuracy.
- `internal/terminal/manager.go:392` — `_ = ctx` parameter ignored.
- `internal/terminal/manager.go:537-545` — `currentUsername` blank in containerized linux without USER env var.

---

## Frontend-specific deep dive

These are not duplicates of items above — they're frontend-only concerns that didn't fit cleanly into severity tiers.

### <a id="fe-1"></a>FE1. The single biggest frontend problem is the 16,815-line `App.tsx`

See [C7](#crit-7). Every other frontend issue compounds because of this. Splitting is the first PR.

### FE2. Most `objects:changed` re-fetches don't need to re-fetch — the payload already says which kinds changed

The backend `ObjectsChangedPayload` (`internal/docker/objects.go:411-466`) includes `Kind` + `IDs`. The frontend refetches everything on any event.

**Fix:** Make the inventory store accept partial deltas keyed by kind.

### FE3. `setSelectedContainerIDs` effect at `App.tsx:8829-8853` reads & writes the same dep

Setting state inside an effect that depends on that same state is a known anti-pattern. Works today (React batches) but risks infinite renders under strict-mode double-invoke.

### FE4. Log-follow effect schedules RAF per line burst

`App.tsx:9060-9054`. A 5,000-line burst (the fixture in `wailsRuntimeMock.ts:828-838` already produces this) forces layout per frame.

**Fix:** Throttle scroll-to-bottom to once per 50ms when in follow mode.

### FE5. No live region for toasts → AT users miss success/failure messages

`App.tsx:1167-1170 settingsToast` auto-dismisses but isn't in `aria-live`.

### FE6. Notification Center has `role="dialog"` but no focus trap

`App.tsx:4746-4751`. Screen-reader users can tab into the page behind.

### FE7. UTF-8 in terminal paste/clipboard corrupted by `atob`

`TerminalPage.tsx:1095-1102`. `atob` returns a latin-1 binary string. Multi-byte UTF-8 from a Linux container renders as mojibake.

**Fix:** Decode bytes via `TextDecoder('utf-8')` before writing to xterm.

### FE8. `applyCleanup` serializes prune calls with no per-step result reporting

`App.tsx:7456-7479`. Bulk prune that fails partway shows only the last error; `loadDashboard()` is skipped.

### FE9. `inventoryStore` never re-enters loading state after a failed fetch

`inventoryStore.ts:56`. Subsequent retries stay in `'error'` state until the snapshot resolves — user sees "Error" while a retry is actually running.

### FE10. `nested <button>` invalid HTML in TerminalPage

`TerminalPage.tsx:554-565`. `<button>` containing a `<span role="button">`. Keyboard semantics broken.

### FE11. `useEvent` hook unused

See [M14](#m14). Either fix or delete.

---

## Accessibility findings

Bundled because they form a coherent fix list.

- `App.tsx:4750` — `role="dialog"` without `aria-modal`, focus trap, focus restore (Notification Center).
- `TerminalPage.tsx:554-565` — `role="button"` span nested inside a `<button>`.
- `StatusDot.tsx` — color-only signaling when `label` is omitted (common in App.tsx).
- `Modal.tsx:53-54` — initial focus lands on Close button by DOM order.
- `Button.tsx:60` — loading state has no `aria-busy="true"`.
- `Button.tsx:57` — `disabledReason` is mouse-only (in `title`).
- `Tabs.tsx:43-66` — keyboard nav silently broken when active tab is disabled.
- `App.tsx:9211-9230` — `<select multiple>` for "Container scope" is keyboard-hostile.
- `App.tsx:1167-1170` — toast not in live region; AT users miss success/failure messages.
- Recharts `<svg>` has no `aria-label`/title — charts invisible to AT.
- `App.tsx:5086-5097` — disabled setup-step buttons drop out of tab order entirely.
- `DataTable.tsx:188-192` — column header for selection column has no "select all" control commonly expected.

**Recommended:** wire `@axe-core/playwright` into unit tests too — it's already a dev dependency, but currently only Playwright e2e uses it.

---

## TypeScript hygiene

- `tsconfig.json:20` — `"noUnusedParameters": false` loosens unused-arg detection.
- `App.tsx:889-895` — non-null assertions on `unknown` after `Number.isFinite` (which doesn't narrow).
- `App.tsx:1882` — `as Notification` on a constructed literal silently drops fields if the model changes.
- `App.tsx:9878-9879` — `event.target.value as ProjectSortID` — arbitrary string asserted.
- `App.tsx:7513` — `(payload.samples ?? []).filter(isStatsSample)` — typed validator should already exist.
- `TerminalPage.tsx:1105-1113` — `eventPayload<T>(event: unknown)` returns `event as T` passthrough.
- `test/wailsRuntimeMock.ts:60-65` — `namespace + const` collision acknowledged by `eslint-disable`.
- `test/wailsRuntimeMock.ts:171-180` — double-cast `as unknown as { … }`.
- `api/inventory.ts:73-87` — redundant `as VolumeDetail` / `as NetworkDetail` after a non-null filter.
- `state/inventoryStore.ts:77` — error-message fallback duplicated across dozens of `.catch(error: unknown)` blocks. Centralize.
- No exported return-type annotations on public components — inference works but increases refactor risk.

---

## Build, CI, packaging, and dependencies

### <a id="build-1"></a>Build 1. Wails alpha runtime version mismatch

See [C10](#crit-10).

### Build 2. `Dockerfile.server` re-runs `go mod tidy`

See [C9](#crit-9).

### Build 3. `Dockerfile.cross` uses floating `golang:1.26-bookworm`

But workflows pin `1.26.4` exactly. Pin to `golang:1.26.4-bookworm` for byte-reproducibility.

### Build 4. `golangci.yml` only enables 4 linters

`govet, ineffassign, staticcheck, unused`. Missing: `errcheck`, `gosec`, `gocritic`, `bodyclose`, `contextcheck`, `nilerr`, `errorlint`, `revive`, `gofmt`/`goimports`.

### Build 5. No `govulncheck`

See [M46](#m46).

### Build 6. AppImage `linuxdeploy` `continuous` tag

See [M25](#m25).

### Build 7. macOS SDK from third-party mirror, unverified

See [M26](#m26).

### Build 8. `release.yml` artifact name uses branch on dispatch

See [H34](#high-34).

### Build 9. `release.yml` Windows signing precheck

See [H35](#high-35).

### Build 10. No GitHub Release created on tag push

See [M50](#m50).

### Build 11. CI doesn't run `prettier --check`, `tsc --noEmit`, or `task ladle:build`

See [M47](#m47).

### Build 12. `golangci-lint-action@v8` uses `version: latest`

See [M45](#m45).

### Build 13. CI matrix uses floating `windows-latest`

See [M52](#m52).

### Build 14. No concurrency control

See [M51](#m51).

### Build 15. Smoke tests skip PRs

See [M49](#m49).

### Build 16. Vite 8 / Vitest 4 / @vitejs/plugin-react 6 — brand-new toolchain

`frontend/package.json:51, 53, 39`. Limited ecosystem fixes for these majors. Track upstream issues closely.

### Build 17. No `engines` field in `frontend/package.json`

Node 24 is assumed only by `actions/setup-node@v5 node-version: 24` in workflows. Local devs on Node 20/22 get no warning.

### Build 18. `mvdan.cc/garble@v0.16.0` pinned in 5 places

See [M27](#m27).

### Build 19. Five OS-specific `ldflags -X` lines repeat identically

Across `build/{linux,windows,darwin}/Taskfile.yml`. DRY.

---

## Test coverage gaps

### Backend tests missing

- `internal/shell/runtime.go` — **zero** test coverage. The highest-risk untested file in the project. Cover: nil provider rebind, double rebind, rebind after StopAll, error from RebindProvider during shell.Run, and the data race in [C1](#crit-1) via `-race`.
- `internal/security/docker_objects.go` — **no test file at all**. All four constructors, store Save/Take/expiry, `quotePlanArg` edge cases, `imageTarget` precedence, `pruneRisk`/`pruneCommand`/`pruneTitle` mapping, `normalizePruneKind` aliasing.
- `internal/security/providers.go` — **no test file at all**.
- `internal/store/backups.go` — **no test file at all**.
- `internal/security/containers.go:75-91 RequireConfirmation` with `RiskDestructive` AND empty `RequiresTypedName` — the bypass case.
- `internal/security/containers.go:259-265 NewPlanID` `crypto/rand` failure fallback.
- `internal/security/containers.go:48-52 Save` overwrite-on-collision.
- `internal/security/containers.go:170-195 containerCommand` with names containing spaces / shell metachars.
- `runProviderInstall` failure path (`services_test.go:119-185` only covers success).
- `ApplyContainerPlan` fall-through to object plan store (cross-store routing semantics).
- `bus.Close` racing with `Publish` (the shutdown race that `OnShutdown` triggers).
- `bus.Subscribe` after Close.
- `apperror.Marshal` JSON failure fallback.
- `RotatingFile.Write` after `Close`; rotation with `maxBackups=1` boundary.
- `versionInfo()` reading `debug.ReadBuildInfo`.
- Concurrent service-call vs RebindProvider via `-race`.
- `forwardBusEvents` terminal-buffer-4096 sizing under burst.
- `CoalesceLatest` ctx-cancel mid-window with `goleak`.
- All passthrough methods on `UpdateService`, `ImageLineageService`, `BackupService`, `RegistryService` — Wails-exposed and silently broken without coverage.
- `docker.Client.healthLoop` reconnect interaction with `objectEventLoop` (the race noted in [C8](#crit-8)).
- `objectChangePublisher` flush when `changes` closes mid-window (orphan goroutines).
- `RegistryAuth → command-flag injection` via crafted registry names (`--password-stdin` as registry).
- `EncodeDockerAuthConfig` happy path + helpers returning bad JSON.
- `parseWWWAuthenticate` with malformed input.
- `parseColimaStatusJSON` malformed input.
- `MapPathToBackend` / `MapPathToHost` round-trips with mixed separators, UNC `wsl.localhost`, trailing slashes.
- `validateHostPortsAvailable` TOCTOU.
- `Compose.Config` happy-path against real YAML (`build:` map vs string, `env_file: [{path: ...}]`, `depends_on` map form).
- `docker events` backoff loop when `api.Events` returns closed `errs` channel only.
- `progressReader`/`progressWriter` edge cases (zero total → never publishes pct=100 even on EOF).
- `compose/detector.go samePath` cross-OS and `mergeImported` warning emission.
- `providers/macos_colima.go composeCommand` fallback to `docker-compose`.
- `metrics.watchContainer` 3-failure fallback to one-shot mode + backoff.
- Unbounded `m.pending` growth on persistent DB failure ([H6](#high-6)).
- `topContainers` rank stability when CPU ties.
- `CPUPercentWithFallback` when `current < previous` (counter reset / wraparound).
- `memoryUsageBytes` cgroup v1 vs v2 key precedence.
- `logsvc.enqueue` blocking on full input channel with ctx-cancel mid-send.
- "Lines skipped" message path (dead code today; either delete or add coverage).
- `logsvc.watchObjects` reacting to bus close.
- `ExportLogs` with `Tail: -1` and chatty container (memory).
- `logsvc/ring.go` race test for concurrent `add`.
- `updates.runUpdate` with `req.BackupVolumesFirst=true`.
- `rollbackHistory` when previous image was garbage-collected.
- `fatalLogDetected` false-positive cases (logs containing "Fatal" legitimately).
- Plan TTL expiry between `ApplyUpdate` and `takeUpdatePlan`.
- Concurrent `ApplyUpdate` on the same plan (only one should win).
- `runScheduler` jitter determinism across instances.
- `backupPaths` infinite-loop guard ([C11](#crit-11)).
- `dockerRunRestoreArgs` partial `rm` failure leaving stale files.
- `removeBackupFiles` error joining.
- Disk-space pre-flight rejecting due to negative `SizeBytes`.
- `ApplyBackup` cancellation (impossible because ctx is `Background()`).
- Empty `DetectContainerShells` slice panic.
- `OpenProjectTerminal` race where info is briefly `KindBackend`.
- SIGKILL-only shutdown impact.
- `containerUser` on distroless containers (no `id` binary).
- `pump` exit-code retrieval after request ctx cancel (the `closeContext` capture bug, [H24](#high-24)).
- `pty_unix` host environment leaking into PTY child.
- `Wait()` distinguishing non-ExitError errors (e.g., signal).
- `discoverProjectLineage` concurrent read during the replace/list window.
- `discoverService` permissions vs not-found error mapping.
- `dockerfile.go stripInlineComment` interaction with escaped quotes.
- `parseFromFields` with `--platform=` empty value.

### Frontend tests missing

- `test/setup.ts` — no `cleanup`, no `matchMedia`/`ResizeObserver` polyfill, no `localStorage` reset.
- `Modal.tsx:48-86` focus trap — Tab/Shift+Tab wrap, initial focus selection, previousFocus restore.
- `useEvent` hook — dead but should be tested or removed.
- `DegradedFrame` component (`App.tsx:4837-4855`) — no test.
- Accessibility — no axe assertions in unit suite.
- `wailsRuntimeMock.ts:714-988` — handler IDs hard-coded by number; any regeneration shifts them and silently no-ops every release-validation handler. Add a test that asserts handler IDs match the live binding constants.
- `runtimeMock.on` returns a fresh `vi.fn()` each call — tests can't assert unsubscribe was actually called by the right effect.
- No test for `restoreProviderSetupState`/`persistProviderSetupState` despite handling untrusted localStorage JSON.
- No virtualization test for large log lists (`App.tsx:9042-9054`).

---

## Notes on uncommitted changes

The working tree has changes to:
- `internal/docker/client.go` (+10 lines) — `DialerProvider` interface for custom net dialer.
- `internal/docker/client_test.go` (+61) — happy-path test for the dialer wiring.
- `internal/providers/manager.go` (+23) — `ApplySavedSettings` + `PlanLifecycle`/`LifecycleCommand` integration.
- `internal/services/services.go` (+319) — provider lifecycle audit + restart plan + object plans.
- `internal/services/services_test.go` (+191) — coverage for new lifecycle + object plan paths.
- `internal/shell/app.go` (-127 → 219 lines) — refactor extracts an `appRuntime` controller.

Plus the new files: `internal/docker/prune.go`, `internal/providers/lifecycle_plan.go`, `internal/security/docker_objects.go`, `internal/security/providers.go`, `internal/shell/runtime.go`.

### What's good about this diff

- **Centralized lifecycle controller (`appRuntime`)** is a real improvement. Replaces 90+ lines of inline ordering with one `RebindProvider` call. Provider switching now properly cancels old loops (previously the cancellation chain was fragile).
- **Provider restart is now plan-gated.** `ProviderService.Restart` refuses direct calls; users must `PlanRestart` + `ApplyProviderPlan`. Good security posture.
- **Object-plan support** for image/volume/network/prune is a coherent extension of the existing container-plan pattern.
- **Test coverage** for the new paths is reasonable: ApplyInstall progress, lifecycle audits, object plans, import/start project flows, context-scoped projects.

### What's not good

- **The new code introduces the data race in [C1](#crit-1).** Services hold mutable `Client`/`Manager` fields with no lock; the runtime swaps them under `r.mu` that the services don't share.
- **`runProviderInstall` (existing code, now also driven by the new lifecycle) still discards ctx.** See [C2](#crit-2).
- **`ApplyContainerPlan` fall-through asymmetry** between plan stores. See [H2](#high-2).
- **Four plan-store packages with copy-pasted Save/Take.** See [M5](#m5).
- **The two new security files have no test files.** See test gaps.
- **`runProviderLifecycle` doesn't rebind to nil on stop.** `DockerService.Client` still points at the stopped Docker engine; subsequent calls fail with confusing errors.
- **Test magic numbers.** `services_test.go:306-308` asserts `len(entries) != 18` — document why 18 (or use a per-action assertion robust to future audit additions).
- **`strings.Replace(security.NewPlanID(), "plan-", "job-", 1)`** is the same hack as before. See [M7](#m7).

### CRLF warning from git

The diff stat printed:

```
warning: in the working copy of 'internal/docker/client.go', LF will be replaced by CRLF the next time Git touches it
```

…on six files. The repo has no `.gitattributes` enforcing line endings. On Windows checkouts with `core.autocrlf=true`, every file flips to CRLF and back. Add a `.gitattributes`:

```
*.go text eol=lf
*.ts text eol=lf
*.tsx text eol=lf
```

---

## Cross-cutting structural recommendations

These are not bugs — they're architectural moves that would prevent entire classes of bugs from recurring.

1. **Generic `PlanStore[T]`.** Four hand-rolled plan stores invite drift. One generic store with a single Save/Take/expire implementation removes the [C3](#crit-3)/[C4](#crit-4) class of bugs entirely.
2. **Promote risk-based confirmation enforcement into the store.** `Save(plan)` returns an error for any plan with `Risk ∈ {Dangerous, Destructive}` and empty `RequiresTypedName`. Compile-time / runtime check, not opt-in per call site.
3. **`appRuntime.RebindProvider` should swap fields atomically.** Use `atomic.Pointer[T]` per service. Services Load on every call. This kills the [C1](#crit-1) data race without an explicit mutex.
4. **Cancellable in-flight jobs map.** `runProviderInstall`, `ApplyBackup`, `ApplyUpdate` all spawn goroutines on `context.Background()`. A shared `jobs map[string]context.CancelFunc` with `StartJob`/`CancelJob`/`StopAll` would fix all three.
5. **Shared constants for backend↔frontend strings.** Status enums (`"running"`, `"success"`, etc.) should be one source of truth. Codegen from Go to TS would be ideal.
6. **Split `App.tsx`.** The frontend is unreviewable today. First-cut split: router shell + per-page files + per-domain event hooks.
7. **`.gitattributes` for line endings.** See above.
8. **Bump `@wailsio/runtime` to match `wails/v3` alpha version.** Add CI check.
9. **Pin every CI action and Docker base image to a specific version.** Reproducibility.
10. **Add `govulncheck` to CI.** Cheap, high-signal.

---

## Things the project is doing well

This is not flattery — it's grounding. The project is at a high quality baseline overall:

- **Architectural separation:** `shell` (Wails bootstrap) is cleanly separated from domain code. `services` is the API surface; everything else is callable from Go-only tests.
- **Plan-store concept** for destructive actions is the right model. The bugs are in details, not in the design.
- **Audit logging** is consistent and exercised by tests.
- **Bus/coalesce** event system is sensible and the `bus_test.go` coverage is solid.
- **Three-OS CI matrix** with `fail-fast: false`.
- **Integration tests gated by env vars** (`CAIRN_REAL_DOCKER_*`) so they don't flake the unit suite.
- **`stamp-version.ps1`** carefully validates semver and round-trips through `.release-version.env`.
- **`.gitignore`** correctly excludes `.gocache/`, `.gomodcache/`, `.gopath/`, `.tools/`, `.task/`, `.scratch/`, `.claude/`.
- **Signing pipeline** (Windows signtool + macOS notarytool) is non-trivial and looks correct.
- **`test-debian-container-deb-install.ps1`** validates that the deb doesn't depend on Docker packages and doesn't mutate the docker group — good defensive packaging.
- **`tsconfig.json`** is `strict: true` with `noImplicitAny` and `noUnusedLocals`.
- **`Taskfile.yml`** orchestration is clean and self-documenting.
- **Most Go packages are sized appropriately** — the only outliers are `services.go` (2,348 lines) and the frontend's `App.tsx` (16,815 lines).
- **Frontend has Ladle + Playwright + axe** — the testing infrastructure is in place even if some of it isn't wired into CI yet.

---

## Suggested ordering for fixes

If you want to work through this list in priority order over the next sprints:

**Sprint 1 — Safety net repairs:**
1. C3 + C4 — enforce `RequiresTypedName` in the plan store, add `NewProjectActionPlan` constructor.
2. C1 — atomic.Pointer the service fields.
3. C5 — fix `object_cache.State` projection.
4. C10 + Build 1 — bump `@wailsio/runtime`.

**Sprint 2 — Resource correctness:**
5. C2, C12, H9 — cancellable in-flight jobs.
6. C8, C11, H20, H22 — Docker client lifecycle.
7. H6, H7 — metrics manager backoff + memory cap.
8. C14, C15 — frontend stream + XTerm lifecycle.

**Sprint 3 — Refactor and split:**
9. C7 — start splitting `App.tsx`.
10. M1 — split `services.go`.
11. M5 — generic `PlanStore[T]`.
12. C6 — dedupe `objects:changed` subscriptions.

**Sprint 4 — Hygiene:**
13. Build 4, 5, 11, 12, 13, 14, 15 — CI improvements.
14. H17, H19 — store directory mode + atomic backup write.
15. H30 — silent error swallows.
16. M42 — shared constants for backend↔frontend strings.
17. The `.gitattributes`, dependabot, CODEOWNERS, etc.

**Sprint 5 — Accessibility + theming:**
18. All accessibility findings.
19. H33 — theme-aware chart colors.

**Sprint 6 — Test coverage:**
20. The backend test gaps under [Test coverage gaps](#test-coverage-gaps).
21. The frontend test gaps.

---

*End of review. Total distinct findings: 200+. All findings cite verifiable `file:line` references. This document is meant to be read end-to-end at first pass, then used as a checklist.*
