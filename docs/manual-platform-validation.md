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

## Full Platform Matrix TODO

- [ ] Windows 11 x64: WSL present/absent, Ubuntu present/absent/multiple, Docker in Ubuntu present/absent, systemd on/off.
- [ ] Linux: Ubuntu LTS and Debian stable, Docker present/absent, user in/not in docker group, service stopped, rootless.
- [ ] macOS: Apple Silicon, Homebrew present/absent, Colima present/absent, existing Docker Desktop context, remote context.
