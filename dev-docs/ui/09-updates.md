# UI 09 — Updates Page

One clean place for service-image and base-image updates. The trust-critical surface.

## 1. Layout

Header: title + last-check time ("Checked 2 h ago"), [Check now] (progress bar via `updates:check:progress`), FilterChips, view tabs **Current · History · Ignored**.

FilterChips: All · Image updates · Base updates · Rebuild required · Pinned · Unknown base · Errors (auth/rate-limit/failed) · Up to date.

## 2. Current table

Columns: **Project** (link) · **Service** · **Container** · **Update type** (badge: Image/Base) · **Current image** · **Base image** ("—" for image-backed) · **Local digest → Remote digest** (truncated, copy, arrow colored yellow when differing) · **Confidence** (chip + ⓘ reason tooltip) · **Status/notes** (mutable-tag warning ⚠ on `latest`; auth-required with [How to fix] popover; rate-limited with retry countdown) · **Last checked** · actions.

Row actions: [Update…] (opens confirmation modal §4) · kebab(Check this now, Pull only, Ignore…, Copy digests). Project group headers with [Update project…] when >1 actionable row.

## 3. Grouping logic

Rows grouped by project (collapsible); within group ordered: rebuild_required → base updates → image updates → warnings/errors → pinned/unknown (collapsed under "Not actionable (4)" subrow).

## 4. Update confirmation modal (normative)

```text
Update service "api" in project "apps"                       [✕]
Kind: Base image update → Rebuild & redeploy
─────────────────────────────────────────────
Service image   cairn/apps-api:local
Base image      node:20-alpine
Base digest     sha256:aaa1… → sha256:bbb2…      Confidence: High ⓘ
─────────────────────────────────────────────
Commands                                              [copy]
  $ docker compose build --pull api
  $ docker compose up -d api
  workdir: ~/stacks/apps        context: linux_native
─────────────────────────────────────────────
☐ Back up named volumes first (postgres_data, redis_data)
☑ Watch health after update (60 s)
☑ Roll back automatically if health check fails
   ⓘ Rollback possible: previous image kept locally
─────────────────────────────────────────────
                         [Cancel]  [Update service]
```
Project-level (mixed) variant lists the ordered phases (Pull → Rebuild → Recreate) with per-phase service chips and the exact 3 commands. Rollback line is honest: when not safely possible → "⚠ Automatic rollback unavailable for this rebuild — manual guidance will be provided."

## 5. Apply progress & result

Modal transitions to progress view: step list (Backup → Pull/Build → Recreate → Health watch) with live output expander; cancel allowed until recreate phase. Result: success (green summary + new digest) / success_warn / failed → if rollback ran: status; else [Show manual rollback steps] (copyable commands). Result also lands in History + notification.

## 6. History tab

Table: time, project/service, kind, old→new digest, result chip (success/rolled_back/failed/manual_needed), duration, [Details] drawer (full commands, health result, error, rollback status; [Rollback] button while available). Filterable by result/project.

## 7. Ignored tab

Rows with reason + scope; [Unignore]. Ignore modal (from Current): scope radio (this service / this image everywhere), optional reason.

## 8. Tests

E2E journeys 3–4 ([06-testing.md §8]); modal content golden vs planner output; mixed-plan ordering; ignore/unignore round-trip; history rollback flow; auth-required popover copy.
