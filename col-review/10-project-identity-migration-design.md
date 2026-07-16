# Canonical Project Identity Migration Design

## Purpose

This design closes the remaining identity gap described by BE-005. The immediate
BE-006, BE-007, and BE-018 remediation makes runtime operations and derived
caches fail closed on an exact provider/backend scope, but the current
`projects.id` is still derived from only provider ID and Compose project name.
That legacy key cannot represent two same-named projects in separate contexts or
two imports with the same Compose name.

This migration must preserve user-visible names and history while changing the
internal identity graph. It must not guess when legacy data is ambiguous.

## Interim containment implemented before v2 identity

The remediation snapshot preceding the v2 migration deliberately narrows what
the legacy schema is allowed to authorize. It does not redefine the existing
global project ID or infer a missing origin:

- migration `0007` gives reconstructible Docker object caches an exact
  `(provider_id, context_name, native_id)` key and discards legacy cache rows
  whose runtime context cannot be established safely;
- migration `0008` persists metrics under an exact provider/context pair;
  legacy blank-context samples remain an inaccessible quarantine bucket and are
  aged independently by global retention work rather than merged into an
  active context;
- migration `0009` applies the same exact scope to lineage, update checks,
  ignored updates, and update history; blank-context history is retained but
  cannot authorize or influence work in a later context;
- runtime repositories, Compose/Docker clients, terminals, registry clients,
  metrics, lineage, update application, and rollback all require the immutable
  runtime scope captured when the runtime was built;
- automatic detection skips each legacy project or service-ID collision
  individually, leaves the conflicting rows unchanged, and can still commit
  unrelated projects from the same snapshot; explicit/import mutation paths
  fail closed on the same collisions; and
- stale cleanup is exact-scope, so one provider context cannot delete another
  context's projects, services, caches, metrics, lineage, or update state.

This containment prevents cross-context reuse and accidental legacy ownership,
but same-named projects still cannot coexist where the global legacy
`projects.id` collides. Imports also still lack the stable origin discriminator
and ambiguity-resolution workflow defined below. Consequently BE-005 remains
partial until the v2 identity graph is implemented.

## Required invariants

1. A runtime scope is non-empty and immutable for one runtime binding. It is the
   pair `(provider_id, backend_identity)`, where `backend_identity` is the WSL
   distribution, Colima profile, or exact Docker context/daemon identity used by
   the binding.
2. A project's internal ID is independent from its display name.
3. Same-named Compose projects in different runtime scopes never share an ID.
4. Separate imported origins in one runtime scope never share an ID merely
   because Compose gives them the same project name.
5. Every persistent project reference is remapped atomically or the migration
   rolls back.
6. Ambiguous legacy rows are retained for explicit resolution; they are never
   reassigned to whichever context happens to start first.
7. Derived Docker-object caches are discarded and rebuilt rather than treated as
   authoritative migration input.

## Identity model

Keep these concepts separate:

| Concept | Purpose | Example |
| --- | --- | --- |
| `RuntimeScope` | Authorizes one active Docker backend | `windows_wsl_ubuntu` + `wsl:Ubuntu` |
| `project_id` | Stable opaque database identity | `prj_01J...` or a bounded digest |
| `compose_project_name` | Name supplied to/read from Compose | `cairn` |
| `display_name` | User-facing, editable label | `Cairn development` |
| `origin_kind` | Explains how identity is established | `detected`, `imported` |
| `origin_key` | Stable discriminator within a runtime scope | Compose name or canonical import origin |

The recommended logical key is a length-prefixed tuple, not delimiter
concatenation:

```text
ProjectKeyV2 = encode_v1(
  provider_id,
  backend_identity,
  origin_kind,
  origin_key
)
```

`encode_v1` must encode each UTF-8 field as `byte_length || bytes`, include a
version/domain prefix, and reject empty required fields. Store either the tuple
columns under a unique constraint and use a generated opaque ID, or hash the
encoded bytes with SHA-256 and encode a bounded prefix using lowercase Base32.
Do not build identity by joining fields with `:`, `/`, or another character that
can occur inside a field.

For detected Compose projects, `origin_key` is the normalized Compose project
name within the exact runtime scope. Docker Compose treats that name as the
deployed project identity on one daemon.

For imported projects, `origin_key` is an immutable import identity. On local
backends it should be created from the canonical real path of the project root
plus the canonical ordered Compose-file set. When a canonical host path cannot
be represented reliably across the provider boundary, generate and persist an
import UUID at first review and bind the review/apply token to that UUID and the
canonical file set. Moving an import should be an explicit relink operation,
not an accidental new identity or silent reassignment.

## Schema target

Introduce a new project table shape in one migration generation rather than
mutating the legacy primary key piecemeal:

```sql
CREATE TABLE projects_v2 (
    id                   TEXT PRIMARY KEY,
    provider_id          TEXT NOT NULL,
    backend_identity     TEXT NOT NULL,
    origin_kind          TEXT NOT NULL,
    origin_key           TEXT NOT NULL,
    compose_project_name TEXT NOT NULL,
    display_name         TEXT NOT NULL,
    -- existing project attributes follow
    UNIQUE (provider_id, backend_identity, origin_kind, origin_key)
);
```

All tables that refer to a project must use `projects_v2.id` with an enforced
foreign key. The migration inventory must include at least:

- services and forgotten-project aliases;
- image lineage and base-image references;
- update checks, ignored updates, update history, and update plans if persisted;
- metrics, audit, notification, terminal, backup, or other records that carry a
  project ID;
- any settings or serialized metadata that embeds a project ID.

Before implementation, generate this inventory from SQLite foreign-key/schema
inspection plus a repository-wide search for `project_id`, `ProjectID`, and
serialized project identifiers. The generated migration test should fail if a
new reference is added without a remap rule.

## Migration algorithm

1. Open the database under the existing exclusive migration transaction and
   create a pre-migration backup using the current store backup mechanism.
2. Create `projects_v2`, the v2 dependent tables, and a
   `project_identity_migration` mapping table containing legacy ID, candidate
   v2 ID, classification, and resolution state.
3. Classify every legacy project:
   - A row with a non-empty backend identity and a unique origin can be mapped
     automatically.
   - An imported row requires a recoverable canonical origin or previously
     persisted import UUID.
   - A blank/unknown context, duplicate candidate identity, conflicting
     provider/context evidence, or missing import origin is ambiguous.
4. Copy only unambiguous rows and build the complete old-to-new ID map.
5. Copy every dependent authoritative row through that map. Verify row counts,
   foreign keys, and uniqueness before replacing legacy tables.
6. Move ambiguous projects and their authoritative dependents into quarantine
   tables or leave the application in a migration-required state that exposes a
   resolver UI. Do not expose them to runtime actions until explicitly mapped.
7. Drop derived object caches and other reconstructible snapshots. Reconciliation
   repopulates them with composite runtime-scope keys.
8. Swap v2 tables into place, run `PRAGMA foreign_key_check` and integrity
   checks, record the identity version, and commit.

If product requirements do not permit a quarantine/resolution workflow in the
same release, make the migration preflight-only: report the ambiguous records,
leave the old schema untouched, and require resolution before retrying. A
partial identity-graph migration is not acceptable.

## Runtime and API changes

- Replace constructors that accept a bare project ID with a scoped project
  authorization object loaded from the active `RuntimeScope`.
- Build the runtime from an immutable snapshot of provider configuration. A
  settings write that changes a WSL distribution, Colima profile, socket, or
  Docker context must create a new runtime binding; it must not mutate the
  provider object used by an existing Docker client, Compose runner, terminal,
  backup job, or reconnect loop.
- Capture the v2 project ID and complete runtime scope in every plan. Revalidate
  both after acquiring the runtime lock and immediately before an external side
  effect.
- Keep Compose's project name as a command argument, never as the authorization
  key.
- Make repository APIs require `RuntimeScope` for active-runtime reads,
  reconciliation, cleanup, lineage, update, and mutation paths.
- Treat legacy aliases as lookup-only migration aids. They must never overwrite
  a v2 row or authorize an action by themselves.
- Include identity version and scope in job/event payloads where a delayed event
  can otherwise be confused with a later runtime binding.

## Collision and relink policy

- Same Compose name, different runtime scopes: allowed and isolated.
- Same Compose name, same runtime scope, detected twice: one deployed project.
- Same Compose name, same runtime scope, different imports: allowed only when
  their immutable import origins differ; the UI must show enough path/origin
  detail to distinguish them.
- Import origin already linked to another project: reject with a conflict and
  offer an explicit relink/merge workflow.
- Runtime context/profile/distribution rename: treat as a scope move requiring
  an explicit migration preview. Never infer it solely from matching names.
- Project folder move: preserve identity only through an explicit relink that
  revalidates Compose content and checks for destination collisions.

## Rollout sequence

1. Add identity tuple/value objects, canonical encoders, and collision tests.
2. Add the schema/reference inventory and migration preflight report.
3. Implement v2 repositories behind a feature gate and dual-read comparison;
   do not dual-write external mutations.
4. Ship the transactional migration plus ambiguity resolver and backup/restore
   path.
5. Switch all plans, events, services, lineage, updates, and reconciliation to
   v2 IDs.
6. Remove legacy lookup/write paths only after upgrade and rollback testing on
   representative release databases.

## Required validation

- Same project name in two Docker contexts and two providers survives alternating
  reconciliation, restart, stale cleanup, and actions.
- Two imported folders with the same Compose name in one runtime scope remain
  distinct across restart and relink.
- Every cross-scope Compose, update, rollback, lineage, and project request is
  rejected without disclosure or external side effects.
- A plan created before provider/context rebind is rejected after rebind.
- Migration fault injection at every table-copy/swap boundary leaves either the
  complete legacy graph or the complete v2 graph, never a mixture.
- Ambiguous blank-context and missing-origin fixtures are reported without being
  claimed by the active context.
- `PRAGMA foreign_key_check`, row-count reconciliation, and identity uniqueness
  checks pass after migration.
- Upgrade, downgrade/restore, and repeated-migration tests use databases from
  every supported released schema version.

## Completion criteria for BE-005

BE-005 can be marked fixed only when the v2 schema and all project references
are migrated, imports have a stable origin discriminator, ambiguous legacy data
has a safe user-visible resolution path, all active APIs authorize against the
exact runtime scope, and the cross-context/import regression matrix passes.
The current exact-scope containment remains intentionally partial until those
criteria are met.
