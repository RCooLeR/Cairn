# Cairn Project Specification Package

**Cairn** is a cross-platform, Compose-first Docker management app for Windows, macOS, and Linux.

It is designed as a clean Docker Desktop alternative focused on management, visibility, terminals, Compose projects, charts, and safe image updates. Cairn does **not** implement a custom container runtime. It installs, detects, configures, and manages existing Docker-compatible backends.

## Core direction

```text
Windows  -> Ubuntu on WSL2 + official Docker Engine + Docker Compose plugin
Linux    -> native official Docker Engine + Docker Compose plugin
macOS    -> Colima/existing Docker context + Docker CLI/Compose
```

## Package contents

```text
README.md
CHANGELOG.md

/docs
  SPEC.md                  Full product and technical specification
  ARCHITECTURE.md          System architecture and module boundaries
  PLATFORM_PROVIDERS.md    Windows/Linux/macOS provider behavior
  FEATURES.md              Feature list and priority levels
  UI_UX.md                 Screens, flows, layout, and interface behavior
  UPDATE_SYSTEM.md         One-click image and base image update design
  IMAGE_LINEAGE.md          Base image and service image lineage tracking
  DATA_MODEL.md            SQLite schema and domain models
  API_DESIGN.md            Go service/API boundaries
  SECURITY.md              Permissions, Docker socket risk, destructive actions
  TEST_PLAN.md             Compatibility and acceptance test plan
  REFERENCES.md            Relevant upstream documentation links

/planning
  ROADMAP.md               Milestones from MVP to v1
  BACKLOG.md               Epics and user stories

/scaffolding/go
  README.md                Notes for future Go implementation
  internal/providers/provider.go
  internal/models/models.go
  internal/services/interfaces.go

/references/screenshots
  Reference screenshots from the concept discussion
```

## Best short description

> Cairn is a clean Compose-first Docker manager for Windows, macOS, and Linux. It uses official Docker packages and existing Docker-compatible backends, then adds a polished desktop dashboard, terminals, logs, charts, project views, image lineage tracking, base image update detection, and safe one-click updates.

## Non-goals for v1

Cairn v1 should not include:

```text
Custom container runtime
Custom VM runtime
Kubernetes dashboard
Enterprise policy management
Windows containers
Registry server
Multi-user cloud mode
AI assistant features
```

## v0.2 documentation update

This package now treats **Image Lineage** as a first-class Cairn feature. Cairn should track both direct service image updates and base image updates for locally built Compose services. That means the app can tell whether a project needs a simple pull/recreate or a rebuild/redeploy because a Dockerfile `FROM` image changed upstream.

