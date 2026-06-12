# Cairn Image Lineage and Base Image Tracking

## 1. Goal

Cairn should track **both normal image updates and base image updates** at container, service, and project level.

This is a first-class product feature, not only an update badge. It allows Cairn to answer:

```text
Is this container image itself outdated?
Was this locally built image built FROM an outdated base image?
Does this project need a pull, a rebuild, or both?
Which services are affected by a base image change?
```

This feature is a major differentiator because many Docker tools only show whether a service image tag has changed. Cairn should also show when a locally built service image needs to be rebuilt because its Dockerfile base image has changed upstream.

---

## 2. Update types

Cairn must distinguish two update kinds.

### 2.1 Service image update

A service uses a registry image directly:

```yaml
services:
  web:
    image: nginx:1.25
```

Cairn checks whether the remote digest for `nginx:1.25` differs from the local digest.

Recommended action:

```bash
docker compose pull web
docker compose up -d web
```

### 2.2 Base image update

A service is built locally:

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
WORKDIR /app
COPY package*.json ./
RUN npm ci
COPY . .
CMD ["node", "server.js"]
```

Cairn checks whether `node:20-alpine` has a newer remote digest than the base digest used when the local image was built.

Recommended action:

```bash
docker compose build --pull api
docker compose up -d api
```

---

## 3. Entity relationships

Cairn should track lineage at three levels.

```text
Project
  -> Service
     -> Container
        -> Running service image
           -> Build metadata
              -> Dockerfile
                 -> Base image references
```

Example:

```text
Project: apps
Service: api
Container: api-1
Service image: cairn/apps-api:local
Dockerfile: ./api/Dockerfile
Base image: node:20-alpine
Built with base digest: sha256:aaa...
Remote base digest:     sha256:bbb...
Status: Base image update available
Action: Rebuild & redeploy service
```

---

## 4. Detection sources

Cairn should use several sources, ordered by confidence.

### 4.1 Compose service config

For each Compose service, inspect resolved Compose config.

Useful fields:

```yaml
services:
  api:
    image: cairn/apps-api:local
    build:
      context: ./api
      dockerfile: Dockerfile
      target: runner
      args:
        NODE_VERSION: 20
```

This tells Cairn:

```text
The service is locally built.
The build context is known.
The Dockerfile path is known.
The build target may be known.
Base image tracking is possible.
```

### 4.2 Dockerfile parser

Parse `FROM` instructions.

Simple Dockerfile:

```dockerfile
FROM node:20-alpine
```

Multi-stage Dockerfile:

```dockerfile
FROM golang:1.23-alpine AS builder
FROM alpine:3.20 AS runtime
```

Cairn should track:

```text
golang:1.23-alpine -> build-stage base
alpine:3.20        -> final/runtime base
```

For a Compose service with `build.target`, Cairn should identify the stage selected by the target. If no target is set, the last stage is the final runtime stage.

### 4.3 Image labels and OCI annotations

When present, Cairn should read base image metadata from image labels and OCI annotations.

Useful keys:

```text
org.opencontainers.image.base.name
org.opencontainers.image.base.digest
org.opencontainers.image.source
org.opencontainers.image.revision
```

Cairn-specific labels should use the Cairn namespace:

```text
io.cairn.project
io.cairn.service
io.cairn.compose.file
io.cairn.dockerfile
io.cairn.base.name
io.cairn.base.digest
io.cairn.build.time
io.cairn.build.platform
```

Do not define new keys under `org.opencontainers.image.*` unless they are established OCI annotation keys.

### 4.4 Compose labels

For running containers created by Compose, Cairn should map containers back to project and service using labels:

```text
com.docker.compose.project
com.docker.compose.service
com.docker.compose.project.working_dir
com.docker.compose.project.config_files
```

### 4.5 Build logs and local build history

Optional after v1:

```text
Parse recent build output
Record base digests after Cairn-triggered builds
Attach Cairn labels to images built through Cairn
```

---

## 5. Confidence levels

Cairn should be honest about how reliable a lineage result is.

```text
High
  Derived from Compose build config + Dockerfile + Cairn-recorded build digest.

Medium
  Derived from Compose build config + Dockerfile, but build-time base digest is unknown.

Low
  Derived from image metadata labels only.

Unknown
  No reliable base image information available.
```

UI wording example:

```text
Base image: node:20-alpine
Confidence: High
Reason: Found from Compose build config and Dockerfile.
```

---

## 6. Important limitations

Cairn cannot always know the base image for third-party registry images.

Example:

```yaml
services:
  db:
    image: postgres:16
```

For this service, Cairn can track:

```text
postgres:16 image update available
```

But Cairn may not reliably know:

```text
postgres:16 was built from debian:bookworm-slim
```

unless the image publishes base metadata, provenance, or an SBOM that Cairn can use.

UI should show:

```text
Base image: Unknown
Reason: This is a third-party registry image and no base metadata was found.
```

---

## 7. Update status model

Update status values:

```text
Unknown
Checking
UpToDate
ServiceImageUpdateAvailable
BaseImageUpdateAvailable
RebuildRequired
PinnedDigest
BuiltLocally
UnknownBaseImage
LocalOnlyImage
RegistryAuthRequired
RateLimited
CheckFailed
Ignored
```

Update kinds:

```text
service_image
base_image
```

Recommended UI grouping:

```text
Image updates
Base image updates
Rebuild required
Pinned images
Unknown base images
Ignored
Errors
```

---

## 8. UI requirements

### 8.1 Project card

Project cards should show separate update counts:

```text
apps
5 / 5 services
Running

Updates:
  2 image updates
  1 base update
  1 rebuild needed
```

Suggested badges:

```text
Image update
Base update
Rebuild needed
Pinned digest
Unknown base
```

### 8.2 Container detail page

Add an **Image Lineage** section.

Example:

```text
Container: api-1
Service: api
Project: apps

Running image:
  cairn/apps-api:local

Built from:
  node:20-alpine

Base digest at build:
  sha256:aaa...

Current remote base digest:
  sha256:bbb...

Status:
  Base image update available

Recommended action:
  Rebuild service and redeploy
```

### 8.3 Project detail → Updates tab

Show an update plan grouped by action type.

```text
Project: apps

Pull & recreate:
  web     nginx:1.25
  redis   redis:7-alpine

Rebuild & redeploy:
  api     node:20-alpine
  worker  python:3.12-slim
```

### 8.4 Updates page

Columns:

```text
Project
Service
Container
Update type
Current image
Base image
Local digest
Remote digest
Confidence
Recommended action
```

Example rows:

```text
apps  api     api-1  Base update   node:20-alpine       sha256:aaa -> sha256:bbb  Rebuild
apps  web     web-1  Image update  nginx:1.25           sha256:ccc -> sha256:ddd  Pull & recreate
llms  ollama  ollama Image update  ollama/ollama:latest sha256:eee -> sha256:fff  Pull & recreate
```

---

## 9. Actions

### 9.1 Service image update action

For services using `image:`:

```bash
docker compose pull <service>
docker compose up -d <service>
```

### 9.2 Base image update action

For services using `build:`:

```bash
docker compose build --pull <service>
docker compose up -d <service>
```

### 9.3 Project-wide mixed update action

For a project with both pull and rebuild actions, Cairn should display an ordered plan:

```bash
# Pull registry images
docker compose pull web redis

# Rebuild local services with newer bases
docker compose build --pull api worker

# Recreate changed services
docker compose up -d web redis api worker
```

Cairn should preview commands before execution and record them in the audit log.

---

## 10. Rollback considerations

Before an update, Cairn should record:

```text
Previous service image ID
Previous service image digest
Previous base image digest if known
Compose config snapshot
Build args used if known
Dockerfile path and content hash
Named volumes involved
```

Rollback for base-image rebuilds is harder than normal pull updates. If Cairn cannot safely restore the old local built image, it should provide manual recovery guidance instead of promising automatic rollback.

---

## 11. Data model summary

Core tables:

```text
image_lineage
base_image_refs
image_update_checks
update_history
ignored_updates
```

See `DATA_MODEL.md` for SQL details.

---

## 12. API summary

Core service:

```text
ImageLineageService
```

Core methods:

```text
DiscoverProjectLineage
GetServiceLineage
GetContainerLineage
CheckBaseImageUpdates
PlanProjectUpdates
```

See `API_DESIGN.md` for Go interface details.
