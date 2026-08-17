# Cairn Full Project Review — Index and Executive Summary

- Review date: 2026-07-15
- Workspace: repository root
- Branch: `feat/port-forwarding-ui`
- Reviewed HEAD: `92230977c065476a74dbb64f8133d8cddff88cfd`
- Re-review delta: 2026-07-24 on `codex/review-remediation`, base `a223bfb462037969d42277ef20dcae28468e5f8e`
- Registry/helper/single-instance and standalone acceptance follow-up: 2026-07-28 on the same remediation working tree
- Previous full delta report: [11-2026-07-24-full-rereview-and-fix-status.md](./11-2026-07-24-full-rereview-and-fix-status.md)
- Current acceptance report: [12-2026-07-28-registry-credential-helper-and-final-acceptance.md](./12-2026-07-28-registry-credential-helper-and-final-acceptance.md)
- Current dependency/security review: [13-2026-08-17-full-project-review-and-dependency-refresh.md](./13-2026-08-17-full-project-review-and-dependency-refresh.md)

## Executive decision

The current project is **not ready for an unrestricted production release**.

- The advertised server/container mode is a stop-ship risk: it exposes the broad desktop Wails service surface without Cairn authentication, authorization, TLS, origin/CSRF protection, or coherent request/client limits. It should be removed from stable release outputs or rebuilt as a separate server-safe product.
- The desktop app has three other distinct Critical root causes: a registry can redirect stored credentials to an attacker-chosen HTTPS token realm; project-detail async state can make actions operate on the wrong project; and Agent draft/preview/apply state can cross project boundaries.
- High-risk backend gaps cluster around cross-provider/context identity, confirmation-plan bypasses, external-side-effect partial success, secret/file boundaries, backup consistency, unbounded streams/jobs/input, and lifecycle/shutdown.
- The release pipeline can publish from a tag without proving the exact commit passed normal gates, exposes signing secrets too broadly, permits unsigned stable assets, signs only the Windows outer installer, and deliberately allows release asset replacement.
- Frontend compile-time health is good, but the current tests do not cover the most dangerous reordered async responses, native Wails boundary, accessibility contracts, or long-lived stream ownership.

The fastest safe path is to quarantine server mode, fix credential and wrong-target defects, establish a single scoped identity/operation protocol, and make release publication fail closed before continuing feature work.

### 2026-07-24 re-review update

Substantial remediation has occurred since the original decision. The latest delta fixes the Windows WSL scope-casing defect that could show Docker containers while hiding projects; fail-open ordinary Compose path mapping; same-scope reconciliation ordering; zero-service snapshot clearing; atomic import/forget/delete and project-incarnation cleanup; lineage/update-check reference replacement; non-aligned metrics tier cutoffs; provider/update/backup/terminal lifecycle defects; failed-new-volume restore compensation; image save/load filesystem and response safety; critical terminal/job delivery; pre-ID log ownership; hidden bulk selection; last-known lineage retention; terminal startup enumeration; and the remaining Critical Agent chat/tool-approval cross-project ownership gap.

At the pre-final source-remediation checkpoint, a complete uncached native Windows Go test run passed, as did 4 focused frontend files / 171 tests, TypeScript, ESLint, and Prettier. After the final backup-lifecycle and image-load success-contract changes, the complete Go suite, targeted race-enabled backend tests, and Go vet also passed. An intermediate production Wails binary then reached the exact-WebView launch smoke, which correctly exposed a blank-window runtime failure (`Uncaught TypeError: r is not a function`) caused by Rolldown vendor max-size splitting changing execution order. The strict-execution-order correction now passes TypeScript, release-validation and production builds, targeted real-Chromium boot, the full release-UI suite (16 passed, 1 opt-in golden skipped), ESLint, and Prettier in a clean staged frontend copy; output measures 498.84 KiB for the main chunk and 287.26 KiB for the largest vendor chunk. Final-tree full frontend tests, corrected Wails Windows resource build, PE/build-info inspection, and successful native launch smoke remain pending. The project therefore has a strong source-remediation checkpoint but is **not yet certified as a final rebuilt release binary**, and the original architectural/supply-chain residuals remain unless the delta explicitly closes them.

The delta assigns `RR-2026-001` through `RR-2026-030` for traceability. Those records include newly isolated defects, incomplete earlier remediations, build-acceptance gaps, and confirmed residuals; they must not be added mechanically to the original 190-entry count as 30 independent root causes.

### 2026-07-28 registry, single-instance, and final acceptance update

The latest follow-up traces Cairn's registry login from settings policy and secret collection through provider/helper probing, Docker login, authenticated verification, helper-only finalization, rollback, audit, Wails error decoding, and account-status presentation. Host probes confirmed that the active WSL 2 backend was running and had a Docker config, but none of Cairn's bounded credential-helper candidates was installed and responsive. That is a real registry-login prerequisite failure, not an IPv6, WLAN, project-detection, or general Docker-provider failure.

The working tree now classifies missing helpers as `E_REGISTRY_AUTH`, preserves true WSL/provider failures as `E_PROVIDER_NOT_READY`, preserves cancellation/deadline codes, renders bounded structured repair hints instead of raw Wails JSON, audits pre-command failures, computes one terminal login outcome only after verification/finalization/rollback, rolls back a successful login if its success audit cannot be recorded, disables secret collection under disabled/unknown policy, exposes Test Auth diagnostics, and clears credentials when registry identity changes.

Native acceptance exposed one additional High lifecycle defect: relaunching a tray-resident app started a second Cairn runtime, which then failed private named-pipe ownership with Access denied. Wails single-instance ownership now precedes provider/bridge startup, and a second launch safely restores, unminimizes, and focuses the mutex-protected existing window. A final release audit then found that native Linux/macOS tasks still ran build-time `go mod tidy`, omitted `-mod=readonly`, and could generate icons before canonical asset synchronization, while hosted/local vet still omitted repository tooling. The shared task graph now enforces readonly modules and asset synchronization on every platform, and CI/local lint vet the full root module.

A final adversarial lifecycle pass found that project deletion, stale cleanup, update, and rollback did not share a per-project cancellation/join/revision gate; multiple confirmed plans could mutate one project concurrently; and apply did not bind the stored Compose/service configuration reviewed by the user. The working tree now cancels and joins project-owned mutations before exact leased deletion, admits one scoped mutation at a time, expires pre-mutation plans by lifecycle revision, and revalidates a deterministic stored-configuration fingerprint. Image load now reconciles every error after the daemon mutation boundary and reports confirmed-versus-unknown partial state; late terminal-open results are closed after navigation instead of leaking invisible PTYs. A live dependency audit also exposed five newly published advisories. The supported ESLint 10 parent upgrade removes the legacy minimatch/js-yaml chain, DOMPurify and PostCSS are patched, and the final lock resolves minimatch 10.2.6 plus brace-expansion 5.0.8 without an incompatible override. These additions extend the follow-up through `RR-2026-047`.

Final validation passed complete Go tests/vet, `golangci-lint` with 0 issues, `govulncheck` with no reachable vulnerabilities, race-enabled stateful backend suites, 28 frontend files / 343 tests, TypeScript, ESLint 10, Prettier, production build, and Playwright release UI (16 passed, 1 opt-in golden skipped). A real clean `npm ci` ran the nested Go-module postinstall hook; `npm audit --json` reports zero vulnerabilities; and root `go list ./...` returned 24 packages with zero beneath `frontend/node_modules`, closing OPS-014 for the final tree. The final main chunk is 501.66 kB (124.82 kB gzip) and retains its size warning. The exact rebuilt Windows executable must still receive the final PE/resource/build-info and native WebView/provider/project/registry/relaunch acceptance recorded in report 12. No NSIS installer is claimed, and the original architectural, supply-chain, installer, platform, bundle-budget, external-file-fingerprint, and external-writer residuals remain unless an individual record says otherwise.

Across the two re-review deltas, `RR-2026-001` through `RR-2026-047` are traceability records. They overlap original catalog risks and must not be added mechanically to the original 190-entry count as 47 independent root causes.

### 2026-08-17 dependency, plan-integrity, and accessibility update

The latest full-tree pass updates the Go compiler/toolchain and safe Go/npm dependency lines, moves vulnerability and lint tooling to current pinned releases, removes the newly published reachable Go standard-library and npm findings, and hardens CI/release inputs and publication behavior. It also closes cross-runtime Docker plan replay, mutable Docker image/network aliases, the SQLite busy-WAL migration-backup gap, missing project/Compose mutation exclusion, stale project-plan configuration, backup sidecar/delete identity, multi-replica update-health verification, loader/modal/toast/table accessibility, and the known malformed event-array crash paths.

Current frontend source and browser validation is green: 30 files / 350 unit tests, production and Ladle builds, and 16 Playwright scenarios with one intentional golden skip. Exact backend and tool evidence plus deliberate major-version deferrals are recorded in report 13. The decision remains source-acceptance rather than unrestricted production certification because Wails beta migration, signed/installed artifacts, and three-OS native package acceptance remain separate gates.

`RR-2026-048` through `RR-2026-063` are delta traceability records and overlap the original catalog; they do not increase the normalized 190-entry root-cause count mechanically.

## Finding count

The three primary detailed reports contain **190 catalog entries**. Counts are normalized to the highest stated severity when a finding has a conditional range such as “Medium in desktop mode; High in exposed server mode.”

| Detailed report                                   | Critical |   High | Medium |   Low |   Total |
| ------------------------------------------------- | -------: | -----: | -----: | ----: | ------: |
| Backend, data, concurrency, security (`BE-*`)     |        2 |     46 |     33 |     2 |      83 |
| Frontend, UX, accessibility, performance (`FE-*`) |        2 |     29 |     34 |     2 |      67 |
| Build, CI, release, docs, operations (`OPS-*`)    |        1 |     16 |     21 |     2 |      40 |
| **Catalog total**                                 |    **5** | **91** | **88** | **6** | **190** |

These are catalog entries, not 190 independent root causes. Cross-cutting risks intentionally appear in the domain where each must be fixed. Most notably, BE-001 and OPS-001 describe the same Critical server exposure from runtime and delivery perspectives. There are **four distinct Critical root causes** after that obvious overlap: server exposure, registry credential-realm exfiltration, wrong-project detail actions, and cross-project Agent operations. High/Medium entries also overlap where one architectural flaw has backend, frontend, and release consequences.

The architecture (`ARCH-*`) and security (`SEC-*`) documents are synthesis lenses and are not added to the 190 count.

## Report index

| File                                                                                                                 | Purpose                                                                                                                            |
| -------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------- |
| [00-review-index-and-executive-summary.md](./00-review-index-and-executive-summary.md)                               | Outcome, counts, top blockers, validation summary, and navigation.                                                                 |
| [01-system-architecture-and-cross-cutting-gaps.md](./01-system-architecture-and-cross-cutting-gaps.md)               | Current/target architecture, systemic root causes, contracts, ADRs, and acceptance tests.                                          |
| [02-backend-data-correctness-concurrency.md](./02-backend-data-correctness-concurrency.md)                           | 83 detailed `BE-*` findings covering services, data, security, Docker/provider behavior, background work, resources, and shutdown. |
| [03-security-privacy-and-data-integrity.md](./03-security-privacy-and-data-integrity.md)                             | Threat/asset model, consolidated security risk register, attack scenarios, privacy/data flows, and required security gates.        |
| [04-frontend-correctness-ux-accessibility-performance.md](./04-frontend-correctness-ux-accessibility-performance.md) | 67 detailed `FE-*` findings covering async state, streams, settings, accessibility, performance, runtime boundaries, and tests.    |
| [05-build-test-ci-release-documentation.md](./05-build-test-ci-release-documentation.md)                             | 40 detailed `OPS-*` findings covering CI, supply chain, signing, packaging, evidence, operations, documentation, and governance.   |
| [06-remediation-roadmap.md](./06-remediation-roadmap.md)                                                             | P0–P3 implementation sequence, finding mapping, exit criteria, release gates, and ownership suggestions.                           |
| [07-validation-and-review-method.md](./07-validation-and-review-method.md)                                           | Exact commands/results, coverage, limitations, review method, and interpretation rules.                                            |
| [08-complete-finding-register.md](./08-complete-finding-register.md)                                                 | Compact table of all 190 `BE-*`, `FE-*`, and `OPS-*` entries with normalized severity.                                             |
| [09-remediation-status.md](./09-remediation-status.md)                                                               | Append-only remediation commits, validated fixes, current delta, and residual findings.                                            |
| [10-project-identity-migration-design.md](./10-project-identity-migration-design.md)                                 | Target design for canonical project/import-origin identity, migration, compatibility, and rollback.                               |
| [11-2026-07-24-full-rereview-and-fix-status.md](./11-2026-07-24-full-rereview-and-fix-status.md)                     | 2026-07-24 flow-by-flow re-review, `RR-2026-*` delta findings, fixes, evidence, pending gates, and residual risks.                  |
| [12-2026-07-28-registry-credential-helper-and-final-acceptance.md](./12-2026-07-28-registry-credential-helper-and-final-acceptance.md) | 2026-07-28 registry helper/login/audit/UI, single-instance, project-mutation lifecycle, image-load reconciliation, terminal late-open, dependency-security, and cross-platform build/CI follow-up, `RR-2026-031` through `RR-2026-047`, host evidence, and exact standalone-binary acceptance record. |
| [13-2026-08-17-full-project-review-and-dependency-refresh.md](./13-2026-08-17-full-project-review-and-dependency-refresh.md) | 2026-08-17 full-tree dependency/security refresh, Docker/project/backup/update integrity fixes, frontend accessibility/runtime hardening, CI supply-chain controls, validation, and remaining release gates. |

## Immediate release blockers

| Priority | Required outcome                                                                                | Why it blocks                                                                                                                                                           | Findings                                                                                                     |
| -------: | ----------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------ |
|        1 | Remove/disable stable server mode or replace it with an independently secured API.              | Network callers receive Docker/terminal/file/credential-equivalent desktop authority without an application security boundary.                                          | BE-001, BE-002, BE-073, BE-074, BE-076; OPS-001 through OPS-003                                              |
|        2 | Bind registry credentials to an explicitly trusted token issuer.                                | A challenged registry can exfiltrate Basic credentials or identity tokens to an attacker-selected HTTPS realm.                                                          | BE-003                                                                                                       |
|        3 | Make visible and actionable project/Agent targets identical under every async completion order. | Operators can approve actions for A while the UI indicates B.                                                                                                           | FE-001, FE-002; supporting FE-003 through FE-006                                                             |
|        4 | Adopt full provider/context/resource identity everywhere.                                       | Project/data/cache collisions and missing active-scope checks can drive actions against another Docker context.                                                         | BE-005 through BE-007, BE-018; FE-001                                                                        |
|        5 | Close destructive-operation plan bypasses and stale/replay behavior.                            | Several mutation families do not share the confirmation signal the operator expects.                                                                                    | BE-013 through BE-016, BE-045, BE-070 through BE-072                                                         |
|        6 | Constrain Agent/file/secret flows and make edits atomic/auditable.                              | Project/env/log/inspect content can reach arbitrary endpoints; redaction and size limits are incomplete; file writes can truncate or escape intended trust assumptions. | BE-037 through BE-040, BE-060, BE-062, BE-063, BE-082; OPS-040                                               |
|        7 | Make registry credential config updates atomic and helper-safe.                                 | Cairn can mutate valid secrets, lose concurrent Docker config changes, corrupt shared auth config, or leave inline credentials.                                         | BE-017, BE-041, BE-042, BE-077, BE-078                                                                       |
|        8 | Make release signing/publication exact-SHA, isolated, fail-closed, and immutable.               | The current path cannot prove that downloaded artifacts are the exact tested, fully signed outputs of the tagged source.                                                | OPS-004 through OPS-008, OPS-012, OPS-032, OPS-035, OPS-036                                                  |
|        9 | Introduce durable partial-success and external-mutation reconciliation.                         | Cairn can report failure after Docker changed or report success without durable audit/backup metadata, making retries unsafe.                                           | BE-035, BE-046, BE-058, BE-059, BE-075, BE-078, BE-082                                                       |
|       10 | Put public work under coherent resource and lifecycle budgets.                                  | Unbounded bodies/files/logs/output/sessions/jobs/events/maps and slow shutdown make practical denial of service and resource leaks possible.                            | BE-002, BE-012, BE-019 through BE-024, BE-041, BE-048, BE-050, BE-053, BE-056, BE-067 through BE-074, BE-083 |

## Highest-value systemic changes

### 1. Canonical scoped identity

Carry installation, provider, Docker context, resource kind/native ID, source/import identity, and generation through persistence, caches, services, events, plans, streams, and frontend query keys. This resolves an entire cluster rather than patching individual collision checks.

### 2. Unified durable operation protocol

Every dangerous action should bind actor/session, full target scope, normalized inputs, source fingerprint, risk/confirmation, expiry, single-use idempotency, deadline, and audit/outbox identity. Apply should atomically claim, revalidate, execute once, and finish as success/failure/cancelled/partial success.

### 3. Structured concurrency and service budgets

One root task group should own all workers. Admission happens before child work starts. Each request/client/feature has byte/cardinality/session/process/disk/time limits, real cancellation, and one shutdown deadline. Event semantics should differ for telemetry, logs, terminal bytes, progress, and non-droppable final state.

### 4. Safe data boundaries

Classify secret, sensitive content, path/capability, audit-safe, and ordinary metadata fields. Use purpose-specific DTOs, default-deny Agent/file collection, byte/token caps, explicit outbound preview, atomic file publication, data retention, and historical secret scrubbing.

### 5. Enforced release state

Replace narrative “release-ready” evidence with exact-SHA machine-readable gates and immutable artifacts. Isolate signing from dependency/build execution and verify installed payloads on every supported platform.

## Domain assessment

| Area                                 | Assessment                                         | Main reason                                                                                                              |
| ------------------------------------ | -------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------ |
| Server/security boundary             | **Stop-ship**                                      | Full privileged service surface is remotely reachable without Cairn auth/origin/resource controls.                       |
| Desktop destructive-action integrity | **Stop-ship until Critical target bugs are fixed** | Stale responses and fragmented plan semantics can break user intent.                                                     |
| Credential handling                  | **Stop-ship for affected registry flows**          | Bearer realm trust and shared Docker config handling can disclose or damage credentials.                                 |
| Data correctness                     | **High risk**                                      | Scope collisions, stale generations, incorrect latest/tier queries, and reconcile races can drive wrong state/actions.   |
| Backup/recovery                      | **High risk**                                      | Safety/fidelity/cancellation/partial-restore/durability contracts are incomplete.                                        |
| Runtime lifecycle/scale              | **High risk**                                      | Multiple unbounded paths, detached work, dropped final events, and unbounded shutdown.                                   |
| Frontend correctness                 | **High risk despite green typecheck/tests**        | Most dangerous failures are async ownership/state-machine defects absent from existing tests.                            |
| Accessibility                        | **Material release gap**                           | Known contrast failure, incomplete modal/tab/table keyboard contracts, suppressed detection, and limited route coverage. |
| Performance                          | **Material scaling gap**                           | 19k-line root owns live state; hidden terminals persist; metrics/log/loader work and 1.27 MiB main chunk need budgets.   |
| CI/release supply chain              | **High risk**                                      | Publication, signing, mutability, pinning, and evidence are not fail-closed or exact-SHA.                                |
| Documentation/governance             | **Incomplete**                                     | No license/security/contribution/notices baseline; trust/support/recovery claims are underspecified or stale.            |

## Validation outcome

### Passed or usefully validated

- Frontend TypeScript typecheck and ESLint passed.
- Frontend unit tests passed: 7 files / 103 tests.
- Frontend production build passed; it warned that the ~1,274.03 KiB minified main chunk exceeds the 500 KiB threshold.
- Default Playwright release UI passed 15 tests with 1 skipped committed-golden comparison.
- npm audit reported 0 known vulnerabilities across 729 dependencies when run with the system CA store.
- Go formatting and `git diff --check` passed.
- Windows `go vet -unsafeptr=false . ./internal/...`, desktop `go build .`, and compilation of every internal Windows test package passed.
- Linux headless server cross-build passed.
- Under WSL, all non-shell internal Go tests passed; the non-shell race suite passed; server-tag shell tests passed.
- Measured non-shell Go statement coverage was 68.1%; `internal/services` was 46.4% and server-tag shell coverage was 15.9%.

### Failed or exposed a real project issue

- Frontend formatting check fails for `frontend/src/settings/SettingsPage.tsx`.
- Windows `go build -tags server .` fails inside the pinned Wails alpha due to duplicate/undefined platform-server declarations.
- `go list ./...` unexpectedly includes `frontend/node_modules/flatted/golang/pkg/flatted`, proving the Go/npm module-boundary problem.

### Inconclusive host limitations, not project failures

- Ordinary Windows Go test binaries and a locally generated golangci-lint executable were created with their primary thread suspended by the review host. The review used WSL to obtain current portable-package test/race results instead of treating this as a Cairn defect.
- Normal Linux root/shell GUI compilation in WSL lacked GTK4/WebKitGTK development packages. Headless server-tag shell tests passed; native Linux GUI validation still belongs in clean CI/platform evidence.

See [07-validation-and-review-method.md](./07-validation-and-review-method.md) for commands, coverage by package, and interpretation.

## Important positive observations

- The project has meaningful domain separation, provider abstractions, typed errors, persistence repositories, plan/audit foundations, generated bindings, and integration fixtures.
- Strict TypeScript, linting, Go tests, release UI tests, real-Docker/WSL fixtures, and migration/security scripts provide a strong base for targeted regression work.
- Several security-aware choices already exist: registry secrets use stdin, plaintext remote registries are restricted, Wails chunked requests are bounded, Agent responses are capped, and risky actions often have plan abstractions.
- The backend already contains an active provider/context validation pattern that can be generalized.
- Passing tests and race runs indicate the review is not describing a generally broken codebase; the serious defects concentrate at cross-layer contracts, adversarial inputs, lifecycle edges, and deployment boundaries.

## Review scope and caveats

- The review covers the complete on-disk working tree, including pre-existing modified and untracked source. It does not pretend to describe only committed HEAD.
- No application source was changed. Review output is confined to `col-review/`.
- Line references point to the reviewed working tree and may move after edits.
- Source-confirmed defects are distinguished from conditional risks/gaps in the detailed reports. Server findings are conditional on building/deploying server mode, but that mode is in scope because the repository supplies build/container/run paths for it.
- A full review cannot prove the absence of additional defects. Platform-native manual flows, long soak behavior, signing/notarization credentials, hostile network infrastructure, and clean-machine installation require environments outside this workspace.

## Recommended first implementation slice

Start with one coordinated change set that:

1. removes stable server publication/build claims;
2. rejects untrusted registry token realms;
3. adds a scoped async operation key to Project Detail and Agent and proves reordered-response safety;
4. defines the canonical `ResourceKey` and unified operation ADRs;
5. makes release tags wait for exact-SHA required gates and stable signing fail closed.

That slice contains the immediate blast radius while creating the primitives needed for the remaining roadmap instead of accumulating more feature-local guards.
