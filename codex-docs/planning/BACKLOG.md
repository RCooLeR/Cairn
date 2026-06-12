# Cairn Backlog

## Epic: Provider setup

### Story: Detect local Docker

As a user, I want Cairn to detect my existing Docker setup so that I can start managing containers immediately.

Acceptance criteria:

```text
Detect docker CLI
Detect Docker daemon
Detect Docker context
Detect docker compose
Show status and errors
```

### Story: Install Docker on Linux

As a Linux user, I want Cairn to install official Docker packages so that I do not need Docker Desktop.

Acceptance criteria:

```text
Detect supported distro
Show install plan
Ask for confirmation
Install official packages
Start docker service
Verify docker and compose
```

### Story: Use Ubuntu WSL on Windows

As a Windows user, I want Cairn to use Ubuntu WSL so that I can run Linux containers without Docker Desktop.

Acceptance criteria:

```text
Detect WSL
Detect Ubuntu
Detect Docker inside Ubuntu
Start Docker inside Ubuntu
Run docker commands through WSL
Show repair hints
```

### Story: Use Colima on macOS

As a macOS user, I want Cairn to use Colima or an existing Docker context so that I can manage containers without Docker Desktop.

Acceptance criteria:

```text
Detect Colima
Start Colima
Use Colima context
Connect Docker API
Fallback to existing context
```

---

## Epic: Docker object management

### Story: Container list

Acceptance criteria:

```text
Show all containers
Filter by status/project/service
Start/stop/restart
Open logs
Open terminal
Inspect JSON
```

### Story: Image list

Acceptance criteria:

```text
Show repository/tag/size/date
Show usage relationship
Pull image
Remove image with confirmation
```

### Story: Volume list

Acceptance criteria:

```text
Show volumes
Show project/container usage
Estimate size where possible
Backup volume
Delete with confirmation
```

### Story: Network list

Acceptance criteria:

```text
Show networks
Show subnets/gateways
Show connected containers
Inspect network
```

---

## Epic: Compose projects

### Story: Auto-detect projects

Acceptance criteria:

```text
Read Compose labels
Group by project
Group by service
Show project cards
Show project detail
```

### Story: Project actions

Acceptance criteria:

```text
Start project
Stop project
Restart project
Pull images
Redeploy
Show commands before execution
```

### Story: Project logs

Acceptance criteria:

```text
Stream logs from all services
Filter by service
Search logs
Pause/resume
Export logs
```

---

## Epic: Updates

### Story: Detect service image updates

Acceptance criteria:

```text
Detect image refs
Compare local and remote digest
Handle latest tags
Handle pinned digests
Handle private registry errors
Show service image update badges
```

### Story: Detect base image updates

Acceptance criteria:

```text
Detect Compose services with build configuration
Resolve build context and Dockerfile path
Parse Dockerfile FROM references
Handle multi-stage Dockerfiles
Compare build-time or local base digest with remote digest
Show base image update and rebuild-required badges
Explain unknown base image cases
```

### Story: One-click service image update

Acceptance criteria:

```text
Show update plan
Show commands
Offer backup
Run docker compose pull <service>
Run docker compose up -d <service>
Watch health
Record history
```

### Story: One-click base image rebuild

Acceptance criteria:

```text
Show rebuild plan
Show base image digest comparison
Show commands
Offer backup
Run docker compose build --pull <service>
Run docker compose up -d <service>
Watch health
Record history
```

---

## Epic: Terminals and commands

### Story: Container terminal

Acceptance criteria:

```text
Detect shells
Open shell
Resize terminal
Support copy/paste
Show user/root indicator
```

### Story: Command cheatsheet

Acceptance criteria:

```text
Search commands
Show description
Show risk label
Copy command
Run safe commands
Confirm destructive commands
```
