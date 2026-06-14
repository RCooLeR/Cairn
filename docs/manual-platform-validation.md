# Manual Platform Validation TODO

Source: `dev-docs/06-testing.md section 2` and `dev-docs/README.md` "Development environment (Windows)".

## Phase 0.0 Windows Development Distro

Current evidence: `wsl -l -v` shows `cairn-dev` running as WSL2 alongside Docker Desktop's internal distro; `wsl -d cairn-dev -- test -d /run/systemd/system` passes; `docker info` inside `cairn-dev` reports Engine 29.5.3; Compose 5.1.4 and Buildx 0.34.1 are installed; `/usr/bin/docker` resolves to `/usr/bin/docker` rather than `/mnt/wsl/docker-desktop/...`; Windows `docker context ls` still shows `default` and `desktop-linux` contexts untouched.

- [x] Install the dedicated WSL distro: `wsl --install Ubuntu-26.04 --name cairn-dev`.
- [x] Enable systemd inside `cairn-dev` via `/etc/wsl.conf`, then run `wsl --shutdown`.
- [x] Install official Docker Engine, Compose plugin, and Buildx inside `cairn-dev`.
- [x] Add the WSL user to the `docker` group and re-login.
- [x] Disable Docker Desktop WSL integration for `cairn-dev`.
- [x] Verify `wsl -d cairn-dev -- docker info`.
- [x] Verify Windows `docker context ls` still shows Docker Desktop contexts untouched.
- [ ] Set development settings `windows.wsl_distro = cairn-dev` before Windows provider integration tests.

## Phase 0.1 Wails Dev Hot Reload

Current evidence: `wails3 dev -config ./build/config.yml -port 9250 -nocolour` built the dev binary, ran `bin/cairn.exe`, started Vite on `127.0.0.1:9250`, and the Wails/WebView2 app connected to the frontend dev server. The in-app Browser connector is unavailable in this sandbox (`CreateProcessAsUserW failed: 5`), so the proof used Wails/Vite logs and a live module fetch.

- [x] Run `wails3 dev -config ./build/config.yml -port 9250`.
- [x] Edit a React source file and verify Vite HMR: temporary `frontend/src/App.tsx` probe was served by the live dev server and logged `hmr update /src/App.tsx`.
- [x] Edit a Go source file and verify the Wails dev watcher rebuilds/restarts the backend side: temporary `zz_wails_hot_reload_probe.go` produced a second Wails `Build` marker and regenerated bindings.
- [x] Confirm the window/taskbar icon still uses `assets/cairn-icon.png` and the shell still uses `assets/cairn-logo.png`.

## Phase 5.1 Windows WSL Provider Matrix

Current evidence: parser/unit tests cover UTF-16LE `wsl.exe -l -v` output, Docker Desktop distro exclusion, configured custom distro selection, WSL missing, no Ubuntu distro, WSL1 distro, systemd off, Docker Desktop integration conflict, WSL command execution, shell selection, and 23 host/backend path mapping cases including drive paths, UNC, spaces, and unicode.

- [x] Validate on the local Windows host with dedicated `cairn-dev`: WSL2, systemd, Docker Engine, Compose, Buildx, daemon ping, and Docker Desktop integration exclusion.
- [ ] Clean Win11 VM: WSL absent.
- [ ] Clean Win11 VM: WSL present, Ubuntu absent.
- [ ] Clean Win11 VM: multiple Ubuntu/custom distros, `windows.wsl_distro` selects the intended distro.
- [ ] Clean Win11 VM: selected distro is WSL1 and reports `WSL2_REQUIRED`.
- [ ] Clean Win11 VM: selected Ubuntu has systemd off and reports `SYSTEMD_OFF`.
- [ ] Clean Win11 VM: selected Ubuntu has Docker missing and reports `DOCKER_MISSING`.
- [ ] Clean Win11 VM: Docker Desktop WSL integration enabled for selected distro and reports `DESKTOP_INTEGRATION_CONFLICT`.

## Phase 5.2 Windows WSL Install Flow

Current evidence: unit tests cover the generated WSL install command plan, custom distro naming with `--name cairn-dev`, step execution progress, `provider:install:progress` final `totalSteps`, provider-install audit logging, plan expiry after completion, and a simulated Docker apt/network failure returning `E_PROVIDER_NOT_READY` with repair hints. Local `cairn-dev` is already installed, so the destructive clean-machine installer was not run here. The non-destructive final verification was run inside `cairn-dev`: systemd present, Docker Engine 29.5.3, Compose 5.1.4, Buildx 0.34.1, and `docker run --rm hello-world` succeeded; the throwaway `hello-world:latest` image was removed afterward.

- [x] Validate final Docker/Compose/Buildx/hello-world verification inside the local `cairn-dev` WSL distro.
- [x] Verify the install plan uses the official Docker apt repository packages: `docker-ce`, `docker-ce-cli`, `containerd.io`, `docker-buildx-plugin`, and `docker-compose-plugin`.
- [x] Verify the install plan runs privileged Ubuntu setup through WSL root execution and handles docker-group membership with an explicit WSL distro restart.
- [x] Verify simulated no-network/apt failure returns `E_PROVIDER_NOT_READY` with repair hints.
- [ ] Clean Win11 VM: WSL absent -> install plan enables WSL and gives reboot/resume guidance if Windows requires a restart.
- [ ] Clean Win11 VM: WSL present, Ubuntu absent -> install plan installs Ubuntu and reaches a working Docker daemon.
- [ ] Clean Win11 VM: custom distro name -> install plan uses a valid Ubuntu distribution source plus `--name <custom>`.
- [ ] Clean Win11 VM: clean Ubuntu initialized, Docker absent -> install plan adds Docker apt repo, installs packages, enables systemd/service, adds docker group, restarts WSL, and verifies `hello-world`.
- [ ] Clean Win11 VM failure injection: no network during apt/GPG/repository setup -> failed step shows output and repair hints.
- [ ] Clean Win11 VM failure injection: systemd cannot start after `/etc/wsl.conf` update -> `SYSTEMD_OFF`/service-start guidance is shown.
- [ ] Clean Win11 VM failure injection: no initialized non-root Ubuntu user -> docker-group step fails with a repair hint to finish first-run user setup.

## Phase 5.3 Windows WSL Docker Connection

Current evidence: `internal/docker.Client` now accepts provider-supplied Docker SDK dialers, and `WindowsWSLProvider` keeps the SDK host as `unix:///var/run/docker.sock` while routing the transport through `wsl.exe -d <distro> -- docker system dial-stdio`. If the Docker CLI lacks `dial-stdio`, the provider falls back to `wsl.exe -d <distro> -- socat UNIX-CONNECT:/var/run/docker.sock -`; if neither transport exists it returns `E_PROVIDER_NOT_READY` with repair hints. Local `CAIRN_REAL_WSL_DOCKER=1 go test ./internal/docker -run TestWindowsWSLDockerConnection -count=1 -v` passed against the dedicated `cairn-dev` distro, covering Connect/Ping/Info/Version/ListContainers through the WSL stdio path. A frontend test verifies that `/mnt/...` import paths show the performance warning.

- [x] Verify the Docker SDK connection uses WSL stdio, not localhost TCP, npipe, Docker Desktop, or `desktop-linux`.
- [x] Verify `docker system dial-stdio` is preferred when available.
- [x] Verify `socat UNIX-CONNECT:/var/run/docker.sock -` is the fallback when `dial-stdio` is unavailable.
- [x] Verify missing stdio transports return `E_PROVIDER_NOT_READY` with repair hints.
- [x] Run the real local connection test against `cairn-dev`.
- [x] Verify `/mnt/...` import paths show the WSL mount performance warning.
- [ ] Clean Win11 VM: rerun the full Phase 1-3 Docker integration suite through the Windows WSL provider.
- [ ] Clean Win11 VM: import a path-heavy Compose project under `/mnt/c` and verify the warning appears in the running desktop app.

## Phase 5.4 Windows UX

Current evidence: frontend tests cover the Windows setup modal path from no-provider state through WSL checks, CommandPlan preview, install start, `provider:install:progress`, and verify summary. Settings tests cover the Providers-focused Windows WSL section, `windows.wsl_distro` save, `provider.autostart_backend`, and path-mapping panel. Backend provider-manager tests verify `windows.wsl_distro` is applied before WSL detection. Local gates passed: `go test . ./internal/...`, `go vet . ./internal/...`, `go build . ./internal/...`, `golangci-lint v2.12.2 run --timeout=5m`, frontend ESLint, Vitest, Vite dev build, and audit.

- [x] Render the Windows onboarding branch with Ubuntu on WSL2 recommended, Existing Docker context, and disabled Remote host option.
- [x] Render WSL setup checks with repair hints and WSL path-performance guidance.
- [x] Preview the WSL install CommandPlan and consume `provider:install:progress` events in the setup flow.
- [x] Provide WSL settings for distro selection, path mapping, and start-Docker-on-launch.
- [x] Apply `windows.wsl_distro` to WSL provider detection and install planning.
- [ ] Windows runner/VM E2E smoke: first-launch no-provider flow reaches WSL setup checks and install-plan preview.
- [ ] Windows runner/VM E2E smoke: Settings -> Providers updates `windows.wsl_distro`, reruns detection, and preserves Docker Desktop contexts.
- [ ] Fresh clean Win11 manual checklist: run onboarding from WSL absent through working `cairn-dev` Docker backend, including reboot/resume guidance if Windows requires restart.

## Phase 6.1 macOS Colima Provider

Current evidence: parser/unit tests cover `colima status --json`, `docker context ls --format json`, healthy Colima detection, Docker/Colima/context failure mapping, Homebrew-required install guidance, Colima install CommandPlan preview/execution/progress, context-scoped Compose execution with workdir/env passthrough, lifecycle commands, zsh host shell, and `colima ssh` backend shell. Local gates passed: `go test ./internal/providers`, `go test . ./internal/...`, `go vet . ./internal/...`, `go build . ./internal/...`, `golangci-lint v2.12.2 run --timeout=5m`, frontend ESLint, Vitest, TypeScript, and audit. A Colima-capable macOS VM is unavailable in this environment, so the macOS runtime rows below remain manual TODOs.

- [x] Unit-test `colima status --json` parsing.
- [x] Unit-test Docker context list parsing, including line-delimited JSON and array fixtures.
- [x] Unit-test healthy Colima detection with Docker CLI, Compose, Buildx, context, daemon ping, and socket host.
- [x] Unit-test Homebrew-missing install guidance without silently scripting the curl-pipe installer.
- [x] Unit-test `colima start|stop|restart -p <profile>` lifecycle and Colima terminal commands.
- [ ] macOS Apple Silicon VM: Homebrew absent -> setup shows Homebrew guidance and command preview only.
- [ ] macOS Apple Silicon VM: Homebrew present, Colima absent -> install plan installs Docker CLI, Docker Compose, and Colima, starts the configured profile, selects context, and verifies hello-world.
- [ ] macOS Apple Silicon VM: Colima installed but stopped -> detection reports `COLIMA_STOPPED`, Start reaches healthy.
- [ ] macOS Apple Silicon VM: custom profile/resource settings -> `colima start -p <profile> --cpu N --memory N --disk N` and context `colima-<profile>` are used.
- [ ] macOS Apple Silicon VM: rerun the Phase 1-3 Docker integration suite against Colima.
- [ ] macOS Intel best-effort: repeat healthy detection/start/stop and project import.

## Phase 6.2 Existing Docker Context Provider

Current evidence: unit tests cover existing-context healthy detection, missing-context failure mapping, unencrypted `tcp://` warning data, context-scoped Compose command construction with workdir/env passthrough, and manager `ListDockerContexts`/`SetDockerContext` creating an active `ctx:<name>` provider without running `docker context use`. Local gates passed: `go test ./internal/providers`, `go test . ./internal/...`, `go vet . ./internal/...`, `go build . ./internal/...`, `golangci-lint v2.12.2 run --timeout=5m`, frontend ESLint, Vitest, TypeScript, and audit. Real third-party context providers are not available in this environment, so the context matrix below remains manual TODOs.

- [x] Unit-test line-delimited Docker context JSON parsing.
- [x] Unit-test `ctx:<name>` provider detection and daemon ping through `docker --context`.
- [x] Unit-test unencrypted `tcp://` context warning data.
- [x] Unit-test `SetDockerContext` active-provider selection without mutating the user's global Docker context.
- [ ] Windows: Docker Desktop context lists, pings, and can become Cairn's active `ctx:<name>` provider without changing `docker context show`.
- [ ] macOS: Docker Desktop context lists, pings, and can become Cairn's active `ctx:<name>` provider without changing `docker context show`.
- [ ] macOS: OrbStack context lists, pings, and Compose project import works through `--context`.
- [ ] Linux: remote SSH Docker context lists, pings, and Compose commands run through `--context`.
- [ ] Linux: unencrypted `tcp://` context shows the permanent warning chip in Settings.
- [ ] Cross-platform E2E: switch Colima -> Desktop/remote context and back without app restart.

## Phase 6.3 macOS Onboarding, Settings, and Terminal UX

Current evidence: frontend tests cover the macOS Colima onboarding branch through checks and install planning, Colima profile/CPU/RAM/disk settings saves, Docker context listing, unencrypted `tcp://` warning badge rendering, and `ProviderService.SetDockerContext` activation from Settings. Local gates passed: frontend TypeScript, ESLint, Vitest (41 tests), Vite dev build, audit; `go test . ./internal/...`; `go vet . ./internal/...`; `go build . ./internal/...`; `golangci-lint v2.12.2 run --timeout=5m`. zsh host terminal and `colima ssh` backend terminal commands were implemented and unit-tested in Phase 6.1. A Colima-capable macOS VM is unavailable in this environment, so the runtime rows below remain manual TODOs.

- [x] React-test macOS setup backend cards: Colima recommended, Existing Docker context, and disabled Remote host.
- [x] React-test Colima setup checks with profile/CPU/RAM/disk fields and install-plan preview.
- [x] React-test Settings macOS Colima profile/CPU/RAM/disk saves and provider detection refresh.
- [x] React-test Docker context list/switch UI, including current/available badges and unencrypted `tcp://` warning chip.
- [ ] macOS runner/VM E2E: first-launch no-provider flow defaults to Colima and reaches checks/install-plan preview.
- [ ] macOS runner/VM E2E: Settings -> Providers updates Colima profile/resources, reruns detection, and notes restart-required resource changes.
- [ ] macOS runner/VM E2E: switch Colima -> Docker Desktop/OrbStack context and back without app restart or changing `docker context show`.
- [ ] macOS runner/VM E2E: host terminal opens zsh and backend terminal opens `colima ssh` for the selected profile.

## Phase 7 Volume Backup/Restore

Current evidence: backend unit tests cover backup planning warnings, sidecar/checksum validation, filename collisions, backup repository insertion, restore overwrite typed-name confirmation, and provider helper execution. Local Windows-hosted WSL validation passed against the dedicated `cairn-dev` Docker Engine with `CAIRN_REAL_WSL_DOCKER_BACKUPS=1 go test -tags=wslintegration ./internal/backups -run TestManagerRealWSLDockerBackupRestoreRoundTrip -count=1 -timeout=3m -v`, covering backup of a real named volume, restore into a new volume, overwrite restore with typed-name confirmation, and data verification after each restore. CI run 27475067191 passed on Ubuntu 24.04, Windows, and macOS, including the Linux `CAIRN_REAL_DOCKER_BACKUPS=1` integration.

- [x] Validate real backup/restore round-trip against local `cairn-dev` through the Windows WSL provider.
- [x] Verify restore overwrite requires typing the target volume name.
- [x] Verify backup archives and JSON sidecars are written outside Cairn's SQLite database.
- [x] Add Linux CI real-daemon integration with `CAIRN_REAL_DOCKER_BACKUPS=1`.
- [x] Linux CI: confirm the new backup integration step is green after push.
- [ ] Clean Win11 VM: run the tagged WSL backup integration after fresh WSL/provider setup.
- [ ] macOS Colima VM: run the real backup/restore round-trip against Colima.
- [ ] Existing Docker context matrix: run backup/restore against Docker Desktop/remote contexts without mutating global `docker context show`.

## Phase 8 Registry, Updates, and Image Lineage

Current evidence: automated tests cover the 14 normative update/lineage/registry cases from `dev-docs/06-testing.md section 5`: service-image update statuses, mutable `latest` warning, pinned digests, private-auth/rate-limit/unreachable registries, base-image rebuild detection, multi-stage lineage, unknown-base wording, mixed project plan ordering, health failure rollback/manual guidance, ignore/unignore badges, registry login/logout/auth, tag/push/pull digest round-trip, and plaintext Docker config storage labeling. CI run 27482496569 passed on Ubuntu 24.04, Windows, and macOS; Ubuntu CI includes real Docker registry auth and tag/push integrations. Local Windows WSL registry/tag-push runtime smoke passed against `cairn-dev` with `CAIRN_REAL_WSL_DOCKER_REGISTRY=1 go test -tags=wslintegration ./internal/docker -run TestClientRealWSLRegistryTagPushRoundTrip -count=1 -timeout=6m -v`, covering the real Windows WSL provider path, `wsl+stdio://cairn-dev`, a temporary backend Docker config, authenticated `registry:2` login, unauthenticated push rejection, tag, push, digest resolve, pull-back digest check, logout, and auth-required after logout without Docker Desktop contexts. Windows WSL update/rebuild smoke and macOS Colima runtime smoke remain manual because the required clean platform VMs are unavailable in this environment.

- [x] Linux CI: run the full unit/contract/frontend suite plus real registry auth and tag/push integrations.
- [x] Linux CI: verify real Docker integrations still pass after update UI and metrics stabilization.
- [x] React-test global Updates journey, project Updates tab grouping, lineage confidence wording, unknown-base wording, history rollback, ignored unignore, and update notification routing.
- [x] Backend-test update status machine, base-image status machine, mixed plan ordering, health rollback/manual guidance, backup-first option, ignore/unignore badges, registry auth, registry tag/push, and plaintext storage source mapping.
- [ ] Windows WSL `cairn-dev`: run update journey against a seeded old service-image digest, apply one-click update with health watch, and verify history row.
- [ ] Windows WSL `cairn-dev`: run rebuild journey on `build-simple` with mocked/new base digest and verify `build --pull` + `up -d`.
- [x] Windows WSL `cairn-dev`: run local authenticated `registry:2` login, tag, push, pull-back digest, logout, and auth-required check without using Docker Desktop contexts.
- [ ] macOS Colima VM: run update journey against a seeded old service-image digest, apply one-click update with health watch, and verify history row.
- [ ] macOS Colima VM: run rebuild journey on `build-simple` with mocked/new base digest and verify `build --pull` + `up -d`.
- [ ] macOS Colima VM: run local authenticated `registry:2` login, tag, push, pull-back digest, logout, and auth-required check.
- [ ] Existing Docker context matrix: repeat update/rebuild/tag-push smoke against Docker Desktop/OrbStack/remote contexts without mutating global `docker context show`.

## Full Platform Matrix TODO

- [ ] Windows 11 x64: WSL present/absent, Ubuntu present/absent/multiple, Docker in Ubuntu present/absent, systemd on/off.
- [ ] Linux: Ubuntu LTS and Debian stable, Docker present/absent, user in/not in docker group, service stopped, rootless.
- [ ] macOS: Apple Silicon, Homebrew present/absent, Colima present/absent, existing Docker Desktop context, remote context.
