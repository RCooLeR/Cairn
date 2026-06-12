# Cairn Roadmap

## Milestone 0 — Project setup

Goal: Create the initial repository and development workflow.

Deliverables:

```text
Repository structure
Go backend skeleton
Frontend skeleton
SQLite migration setup
Provider interface
Basic app window
CI build pipeline
```

Acceptance criteria:

```text
App launches on dev machine
Frontend can call backend method
SQLite opens and migrates
Provider interface compiles
```

---

## Milestone 1 — Linux native MVP

Goal: Build the core Docker manager on the simplest platform first.

Deliverables:

```text
Linux native provider detection
Docker API connection
Container list
Image list
Volume list
Network list
Basic dashboard
Start/stop/restart container
Inspect container
```

Acceptance criteria:

```text
Cairn connects to /var/run/docker.sock
Dashboard shows live Docker state
Container control actions work
Errors are shown clearly
```

---

## Milestone 2 — Compose project detection

Goal: Make Cairn project-first.

Deliverables:

```text
Group containers by Compose labels
Project cards
Project detail page
Services tab
Project logs
Project start/stop/restart/pull via docker compose
Compose config viewer
```

Acceptance criteria:

```text
Existing Compose projects appear automatically
Services are grouped correctly
Project actions run from correct working directory
Commands are previewed before execution
```

---

## Milestone 3 — Logs, stats, terminals

Goal: Make Cairn useful for daily operations.

Deliverables:

```text
Live logs viewer
Multi-container project logs
Container stats stream
Project aggregate charts
Container terminal
Backend/host terminal
Command cheatsheet
```

Acceptance criteria:

```text
Logs stream without freezing UI
Stats update live
Container shell opens and resizes
Command palette can copy/run safe commands
```

---

## Milestone 4 — Windows WSL provider

Goal: Support Windows without Docker Desktop.

Deliverables:

```text
WSL detection
Ubuntu detection
Docker package detection inside Ubuntu
Docker service control inside Ubuntu
Path mapping
WSL terminal
Provider health checks
```

Acceptance criteria:

```text
Cairn can manage Docker installed inside Ubuntu WSL
Compose projects inside WSL work
Windows path mapping is handled clearly
Provider errors include repair suggestions
```

---

## Milestone 5 — macOS provider

Goal: Support macOS through Colima or existing Docker context.

Deliverables:

```text
Homebrew detection
Docker CLI detection
Colima detection
Colima start/stop/status
Docker context selection
macOS terminal support
```

Acceptance criteria:

```text
Cairn can connect to Colima
Cairn can connect to an existing context
Dashboard and project management work on macOS
```

---

## Milestone 6 — Image and base image updates

Goal: Implement one-click safe image updates and first-class base image tracking.

Deliverables:

```text
Registry digest checker
Service image update badges
Base image update badges
Dockerfile FROM parser
Image lineage discovery
Updates page
Service pull/recreate flow
Service rebuild/redeploy flow
Project mixed update plan
Health monitoring
Update history
Ignore updates
```

Acceptance criteria:

```text
Cairn detects available service image updates
Cairn detects base image changes for local build services
Cairn distinguishes pull/recreate from rebuild/redeploy
Cairn shows exact commands before update
Service update runs successfully
Base-image rebuild update runs successfully
Health result is shown
Rollback/manual guidance is available when needed
```

---

## Milestone 7 — v1 polish

Goal: Make Cairn release-ready.

Deliverables:

```text
Installers/packages
Settings page
Audit log
Notifications
Tray app
Disk cleanup
Volume backup/restore
Error handling polish
Documentation
```

Acceptance criteria:

```text
App is usable on Windows/Linux/macOS
Destructive actions require confirmation
User can recover from common provider problems
App feels clean, fast, and trustworthy
```
