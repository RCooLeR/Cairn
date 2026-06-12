# Cairn — Product & Technical Specification

**Project name:** Cairn  
**Product type:** Cross-platform Docker/Compose desktop management app  
**Primary goal:** Replace the Docker Desktop user experience with a cleaner, Compose-first manager while still using existing Docker-compatible backends instead of building a custom container runtime.

---

## 1. Product definition

Cairn is a desktop app for managing Docker environments across Windows, macOS, and Linux.

Cairn should install, detect, configure, and manage existing Docker-compatible tooling. It should not implement its own container engine, image runtime, networking stack, or VM runtime.

The core idea:

```text
Cairn = installer + dashboard + Compose manager + terminal + charts + updater
```

Not:

```text
Cairn != custom Docker Engine
Cairn != custom container runtime
Cairn != Kubernetes platform in v1
```

Cairn should manage:

```text
Containers
Compose projects
Images
Volumes
Networks
Logs
Stats
Terminals
Image updates
Docker/Compose installation state
```

The app should use the Docker Engine API for low-level Docker object management and the official `docker compose` CLI for Compose lifecycle operations.

---

## 2. Brand direction

**Name:** Cairn

A cairn is a stack of stones used as a marker or guide. That fits the product well:

```text
Compose stacks -> stacked stones
Dashboard -> navigation marker
Infrastructure -> stable, grounded, durable
```

Suggested tagline:

```text
Cairn
A clean Compose-first Docker manager for Windows, macOS, and Linux.
```

Alternative tagline:

```text
Cairn
Your control center for Docker projects, containers, logs, terminals, and updates.
```

---

## 3. Supported platforms

Cairn should use a provider-based architecture. The UI and core app remain the same, but each operating system has a different backend provider.

| Platform | Default backend | Cairn responsibility |
|---|---|---|
| Windows | Ubuntu on WSL2 + official Docker packages | Install/check WSL Ubuntu, install Docker/Compose, manage through WSL |
| Linux | Native Docker Engine | Install/check Docker Engine and Compose plugin, manage local daemon |
| macOS | Colima or existing Docker context | Install/check Colima/Docker CLI, manage selected Docker context |
| Any OS | Existing Docker context | Connect to already configured Docker daemon |
| Any OS | Remote Docker over SSH | Manage remote hosts through Docker contexts |

Docker contexts should be supported from the beginning because they allow one Docker client to manage multiple Docker daemons and remote hosts.

---

## 4. Platform provider specification

### 4.1 Windows provider: WSL Ubuntu

Target model:

```text
Windows
  -> Cairn Desktop App
      -> WSL Ubuntu
          -> Docker Engine
          -> Docker CLI
          -> Docker Compose plugin
          -> Docker Buildx plugin
          -> user containers/images/volumes/networks
```

Responsibilities:

```text
Detect WSL
Detect Ubuntu distro
Detect WSL version
Check systemd support
Install official Docker packages inside Ubuntu
Start/stop Docker service inside Ubuntu
Run Docker and Compose commands through WSL
Map Windows paths to WSL paths
Open WSL terminal
Open container terminal
```

Windows path mapping:

```text
C:\Users\alex\project
-> /mnt/c/Users/alex/project
```

Cairn should also recommend better performance paths:

```text
Recommended for heavy projects:
~/projects/my-stack inside WSL

Allowed but slower for large file trees:
C:\Users\alex\project mounted as /mnt/c/...
```

### 4.2 Linux provider: native Docker

Target model:

```text
Linux
  -> Cairn
      -> local Docker Engine
          -> dockerd
          -> containerd
          -> Docker CLI
          -> Docker Compose plugin
          -> Docker Buildx plugin
```

Initial Linux support:

```text
Ubuntu
Debian
```

Later support:

```text
Fedora
Arch
openSUSE
Rocky Linux / AlmaLinux
```

For Ubuntu/Debian-style installs, Cairn should install official Docker packages:

```text
docker-ce
docker-ce-cli
containerd.io
docker-buildx-plugin
docker-compose-plugin
```

### 4.3 macOS provider: Colima or existing context

Target model:

```text
macOS
  -> Cairn
      -> Colima
          -> Linux VM
              -> Docker-compatible daemon
```

Cairn should not build its own macOS VM backend in v1. It should use Colima as the default open backend while also supporting existing Docker contexts.

Responsibilities:

```text
Detect Homebrew
Detect Docker CLI
Detect Docker Compose support
Detect Colima
Install/start Colima when user chooses it
Select Colima Docker context
Manage Colima CPU/RAM/disk settings where possible
Open local shell
Open container shell
```

Cairn should also support users who already have Docker Desktop, OrbStack, Rancher Desktop, Colima, Podman-compatible tooling, or a remote Docker context installed. In those cases, Cairn should connect to the selected context instead of forcing its own setup.

---

## 5. Core product principles

### 5.1 Compose-first

Cairn should treat Compose projects as first-class objects.

Instead of leading with raw containers, the UI should lead with:

```text
Projects
Services
Stacks
Health
Updates
Resource usage
Logs
Terminals
```

Docker Compose labels can be used to auto-group containers into projects and services.

Common labels:

```text
com.docker.compose.project
com.docker.compose.service
com.docker.compose.config-hash
com.docker.compose.project.working_dir
com.docker.compose.project.config_files
```

### 5.2 Transparent actions

Every major action should show the underlying command before execution.

Example:

```text
Update service "api"

Commands:
docker compose pull api
docker compose up -d api
```

This makes Cairn trustworthy for developers, DevOps users, and home-lab users.

### 5.3 Safe by default

Dangerous actions require explicit confirmation:

```text
Delete volume
Prune images
Prune system
docker compose down --volumes
Force recreate
Remove project
Reset Docker environment
```

Cairn should never expose the Docker daemon over unauthenticated TCP by default.

### 5.4 No lock-in

Cairn should use normal Docker and Compose tooling. A user should be able to close Cairn and continue using:

```text
docker ps
docker compose up -d
docker logs
docker exec
```

Cairn should enhance the workflow, not trap projects inside a proprietary format.

---

## 6. Recommended tech stack

### Desktop shell

Recommended:

```text
Wails + Go backend + web frontend
```

Wails fits Cairn because the project needs native OS integration, Docker control, charts, terminal UI, and a polished frontend.

### Backend

```text
Go
Docker Engine Go SDK
SQLite
Provider abstraction layer
Command runner
PTY/terminal manager
Registry update checker
Metrics collector
Event bus
```

### Frontend

Recommended:

```text
React or Svelte
TypeScript
Tailwind-style styling
xterm.js-style terminal component
Monaco-style editor for compose.yaml and .env files
Chart library for resource graphs
```

### Data storage

Use local SQLite.

Cairn does not need a server database. It should store cached state, settings, history, metrics, update checks, and audit events locally.

---

## 7. High-level architecture

```text
Cairn Desktop App
  -> Frontend UI
      -> Dashboard
      -> Projects
      -> Containers
      -> Images
      -> Volumes
      -> Networks
      -> Logs
      -> Terminals
      -> Updates
      -> Settings

  -> Go Backend
      -> Docker API client
      -> Compose CLI wrapper
      -> Provider manager
      -> Project detector
      -> Metrics collector
      -> Log streamer
      -> Terminal manager
      -> Image update checker
      -> Backup/restore manager
      -> Command palette engine
      -> SQLite store
      -> Event bus

  -> Platform Providers
      -> Windows WSL Ubuntu provider
      -> Linux native provider
      -> macOS Colima provider
      -> Existing Docker context provider
      -> Remote Docker SSH provider
```

---

## 8. Provider interface

The provider layer is the most important architectural boundary.

```go
type PlatformProvider interface {
    ID() string
    DisplayName() string
    Platform() Platform

    Detect(ctx context.Context) (*ProviderStatus, error)
    Install(ctx context.Context, opts InstallOptions) error

    Start(ctx context.Context) error
    Stop(ctx context.Context) error
    Restart(ctx context.Context) error

    DockerHost(ctx context.Context) (string, error)
    DockerContext(ctx context.Context) (string, error)

    RunDocker(ctx context.Context, args ...string) (*CommandResult, error)
    RunCompose(ctx context.Context, workdir string, args ...string) (*CommandResult, error)

    OpenHostTerminal(ctx context.Context, opts TerminalOptions) (*TerminalSession, error)
    OpenContainerTerminal(ctx context.Context, containerID string, opts ContainerTerminalOptions) (*TerminalSession, error)

    MapPathToBackend(hostPath string) (string, error)
    MapPathToHost(backendPath string) (string, error)
}
```

Provider status:

```go
type ProviderStatus struct {
    Installed        bool
    Running          bool
    Healthy          bool

    DockerInstalled  bool
    DockerRunning    bool
    ComposeInstalled bool
    BuildxInstalled  bool

    DockerVersion    string
    ComposeVersion   string
    BackendVersion   string

    CurrentContext   string
    DockerHost       string

    Problems         []ProviderProblem
    Warnings         []ProviderWarning
}
```

---

## 9. Core modules

### 9.1 Docker client module

Responsibilities:

```text
List containers
Inspect containers
Start/stop/restart/remove containers
Stream logs
Stream stats
Exec into containers
List images
Pull images
Remove images
List volumes
Inspect volumes
List networks
Inspect networks
Subscribe to Docker events
```

Use Docker API/Go SDK for this module.

### 9.2 Compose module

Responsibilities:

```text
Detect Compose projects
Resolve project working directory
Run docker compose config
Run docker compose up -d
Run docker compose down
Run docker compose pull
Run docker compose restart
Run docker compose logs
Run docker compose ps
Validate compose.yaml
```

Do not reimplement Compose in v1. Use the official CLI.

### 9.3 Project detector

Detection sources:

```text
Compose labels on containers
docker compose ls output
Imported compose.yaml files
User-added project folders
Recently used project folders
```

Grouping logic:

```text
Container
  -> label com.docker.compose.project
  -> label com.docker.compose.service
  -> label com.docker.compose.config-hash
  -> label com.docker.compose.project.working_dir
  -> label com.docker.compose.project.config_files
```

Output model:

```go
type Project struct {
    ID           string
    Name         string
    ProviderID   string
    ContextName  string
    WorkingDir   string
    ComposeFiles []string
    Services     []ServiceSummary
    Containers   []ContainerSummary
    Status       ProjectStatus
    Stats        ProjectStats
    Updates      []ImageUpdate
    Health       HealthSummary
}
```

### 9.4 Metrics collector

Collect:

```text
CPU usage
Memory usage
Network RX/TX
Block I/O
Container restart count
Health status
Uptime
Project aggregate CPU
Project aggregate memory
Project aggregate network
Docker disk usage
Image sizes
Volume sizes
```

Storage policy:

```text
Last 1 hour: high resolution
Last 24 hours: medium resolution
Last 7 days: low resolution
Older: delete or compact
```

### 9.5 Update checker

Purpose:

```text
Detect newer images for running services and allow safe one-click upgrades.
```

Update states:

```text
Unknown
Checking
Up to date
Update available
Pinned digest
Registry auth required
Rate limited
Error
Ignored
```

Update flow:

```text
1. Identify project/service/container image.
2. Read local image ID/digest.
3. Check remote registry digest for same tag.
4. Compare local and remote digest.
5. Mark update available if different.
6. On update, run docker compose pull <service>.
7. Recreate service using docker compose up -d <service>.
8. Watch health status and logs.
9. Show result.
10. Offer rollback when possible.
```

Cairn should not blindly auto-update containers by default. It should show update information and let the user approve upgrades.

### 9.6 Terminal manager

Terminal types:

```text
Host terminal
Backend terminal
Project terminal
Container terminal
```

Examples:

```text
Windows host terminal: PowerShell
Windows backend terminal: wsl.exe -d Ubuntu
Linux host terminal: bash, zsh, fish
macOS host terminal: zsh
Container terminal: /bin/bash, /bin/sh, /busybox/sh
```

Container terminal behavior:

```text
Detect available shells
Allow user selection
Support resize
Support copy/paste
Support working directory
Support user selection where possible
Show warning for root sessions
```

### 9.7 Command cheatsheet

Cairn should include a searchable Docker command cheatsheet and command palette.

Categories:

```text
Containers
Images
Compose
Volumes
Networks
Logs
Exec/terminal
Stats/debugging
Cleanup
```

Example commands:

```text
docker ps
docker ps -a
docker logs -f <container>
docker exec -it <container> /bin/sh
docker inspect <container>
docker compose up -d
docker compose down
docker compose pull
docker compose logs -f
docker system df
docker image prune
docker volume ls
docker network ls
```

Each command should have:

```text
Description
Risk level
Required context
Copy button
Run button where safe
Variables/placeholders
```

Risk levels:

```text
Safe
Needs confirmation
Destructive
Dangerous
```

---

## 10. Main UI specification

### 10.1 Dashboard

Purpose: give a fast overview of the whole Docker environment.

Sections:

```text
Runtime status
Project health
Resource usage
Update summary
Disk usage
Recent events
Quick actions
```

Dashboard cards:

```text
Docker status
Provider status
Running containers
Stopped containers
Unhealthy containers
Compose projects
Images
Volumes
Networks
Updates available
Docker disk usage
```

Charts:

```text
CPU over time
Memory over time
Network traffic
Disk usage by images/volumes/build cache
Top containers by CPU
Top containers by RAM
```

Quick actions:

```text
Start Docker
Restart Docker
Open terminal
Check updates
Import compose project
Prune unused resources
```

### 10.2 Projects page

Purpose: make Compose projects the primary management unit.

Project card fields:

```text
Project name
Status
Running/total services
CPU
Memory
Network
Health
Updates available
Ports
Last changed
Working directory
```

Project actions:

```text
Open
Start
Stop
Restart
Redeploy
Pull images
Check updates
Open logs
Open terminal
Open compose file
```

Project filters:

```text
Running
Stopped
Unhealthy
Updates available
High CPU
High memory
Recently changed
```

### 10.3 Project detail page

Tabs:

```text
Overview
Services
Containers
Logs
Stats
Compose
Environment
Volumes
Networks
Updates
Backups
Events
```

Project overview should show:

```text
Service graph
Health summary
Exposed ports
Recent logs
Resource charts
Update cards
```

Compose tab:

```text
Read compose.yaml
Validate config
Show docker compose config output
Show env files
Show resolved configuration
```

Editing Compose files should be optional and protected. For v1, read-only view is enough; editing can be v1.1.

### 10.4 Containers page

Table columns:

```text
Name
Status
Project
Service
Image
Ports
CPU
Memory
Network
Uptime
Health
Restart count
Actions
```

Actions:

```text
Start
Stop
Restart
Kill
Logs
Terminal
Inspect
Remove
```

Bulk actions:

```text
Start selected
Stop selected
Restart selected
Remove stopped selected
```

### 10.5 Images page

Table columns:

```text
Repository
Tag
Image ID
Size
Created
Used by
Update status
Digest
Actions
```

Actions:

```text
Pull
Remove
Inspect
Show containers using image
Check update
```

Cleanup tools:

```text
Remove dangling images
Remove unused images
Show reclaimable space
```

### 10.6 Volumes page

Volume table:

```text
Name
Driver
Size
Used by project
Used by containers
Created
Mountpoint/backend path
Backup status
Actions
```

Actions:

```text
Inspect
Backup
Restore
Browse
Delete
```

Delete protection:

```text
Block deleting volumes used by running containers unless user explicitly confirms.
```

### 10.7 Networks page

Network table:

```text
Name
Driver
Scope
Subnet
Gateway
Connected containers
Project
Actions
```

Network detail:

```text
Connected containers
Container IPs
Aliases
Exposed ports
Internal/external flag
```

Optional v1.1 feature:

```text
Simple topology graph
```

### 10.8 Logs page

Features:

```text
Multi-container logs
Project logs
Service logs
Container logs
Search
Filter by level
Filter by container
Pause/resume
Follow tail
Export logs
Timestamp toggle
```

### 10.9 Terminal page

Terminal modes:

```text
Host shell
Backend shell
Project shell
Container shell
```

The terminal UI should have a side panel with:

```text
Command cheatsheet
Recent commands
Saved snippets
Risk labels
Copy/run buttons
```

### 10.10 Updates page

Purpose: one clean place for image updates.

Columns:

```text
Project
Service
Current image
Current digest
Remote digest
Status
Last checked
Risk notes
Actions
```

Actions:

```text
Check now
Pull only
Update service
Update project
Ignore image
Rollback
```

Update confirmation modal:

```text
Service: api
Current image: myapp/api:latest
Action:
  docker compose pull api
  docker compose up -d api

Options:
  Backup volumes first
  Watch health after update
  Roll back on failed health check
```

---

## 11. Installation and onboarding

### 11.1 First launch flow

```text
1. Welcome to Cairn
2. Choose backend
3. Run checks
4. Install missing dependencies
5. Verify Docker
6. Import/detect projects
7. Open dashboard
```

Backend choices:

```text
Windows:
  Use Ubuntu on WSL

Linux:
  Use native Docker Engine

macOS:
  Use Colima
  Use existing Docker context

Advanced:
  Use existing context
  Connect remote Docker host
```

### 11.2 Health checks

Cairn should verify:

```text
Docker CLI exists
Docker daemon reachable
Docker Engine API reachable
Docker Compose available
Buildx available
Current user has permission
Disk has enough free space
Test container can run
Docker context is valid
```

### 11.3 Setup result

After setup, show:

```text
Docker version
Compose version
Provider/backend
Current Docker context
Socket/host
Storage location
Resource settings
```

---

## 12. Permissions and security

Docker access is powerful. Cairn must explain this clearly.

Security rules:

```text
Do not expose Docker over open TCP by default.
Do not silently add users to docker group.
Do not delete volumes without explicit confirmation.
Do not run destructive prune commands silently.
Do not store registry passwords in plaintext.
Do not auto-update containers by default.
```

Linux permission options:

```text
Option A: Use sudo when needed.
Option B: Add user to docker group after warning.
Option C: Use existing rootless Docker setup where detected.
```

Cairn should include an audit log:

```text
Timestamp
User action
Provider
Project/container
Command executed
Result
Duration
Error message
```

---

## 13. SQLite data model

Recommended tables:

```text
settings
providers
docker_contexts
projects
services
containers_cache
images_cache
volumes_cache
networks_cache
metrics_samples
image_update_checks
update_history
backups
command_history
audit_log
notifications
ignored_updates
```

Example schema sketch:

```sql
CREATE TABLE providers (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    platform TEXT NOT NULL,
    display_name TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1,
    last_status TEXT,
    last_checked_at DATETIME
);

CREATE TABLE projects (
    id TEXT PRIMARY KEY,
    provider_id TEXT NOT NULL,
    context_name TEXT NOT NULL,
    name TEXT NOT NULL,
    working_dir TEXT,
    compose_files TEXT,
    status TEXT,
    last_seen_at DATETIME,
    FOREIGN KEY(provider_id) REFERENCES providers(id)
);

CREATE TABLE metrics_samples (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    provider_id TEXT NOT NULL,
    project_id TEXT,
    container_id TEXT,
    cpu_percent REAL,
    memory_bytes INTEGER,
    memory_limit_bytes INTEGER,
    network_rx_bytes INTEGER,
    network_tx_bytes INTEGER,
    block_read_bytes INTEGER,
    block_write_bytes INTEGER,
    sampled_at DATETIME NOT NULL
);

CREATE TABLE image_update_checks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    provider_id TEXT NOT NULL,
    project_id TEXT,
    service_name TEXT,
    image_ref TEXT NOT NULL,
    local_digest TEXT,
    remote_digest TEXT,
    status TEXT NOT NULL,
    checked_at DATETIME NOT NULL,
    error TEXT
);

CREATE TABLE audit_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    action TEXT NOT NULL,
    target_type TEXT,
    target_id TEXT,
    command TEXT,
    status TEXT NOT NULL,
    error TEXT,
    created_at DATETIME NOT NULL
);
```

---

## 14. Backend service API

Frontend should call typed backend methods instead of shelling out directly.

Example service groups:

```text
ProviderService
DockerService
ComposeService
ProjectService
MetricsService
LogsService
TerminalService
UpdateService
BackupService
SettingsService
```

Example methods:

```go
type ProviderService interface {
    ListProviders(ctx context.Context) ([]ProviderSummary, error)
    Detect(ctx context.Context, providerID string) (*ProviderStatus, error)
    Install(ctx context.Context, providerID string, opts InstallOptions) error
    Start(ctx context.Context, providerID string) error
    Stop(ctx context.Context, providerID string) error
}

type ProjectService interface {
    ListProjects(ctx context.Context) ([]ProjectSummary, error)
    GetProject(ctx context.Context, projectID string) (*ProjectDetail, error)
    StartProject(ctx context.Context, projectID string) error
    StopProject(ctx context.Context, projectID string) error
    RestartProject(ctx context.Context, projectID string) error
    RedeployProject(ctx context.Context, projectID string) error
    PullProject(ctx context.Context, projectID string) error
}

type UpdateService interface {
    CheckProjectUpdates(ctx context.Context, projectID string) ([]ImageUpdate, error)
    CheckAllUpdates(ctx context.Context) ([]ImageUpdate, error)
    ApplyServiceUpdate(ctx context.Context, req ApplyServiceUpdateRequest) (*UpdateResult, error)
    IgnoreUpdate(ctx context.Context, imageRef string) error
}
```

---

## 15. One-click update specification

### 15.1 Update detection

For each Compose service:

```text
Read image reference
Resolve local image
Read local digest if available
Query remote registry digest
Compare local vs remote
Show update status
```

Special cases:

```text
Pinned digest image:
  Do not mark as normal update.
  Show "Pinned digest".

Local build service:
  Show "Built locally".
  Offer rebuild instead of pull.

Private registry:
  Show auth-required state.
  Use Docker credential helper where possible.

latest tag:
  Support update checks, but show warning that latest is mutable.

Multi-arch image:
  Compare digest for the current platform where possible.
```

### 15.2 Update execution

For service-level update:

```text
docker compose pull <service>
docker compose up -d <service>
```

For project-level update:

```text
docker compose pull
docker compose up -d
```

Optional pre-update steps:

```text
Backup named volumes
Snapshot current image IDs
Save current compose config
Record rollback plan
```

Post-update checks:

```text
Container recreated
Container running
Healthcheck healthy if defined
No crash loop
Recent logs do not show obvious startup failure
```

Rollback:

```text
Use previous image ID/tag when possible
Run docker compose up -d again
Show manual rollback instructions if automatic rollback is not safe
```

---

## 16. Backup and restore specification

### 16.1 Volume backup

Support named volume backup through temporary helper containers.

Backup metadata:

```text
Volume name
Project
Container usage
Created at
Compressed size
Docker context
Provider
Cairn version
```

Backup format:

```text
.tar.gz for volume contents
.json metadata sidecar
```

### 16.2 Restore safety

Before restore:

```text
Warn if volume is used by running containers
Offer to stop affected project
Require confirmation before overwriting existing volume
```

Restore options:

```text
Restore into existing volume
Restore into new volume
Create duplicate with suffix
```

---

## 17. Settings specification

Settings groups:

```text
General
Providers
Docker contexts
Resources
Updates
Terminal
Appearance
Security
Backups
Notifications
Advanced
```

Important settings:

```text
Default provider
Default Docker context
Metrics retention
Update check interval
Auto-start Cairn
Auto-start Docker backend
Confirm destructive actions
Terminal shell preference
Theme
Backup directory
Registry credentials behavior
```

Platform-specific settings:

```text
Windows:
  WSL distro name
  WSL path mapping
  Start Docker on app launch

Linux:
  Docker socket path
  Sudo behavior
  Systemd service status

macOS:
  Colima profile
  CPU/RAM/disk
  Colima autostart
```

---

## 18. Packaging and distribution

### Windows

```text
Installer: MSI or NSIS
Architecture: x64 first, ARM64 later
Optional PATH shims:
  cairn.exe
  docker.exe wrapper later
  docker-compose.exe wrapper later
```

Windows app should not require admin for normal management. Elevation should only be requested for installation or system-level changes.

### Linux

```text
Formats:
  AppImage
  deb
  rpm later
```

Linux app should support both:

```text
User already has Docker
User wants Cairn to install Docker
```

### macOS

```text
Formats:
  .dmg
  signed .app
  notarized release
```

macOS should prefer existing Homebrew installations where available.

---

## 19. MVP scope

### MVP 1 — Core manager

Goal: Cairn connects to Docker and provides a better dashboard than Docker Desktop.

Features:

```text
Provider detection
Docker connection
Container list
Image list
Volume list
Network list
Compose project grouping
Basic dashboard
Start/stop/restart container
Logs viewer
Basic container terminal
```

### MVP 2 — Compose-first UI

Features:

```text
Project cards
Project detail
Service list
Project logs
Project resource charts
docker compose up/down/restart/pull
Compose config viewer
Import compose project
```

### MVP 3 — Installers/providers

Features:

```text
Windows WSL Ubuntu provider
Linux native provider
macOS Colima provider
Existing Docker context provider
Provider health checks
Repair suggestions
```

### MVP 4 — Updates

Features:

```text
Image update detection
Update badges
Service update
Project update
Health monitoring after update
Update history
Ignore updates
```

### MVP 5 — Power features

Features:

```text
Volume backup/restore
Advanced terminal
Command cheatsheet
Disk cleanup
Audit log
Notifications
Tray app
Remote Docker context support
```

---

## 20. v1 acceptance criteria

Cairn v1 should be considered successful when the following work reliably:

```text
Windows:
  Fresh Windows machine with WSL support
  Cairn installs/checks Ubuntu Docker setup
  docker compose projects work inside WSL
  Cairn manages containers/projects/images/volumes/networks

Linux:
  Cairn detects native Docker
  Cairn can install Docker on Ubuntu/Debian
  Cairn manages local Docker without Docker Desktop

macOS:
  Cairn detects or installs Colima path
  Cairn uses the selected Docker context
  Cairn manages containers/projects/images/volumes/networks

All platforms:
  Project dashboard works
  Compose labels group services correctly
  Logs stream correctly
  Container terminal works
  Stats charts update live
  One-click service update works
  Destructive actions require confirmation
```

---

## 21. Explicit non-goals for v1

Do not include these in v1:

```text
Custom container runtime
Custom VM runtime
Kubernetes dashboard
Docker Scout clone
Enterprise policy management
Windows containers
Registry server
Multi-user server mode
Team collaboration
Cloud sync
AI assistant features
```

These can come later, but they would slow the first useful release.

---

## 22. Suggested repository structure

```text
cairn/
  cmd/
    cairn/
      main.go

  internal/
    app/
      app.go
      lifecycle.go

    providers/
      provider.go
      manager.go
      windows_wsl/
      linux_native/
      macos_colima/
      existing_context/
      remote_ssh/

    docker/
      client.go
      containers.go
      images.go
      volumes.go
      networks.go
      logs.go
      stats.go
      exec.go
      events.go

    compose/
      cli.go
      project.go
      detect.go
      config.go
      actions.go

    metrics/
      collector.go
      aggregator.go
      retention.go

    updates/
      checker.go
      registry.go
      planner.go
      executor.go
      rollback.go

    terminal/
      session.go
      host.go
      container.go
      pty.go

    backups/
      volumes.go
      metadata.go
      restore.go

    store/
      sqlite.go
      migrations/
      models.go

    security/
      confirmations.go
      audit.go
      permissions.go

  frontend/
    src/
      pages/
        Dashboard/
        Projects/
        Containers/
        Images/
        Volumes/
        Networks/
        Logs/
        Terminal/
        Updates/
        Settings/

      components/
        charts/
        terminal/
        tables/
        command-palette/
        modals/

      api/
      state/
      styles/

  docs/
    SPEC.md
    ARCHITECTURE.md
    PROVIDERS.md
    SECURITY.md
```

---

## 23. Recommended first implementation path

Build in this order:

```text
1. Linux native provider
2. Docker API connection
3. Read-only container/image/volume/network UI
4. Compose project grouping from labels
5. Logs and stats
6. Container terminal
7. Windows WSL Ubuntu provider
8. macOS Colima provider
9. Compose actions
10. Image update checker
11. Volume backup/restore
12. Polish, installers, release builds
```

Linux first is the cleanest path because there is no WSL path mapping or macOS VM layer. Once the core Docker UI works on Linux, Windows WSL and macOS Colima become provider implementations instead of separate apps.

---

## 24. Product identity summary

Cairn should feel like this:

```text
Clean
Fast
Project-first
Transparent
Developer-friendly
Home-lab friendly
Safe around destructive actions
Better than Docker Desktop for Compose workflows
```

The core promise:

```text
Cairn gives you a clean desktop control center for Docker and Compose without replacing the Docker engine you already trust.
```


---

## 25. Image Lineage and Base Image Updates

Cairn should track update requirements beyond direct service image digests. A Compose service can be locally built from a Dockerfile, and that Dockerfile can reference a base image that changes upstream. In that case, the correct action is not a simple pull. The service must be rebuilt with `docker compose build --pull` and then redeployed.

### Update kinds

```text
Service image update
  The service uses `image:` and the remote digest changed.

Base image update
  The service uses `build:` and one or more Dockerfile FROM images changed.
```

### Required tracking levels

```text
Per container
Per Compose service
Per Compose project
```

### Required lineage sources

```text
Compose build config
Dockerfile FROM parser
Compose labels
OCI image annotations when available
Cairn-specific image labels for builds triggered by Cairn
```

### Required UI behavior

```text
Show image update and base update badges separately.
Show Image Lineage on container detail pages.
Show base update/rebuild plans on project update pages.
Explain unknown base image cases honestly.
Preview exact commands before pull, rebuild, or redeploy.
```

### Required update commands

For service image updates:

```bash
docker compose pull <service>
docker compose up -d <service>
```

For base image updates:

```bash
docker compose build --pull <service>
docker compose up -d <service>
```

For mixed project updates:

```bash
docker compose pull <image-backed-services>
docker compose build --pull <locally-built-services>
docker compose up -d <changed-services>
```

Detailed rules are specified in `docs/IMAGE_LINEAGE.md` and `docs/UPDATE_SYSTEM.md`.
