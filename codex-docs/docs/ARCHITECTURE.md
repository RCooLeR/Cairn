# Cairn Architecture

## 1. Architectural goal

Cairn must behave as one app across Windows, Linux, and macOS, while hiding platform-specific differences behind provider adapters.

The core app should not care whether Docker is running:

```text
- natively on Linux
- inside Ubuntu on WSL2
- inside Colima/Lima on macOS
- on a remote SSH host
- under an existing Docker context
```

The provider layer owns those differences.

---

## 2. System diagram

```mermaid
flowchart TD
    UI[Frontend UI]
    Backend[Go Backend]
    Store[(SQLite Store)]
    DockerAPI[Docker Engine API]
    ComposeCLI[docker compose CLI]
    Provider[Provider Manager]

    UI --> Backend
    Backend --> Store
    Backend --> DockerAPI
    Backend --> ComposeCLI
    Backend --> Provider

    Provider --> WSL[Windows WSL Ubuntu Provider]
    Provider --> Linux[Linux Native Provider]
    Provider --> Mac[macOS Colima Provider]
    Provider --> Existing[Existing Docker Context Provider]
    Provider --> Remote[Remote SSH Provider]
```

---

## 3. Core layers

### 3.1 Frontend UI

Responsibilities:

```text
Rendering pages
Showing charts
Showing logs
Showing terminal sessions
Triggering backend actions
Displaying confirmation modals
Showing real command previews
```

The frontend should not execute shell commands directly.

### 3.2 Go backend

Responsibilities:

```text
Docker API communication
Compose CLI execution
Provider installation/detection
Project grouping
Metrics aggregation
Log streaming
Terminal session creation
Image update checks
SQLite persistence
Audit logging
```

### 3.3 Provider manager

Responsibilities:

```text
Detect available providers
Select active provider/context
Start/stop backend services
Map paths
Run platform-specific commands
Return Docker connection details
```

### 3.4 Docker API client

Use for:

```text
Containers
Images
Volumes
Networks
Stats
Logs
Exec
Events
Inspections
```

### 3.5 Compose CLI wrapper

Use for:

```text
up/down/restart/pull
config validation
project lifecycle
service-level operations
project logs where Compose semantics matter
```

---

## 4. Event flow

### App startup

```mermaid
sequenceDiagram
    participant UI
    participant Backend
    participant Providers
    participant Docker
    participant Store

    UI->>Backend: Start app
    Backend->>Providers: Detect providers
    Providers-->>Backend: Provider statuses
    Backend->>Docker: Ping active Docker daemon
    Docker-->>Backend: OK / error
    Backend->>Store: Load settings/cache
    Backend-->>UI: Dashboard state
```

### Project detection

```mermaid
sequenceDiagram
    participant Backend
    participant Docker
    participant Compose
    participant Store

    Backend->>Docker: List containers with labels
    Docker-->>Backend: Containers
    Backend->>Backend: Group by com.docker.compose.project
    Backend->>Compose: docker compose ls
    Compose-->>Backend: Known projects
    Backend->>Store: Persist project cache
```

### One-click service update

```mermaid
sequenceDiagram
    participant UI
    participant Backend
    participant Updates
    participant Compose
    participant Docker
    participant Store

    UI->>Backend: Update service
    Backend->>Store: Create audit entry
    Backend->>Updates: Build update plan
    Updates-->>Backend: Plan
    Backend-->>UI: Confirmation with commands
    UI->>Backend: Confirm
    Backend->>Compose: docker compose pull service
    Compose-->>Backend: Pull result
    Backend->>Compose: docker compose up -d service
    Compose-->>Backend: Recreate result
    Backend->>Docker: Watch health/logs
    Docker-->>Backend: Healthy / failed
    Backend->>Store: Save update history
    Backend-->>UI: Result
```

---

## 5. Module boundaries

```text
internal/providers
  Platform detection, install, start/stop, path mapping.

internal/docker
  Docker API wrapper.

internal/compose
  docker compose CLI wrapper and Compose project discovery.

internal/metrics
  Stats collection, aggregation, retention.

internal/updates
  Registry digest checks, update plans, update execution.

internal/terminal
  Host/backend/container terminal management.

internal/backups
  Volume backup/restore.

internal/security
  Confirmation rules, audit log, permission checks.

internal/store
  SQLite persistence and migrations.
```

---

## 6. Design constraints

```text
Never assume Docker Desktop exists.
Never require Docker Desktop.
Never implement a custom runtime in v1.
Never bypass user confirmation for destructive actions.
Never store secrets unencrypted.
Use Docker API for live state.
Use Compose CLI for Compose lifecycle behavior.
Use provider adapters for OS differences.
```
