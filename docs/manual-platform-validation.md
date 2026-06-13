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

## Full Platform Matrix TODO

- [ ] Windows 11 x64: WSL present/absent, Ubuntu present/absent/multiple, Docker in Ubuntu present/absent, systemd on/off.
- [ ] Linux: Ubuntu LTS and Debian stable, Docker present/absent, user in/not in docker group, service stopped, rootless.
- [ ] macOS: Apple Silicon, Homebrew present/absent, Colima present/absent, existing Docker Desktop context, remote context.
