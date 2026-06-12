# Module 09 — Update System

`internal/updates` — checker, planner, executor, health watcher, rollback. The product's flagship feature together with modules/10.

## 1. Update kinds

```text
service_image : service uses image:  → remote digest differs → pull & recreate
base_image    : service uses build:  → a Dockerfile FROM digest changed → rebuild & redeploy
```

## 2. Checker

For each project service (from compose config + lineage):

```text
image-backed service:
  pinned digest? → pinned_digest, stop
  resolve local digest (RepoDigests of running image)
  remote digest via registry module (daemon platform)
  equal → up_to_date ; differ → service_image_update_available
  latest-family tag → attach mutable-tag warning

built service:
  status built_locally as baseline
  lineage exists with base refs?
    per base ref: known build-time digest (high conf) else local digest of base image else none
    remote digest per base ref
    final-stage base changed → rebuild_required
    non-final stage changed → base_image_update_available (lower urgency)
    no reliable comparison → unknown_base_image
```

Results persist to `image_update_checks`; latest-per-target view feeds `ListCurrentUpdates` and badge counts. Ignored items (match on image_ref+kind+scope) are excluded from badges, shown under Ignored filter. Scheduler: per settings interval, jittered, skipped offline; manual `CheckAllUpdates` emits `updates:check:progress`.

## 3. Planner

`PlanServiceUpdate` / `PlanProjectUpdate` produce `UpdatePlan` ([04-api-contracts.md §5]) with ordered commands:

```bash
# mixed project example
docker compose pull web redis
docker compose build --pull api worker
docker compose up -d web redis api worker
```

Rules: only services with actionable statuses included; pinned/ignored/unknown listed under Warnings with explanations; `up -d` lists exactly the changed services; plans expire in 10 min; risk = `needs_confirmation` (more if force-recreate involved).

## 4. Executor

Steps (all audited, progress via `job:progress`):
1. Optional volume backup (named volumes of affected services; modules/11).
2. Rollback snapshot: old image IDs+digests, old base digests, compose config (`config` output), dockerfile hash, container IDs, volumes list → `update_history` row (status started).
3. Run plan commands sequentially via compose wrapper; stop on first failure.
4. Health watch (§5) if enabled.
5. Finalize history (result, new digests, health_result, rollback_status) → `updates:applied` event + notification.

## 5. Health watcher

Window 60 s (configurable) per recreated service: container running; healthcheck (if defined) reaches healthy; restart count stable (no crash loop: <2 restarts); log scan for fatal patterns (`panic:`, `Fatal`, `Exception in thread`, exit-on-start). Outcomes: `success | success_warn (no healthcheck defined, heuristics only) | failed`.

## 6. Rollback

- `service_image`: previous image ID still present → `docker tag <oldID> <ref>` + `up -d <service>`; mark `rolled_back`. Image gone (pruned) → `manual_needed` + guidance (pull old tag if versioned).
- `base_image` rebuild: previous built image ID present → retag + `up -d` (compose must not rebuild: use `--no-build`); else `manual_needed` with honest explanation (old local build lost).
- Auto-rollback only when user enabled `RollbackOnFailure` and the safe path exists. Manual `Rollback(historyID)` available while `rollback_status=available`.

## 7. Tests

Normative cases 1–11 in [06-testing.md §5]. Plus unit: status machine table, planner ordering/exclusions, fatal-log patterns, plan expiry; integration: backup-then-update path; rollback with pruned old image → manual_needed.
