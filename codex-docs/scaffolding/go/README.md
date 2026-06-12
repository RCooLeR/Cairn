# Cairn Go Scaffolding

This folder contains starter interface sketches for the future Go implementation.

It is not a full app yet. It is a starting point for architecture discussion and repository setup.

Recommended stack:

```text
Go backend
Wails desktop shell
React/Svelte frontend
SQLite store
Docker Engine Go SDK
Compose CLI wrapper
```

Recommended next files:

```text
cmd/cairn/main.go
internal/app/app.go
internal/store/sqlite.go
internal/docker/client.go
internal/compose/cli.go
internal/providers/manager.go
```
