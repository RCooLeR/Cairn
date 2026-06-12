# Cairn v0.2 Documentation Patch Notes

This update adds first-class **Image Lineage** and **Base Image Update Tracking**.

## Changed docs

```text
README.md
CHANGELOG.md
docs/SPEC.md
docs/FEATURES.md
docs/UPDATE_SYSTEM.md
docs/IMAGE_LINEAGE.md
docs/DATA_MODEL.md
docs/API_DESIGN.md
docs/UI_UX.md
docs/TEST_PLAN.md
docs/REFERENCES.md
planning/ROADMAP.md
planning/BACKLOG.md
scaffolding/go/internal/models/models.go
scaffolding/go/internal/services/interfaces.go
```

## Main product change

Cairn now tracks updates in two categories:

```text
Service image update
  Use docker compose pull + docker compose up -d.

Base image update
  Use docker compose build --pull + docker compose up -d.
```

## UI impact

Cairn should show these separately:

```text
Image update
Base update
Rebuild required
Pinned digest
Unknown base
```

## Backend impact

New logical module:

```text
ImageLineageService
```

New persistent data:

```text
image_lineage
base_image_refs
image_update_checks.kind
update_history.update_kind
```
