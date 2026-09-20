# Cairn Documentation

This directory contains user-facing and validation documentation for Cairn.

For source builds and the `src/` layout, see [Build From Source](../README.md#build-from-source). Commands in these documents assume the repository root unless stated otherwise; direct application checks use `go -C src ...` or `npm --prefix src/frontend ...`.

## User Docs

- [help.md](help.md) - practical user guide and common workflows.
- [user-quickstart.md](user-quickstart.md) - first launch, import, daily use, safety, backups, and updates.
- [provider-troubleshooting.md](provider-troubleshooting.md) - Windows WSL, Linux native, macOS Colima, and existing context repair notes.
- [local-agent.md](local-agent.md) - local Docker agent setup, tools, limits, and file-edit flow.
- [release-process.md](release-process.md) - CI packaging, GoReleaser publishing, signing, and tagging.
- [release-notes-v1.0.0.md](release-notes-v1.0.0.md) - v1.0 release notes.

## Validation Docs

- [project-analysis-2026-09-20.md](project-analysis-2026-09-20.md) - architecture review, startup/WSL findings, fixes, validation, and remaining risks.
- [dependency-refresh-2026-09-20.md](dependency-refresh-2026-09-20.md) - dependency versions, compatibility decisions, new compiler features, and audit evidence.
- [manual-platform-validation.md](manual-platform-validation.md) - platform-specific manual validation notes.
- [v1-release-validation.md](v1-release-validation.md) - release validation evidence.
- [v1-release-checklist.md](v1-release-checklist.md) - v1 release checklist status.

## Where To Start

Use [help.md](help.md) when you want the broad user guide, and [provider-troubleshooting.md](provider-troubleshooting.md) when Cairn says Docker is not reachable or provider checks fail.
