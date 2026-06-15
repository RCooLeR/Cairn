# Cairn v1.0.0 Release Notes

Cairn v1.0.0 is the first release-ready version of the Compose-first Docker management desktop app.

## Highlights

- Cross-platform provider model for Linux native Docker, Windows WSL Ubuntu, macOS Colima, and existing Docker contexts.
- Compose project detection, import, lifecycle actions, project detail views, service status, and raw/resolved Compose inspection.
- Docker inventory for containers, images, volumes, and networks with safe lifecycle and object actions.
- Live dashboard with Docker health, resource charts, project activity, container status, logs, updates, and cleanup entry points.
- Log streaming with search, filters, pause/follow, export, and high-volume buffering.
- Terminal sessions for host, backend, project, and container shells, plus a curated command cheatsheet.
- Image update, base-image lineage, registry auth, tag, push, ignore/unignore, update history, rollback, and health-watch flows.
- Volume backup and restore with checksums, sidecar metadata, overwrite confirmation, and audit entries.
- Settings for providers, contexts, registries, updates, metrics, terminal, appearance, backups, security, audit, and advanced controls.

## Safety

- Mutating actions use the command-plan pipeline: plan, preview, confirm, execute, audit.
- Dangerous actions require typed-name confirmation.
- Registry passwords are sent to Docker through stdin and are not persisted by Cairn.
- Cairn does not configure Docker TCP exposure.
- Unencrypted existing Docker contexts show a warning.
- Docker group membership changes are never applied silently.

## Validation

- Frontend unit, axe, visual-regression, release UI, and seed-scale performance checks are green.
- Backend unit, focused race, security, release DB upgrade, Docker integration, and WSL provider smokes are green.
- Windows WSL provider validation is green against the dedicated `cairn-dev` distro.
- A real 24 h active-stream soak completed successfully with logs, stats, terminal, and dashboard activity.
- Windows local executable and NSIS installer build successfully.
- CI packaging produces Windows NSIS, Linux AppImage and `.deb`, and macOS `.dmg` artifacts.

## Known Release Sign-Off Items

These are external validation/signing items, not known code gaps:

- Clean Windows 11 VM onboarding, installed-app smoke, and uninstall cleanliness.
- macOS Apple Silicon Colima and existing-context installed-app smoke and uninstall cleanliness.
- Debian stable desktop/AppImage/rootless installed-app matrix.
- Production signing/notarization certificates for final public artifacts.

## Not In v1

Post-v1 candidates include scheduled per-service auto-updates, container/volume file browser, diagnostics bundle export, tray app, desktop notifications, network topology, Compose editing, standalone image-build UI, rpm/brew cask packaging, ARM64 Windows, and opt-in crash reporting.
