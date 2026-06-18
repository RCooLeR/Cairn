# v1 Release Validation

Source of truth: `dev-docs/06-testing.md`, `dev-docs/05-security.md`, and `dev-docs/08-packaging-release.md`.

## Automated release smoke

Every push to `main` runs the normal CI matrix on Ubuntu 24.04, Windows, and macOS, then package smoke for NSIS, AppImage, deb, and dmg. The Linux package-smoke leg also installs and removes the generated `.deb` on Ubuntu, verifies the installed binary/desktop file/icon, checks that Docker package dependencies and the Docker group are untouched, and repeats the `.deb` install/remove smoke inside `debian:stable-slim`. It installs Chromium for Playwright and also runs:

```powershell
./scripts/run-release-validation.ps1 -Suite checklist,manual-matrix,soak-checker,upgrade-fixtures,security,performance,soak-smoke,ui-release -SoakDuration 30s -SoakTimeout 5m
```

That release smoke covers:
- the v1 release checklist evidence ledger mirroring every normative checkbox from `dev-docs/06-testing.md section 9` with an allowed release status;
- the manual platform TODO ledger retaining every Windows/Linux/macOS condition from `dev-docs/06-testing.md section 2`;
- synthetic soak-status checker fixtures proving completed runs must be at least 24 h, active, exit 0, and within the goroutine threshold;
- release DB upgrade fixtures that open a seeded v1.0.0-rc1 database through the current migrator and verify settings defaults plus representative provider, project, service, metrics, lineage, update, backup, audit, and notification data survive;
- security policy review tests for confirmation, typed-name requirements, redaction, unencrypted TCP warnings, registry password stdin handling, update rollback, restore overwrite, and cheatsheet risk labels;
- seed-scale performance for dashboard metrics at 100 containers, 500 images, 200 volumes, 20 networks, and 10 projects;
- a short active-stream soak that opens logs, stats, terminal, and dashboard reads against a real Linux Docker daemon and checks goroutine cleanup.
- release UI browser smoke against the built Vite app with Wails runtime/service fixtures: axe scans on every route plus command palette, notification center, import modal, and every route in daemon-stopped degraded state; screenshot stability checks and committed-golden visual regression checks on every route with a 0.2 % changed-pixel ceiling; a seeded browser performance fixture at 100 containers, 500 images, 200 volumes, 20 networks, and 10 projects that checks dashboard first meaningful render, route-switch latency, inventory filter latency, and 5,000-line log virtualization.

On Windows developer machines with the dedicated `cairn-dev` distro, run the local platform smoke with:

```powershell
./scripts/run-release-validation.ps1 -Suite wsl-provider
```

This suite is intentionally not part of default CI because hosted Windows runners do not have `cairn-dev`; it preflights WSL2/systemd/Docker/Compose/Buildx, asserts the pinned Go 1.26.4 toolchain, runs the real WSL SDK connection, backup/restore, registry tag/push, and update/rebuild smokes, and fails if the Windows Docker context changes.

The Debian container package smoke can also be run after building Linux packages:

```powershell
./scripts/run-release-validation.ps1 -Suite debian-deb-container
```

This improves Debian stable package-install evidence, but it does not replace the required Debian desktop/AppImage/rootless manual rows in `docs/manual-platform-validation.md`.

The release DB upgrade fixture can be run directly with:

```powershell
./scripts/run-release-validation.ps1 -Suite upgrade-fixtures
```

The seed lives in `testdata/dbs/v1.0.0-rc1-seed.sql` and represents the v1.0.0 release-candidate schema/data shape until the first post-v1 migration fixture is added.

## 24 h soak command

Run on a Linux host with a real Docker Engine and enough free disk for transient test images:

```powershell
./scripts/run-release-validation.ps1 -Suite soak-24h -SoakDuration 24h -SoakTimeout 25h
```

Acceptance: the test logs `phase 3 soak complete`, keeps logs/stats/terminal activity recent throughout the run, runs for at least 24 h, and exits with final goroutines within the allowed threshold. If it fails, keep the goroutine profile from the test output with the release report.

Live/final checker:

```powershell
./scripts/check-soak-status.ps1 -LogPath .scratch/release-soak/phase10-24h-<stamp>.log -StatusPath .scratch/release-soak/phase10-24h-<stamp>.status
./scripts/check-soak-status.ps1 -LogPath .scratch/release-soak/phase10-24h-<stamp>.log -StatusPath .scratch/release-soak/phase10-24h-<stamp>.status -RequireComplete
```

Without `-RequireComplete`, the checker validates that the last two heartbeats are fresh and that logs, stats, terminal bytes, and dashboard reads are all increasing. With `-RequireComplete`, it validates the completion line, non-zero activity counts, exit code 0, duration >= 24 h by default, and `final_goroutines <= baseline_goroutines + 8`.

Completed run: the real WSL/Linux 24 h soak `phase10-24h-20260614T071038Z` completed successfully against runtime commit `b51f57f25c8de764da92a837ff87d1240f012ec5` using Go 1.26.4, Docker Engine 29.5.3, and Docker Compose 5.1.4 in `cairn-dev`. Final evidence is under `.scratch/release-soak/phase10-24h-20260614T071038Z.*`. `scripts/check-soak-status.ps1 -RequireComplete` validated duration `24h0m0s`, logs `1728027`, stats `86401`, terminal bytes `146870`, dashboard reads `2881`, baseline goroutines `3`, peak goroutines `35`, final goroutines `8`, and exit code `0`. The earlier `20260614T053522Z` run was marked superseded because it started before the Windows WSL Compose-env fix landed.

## Performance re-verification

Required evidence before v1.0:
- CI `Release validation smoke` green on Linux for the seed-scale backend target.
- Frontend Vitest dashboard/search performance assertions green in `frontend/src/App.test.tsx`.
- Browser-level release UI seeded fixture green for dashboard first meaningful render, page switches, inventory filtering, and 5,000-line virtualized log rendering at the v1 scale target.
- Real Docker log, stats, terminal, backup, registry auth, and tag/push integration jobs green on Ubuntu 24.04.
- Completed 24 h active-stream soak with logs, stats, terminal, dashboard reads, and final goroutines within threshold.

## Dependency Refresh Evidence

2026-06-18 local dependency refresh:
- Go modules were tidied and verified after updating Wails to `v3.0.0-alpha2.103` plus current transitive security/runtime modules.
- CI and release workflows now install the matching Wails CLI `v3.0.0-alpha2.103`; generated TypeScript bindings stayed clean with that CLI.
- Frontend package ranges were aligned with the resolved lockfile versions while keeping the locked v1 stack constraints: React 18, Tailwind 3, Vite 8, Vitest 4, and `@wailsio/runtime` `3.0.0-alpha.79`.
- `@wailsio/runtime` remains on `3.0.0-alpha.79` because that is the published runtime package consumed by the generated bindings.
- Local verification after the refresh covered `npm install`, `npm run format:check`, `npm run lint`, `npm test -- --run`, `npm run build`, `npm run audit` with the system CA store, `go mod verify`, `go test -p 1 . ./internal/... -count=1`, `go vet -unsafeptr=false . ./internal/...`, `go build . ./internal/...`, binding generation, and a Windows Wails build.

Manual tester focus after this dependency refresh:
- Launch the Windows build and verify the Settings -> About Wails version shows `v3.0.0-alpha2.103`.
- Verify provider detection against the intended WSL distro or Docker context.
- Open a project, drill into a running container, then check logs, files, and terminal.
- Exercise registry login/logout, project updates, and destructive confirmation modals with real Docker state.
- Confirm cached/error projects can be refreshed, stopped/downed when Docker has resources, or removed from Cairn when their workdir is gone.

## Visual and accessibility evidence

Automated local/CI evidence: `npm run test:release-ui` passed on Windows with 16 Playwright checks: 10 route axe scans, command palette/notification/import-modal axe scans, route screenshot stability, route visual regression against committed goldens, scroll-region reachability on overflowing routes, a daemon-stopped degraded-mode browser check that verifies every route shows the degraded banner/stale cached-data watermark with no serious axe violations, disables the container mutation, and does not start log/stats streams, plus the seeded browser performance fixture for dashboard, route-switch, inventory-search, and 5,000-line log virtualization budgets. CI run 27502520080 passed Ubuntu release validation smoke with the release UI suite before the internal-scroll capture helper was updated. On 2026-06-15, Windows and Linux visual goldens were regenerated with reviewed full internal-scroll-region capture; the Linux goldens were produced inside `mcr.microsoft.com/playwright:v1.60.0-noble` from a throwaway container-local repo copy.

Committed golden baselines live under `frontend/e2e/goldens/release-ui/` for Windows local validation and Linux/Ubuntu CI validation. To intentionally update them, run the suite with `CAIRN_UPDATE_VISUALS=1` on the target platform and review the PNG diff before committing.

## Security review checklist

- [x] No destructive/dangerous action can execute without a fresh command plan and confirmation.
- [x] Dangerous actions require typed target confirmation.
- [x] `security.confirm_destructive` remains locked on in v1.
- [x] Registry secrets are sent through stdin only and never stored by Cairn.
- [x] Audit/log/DTO redaction masks password/token/key/auth environment values.
- [x] Cairn never configures Docker TCP exposure; existing unencrypted `tcp://` contexts show a warning only.
- [x] Linux Docker permission choices remain explicit; Cairn never silently adds the user to the Docker group.
- [x] Provider lifecycle and `needs_confirmation+` actions write audit rows with redacted command details.

Security review evidence on 2026-06-14:
- `./scripts/run-release-validation.ps1 -Suite security` passed on Windows with the pinned Go 1.26.4 toolchain.
- The same focused suite passed in `cairn-dev` WSL with Linux Go 1.26.4 while the 24 h soak was running.
- CI run 27502520080 passed on Ubuntu 24.04, Windows, and macOS for commit `0c2a928`; the Ubuntu package-smoke job also passed `Release validation smoke` with the expanded security suite.
- The suite covers backend risk mapping and 10-minute plan expiry, typed-name requirements for dangerous plans, command-plan enforcement for container/project/restore/update paths, registry login via `--password-stdin`, redacted container env/audit command details, unencrypted `tcp://` context warnings, explicit Linux permission modes, provider-install audit rows, update rollback safety, restore overwrite confirmation, and cheatsheet risk-label parity.
- The focused `internal/security` command-plan package coverage is 92.4% statements after adding direct plan-construction, confirmation, expiry, context-cancel, fallback-label, and project-plan-store tests to the release security suite.
- The review found and fixed one enforcement gap: the UI already disabled `security.confirm_destructive`, but the backend settings repository now also rejects `security.confirm_destructive=false` through both typed and raw setting writes; the release security suite includes that regression check.

## Manual platform matrix

Clean Windows WSL, macOS Colima, and Debian stable VMs are not available in this environment. The unresolved manual matrix is tracked in `docs/manual-platform-validation.md` and must be closed before the v1 release checklist can be fully checked.

Local Windows WSL evidence on 2026-06-15: `./scripts/run-release-validation.ps1 -Suite wsl-provider` passed against `cairn-dev`, covering SDK connection, backup/restore, registry tag/push, update/rebuild, Go 1.26.4, and Docker Desktop context immutability. Clean Windows VM onboarding and installed-app rows remain open.

The current item-by-item `dev-docs/06-testing.md section 9` evidence ledger is `docs/v1-release-checklist.md`. It mirrors each v1 release checkbox, records whether evidence is green, in progress, or blocked by unavailable platform VMs, and names the exact remaining proof needed before v1.0. The unresolved platform matrix TODO is `docs/manual-platform-validation.md`; CI now checks that its full-matrix summary retains every Windows/Linux/macOS condition from `dev-docs/06-testing.md section 2`.

Minimum manual evidence to append here before v1.0:
- Windows 11 clean VM onboarding from WSL absent to working WSL2 Docker backend, plus uninstall cleanliness.
- Clean Windows 11 rerun of the WSL runtime smoke after fresh onboarding, excluding Docker Desktop contexts.
- macOS Apple Silicon Colima onboarding, existing-context switch, runtime smoke, and uninstall cleanliness.
- Linux native install/uninstall smoke on Ubuntu LTS and Debian stable, including Docker stopped/degraded behavior.

## Current status

Phase 10.3 packaging evidence is green: CI run 27487171104 and marker run 27487366493 passed lint/unit and package smoke on Ubuntu 24.04, Windows, and macOS. Phase 10.4 automated release smoke evidence is green in CI run 27488248685, including Ubuntu `security,performance,soak-smoke,ui-release` after package artifacts pass. CI run 27488974321 confirms the committed release UI visual goldens on the Ubuntu package-smoke release-validation path, with Windows, macOS, and Ubuntu lint/unit plus package-smoke jobs green. CI run 27489793646 confirms the backend lock for `security.confirm_destructive=false` and the updated release security suite. CI run 27491212030 is green for commit `b51f57f`, including Windows/macOS/Ubuntu lint-unit jobs, package smoke for NSIS/AppImage/deb/dmg, and Ubuntu release validation smoke. CI run 27493199996 is green for commit `968fa2a`, including Windows/macOS/Ubuntu lint-unit jobs, package smoke for NSIS/AppImage/deb/dmg, the Ubuntu Linux `.deb` install/uninstall smoke, and Ubuntu release validation smoke. CI run 27493467983 is green for commit `1e71fca`, confirming the current-head matrix after the release evidence update. CI run 27494416242 is green for commit `7a02681`, confirming the current-head matrix after the metrics shutdown-flush fix, including Ubuntu real-Docker integrations, package smoke, `.deb` install/uninstall, and release validation smoke. CI run 27495206501 is green for commit `1d724c2`, confirming the matrix after the route-complete degraded release UI coverage, including Windows/macOS/Ubuntu lint-unit jobs, package smoke for NSIS/AppImage/deb/dmg, Ubuntu `.deb` install/uninstall, and Ubuntu release validation smoke. CI run 27495650306 is green for commit `32c5742`, confirming the same matrix after the release evidence refresh. CI run 27496699180 is green for commit `5bde629`, confirming the current-head matrix after adding the seeded release UI performance smoke, including Windows/macOS/Ubuntu lint-unit jobs, package smoke for NSIS/AppImage/deb/dmg, Ubuntu `.deb` install/uninstall, and Ubuntu `security,performance,soak-smoke,ui-release` with the 15-check release UI suite. CI run 27497615590 is green for commit `18115a9`, confirming the current-head matrix after adding the v1 checklist evidence-ledger guard to Ubuntu release validation smoke. CI run 27498873562 is green for commit `bc54c78`, confirming the current-head matrix after adding the soak-status checker smoke to Ubuntu release validation. CI run 27499807123 is green for commit `6e3dc6a`, confirming the current-head matrix after adding the explicit Windows WSL provider validation harness. CI run 27500726482 is green for commit `7b150e7`, confirming the current-head matrix after adding the Debian stable container `.deb` install/remove smoke to CI and release packaging. CI run 27501620290 is green for commit `36d3abf`, confirming the current-head matrix after adding the release DB upgrade fixture to CI and release validation. CI run 27502520080 is green for commit `0c2a928`, confirming the current-head matrix after expanding command-plan security coverage. The Phase 10.4 security checklist is reviewed and green after backend-locking `security.confirm_destructive`. Local 2026-06-15 release validation is green with `./scripts/run-release-validation.ps1 -Suite checklist,manual-matrix,soak-checker,upgrade-fixtures,security,performance,ui-release,wsl-provider`, covering release evidence ledgers, DB upgrade, security, seeded performance, 16-check release UI, and real WSL SDK/backup/registry/update smokes. The real 24 h Linux soak completed successfully and passed the final soak checker. Local Windows packaging produced `bin/cairn.exe` and `bin/cairn-amd64-installer.exe`; `scripts/check-release-artifacts.ps1 -Platform windows` passed. Phase 10.4 environment-available automated evidence is complete; clean Windows/macOS/Linux desktop manual rows and production signing/notarization remain external release sign-off items.
