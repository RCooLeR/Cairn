# Cairn v1.0 Quickstart

Cairn is a Compose-first Docker desktop app for Linux, Windows WSL, macOS Colima, and existing Docker contexts.

## Install

Download the package for your OS:

- Windows: `cairn-amd64-installer.exe`
- Linux: AppImage or `.deb`
- macOS: `.dmg`

Unsigned development builds are for smoke testing only. Release builds should be signed or clearly marked unsigned by CI.

## First Launch

1. Open Cairn.
2. Choose a backend:
   - Windows: Windows WSL Ubuntu.
   - Linux: Linux native Docker Engine.
   - macOS: Colima.
   - Advanced: an existing Docker context.
3. Let Cairn run provider checks.
4. If checks pass, continue to project detection.
5. If checks fail, use the repair hints shown in the setup flow or Settings -> Providers.

Cairn never exposes Docker on TCP. Windows WSL uses the selected distro through WSL stdio transport, and macOS Colima uses the Colima/Docker context owned by that provider.

## Import Projects

1. Open Projects.
2. Select Import Project.
3. Pick a folder containing `compose.yaml`, `compose.yml`, `docker-compose.yml`, or `docker-compose.yaml`.
4. Review validation output.
5. Import the project.

On Windows WSL, projects under `/mnt/c`, `/mnt/d`, or other mounted Windows drives work, but Cairn warns that heavy Compose workloads perform better inside the WSL filesystem, such as `~/projects`.

## Daily Use

- Dashboard shows Docker health, resource charts, projects, updates, logs, and recent activity.
- Projects controls Compose lifecycle actions.
- Containers, Images, Volumes, and Networks provide Docker inventory and safe actions.
- Logs streams container and project logs with pause, search, filters, and export.
- Terminal opens host, backend, project, or container shells.
- Agent provides local read-only Docker help through Ollama or an OpenAI-compatible endpoint; see `docs/local-agent.md`.
- Updates checks service images and base-image lineage, then applies confirmed update plans with rollback handling.
- Settings manages providers, Docker contexts, registries, update cadence, appearance, backups, audit, and advanced options.

## Safety Model

Mutating actions use Cairn command plans:

1. Plan.
2. Preview commands and effects.
3. Confirm.
4. Execute.
5. Audit.

Dangerous actions require typing the target name. Registry passwords are passed through stdin to Docker and are never persisted by Cairn.

## Backups

Set a backup directory in Settings -> Backups. Volume backups create compressed archives plus sidecar metadata and checksums. Restore-overwrite actions require typed-name confirmation.

## Updating Cairn

v1.0 shows an in-app update notice that links to GitHub releases when `updates.notify` is enabled. It does not silently self-update.
