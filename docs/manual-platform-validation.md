# Manual Platform Validation TODO

Source: `dev-docs/06-testing.md §2` and `dev-docs/README.md` "Development environment (Windows)".

## Phase 0.0 Windows Development Distro

Current evidence: `wsl -l -v` reports no installed WSL distributions in this environment, so the dedicated `cairn-dev` validation cannot be completed here yet.

- [ ] Install the dedicated WSL distro: `wsl --install Ubuntu-26.04 --name cairn-dev`.
- [ ] Enable systemd inside `cairn-dev` via `/etc/wsl.conf`, then run `wsl --shutdown`.
- [ ] Install official Docker Engine, Compose plugin, and Buildx inside `cairn-dev`.
- [ ] Add the WSL user to the `docker` group and re-login.
- [ ] Disable Docker Desktop WSL integration for `cairn-dev`.
- [ ] Verify `wsl -d cairn-dev -- docker run --rm hello-world`.
- [ ] Verify Windows `docker context ls` still shows Docker Desktop contexts untouched.
- [ ] Set development settings `windows.wsl_distro = cairn-dev` before Windows provider integration tests.

## Phase 0.1 Wails Dev Hot Reload

Current evidence: `wails3 dev -config ./build/config.yml -port 9247 -nocolour` built the dev binary, ran `bin/cairn.exe`, started Vite on `127.0.0.1:9247`, and the Wails/WebView2 app connected to the frontend dev server. The in-app Browser connector is unavailable in this sandbox (`CreateProcessAsUserW failed: 5`), and scripted timestamp-touch reload probes did not yield reliable reload logs before the tool timeout.

- [ ] Run `wails3 dev -config ./build/config.yml -port 9245` locally.
- [ ] Edit a React/Tailwind source file and verify the Wails window updates without a full manual restart.
- [ ] Edit a Go source file and verify the Wails dev watcher rebuilds/restarts the backend side.
- [ ] Confirm the window/taskbar icon still uses `assets/cairn-icon.png` and the shell still uses `assets/cairn-logo.png`.

## Full Platform Matrix TODO

- [ ] Windows 11 x64: WSL present/absent, Ubuntu present/absent/multiple, Docker in Ubuntu present/absent, systemd on/off.
- [ ] Linux: Ubuntu LTS and Debian stable, Docker present/absent, user in/not in docker group, service stopped, rootless.
- [ ] macOS: Apple Silicon, Homebrew present/absent, Colima present/absent, existing Docker Desktop context, remote context.
