# v1 Release Validation

Source of truth: `dev-docs/06-testing.md`, `dev-docs/05-security.md`, and `dev-docs/08-packaging-release.md`.

## Automated release smoke

Every push to `main` runs the normal CI matrix on Ubuntu 24.04, Windows, and macOS, then package smoke for NSIS, AppImage, deb, and dmg. The Linux package-smoke leg installs Chromium for Playwright and also runs:

```powershell
./scripts/run-release-validation.ps1 -Suite security,performance,soak-smoke,ui-release -SoakDuration 30s -SoakTimeout 5m
```

That release smoke covers:
- security policy review tests for confirmation, typed-name requirements, redaction, unencrypted TCP warnings, registry password stdin handling, update rollback, restore overwrite, and cheatsheet risk labels;
- seed-scale performance for dashboard metrics at 100 containers, 500 images, 200 volumes, 20 networks, and 10 projects;
- a short active-stream soak that opens logs, stats, terminal, and dashboard reads against a real Linux Docker daemon and checks goroutine cleanup.
- release UI browser smoke against the built Vite app with Wails runtime/service fixtures: axe scans on every route plus command palette, notification center, and import modal states; screenshot stability checks on every route with a 0.2 % changed-pixel ceiling.

## 24 h soak command

Run on a Linux host with a real Docker Engine and enough free disk for transient test images:

```powershell
./scripts/run-release-validation.ps1 -Suite soak-24h -SoakDuration 24h -SoakTimeout 25h
```

Acceptance: the test logs `phase 3 soak complete`, keeps logs/stats/terminal activity recent throughout the run, and exits with final goroutines within the allowed threshold. If it fails, keep the goroutine profile from the test output with the release report.

## Performance re-verification

Required evidence before v1.0:
- CI `Release validation smoke` green on Linux for the seed-scale backend target.
- Frontend Vitest dashboard/search performance assertions green in `frontend/src/App.test.tsx`.
- Real Docker log, stats, terminal, backup, registry auth, and tag/push integration jobs green on Ubuntu 24.04.
- Manual UI observation during soak: log viewer remains responsive and dashboard page switches do not visibly stall.

## Visual and accessibility evidence

Automated local evidence: `./scripts/run-release-validation.ps1 -Suite ui-release` passed on Windows with 12 Playwright checks: 10 route axe scans, command palette/notification/import-modal axe scans, and route screenshot stability.

Remaining release evidence: committed golden screenshot baselines for the final Linux release host must still be recorded before checking the `Visual-regression and axe accessibility suites green` item in `dev-docs/06-testing.md §9`.

## Security review checklist

- [ ] No destructive/dangerous action can execute without a fresh command plan and confirmation.
- [ ] Dangerous actions require typed target confirmation.
- [ ] `security.confirm_destructive` remains locked on in v1.
- [ ] Registry secrets are sent through stdin only and never stored by Cairn.
- [ ] Audit/log/DTO redaction masks password/token/key/auth environment values.
- [ ] Cairn never configures Docker TCP exposure; existing unencrypted `tcp://` contexts show a warning only.
- [ ] Linux Docker permission choices remain explicit; Cairn never silently adds the user to the Docker group.
- [ ] Provider lifecycle and `needs_confirmation+` actions write audit rows with redacted command details.

## Manual platform matrix

Clean Windows WSL and macOS Colima VMs are not available in this environment. The unresolved manual matrix is tracked in `docs/manual-platform-validation.md` and must be closed before the v1 release checklist can be fully checked.

Minimum manual evidence to append here before v1.0:
- Windows 11 clean VM onboarding from WSL absent to working WSL2 Docker backend, plus uninstall cleanliness.
- Windows 11 existing WSL/backend runtime smoke against `cairn-dev`, excluding Docker Desktop contexts.
- macOS Apple Silicon Colima onboarding, existing-context switch, runtime smoke, and uninstall cleanliness.
- Linux native install/uninstall smoke on Ubuntu LTS and Debian stable, including Docker stopped/degraded behavior.

## Current status

Phase 10.3 packaging evidence is green: CI run 27487171104 and marker run 27487366493 passed lint/unit and package smoke on Ubuntu 24.04, Windows, and macOS. Phase 10.4 remains open until the 24 h soak, committed visual goldens, and manual platform rows above are recorded.
