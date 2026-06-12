# Module 10 — Image Lineage

`internal/lineage` — maps running containers/services to their build sources and base images, with honest confidence levels. Differentiator feature.

## 1. Questions it answers

Is this image itself outdated? Was this locally built image built FROM an outdated base? Does this project need pull, rebuild, or both? Which services does a base-image change affect?

## 2. Entity chain

```text
Project → Service → Container → running service image
        → build metadata (compose build config) → Dockerfile → FROM refs (stages)
        → base digests: build-time | local | remote
```

## 3. Discovery pipeline (ordered by confidence)

1. **Compose build config** (resolved `config`): `build.{context, dockerfile, target, args}` → service is locally built; source `compose_dockerfile`.
2. **Dockerfile parse** (§4) for FROM refs/stages.
3. **OCI annotations / image labels** on the local image: `org.opencontainers.image.base.name`, `org.opencontainers.image.base.digest` → fills build-time base digest when present; source `oci_annotation` if it's the only source.
4. **Cairn labels** (highest precision when present): builds triggered through Cairn attach `io.cairn.project/service/compose.file/dockerfile/base.name/base.digest/build.time/build.platform` (never invent keys under `org.opencontainers.*`); source `cairn_label`.
5. Compose container labels map containers→service/project (modules/04).

Persistence: `image_lineage` + `base_image_refs` ([03-data-model.md §5]); refreshed on project refresh, after builds, and before update checks.

## 4. Dockerfile parser

Requirements (all covered by fixtures):
- `FROM <ref> [AS <name>]`, case-insensitive instruction, line continuations `\`, comments, BOM.
- Multi-stage: index + stage names; `FROM <previous-stage>` references resolved (not external bases); final stage = `build.target` if set, else last stage; its external base flagged `is_final_stage_base`.
- `ARG` substitution in FROM (`ARG V=20` + `FROM node:${V}-alpine`): resolve from compose `build.args` then Dockerfile defaults; unresolved → keep raw, confidence ≤ low.
- `--platform=$BUILDPLATFORM` flag captured.
- `FROM scratch` → no base tracking, status `local_only_image`.
- Digest-pinned FROM (`node@sha256:…`) → base pinned, never an update.

Parser is hand-rolled line-lexer (no full BuildKit dependency), ~strict: anything unparseable degrades confidence, never crashes discovery.

## 5. Confidence model (normative)

| Level | Condition |
|---|---|
| high | compose build config + Dockerfile + known build-time base digest (Cairn label or OCI annotation) |
| medium | compose build config + Dockerfile, build-time digest unknown (compares local base-image digest instead) |
| low | image metadata labels only (no Dockerfile available) |
| unknown | no reliable base info (e.g. third-party registry image without metadata) |

UI wording: `Base image: node:20-alpine · Confidence: High · Reason: from Compose build config and Dockerfile.`

## 6. Comparison semantics

- Preferred: build-time base digest vs remote digest (true "was my build made from old base").
- Medium path: local pulled base image digest vs remote (proxy: base updated locally but service not rebuilt also matters → if local base digest ≠ remote → base update; if local base newer than build-time (unknown) → rebuild may already be pending — status `rebuild_required` when any reliable comparison shows drift on final-stage base).
- Multi-stage: final-stage base drives `rebuild_required`; builder-stage drift reported as informational `base_image_update_available`.

## 7. Honest unknowns (normative copy)

Third-party image, no metadata: `Base image: Unknown — this is a third-party registry image and no base metadata was found.` Never guess (no "postgres is probably debian"). Unparseable Dockerfile: `Base tracking unavailable — Dockerfile could not be parsed (see details).`

## 8. Cairn-triggered builds

When Cairn runs `compose build`, it appends `--label io.cairn.*` values: resolves each external base's current local digest immediately after build and records as build-time digest → future checks become high confidence. Recorded also into `image_lineage.dockerfile_hash` (sha256 of Dockerfile content) to detect Dockerfile edits (hash change → lineage refresh).

## 9. Tests

Parser: ≥ 30 fixtures per §4 list. Discovery: golden lineage for `build-simple`, `build-multistage`, `mixed-updates` (expected.json). Confidence: table tests per §5. E2E: [06-testing.md §5] cases 6–8; container detail Lineage card content matches backend exactly.
