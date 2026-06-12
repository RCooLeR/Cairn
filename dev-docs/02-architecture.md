# 02 — System Architecture

## 1. Architectural goal

One app, one UI, one Go core across Windows/Linux/macOS. All OS differences live behind **provider adapters**. The core never knows whether Docker runs natively, in WSL, in Colima, or remotely.

## 2. Layer diagram

```text
┌──────────────────────────────────────────────────────────────┐
│ Frontend (React + TS, Wails WebView)                         │
│  pages · components · state (zustand) · generated bindings   │
└───────────────▲──────────────────────────────▲───────────────┘
        typed method calls               events (push)
┌───────────────┴──────────────────────────────┴───────────────┐
│ Wails v3 Services layer (Go)  — one Service struct per       │
│ API group in 04-api-contracts.md                             │
├──────────────────────────────────────────────────────────────┤
│ Domain core (Go, internal/)                                  │
│  docker client · compose wrapper · project detector ·        │
│  metrics · logs · terminal · registry · updates · lineage ·  │
│  backups · security/audit · store (SQLite) · event bus       │
├──────────────────────────────────────────────────────────────┤
│ Provider manager + providers                                 │
│  linux_native · windows_wsl · macos_colima ·                 │
│  existing_context · remote_ssh                               │
└──────▲─────────────────▲──────────────────▲──────────────────┘
       │                 │                  │
 Docker Engine API   docker compose CLI   OS commands
 (socket/pipe/ssh)   (exec via provider)  (wsl.exe, brew, systemctl…)
```

Hard rules:
- The frontend NEVER executes shell commands. It calls typed service methods only.
- The domain core NEVER calls OS-specific commands directly; it asks the active provider.
- Docker Engine API is used for live object state, streams, events, exec. Compose CLI is used for Compose lifecycle semantics. SQLite is a cache/history store — **Docker is the source of truth** for live state.

## 3. Wails v3 integration

Wails v3 is alpha-track (v3.0.0-alpha.9x as of mid-2026) but API-stable with production usage. We accept this with mitigations:

- Pin the exact Wails version in `go.mod`; upgrade only at phase boundaries.
- Isolate Wails-specific code to `internal/shell/` (app bootstrap, window, menus, events adapter, service registration). The domain core MUST compile without Wails imports → if v3 regresses badly, fallback to Wails v2 touches only `internal/shell/` and binding generation.
- Use Wails v3 **Services** for the API groups; use the v3 **events API** for backend→frontend push; use generated TS bindings (`wails3 generate bindings`) — never hand-write binding signatures.
- Single main window in v1. Tray (P2) uses v3 systray support.

## 4. Repository layout

```text
cairn/
  main.go                       # wails3 entry
  internal/
    shell/                      # Wails bootstrap, service registration, event adapter
    app/                        # lifecycle, config, DI wiring, background schedulers
    bus/                        # event bus (see modules/01)
    providers/                  # interface, manager, linux_native/, windows_wsl/,
                                # macos_colima/, existing_context/, remote_ssh/
    docker/                     # Engine API wrapper (modules/03)
    compose/                    # CLI wrapper + detector (modules/04)
    metrics/                    # collector/aggregator/retention (modules/05)
    logsvc/                     # log streaming/multiplexing (modules/06)
    terminal/                   # PTY sessions (modules/07)
    registry/                   # digest checks, auth (modules/08)
    updates/                    # checker/planner/executor/health/rollback (modules/09)
    lineage/                    # dockerfile parser, discovery (modules/10)
    backups/                    # volume backup/restore (modules/11)
    store/                      # sqlite + migrations (modules/12)
    security/                   # confirmations, audit, secrets (modules/13)
    services/                   # Wails service structs implementing 04-api-contracts
    models/                     # shared domain structs (single source for DTO shapes)
  frontend/
    src/
      api/                      # generated bindings + thin wrappers
      state/                    # zustand stores per domain
      pages/                    # Dashboard/ Projects/ Containers/ Images/ Volumes/
                                # Networks/ Logs/ Terminal/ Updates/ Settings/ Onboarding/
      components/               # design-system components (ui/00)
      hooks/                    # useEvent, useStream, usePolling, useConfirm
      styles/
    index.html  vite.config.ts  tailwind.config.js
  build/                        # wails packaging config per OS
  testdata/                     # sample compose projects (06-testing.md §4)
  dev-docs/
```

## 5. Process & concurrency model

- One Go process (Wails). Long-running work runs in goroutines owned by managers; every stream/session is context-cancellable and registered in a session table for cleanup.
- **Event bus** (`internal/bus`): in-process pub/sub. Topics are listed in [04-api-contracts.md §7]. The shell layer subscribes and forwards selected topics to the frontend via Wails events. Backpressure: per-topic coalescing (e.g., stats samples coalesce to latest; log lines batch at ≤ 50 ms intervals).
- **Schedulers** (in `internal/app`):
  - refresh loop: Docker events subscription drives cache invalidation; full reconcile every 60 s as fallback;
  - metrics sampling: every 2 s for visible containers, 10 s for others (adaptive, see modules/05);
  - update checks: manual + configurable interval (default: daily, off for metered);
  - retention vacuum: hourly.
- All Docker API calls carry timeouts (default 10 s; streams unbounded with cancel). All provider shell commands carry timeouts and capture stdout/stderr/exit code.

## 6. Startup sequence

```text
1. Open SQLite, run migrations.
2. Load settings; construct providers for current OS + saved configs.
3. Detect providers (parallel, 5 s budget each) → pick active provider
   (saved choice if healthy, else best detected, else onboarding).
4. Connect Docker client via provider's DockerHost/context. Ping.
5. Subscribe to Docker events; start schedulers.
6. Emit app:ready with bootstrap state (provider status, counts).
7. Frontend renders Dashboard or Onboarding based on bootstrap state.
```

Failure handling: any step's failure degrades gracefully — the app always opens; pages show actionable empty/error states with repair hints from the provider ([ui/01-app-shell.md §5]).

## 7. Command-plan pipeline (the trust mechanism)

Every mutating operation flows through one pipeline in `internal/security`:

```text
Request → Plan (ordered real commands + risk level + effects)
        → [UI confirmation if risk ≥ needs_confirmation; typed-name if dangerous]
        → Execute (step by step; stream output; stop on error)
        → Audit (command, target, result, duration, error)
        → Events (object change notifications)
```

Risk levels: `safe`, `needs_confirmation`, `destructive`, `dangerous`. The mapping of operations to risk levels is normative in [05-security.md §2].

## 8. Error model

Go errors are wrapped into a typed `AppError{Code, Message, Detail, RepairHints[], Cause}` at the services boundary. Codes are enumerated in [04-api-contracts.md §8]. The frontend maps codes to UI treatments (inline error, toast, blocking state, onboarding redirect). Never leak raw stderr to the user without the structured wrapper (raw output available in a "details" expander).

## 9. Key sequence: one-click service update

```text
UI → UpdateService.PlanServiceUpdate
   ← plan {kind, commands, backupOption, healthOption, rollbackOption}
UI shows confirmation modal with real commands
UI → UpdateService.ApplyServiceUpdate(planID, options)
   backend: audit(start) → [backup volumes] → snapshot rollback metadata
   → provider.RunCompose("pull"/"build --pull", service)
   → provider.RunCompose("up -d", service)
   → health watcher (status, healthcheck, restart count, log scan, timeout 60 s)
   → success | failed → [auto-rollback if enabled & safe] → audit(end)
   ← UpdateResult; events: update:progress*, project:changed
```

## 10. Design constraints (normative)

- Never assume or require Docker Desktop.
- Never implement a custom runtime.
- Never bypass confirmation for destructive actions.
- Never store secrets unencrypted ([05-security.md §4]).
- Domain core compiles without Wails; providers compile per-OS via build tags where needed.
- One source of truth for DTO shapes: `internal/models` → generated TS bindings.
