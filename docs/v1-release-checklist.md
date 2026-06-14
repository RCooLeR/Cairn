# v1 Release Checklist Evidence

Source of truth: `dev-docs/06-testing.md section 9`. This file is a release evidence ledger, not a replacement for the normative checklist.

Status legend:
- `green`: current evidence is sufficient for the checklist item.
- `in_progress`: implementation exists, but required release evidence is incomplete.
- `blocked_by_platform`: implementation exists, but the remaining proof requires a platform VM or manual installed-app run that is not available in this environment.

| Checklist item | Status | Current evidence | Remaining proof before v1.0 |
|---|---:|---|---|
| All P0 features pass on Linux native | in_progress | Ubuntu 24.04 CI run 27495206501 is green for commit `1d724c2`, including lint/unit, real Docker reconnect/logs/metrics/terminal/backup/registry/tag-push integrations, package smoke, `.deb` install/uninstall, and release validation smoke. | Finish the 24 h Linux soak and close the Ubuntu installed-app desktop, AppImage, permission, rootless, and Debian stable rows in `docs/manual-platform-validation.md`. |
| All P0 features pass on Windows WSL | blocked_by_platform | Local dedicated `cairn-dev` WSL evidence covers provider setup, Docker SDK over WSL stdio, backup/restore, registry tag/push, and update/rebuild smokes without Docker Desktop contexts. Windows CI run 27495206501 is green for unit/frontend/package smoke. | Clean Windows 11 VM onboarding from WSL absent/partial states, full WSL provider integration suite, installed app smoke, and uninstall cleanliness. |
| All P0 features pass on macOS (Colima + existing context) | blocked_by_platform | macOS CI run 27495206501 is green for unit/frontend/package smoke and dmg/app artifact checks. Colima/existing-context behavior is covered by parser, provider, service, and frontend fixture tests. | Apple Silicon Colima VM onboarding/runtime/update/rebuild/tag-push smoke, existing Docker Desktop/OrbStack context switch, host/backend terminal smoke, and uninstall cleanliness. |
| Update + lineage + registry-account test suite green (section 5 all 14 cases) | green | Phase 8 exit evidence records all 14 normative cases across registry, lineage, updates, Docker, store, and frontend tests; Ubuntu CI includes real registry auth and tag/push integrations; Windows WSL `cairn-dev` registry and update/rebuild smokes are green. | macOS Colima runtime repeat remains a platform-matrix row, but the normative automated suite is green. |
| Visual-regression and axe accessibility suites green | green | Release UI smoke runs axe on every route plus command palette, notification center, import modal, and daemon-stopped degraded states; screenshots are compared against committed Windows/Linux goldens with a 0.2 percent pixel threshold. CI run 27495206501 passed Ubuntu release validation smoke, and local WSL Docker Playwright passed the current 14-check route-complete suite. | Repeat intentional golden updates only with `CAIRN_UPDATE_VISUALS=1` and reviewed PNG diffs. |
| No destructive action without confirmation (section 6 suite green) | green | `scripts/run-release-validation.ps1 -Suite security` covers command-plan enforcement, typed-name requirements, restore/update/container/project paths, and backend locking of `security.confirm_destructive`. Ubuntu release validation smoke passed in CI run 27495206501. | None currently known. |
| No plaintext secrets anywhere (redaction suite green) | green | Security suite covers registry login via `--password-stdin`, audit/log/DTO redaction, auth env masking, and no Cairn persistence of registry secrets. | None currently known. |
| No Docker TCP exposure configured by Cairn | green | Security review verifies Cairn never configures Docker TCP exposure and only warns on existing unencrypted `tcp://` contexts. | Manual context matrix should verify warning visibility on a real unencrypted remote context. |
| Performance targets met at seeded scale (section 7) | in_progress | Release validation smoke covers seed-scale backend performance; frontend tests cover route/search/dashboard behavior; Ubuntu real Docker stream integrations are green. | Manual UI observation during the 24 h soak still needs to record that log viewer responsiveness and dashboard page switches remain acceptable. |
| Installers install/uninstall cleanly on all 3 OS | blocked_by_platform | CI run 27495206501 package smoke is green for NSIS, AppImage, `.deb`, dmg, and macOS app artifact checks. Ubuntu `.deb` install/uninstall smoke verifies binary, desktop file, icon, no Docker package deps/group mutation, and clean removal. | Installed-app smoke and uninstall cleanliness on clean Windows 11, macOS, Ubuntu desktop, Debian stable, and AppImage launch on clean Linux. |
| App handles Docker-stopped state gracefully on all pages | in_progress | Browser release smoke includes daemon-stopped degraded fixture across every release route: global degraded banner, stale cached-data watermark, no serious axe violations, disabled container mutation, and no log/stats stream startup. CI release validation smoke passed on Ubuntu in run 27495206501; local Windows `npm run test:release-ui` and local WSL Docker Playwright both passed the 14-check route-complete degraded suite. | Manual installed-app Docker-stopped/degraded pass on Ubuntu desktop, Debian, Windows WSL, and macOS Colima/existing context. |
| Crash-free soak: 24 h run with active streams, zero goroutine leaks (pprof check) | in_progress | Real WSL/Linux 24 h soak started `20260614T071038Z`; latest checked heartbeat remains active with logs/stats/terminal/dashboard counters increasing and goroutines steady at 26. | Wait for `phase 3 soak complete`, exit code 0, and final goroutine count within threshold; retain pprof output if it fails. |

## Current blockers

- 24 h Linux soak completion.
- Clean Windows 11 WSL VM matrix and installed-app smoke.
- macOS Apple Silicon Colima/existing-context VM matrix and installed-app smoke.
- Debian stable install/AppImage/degraded/rootless matrix.
- Manual installed-app Docker-stopped behavior on real desktop apps.
