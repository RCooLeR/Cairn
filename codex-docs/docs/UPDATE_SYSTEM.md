# Cairn Update System

## 1. Goal

Cairn should detect update needs and allow safe service/project upgrades with one click.

The update system must track two different kinds of changes:

```text
1. Service image updates
2. Base image updates for locally built services
```

This is one of Cairn's strongest differentiators. A project may have no obvious service image update but still need a rebuild because one of its Dockerfile base images changed upstream.

---

## 2. Core concepts

### Update target types

```text
Container
Compose service
Compose project
Image
Base image
```

### Update kinds

```text
service_image
base_image
```

### Image types

```text
Registry image: nginx:latest
Versioned tag: postgres:16
Pinned digest: redis@sha256:...
Local build: built from Dockerfile
Private registry image
Multi-arch image
```

### Update states

```text
Unknown
Checking
Up to date
Service image update available
Base image update available
Rebuild required
Pinned digest
Built locally
Unknown base image
Local-only image
Registry auth required
Rate limited
Check failed
Ignored
```

---

## 3. Service image update detection

Use this path for Compose services using `image:` directly.

```yaml
services:
  web:
    image: nginx:1.25
```

Detection flow:

```text
1. List Compose projects.
2. For each service, detect image reference.
3. Resolve local image ID/digest.
4. Query registry manifest/digest for the same tag and current platform.
5. Compare local digest and remote digest.
6. Store result in SQLite.
7. Show update badges in Dashboard, Projects, Images, Containers, and Updates pages.
```

Recommended service update commands:

```bash
docker compose pull web
docker compose up -d web
```

Recommended project update commands:

```bash
docker compose pull
docker compose up -d
```

---

## 4. Base image update detection

Use this path for Compose services using `build:`.

```yaml
services:
  api:
    image: cairn/apps-api:local
    build:
      context: ./api
      dockerfile: Dockerfile
```

Dockerfile:

```dockerfile
FROM node:20-alpine AS base
```

Detection flow:

```text
1. Read resolved Compose config.
2. Find services with build configuration.
3. Resolve build context and Dockerfile path.
4. Parse Dockerfile FROM lines.
5. Resolve base image references and stages.
6. Determine build-time base digest if known.
7. Query remote registry digest for each base image.
8. Compare build-time/local base digest with remote digest.
9. Store lineage and base update results.
10. Mark service/project as rebuild required when base changed.
```

Recommended service rebuild commands:

```bash
docker compose build --pull api
docker compose up -d api
```

Recommended project rebuild commands:

```bash
docker compose build --pull
docker compose up -d
```

For mixed projects, Cairn should generate an ordered update plan:

```bash
# Pull registry images
docker compose pull web redis

# Rebuild local services whose base images changed
docker compose build --pull api worker

# Recreate changed services
docker compose up -d web redis api worker
```

---

## 5. Special cases

### latest tag

`latest` is mutable. Cairn can check it, but the UI should show a note:

```text
This image uses a mutable tag. The remote image may change without a version number change.
```

### pinned digest

For images like:

```text
nginx@sha256:abc123...
```

Cairn should not show normal updates. It should show:

```text
Pinned digest
```

### local build services

For Compose services with `build:`, Cairn should not pretend a pull is enough.

Show:

```text
Locally built service
Base image tracking available when Dockerfile can be resolved
Rebuild required when a base image digest changed
```

Possible action:

```bash
docker compose build --pull <service>
docker compose up -d <service>
```

### multi-stage Dockerfiles

For Dockerfiles such as:

```dockerfile
FROM golang:1.23-alpine AS builder
FROM alpine:3.20 AS runtime
```

Cairn should track both base images but mark the final/runtime stage as the most important for runtime risk.

### third-party registry images

For a service like:

```yaml
services:
  db:
    image: postgres:16
```

Cairn can track the `postgres:16` image digest. It usually cannot reliably know the upstream base image unless metadata/provenance is available.

Show:

```text
Base image: Unknown
Reason: third-party image with no base metadata available
```

### private registries

If registry auth is required:

```text
Show auth required
Use Docker credential helper where possible
Do not store registry passwords in plaintext
```

---

## 6. Update confirmation modal

The confirmation screen should include:

```text
Project name
Service name
Update kind
Current image
Base image if applicable
Local digest
Remote digest
Confidence level
Commands to run
Backup named volumes option
Health check option
Rollback option
```

Example for direct image update:

```text
Update service "web" in project "apps"

Kind:
  Service image update

Current image:
  nginx:1.25

Commands:
  docker compose pull web
  docker compose up -d web
```

Example for base image update:

```text
Rebuild service "api" in project "apps"

Kind:
  Base image update

Service image:
  cairn/apps-api:local

Base image:
  node:20-alpine

Commands:
  docker compose build --pull api
  docker compose up -d api
```

---

## 7. Health monitoring

After update, watch:

```text
Container status
Healthcheck status
Restart count
Recent logs
Exit code
Startup timeout
```

Result states:

```text
Success
Success with warnings
Failed
Rollback available
Rollback attempted
Manual intervention required
```

---

## 8. Rollback strategy

Before update, record:

```text
Previous image ID
Previous image tag
Previous service image digest
Previous base image digest if known
Previous compose config
Dockerfile content hash
Build args if known
Previous container IDs
Named volumes involved
```

Automatic rollback should be conservative. Base-image rebuild rollback is not always safe unless Cairn still has the previous built image ID and can retag/recreate from it.

Possible rollback command for service image update:

```bash
docker tag <previous-image-id> <image-ref>
docker compose up -d <service>
```

For base image rebuilds, if the previous local built image is unavailable, Cairn should show manual guidance rather than pretending it can restore everything.

---

## 9. Related document

Detailed lineage detection rules are defined in:

```text
docs/IMAGE_LINEAGE.md
```
