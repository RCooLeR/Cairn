# Cairn Full Project Review — Index and Executive Summary

- Review date: 2026-07-15
- Workspace: `E:\Development\projects\apps\rcooler\Cairn`
- Branch: `feat/port-forwarding-ui`
- Reviewed HEAD: `92230977c065476a74dbb64f8133d8cddff88cfd`

## Executive decision

The current project is **not ready for an unrestricted production release**.

- The advertised server/container mode is a stop-ship risk: it exposes the broad desktop Wails service surface without Cairn authentication, authorization, TLS, origin/CSRF protection, or coherent request/client limits. It should be removed from stable release outputs or rebuilt as a separate server-safe product.
- The desktop app has three other distinct Critical root causes: a registry can redirect stored credentials to an attacker-chosen HTTPS token realm; project-detail async state can make actions operate on the wrong project; and Agent draft/preview/apply state can cross project boundaries.
- High-risk backend gaps cluster around cross-provider/context identity, confirmation-plan bypasses, external-side-effect partial success, secret/file boundaries, backup consistency, unbounded streams/jobs/input, and lifecycle/shutdown.
- The release pipeline can publish from a tag without proving the exact commit passed normal gates, exposes signing secrets too broadly, permits unsigned stable assets, signs only the Windows outer installer, and deliberately allows release asset replacement.
- Frontend compile-time health is good, but the current tests do not cover the most dangerous reordered async responses, native Wails boundary, accessibility contracts, or long-lived stream ownership.

The fastest safe path is to quarantine server mode, fix credential and wrong-target defects, establish a single scoped identity/operation protocol, and make release publication fail closed before continuing feature work.

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
- Under Ubuntu WSL, all non-shell internal Go tests passed; the non-shell race suite passed; server-tag shell tests passed.
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
