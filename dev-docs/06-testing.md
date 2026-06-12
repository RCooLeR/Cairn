# 06 — Test Plan & Quality Strategy

## 1. Test pyramid

| Level | Scope | Tooling | Runs |
|---|---|---|---|
| Unit (Go) | parsers, planners, store, redaction, digest compare, path mapping | `go test`, table-driven | every commit |
| Unit (TS) | components, stores, formatting, badge logic | Vitest + React Testing Library | every commit |
| Contract | services ↔ bindings: golden JSON for every DTO/event | generated-bindings diff check | every commit |
| Docker integration | real daemon: object ops, streams, compose lifecycle, update flows | `go test -tags=integration` against dockerd in CI (Linux) | every PR |
| Provider integration | WSL / Colima / install flows | scripted manual + VM jobs | per phase exit |
| E2E UI | user journeys against seeded daemon | Playwright driving built app (Wails dev server) | nightly + release |
| Visual regression | per-page screenshots (dark+light, default & degraded states) diffed against goldens | Playwright screenshots + pixel diff (0.2 % threshold) | nightly + release |
| Accessibility | automated axe-core scan on every route + modal | @axe-core/playwright; zero serious/critical violations | nightly |
| Upgrade path | open previous-release DB (seeded fixture per release) with new build → migrations succeed, data intact | fixture DBs in `testdata/dbs/` | release |

Coverage gates: domain core ≥ 80 % lines; parsers/planners (lineage, updates, Dockerfile) ≥ 95 % branches.

## 2. Platform matrix

**Windows:** Win 11 x64 · WSL present/absent · Ubuntu present/absent/multiple · Docker in Ubuntu present/absent · systemd on/off · coexistence with Docker Desktop installed (its `docker-desktop*` distros excluded from detection; `desktop-linux` context untouched). Developer machines run all Windows integration tests against the dedicated **`cairn-dev`** distro (README "Development environment"), never against Docker Desktop.
**Linux:** Ubuntu LTS, Debian stable · Docker present/absent · user in/not in docker group · service stopped · rootless.
**macOS:** Apple Silicon (Intel best-effort) · Homebrew present/absent · Colima present/absent · existing Docker Desktop context · remote context.

## 3. Core acceptance tests (P0 gate)

ping/version/compose-version load; containers/images/volumes/networks lists load and reconcile with `docker ps -a` etc.; stats stream live; logs stream live; container terminal opens/resizes/closes; compose label grouping correct; run-image wizard creates a working container (ports/env/volumes verified via inspect); volume/network creation; rename; image save/load `.tar` round-trip.

## 4. Sample Compose projects (`testdata/projects/`)

| Project | Purpose |
|---|---|
| `single-web` | 1 service, nginx, published port |
| `app-db` | app + postgres, named volume, depends_on, healthcheck on db |
| `proxy-app` | reverse proxy + app, custom network, aliases |
| `healthcheck-fail` | service whose healthcheck fails after N seconds (update-failure tests) |
| `env-project` | `.env` + `env_file`, variable interpolation |
| `build-simple` | `build:` context, single-stage Dockerfile `FROM nginx:alpine` |
| `build-multistage` | builder+runtime stages, `build.target`, ARG in FROM |
| `mixed-updates` | 2 image services + 2 built services (mixed update plan tests) |
| `big-logs` | service emitting ≥ 5 000 lines/s (perf tests) |
| `many-services` | 12 services (grouping/aggregate stats) |

Each project ships with a `expected.json` (services, images, lineage expectations) consumed by integration tests.

## 5. Update & lineage tests (normative cases)

1. **Versioned tag update:** local digest ≠ mocked remote digest → `service_image_update_available`; plan = `pull` + `up -d`.
2. **latest tag:** update detected + mutable-tag warning shown.
3. **Pinned digest:** `nginx@sha256:…` → `pinned_digest`, never an update row.
4. **Private registry:** 401 → `auth_required`, no error modal.
5. **Registry unreachable / 429:** `error` / `rate_limited`, retry/backoff respected.
6. **Local build base update:** `build-simple`; record build-time base digest; mock newer remote → `base_image_update_available` / `rebuild_required`; plan = `build --pull` + `up -d`.
7. **Multi-stage:** all FROMs discovered; final-stage base flagged; builder vs runtime updates shown separately; `build.target` selects correct stage; `ARG`-in-FROM resolved from compose build args or marked low confidence.
8. **Unknown base:** `postgres:16` w/o metadata → service-image status only; UI says base unknown, never guesses.
9. **Mixed project plan:** ordered `pull a b` → `build --pull c d` → `up -d a b c d`.
10. **Health success / failure / rollback:** apply update on `healthcheck-fail` → failed → auto-rollback when old image present; `rolled_back` recorded; manual guidance when image absent.
11. **Ignore/unignore** round-trip excludes/includes rows and badges.

Registry mocking: a local OCI registry container (`registry:2`) plus an httptest manifest server for digest/429/401 simulation — no network dependency in CI.

### Registry accounts & publish tests

12. **Login/logout:** local `registry:2` with htpasswd auth → Login via RegistryService → account listed, TestAuth verified → private manifest check succeeds (no `auth_required`) → Logout → `auth_required` returns. Secret never appears in DB/audit/logs (redaction assert).
13. **Tag & push:** tag local image to `localhost:5000/test/app:1.0` → push not-logged-in → `E_REGISTRY_AUTH` with registry name → login → push succeeds with layer progress → pull-back digest matches.
14. **Plaintext-store warning:** backend without credential helper → login flow shows unencrypted-storage warning; account row flagged.

## 6. Safety tests

Confirmations enforced for every `destructive|dangerous` op (attempt apply without plan → `E_CONFIRMATION_REQUIRED`); typed-name required for volume delete, volume prune, `down --volumes`, system prune, restore-overwrite, backend reset. Audit entries exist for all confirmed actions and provider lifecycle ops. Redaction test corpus (passwords in args/env) must never appear in DB.

## 7. Performance tests

Seed: 100 containers, 500 images, 200 volumes, 20 networks, 10 projects. Targets:
- Dashboard first meaningful render < 1.5 s after daemon ping; page switches < 200 ms.
- Log viewer: ≥ 5 000 lines/s sustained, UI ≥ 30 fps, memory bounded (ring buffer ≤ 50 000 lines).
- Stats: 100 concurrent container streams coalesced; main-thread CPU of frontend < 30 %.
- Search/filter latency < 100 ms at seeded scale.
- SQLite: metrics table bounded by retention (verify vacuum), DB < 200 MB steady state.

## 8. E2E journeys (Playwright)

1. Fresh start → onboarding → existing-context connect → dashboard live.
2. Import `app-db` → start project → see services healthy → open logs → open db terminal → stop project.
3. Update journey: seed old nginx digest → check updates → badge → plan modal shows commands → apply with health watch → history row.
4. Rebuild journey on `build-simple` (mocked base digest).
5. Danger journey: delete named volume → typed-name modal → audit entry.
6. Provider failure: stop dockerd → banner + repair hints → start → auto-recover.

## 9. v1 release checklist

```text
□ All P0 features pass on Linux native
□ All P0 features pass on Windows WSL
□ All P0 features pass on macOS (Colima + existing context)
□ Update + lineage + registry-account test suite green (§5 all 14 cases)
□ Visual-regression and axe accessibility suites green
□ No destructive action without confirmation (§6 suite green)
□ No plaintext secrets anywhere (redaction suite green)
□ No Docker TCP exposure configured by Cairn
□ Performance targets met at seeded scale (§7)
□ Installers install/uninstall cleanly on all 3 OS
□ App handles Docker-stopped state gracefully on all pages
□ Crash-free soak: 24 h run with active streams, zero goroutine leaks (pprof check)
```
