# Provider Troubleshooting

Use Settings -> Providers when Docker is not reachable, project detection looks stale, or Cairn is using the wrong backend.

## Windows WSL Ubuntu

Expected healthy state:

- WSL 2 is installed.
- The selected Ubuntu distro is WSL 2.
- systemd is enabled.
- Docker Engine, Compose, and Buildx are installed inside the distro.
- Docker Desktop internal distros are ignored.
- Docker Desktop contexts are not mutated by Cairn.

Common fixes:

- No usable distro: install Ubuntu on WSL 2, or set `windows.wsl_distro` to a Docker-capable Ubuntu distro.
- Wrong distro: set the WSL distro in Settings -> Providers, save, then run Detect again.
- Docker missing: run the setup flow or install Docker Engine inside the distro.
- Docker not running: start the backend from Cairn or run `sudo systemctl start docker` inside the distro.
- Docker Desktop integration conflict: disable Docker Desktop WSL integration for the selected distro or choose a clean distro such as `cairn-dev`.
- Slow project IO: move heavy Compose projects from `/mnt/c/...` into the WSL filesystem, such as `~/projects`.

## Linux Native

Expected healthy state:

- Docker Engine is installed.
- The configured socket exists, usually `/var/run/docker.sock`.
- The selected permission mode matches the user setup.
- Compose and Buildx are available.

Common fixes:

- Permission denied: choose sudo per action, use a rootless socket, or intentionally add the user to the Docker group outside Cairn.
- Docker stopped: start Docker through Cairn or `systemctl start docker`.
- Rootless Docker: set the socket path to the rootless user socket in Settings -> Providers.

Cairn does not silently add users to the Docker group.

## macOS Colima

Expected healthy state:

- Homebrew is installed if Cairn needs to install dependencies.
- Docker CLI, Compose, and Colima are installed.
- The configured Colima profile exists and is running.
- The matching Docker context is reachable.

Common fixes:

- Homebrew missing: install Homebrew first, then rerun setup.
- Colima stopped: start it from Cairn or run `colima start`.
- Resource changes not applied: restart the Colima profile after changing CPU, memory, or disk settings.
- Existing context needed: use Settings -> Docker contexts and select the desired context without changing the global Docker context.

## Existing Docker Contexts

Cairn can use existing Docker contexts without running `docker context use`.

Common fixes:

- Context unreachable: verify it with `docker --context <name> info`.
- Unencrypted TCP warning: migrate to SSH or TLS. Cairn warns on existing unencrypted `tcp://` contexts and never creates them.
- Project commands fail: confirm the context has access to the same paths or use provider-appropriate paths.

## When Docker Is Stopped

Cairn opens in degraded mode. It keeps cached data visible, disables mutations, shows repair actions, and avoids starting log/stats streams until Docker is healthy again.
