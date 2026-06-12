# 08 — Packaging, Distribution & Release

## 1. Build pipeline

- `wails3 task build` per OS in CI (GitHub Actions matrix: `windows-latest`, `ubuntu-22.04`, `macos-14`).
- App icons/logo come from `assets/cairn-icon.png` and `assets/cairn-logo.png` (normative, see [ui/00-design-system.md §0]); CI generates `.ico` (Windows/NSIS), `.icns` (macOS), and the Linux PNG icon set (16–512px for .desktop/AppImage) from `cairn-icon.png`.
- Version stamped from git tag (`vX.Y.Z`) into Go `VersionInfo` and frontend.
- Artifacts uploaded per build; releases cut from tags only; changelog generated from conventional commits.

## 2. Windows

- Installer: **NSIS** (Wails-supported) x64; ARM64 post-v1.
- Per-user install by default (no admin). Elevation requested only by actions that need it at runtime (WSL feature install), via explicit UAC prompt — never the app itself.
- Optional PATH shim: `cairn.exe` CLI launcher (v1.1: `docker.exe`/`docker-compose.exe` wrappers explicitly out of v1).
- Code signing: OV/EV cert via Azure Trusted Signing or SignTool in CI; unsigned dev builds clearly marked.

## 3. Linux

- Formats: **AppImage** (primary) + **.deb**; .rpm post-v1.
- .deb: depends on nothing Docker-related (Cairn installs/uses Docker itself); desktop file + icon; postinst does NOT touch docker group.
- AppImage: bundles WebKitGTK requirements per Wails v3 guidance; tested on Ubuntu LTS + Debian stable.

## 4. macOS

- `.app` in `.dmg`; universal binary (arm64 + amd64) if Wails v3 build permits, else arm64 first.
- Signed (Developer ID) + notarized in CI (`notarytool`). Hardened runtime enabled.
- Prefer existing Homebrew for backend installs; Cairn itself ships as dmg (brew cask post-v1).

## 5. App self-update

v1: in-app "new version available" notification (checks GitHub releases JSON, respects `updates.notify`), links to download. No silent self-update in v1. Auto-update framework is a v1.1 decision.

## 6. Release process

```text
1. Phase-exit checklist green (06-testing.md §9 for v1.0).
2. Tag vX.Y.Z → CI builds, signs, notarizes all artifacts.
3. Smoke-test installers on all 3 OS (manual checklist: install, onboard, manage, uninstall).
4. Publish GitHub release with notes (features, fixes, known issues).
5. Update website/docs.
```

## 7. Telemetry

None in v1. No analytics, no crash reporting without explicit opt-in (decision deferred to v1.1).
