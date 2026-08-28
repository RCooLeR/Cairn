# Cairn

Cairn is a Compose-first Docker management desktop app for Windows, macOS, and Linux. It is built for day-to-day local Docker work: provider setup, Compose projects, containers, images, volumes, networks, logs, terminals, updates, backups, registries, and a local Docker agent.

Current desktop builds target Windows 10/Server 2016 or newer, macOS 13.3 or newer, and Linux distributions with GTK 4.14+ and WebKitGTK 6.0 (for example Debian 13 or Ubuntu 24.04).

The v1.0 app is intentionally desktop-first and safety-first. Cairn runs against a selected Docker backend, keeps command previews visible before mutations, and records audited actions.

## Highlights

- Docker backend setup for Windows WSL Ubuntu, Linux native Docker Engine, macOS Colima, and existing Docker contexts.
- Compose project import, detection, lifecycle actions, updates, logs, files, backups, and project-level resource metrics.
- Container drill-down with logs, file browser, terminal, inspect data, and start/stop/restart/remove actions.
- Docker inventory for images, volumes, networks, and containers, with safe pruning and confirmation flows.
- Registry login through Docker credential storage, with unencrypted config warnings.
- Local Docker agent for Dockerfiles, Compose, runtime diagnostics, app config review, and approval-gated Docker tools.
- Windows Docker CLI shim so `docker ...` in PowerShell can forward into the selected WSL distro when Docker Desktop is not installed.

## Supported Backends

| Platform | Backend | Notes |
| --- | --- | --- |
| Windows | Ubuntu on WSL2 | Recommended. Docker Engine, Compose, and Buildx run inside the selected Ubuntu-family distro. |
| Linux | Native Docker Engine | Uses the configured Docker socket, usually `/var/run/docker.sock`. |
| macOS | Colima | Uses Homebrew-managed Docker CLI/Compose/Buildx and a Colima profile. |
| Any | Existing Docker context | Uses a selected context without changing the global Docker context. |

On Windows, Cairn does not require Docker Desktop. If Windows cannot resolve `docker`, Cairn installs a user-local `docker.cmd` shim on startup when the Windows WSL provider is active. Open a new PowerShell window after the first install so Windows loads the updated user PATH.

## Quick Start

1. Install and open Cairn.
2. Pick a backend in the setup flow.
3. Run provider checks.
4. If checks fail, use **Auto repair / update**.
5. Import a folder that contains `compose.yaml`, `compose.yml`, `docker-compose.yml`, or `docker-compose.yaml`.
6. Manage projects, containers, logs, terminals, updates, and backups from the sidebar.

More detail: [docs/user-quickstart.md](docs/user-quickstart.md).

## Safety Model

Mutating work goes through Cairn's command-plan pipeline:

1. Plan.
2. Preview commands and effects.
3. Confirm.
4. Execute.
5. Audit.

Destructive actions require confirmation. Dangerous actions require typing the target name. Registry secrets are passed to Docker through stdin and are not persisted by Cairn.

## Local Agent

Cairn can use a local Ollama or OpenAI-compatible endpoint for Docker help. The default endpoint is:

```text
http://127.0.0.1:11434
```

For containment, the endpoint must be an `http` or `https` URL using a literal IPv4 or IPv6 loopback address and an explicit port. Hostnames (including `localhost`), remote/private/link-local addresses, URL credentials, queries, fragments, path-prefixed bases, proxies, and redirects are rejected.

Preferred models start with `gemma4:12b-it-q8_0`, then `gemma4:12b`, with other local chat/code models as fallbacks. The agent can inspect Docker inventory, selected project files, logs, networks, images, and can request approval-gated Cairn tools.

More detail: [docs/local-agent.md](docs/local-agent.md).

## Build From Source

Required tools:

- Go 1.27.0
- Node.js 24.20.0 LTS and npm 11.19 or newer
- Task
- Wails v3 beta 15, installed at the exact version pinned by the project build config

Common commands:

```powershell
task frontend:install
task test
task build
```

Windows GUI build:

```powershell
task windows:build
```

The Windows executable is written to:

```text
bin/cairn.exe
```

Run in development mode:

```powershell
task dev
```

Wails server mode is not a supported deployment target because Cairn's desktop services are not a remotely safe API. Stable server and server-container builds are blocked. The narrowly scoped, explicitly acknowledged test path is documented in [docs/development-server-mode.md](docs/development-server-mode.md).

## Releases

CI builds native installers on each target OS and GoReleaser publishes tagged releases with checksums:

- Windows: NSIS installer.
- Linux: AppImage and Debian package.
- macOS: DMG installer.

Push a semver tag such as `v1.0.0` to run the release workflow. If code-signing secrets are not configured, Windows and macOS artifacts are published with an `-unsigned` suffix.

More detail: [docs/release-process.md](docs/release-process.md).

## Documentation

- [docs/help.md](docs/help.md) - user guide and common workflows.
- [docs/user-quickstart.md](docs/user-quickstart.md) - first launch and daily use.
- [docs/provider-troubleshooting.md](docs/provider-troubleshooting.md) - backend checks and fixes.
- [docs/local-agent.md](docs/local-agent.md) - local Docker agent behavior.
- [docs/release-process.md](docs/release-process.md) - CI packaging and GoReleaser publishing.
- [docs/release-notes-v1.0.0.md](docs/release-notes-v1.0.0.md) - release notes.
- [docs/manual-platform-validation.md](docs/manual-platform-validation.md) - manual platform validation.

## Status

Cairn v1.0 targets a release-ready desktop app with installers for Windows, Linux, and macOS. Unsigned development builds are for local smoke testing; release builds should be signed or clearly marked unsigned by CI.
