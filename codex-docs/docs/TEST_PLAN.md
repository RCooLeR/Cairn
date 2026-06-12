# Cairn Test Plan

## 1. Test strategy

Cairn should be tested at four levels:

```text
Unit tests
Provider integration tests
Docker integration tests
End-to-end UI tests
```

---

## 2. Platform test matrix

### Windows

```text
Windows 11 x64
WSL installed
WSL not installed
Ubuntu installed
Ubuntu missing
Multiple Ubuntu distros
Docker installed inside Ubuntu
Docker missing inside Ubuntu
systemd enabled
systemd disabled
```

### Linux

```text
Ubuntu LTS
Debian stable
Docker installed
Docker missing
User in docker group
User not in docker group
Docker service stopped
Rootless Docker if detected
```

### macOS

```text
Apple Silicon
Intel if available
Homebrew installed
Homebrew missing
Colima installed
Colima missing
Existing Docker Desktop context
Existing remote context
```

---

## 3. Core acceptance tests

```text
docker ping succeeds
Docker version loads
Compose version loads
Containers list loads
Images list loads
Volumes list loads
Networks list loads
Stats stream works
Logs stream works
Container terminal opens
Compose projects are grouped by labels
```

---

## 4. Compose tests

Use sample projects:

```text
single-service app
app + database
reverse proxy + app
healthcheck project
project with named volume
project with custom network
project with .env file
project with build context
```

Tests:

```text
Project detected
Services grouped correctly
Project start works
Project stop works
Project restart works
Project pull works
Project logs stream
Project stats aggregate
```

---

## 5. Update system tests

Cases:

```text
Versioned tag update
latest tag update
Pinned digest
Private registry auth required
Local build service
Registry unavailable
Rate limit/error
Healthcheck success
Healthcheck failure
Rollback available
```

---

## 6. Safety tests

Verify confirmations for:

```text
Delete volume
Delete running container
Prune images
Prune volumes
compose down --volumes
Reset provider/backend
```

Verify audit logs for:

```text
Start/stop/restart container
Project update
Volume backup
Volume delete
Prune action
Provider install/start/stop
```

---

## 7. Performance tests

Test with:

```text
100 containers
500 images
200 volumes
Large log streams
High-frequency stats
Multiple projects
Slow remote context
```

Targets:

```text
Dashboard loads quickly
UI does not freeze during logs
Stats charts remain smooth
Search/filter remains responsive
SQLite storage does not grow without retention
```

---

## 8. v1 release checklist

```text
All P0 features pass on Linux
All P0 features pass on Windows WSL
All P0 features pass on macOS Colima/existing context
No destructive action runs without confirmation
No registry password stored in plaintext
No Docker TCP exposure enabled by default
Installer/uninstaller behaves cleanly
App handles Docker stopped state gracefully
```


---

## Image lineage and base image update tests

### Direct service image update

```text
Create Compose service with image: nginx:1.25
Pull an older digest if available or mock registry digest comparison
Verify Cairn marks service image update available
Verify recommended command is docker compose pull + docker compose up -d
```

### Local build base image update

```text
Create Compose service with build: ./api
Dockerfile uses FROM node:20-alpine
Record build-time base digest
Mock or detect newer remote base digest
Verify Cairn marks base image update available
Verify recommended command is docker compose build --pull + docker compose up -d
```

### Multi-stage Dockerfile

```text
Dockerfile uses builder and runtime stages
Verify Cairn discovers all FROM references
Verify final runtime stage is marked as final-stage base
Verify build-stage updates and runtime-stage updates are shown separately
```

### Unknown base image

```text
Create service using image: postgres:16
No local Dockerfile available
No base metadata available
Verify Cairn shows service image update status only
Verify UI says base image is unknown instead of guessing
```

### Mixed project update plan

```text
Project contains image-backed services and locally built services
Verify Cairn produces ordered plan:
  docker compose pull <image-backed-services>
  docker compose build --pull <built-services>
  docker compose up -d <changed-services>
```
