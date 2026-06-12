# Cairn v1.0 Progress

Source of truth: `dev-docs/`, especially `dev-docs/07-development-plan.md`.

Status legend: `todo`, `in_progress`, `green`, `blocked`.

| Phase | Step | Status | Evidence | Notes |
|---|---|---:|---|---|
| 0 | 0.0 Dev environment (Windows machines) | blocked | `wsl -l -v` reports no installed WSL distributions; see `docs/manual-platform-validation.md` | All local Windows Docker work must target `cairn-dev`, never Docker Desktop or `desktop-linux`. No Docker integration tests run against the visible Docker Desktop context. |
| 0 | 0.1 Repo & toolchain | green | `npm audit --audit-level=moderate`; ESLint; Vitest; `tsc --noEmit`; Vite production build; `go test . ./internal/...`; `go vet . ./internal/...`; `go build ./...`; `wails3 build`; `golangci-lint v2.12.2 run --timeout=5m`; CI run 27431749444 green on Ubuntu 24.04, Windows, macOS; `wails3 dev -port 9250` built, launched `bin/cairn.exe`, started Vite, and WebView2 connected; adding `zz_wails_hot_reload_probe.go` produced a second Wails `Build` marker and regenerated bindings; temporary `frontend/src/App.tsx` probe produced Vite `hmr update /src/App.tsx` and was served by the live dev server | Wails v3 pinned to `v3.0.0-alpha.99`; Go pinned to `toolchain go1.26.4`; logo/icon sourced from `assets/`. CI Linux runner changed to `ubuntu-24.04` in `dev-docs/08-packaging-release.md` because Wails alpha.99 defaults to GTK4/WebKitGTK 6.0. Golangci excludes frontend dependencies and build outputs. Browser plugin unavailable in this sandbox (`CreateProcessAsUserW failed: 5`), so hot-reload proof used Wails/Vite logs and live module fetch. |
| 0 | 0.2 SQLite store + migrations | green | `internal/store` with modernc SQLite, writer/reader handles, WAL/foreign-key/busy-timeout/synchronous pragmas; embedded `0001_v1_schema.sql` from `dev-docs/03-data-model.md`; forward-only migrator with newer-schema refusal and pre-migration backup retention; settings repository typed defaults/accessors; `go test ./internal/store`; `go test . ./internal/...`; `go vet . ./internal/...`; `golangci-lint v2.12.2 run --timeout=5m` | Aggregate-specific repos beyond settings will be added with their owning phases as their domain behavior lands. |
| 0 | 0.3 Event bus + error model + logging | green | `internal/bus` topic constants + in-process pub/sub with drop-oldest slow-subscriber policy and coalescing/batching helpers; `internal/apperror` contract error codes with wrap/unwrap helpers; `internal/logging` JSON structured logger with rotating file writer; `go test ./internal/bus ./internal/logging`; `go test` for `internal/apperror` via explicit test binary workaround; `go test` for all other repo packages; `go vet . ./internal/...`; `go build ./...`; `golangci-lint v2.12.2 run --timeout=5m`; frontend ESLint, Vitest, and `tsc --noEmit`; CI run 27433708316 green on Ubuntu 24.04, Windows, macOS | Local standard Vite production build under the bundled Codex Node runtime fails before app output with `EPERM` inside Vite/Rolldown while copying public assets/creating ignored `dist` files; manual Node/PowerShell writes to the same paths succeed. The normal CI frontend build is green. |
| 0 | 0.4 Wails services skeleton + bindings | green | `internal/models` DTO/enums from `dev-docs/04-api-contracts.md`; `internal/services` Wails-bound skeleton structs for all 12 API groups; shell registers services and marshals `AppError`; `wails3 generate bindings -clean=true -ts` produced 12 services, 115 methods, 8 enums, 80 models; frontend `api/` wrappers, `useEvent` hook, zustand skeleton stores, and `AppVersion()` render path; `go test . ./internal/...`; `go vet . ./internal/...`; `go build ./...`; `golangci-lint v2.12.2 run --timeout=5m`; frontend ESLint, Vitest, and `tsc --noEmit`; CI run 27434849787 green on Ubuntu 24.04, Windows, macOS | CI binding-diff guard added and green. Local standard Vite production build still has the bundled Codex Node `EPERM` issue documented in 0.3 notes; CI normal frontend build is green. |
| 0 | 0.5 Design system base | green | Core UI tokens/components added per `dev-docs/ui/00-design-system.md`; app shell placeholders use logo/icon assets from `assets/`; Ladle component catalog added and wired into CI; frontend ESLint, `tsc --noEmit`, Vitest, `npm audit --audit-level=moderate`; `go test . ./internal/...`; `go vet . ./internal/...`; `go build . ./internal/...`; `golangci-lint run --timeout=5m` with workspace cache; CI run 27438596807 green on Ubuntu 24.04, Windows, macOS | Local Ladle build compiles 1771 modules and reaches Rollup output, then the bundled Codex Node runtime hits `EPERM` writing ignored files under `frontend/dist/ladle`; direct Node/PowerShell writes to the same directory succeed. CI catalog build is green. |
| 0 | Exit gate 0 | todo |  | Requires app launch, frontend-backend call, DB migration, green CI. |
| 1 | Docker read-only core on Linux | todo |  | Starts only after Exit gate 0 is green. |
| 2 | Compose-first projects | todo |  |  |
| 3 | Logs, stats, terminals | todo |  |  |
| 4 | Hardening I + permission flows | todo |  |  |
| 5 | Windows WSL provider | todo |  |  |
| 6 | macOS Colima provider | todo |  |  |
| 7 | Volume backup/restore engine | todo |  |  |
| 8 | Registry, updates & image lineage | todo |  |  |
| 9 | Settings, audit, notifications, cheatsheet polish | todo |  |  |
| 10 | v1 polish & release | todo |  |  |

## Current Manual Platform TODOs

No platform VM validation has been attempted yet. Per `dev-docs/06-testing.md §2`, Windows WSL and macOS Colima checklists will be emitted when their phases are reached if those VMs are unavailable in this environment.
