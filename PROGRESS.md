# Cairn v1.0 Progress

Source of truth: `dev-docs/`, especially `dev-docs/07-development-plan.md`.

Status legend: `todo`, `in_progress`, `green`, `blocked`.

| Phase | Step | Status | Evidence | Notes |
|---|---|---:|---|---|
| 0 | 0.0 Dev environment (Windows machines) | blocked | `wsl -l -v` reports no installed WSL distributions; see `docs/manual-platform-validation.md` | All local Windows Docker work must target `cairn-dev`, never Docker Desktop or `desktop-linux`. No Docker integration tests run against the visible Docker Desktop context. |
| 0 | 0.1 Repo & toolchain | in_progress | `npm audit --audit-level=moderate`; ESLint; Vitest; `tsc --noEmit`; Vite production build; `go test . ./internal/...`; `go vet . ./internal/...`; `go build ./...`; `wails3 build` | Wails v3 pinned to `v3.0.0-alpha.99`; Go pinned to `toolchain go1.26.4`; logo/icon sourced from `assets/`. Pending: `wails3 dev` hot-reload proof and live CI run. Browser plugin unavailable in this sandbox (`CreateProcessAsUserW failed: 5`). |
| 0 | 0.2 SQLite store + migrations | todo |  |  |
| 0 | 0.3 Event bus + error model + logging | todo |  |  |
| 0 | 0.4 Wails services skeleton + bindings | todo |  |  |
| 0 | 0.5 Design system base | todo |  |  |
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
