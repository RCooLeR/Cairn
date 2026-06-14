# v1 Release Validation

Source of truth: `dev-docs/06-testing.md`, `dev-docs/05-security.md`, and `dev-docs/08-packaging-release.md`.

## Automated release smoke

Every push to `main` runs the normal CI matrix on Ubuntu 24.04, Windows, and macOS, then package smoke for NSIS, AppImage, deb, and dmg. The Linux package-smoke leg also installs and removes the generated `.deb`, verifies the installed binary/desktop file/icon, and checks that Docker package dependencies and the Docker group are untouched. It installs Chromium for Playwright and also runs:

```powershell
./scripts/run-release-validation.ps1 -Suite security,performance,soak-smoke,ui-release -SoakDuration 30s -SoakTimeout 5m
```

That release smoke covers:
- security policy review tests for confirmation, typed-name requirements, redaction, unencrypted TCP warnings, registry password stdin handling, update rollback, restore overwrite, and cheatsheet risk labels;
- seed-scale performance for dashboard metrics at 100 containers, 500 images, 200 volumes, 20 networks, and 10 projects;
- a short active-stream soak that opens logs, stats, terminal, and dashboard reads against a real Linux Docker daemon and checks goroutine cleanup.
- release UI browser smoke against the built Vite app with Wails runtime/service fixtures: axe scans on every route plus command palette, notification center, import modal, and every route in daemon-stopped degraded state; screenshot stability checks and committed-golden visual regression checks on every route with a 0.2 % changed-pixel ceiling.

## 24 h soak command

Run on a Linux host with a real Docker Engine and enough free disk for transient test images:

```powershell
./scripts/run-release-validation.ps1 -Suite soak-24h -SoakDuration 24h -SoakTimeout 25h
```

Acceptance: the test logs `phase 3 soak complete`, keeps logs/stats/terminal activity recent throughout the run, and exits with final goroutines within the allowed threshold. If it fails, keep the goroutine profile from the test output with the release report.

Current run: a real WSL/Linux 24 h soak is in progress from `20260614T071038Z` against runtime commit `b51f57f25c8de764da92a837ff87d1240f012ec5` using Go 1.26.4, Docker Engine 29.5.3, and Docker Compose 5.1.4 in `cairn-dev`. Live evidence is under `.scratch/release-soak/phase10-24h-20260614T071038Z.*`; this does not satisfy the checklist until the log reaches `phase 3 soak complete` and exits with code 0. The earlier `20260614T053522Z` run was marked superseded because it started before the Windows WSL Compose-env fix landed.

## Performance re-verification

Required evidence before v1.0:
- CI `Release validation smoke` green on Linux for the seed-scale backend target.
- Frontend Vitest dashboard/search performance assertions green in `frontend/src/App.test.tsx`.
- Real Docker log, stats, terminal, backup, registry auth, and tag/push integration jobs green on Ubuntu 24.04.
- Manual UI observation during soak: log viewer remains responsive and dashboard page switches do not visibly stall.

## Visual and accessibility evidence

Automated local evidence: `npm run test:release-ui` passed on Windows with 14 Playwright checks: 10 route axe scans, command palette/notification/import-modal axe scans, route screenshot stability, route visual regression against committed goldens, and a daemon-stopped degraded-mode browser check that verifies every route shows the degraded banner/stale cached-data watermark with no serious axe violations, disables the container mutation, and does not start log/stats streams.

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
- CI run 27489793646 passed on Ubuntu 24.04, Windows, and macOS for commit `84f3293`; the Ubuntu package-smoke job also passed `Release validation smoke` with the updated security suite.
- The suite covers backend risk mapping and 10-minute plan expiry, typed-name requirements for dangerous plans, command-plan enforcement for container/project/restore/update paths, registry login via `--password-stdin`, redacted container env/audit command details, unencrypted `tcp://` context warnings, explicit Linux permission modes, provider-install audit rows, update rollback safety, restore overwrite confirmation, and cheatsheet risk-label parity.
- The review found and fixed one enforcement gap: the UI already disabled `security.confirm_destructive`, but the backend settings repository now also rejects `security.confirm_destructive=false` through both typed and raw setting writes; the release security suite includes that regression check.

## Manual platform matrix

Clean Windows WSL, macOS Colima, and Debian stable VMs are not available in this environment. The unresolved manual matrix is tracked in `docs/manual-platform-validation.md` and must be closed before the v1 release checklist can be fully checked.

The current item-by-item `dev-docs/06-testing.md section 9` evidence ledger is `docs/v1-release-checklist.md`. It mirrors each v1 release checkbox, records whether evidence is green, in progress, or blocked by unavailable platform VMs, and names the exact remaining proof needed before v1.0.

Minimum manual evidence to append here before v1.0:
- Windows 11 clean VM onboarding from WSL absent to working WSL2 Docker backend, plus uninstall cleanliness.
- Clean Windows 11 rerun of the WSL runtime smoke after fresh onboarding, excluding Docker Desktop contexts.
- macOS Apple Silicon Colima onboarding, existing-context switch, runtime smoke, and uninstall cleanliness.
- Linux native install/uninstall smoke on Ubuntu LTS and Debian stable, including Docker stopped/degraded behavior.

## Current status

Phase 10.3 packaging evidence is green: CI run 27487171104 and marker run 27487366493 passed lint/unit and package smoke on Ubuntu 24.04, Windows, and macOS. Phase 10.4 automated release smoke evidence is green in CI run 27488248685, including Ubuntu `security,performance,soak-smoke,ui-release` after package artifacts pass. CI run 27488974321 confirms the committed release UI visual goldens on the Ubuntu package-smoke release-validation path, with Windows, macOS, and Ubuntu lint/unit plus package-smoke jobs green. CI run 27489793646 confirms the backend lock for `security.confirm_destructive=false` and the updated release security suite. CI run 27491212030 is green for commit `b51f57f`, including Windows/macOS/Ubuntu lint-unit jobs, package smoke for NSIS/AppImage/deb/dmg, and Ubuntu release validation smoke. CI run 27493199996 is green for commit `968fa2a`, including Windows/macOS/Ubuntu lint-unit jobs, package smoke for NSIS/AppImage/deb/dmg, the Ubuntu Linux `.deb` install/uninstall smoke, and Ubuntu release validation smoke. CI run 27493467983 is green for commit `1e71fca`, confirming the current-head matrix after the release evidence update. CI run 27494416242 is green for commit `7a02681`, confirming the current-head matrix after the metrics shutdown-flush fix, including Ubuntu real-Docker integrations, package smoke, `.deb` install/uninstall, and release validation smoke. The Phase 10.4 security checklist is reviewed and green after backend-locking `security.confirm_destructive`. Windows WSL registry/tag-push smoke is green against `cairn-dev` through the real provider path with Docker Desktop contexts untouched. Windows WSL update/rebuild smoke is green against `cairn-dev` with `CAIRN_REAL_WSL_DOCKER_UPDATES=1 go test -tags=wslintegration ./internal/updates -run TestManagerRealWSLUpdateAndRebuildSmoke -count=1 -timeout=9m -v`, covering pull/recreate, rebuild with `build --pull`, `up -d`, health watch, and rollback-history image IDs. A real 24 h Linux soak is currently running in `cairn-dev`; Phase 10.4 remains open until that soak completes successfully and the manual platform rows above are recorded.
