# Prioritized Remediation Roadmap

- Review date: 2026-07-15
- Purpose: turn the review catalog into an implementation sequence with dependencies, acceptance gates, and release decisions. Time ranges are sequencing guidance, not effort commitments; actual staffing and platform access will change them.

## Release decision

Do **not** publish the advertised server/container mode in its current form. Treat it as disabled/experimental until a separately secured server boundary is complete.

Do **not** declare the desktop product production-ready until the distinct Critical defects and the highest-risk destructive-action/data-integrity paths are fixed and regression-tested. The existing passing compile/test/build results are useful but do not exercise those invariants.

## Prioritization rules

1. Contain externally reachable or credential-loss risks before refactoring.
2. Establish identity and operation contracts before repairing each feature independently; otherwise fixes will diverge again.
3. Fix silent wrong-target/data-loss behavior before cosmetic UX and throughput work.
4. Add a failing regression test or executable policy gate with each defect fix.
5. Separate “must block release” from “important architecture improvement,” while retaining every finding in the backlog.
6. Use partial-success states for side effects; never retry blindly when the external outcome is uncertain.

## Phase 0 — Stop-ship containment and proof

Target: immediate through the first stabilization milestone.

### P0.1 Disable the unsafe server surface

Actions:

- Remove server artifacts/tasks from stable releases and documentation, or make startup fail closed unless a deliberately experimental local-only flag is supplied.
- Change any retained Docker publication to loopback only as an interim control.
- Add a test that rejects wildcard server startup in production configuration.
- Decide via ADR whether server mode will be removed or rebuilt as a separate authenticated product. Do not attempt to secure it by sprinkling checks across the desktop service registry.

Findings: BE-001, BE-002, BE-073, BE-074, BE-076; OPS-001, OPS-002, OPS-003.

Exit criteria:

- No stable artifact exposes Wails desktop object dispatch over the network.
- Documentation and build/release matrices no longer imply a safe/supported server deployment.
- If an experimental artifact remains, it is loopback-only, clearly unsupported, and carries no Docker socket/host mounts by default.

### P0.2 Stop registry credential exfiltration and corruption

Actions:

- Bind Bearer token issuers to the challenged registry using same-origin or an explicit trusted issuer mapping; restrict redirects and downgrade.
- Preserve registry secrets byte-for-byte; never trim opaque credential values.
- Replace direct Docker `config.json` rewriting with helper-native operations where possible. Otherwise add locking/conflict detection and atomic durable replacement.
- Bound token/index bodies and create an explicit failure if the cap is exceeded.
- Prevent helper-detection failure from silently leaving inline credentials.
- Ensure all private pull/update paths use the same configured credentials.

Findings: BE-003, BE-017, BE-041, BE-042, BE-077, BE-078, BE-083.

Exit criteria:

- Adversarial issuer/redirect/DNS/downgrade tests prove credentials never reach an unapproved final origin.
- Leading/trailing-whitespace secrets pass unchanged through a fake Docker login.
- Concurrent/fault-injected Docker config updates cannot lose unrelated data or publish invalid JSON.
- Registry request sizes and retained host/cache cardinality remain under configured limits.

### P0.3 Enforce visible-target/action-target identity

Actions:

- Introduce immutable scoped query keys and generations for project detail, Agent workflows, restore modal data, provider setup, metrics, audit, and notifications.
- Invalidate old actionable data immediately when the target changes.
- Ignore completions whose full `{provider, context, resource, generation}` does not match current state.
- Disable destructive controls unless displayed object identity equals current selection identity.
- Move Agent/install jobs that survive navigation into a global operation store, or cancel them and wait.

Findings: FE-001 through FE-006, FE-019 through FE-021; BE-005 through BE-007.

Exit criteria:

- Deferred-promise tests cover both completion orders, failures, cancellation, target change, route change, and provider/context rebind.
- Every captured action argument is the exact visible current target.
- No stale modal/Agent/provider response can become actionable under another selection.

### P0.4 Close destructive-operation policy bypasses

Actions:

- Inventory every public method capable of external mutation with a machine-readable manifest.
- Require a unified, single-use, expiring, context/actor/target/fingerprint-bound plan for all dangerous operations.
- Close ImportProject auto-deploy, local volume `device/o=bind` detection, arbitrary RunImage, Provider Stop, concurrent provider install, stale update, restore, and Agent edit gaps.
- Revalidate Docker/source state immediately before apply.
- Make apply idempotent and represent partial success explicitly.

Findings: BE-013 through BE-016, BE-030, BE-035, BE-043 through BE-046, BE-058, BE-059, BE-070 through BE-072, BE-082; FE-002 through FE-005.

Exit criteria:

- Reflection/manifest test fails when a mutating service lacks policy metadata.
- Replay, concurrent apply, expiry, actor/context mismatch, stale source, and typed-name mismatch all fail before side effects.
- Each operation produces exactly one durable final/partial outcome and safe retry guidance.

### P0.5 Fix high-impact file and Agent privacy boundaries

Actions:

- Restrict Agent endpoints to loopback by default; require explicit informed consent and HTTPS/trust policy for remote endpoints; enforce final-origin redirects.
- Enforce project containment, regular-file checks, pre-read and aggregate byte/token caps.
- Replace regex-only redaction with structured allowlisting/data minimization plus exact outbound preview.
- Use atomic no-follow file replacement with mode preservation and durable audit/outbox behavior.
- Replace arbitrary renderer filesystem paths with OS-dialog grants or approved-root capabilities.
- Default raw Compose/env review DTOs to redacted structured data; require explicit reveal for external paths/secrets.

Findings: BE-037 through BE-040, BE-060, BE-062, BE-063, BE-067, BE-082; FE-053, FE-054, FE-057; OPS-040.

Exit criteria:

- Multiline PEM, opaque token, huge one-line, symlink, FIFO/device, traversal, absolute external path, and redirect test matrices pass.
- Failed/partial Agent edits preserve the old file and leave one visible durable outcome.
- Renderer calls cannot access unapproved host paths.

### P0.6 Make release publication fail closed

Actions:

- Require the exact tag target SHA to pass mandatory CI/security/package gates before release credentials are available.
- Move signing into isolated least-privilege jobs after dependency/build stages; minimize workflow token permissions and disable persistent checkout credentials where unnecessary.
- Require production signatures; sign/verify the installed Windows executable and uninstaller, not only the outer installer.
- Stop replacing release assets. Publish immutable artifacts, signed checksums/SBOM/provenance, and verify source/tag ancestry.
- Patch the Go toolchain to 1.26.5 or later supported security version and govern vulnerability waivers with owner, scope, evidence, expiry, and review.
- Pin actions/images/downloaded executables immutably and use frozen dependency installs.

Findings: OPS-004 through OPS-013, OPS-020, OPS-032 through OPS-036.

Exit criteria:

- A tag from an unapproved SHA cannot reach signing or publish jobs.
- Missing/partial signing configuration fails a stable release before publication.
- Verification checks inner payload signatures and immutable provenance after download.
- Waivers expire automatically and document reachability for the exact client/daemon paths.

## Phase 1 — Correctness, consistency, and lifecycle foundation

Target: following P0 containment; complete before broad feature expansion.

### P1.1 Migrate to canonical scoped identities

Actions:

- Define `ResourceKey`/scope types for installation, provider, Docker context, resource kind/native ID, source/import identity, and generation.
- Migrate project/service/cache/plan/stream/event tables and keys.
- Detect collisions in existing data and require deterministic merge/selection behavior.
- Scope cleanup, Compose/update/lineage services, Docker caches, forward identities, and frontend stores.

Findings: BE-005 through BE-007, BE-018, BE-028, BE-065; FE-001, FE-008, FE-020, FE-021, FE-029.

Exit criteria:

- Multi-provider/context fixtures with intentionally colliding names/native IDs never cross data or actions.
- Stale generations cannot overwrite newer snapshots in DB, cache, events, or UI.

### P1.2 Introduce transactional generations and retention

Actions:

- Write update-check and reconciliation snapshots under a run/generation transaction, promote only complete generations, and query only the promoted generation.
- Correct metrics tier range merging and raw project time buckets; make retention errors observable/retryable.
- Add time/count/size retention for audit, notification, update, cache, plan, and other durable/history data.
- Remove or hash/redact raw build-argument values; migrate/scrub existing rows.
- Expand database upgrade fixtures from every released schema, including failed/interrupted migrations.

Findings: BE-008 through BE-011, BE-043, BE-052, BE-054, BE-062 through BE-065, BE-083; FE-046; OPS-029.

Exit criteria:

- Overlapping runs and crash injection cannot expose mixed generations.
- Range queries include the newest sample exactly once and project totals use declared buckets.
- Retention is deterministic and bounded under a long-running synthetic workload.
- Schema fixtures cover every shipped/pre-release version being supported.

### P1.3 Root-own all work and enforce budgets

Actions:

- Add one root context/task group and admission manager.
- Reserve limits before creating process/PTY/reader/goroutine/session state.
- Coalesce Docker event reconciliation and update checks; remove duplicate WSL polling.
- Cap log/metrics/terminal/event/registry sessions and bodies; cap provider output and log line/history; bound registry maps.
- Make subprocess/stdio deadlines real and cancellation reach daemon-side helpers where possible.
- Implement one shutdown deadline with joined workers, diagnostics, and force cleanup after budget.

Findings: BE-002, BE-012, BE-019 through BE-024, BE-034, BE-036, BE-043, BE-047 through BE-050, BE-053, BE-056, BE-067 through BE-069, BE-071, BE-073, BE-074, BE-083; FE-004, FE-006, FE-022, FE-023, FE-045.

Exit criteria:

- Adversarial mixed workloads plateau within memory/goroutine/process/socket/disk limits.
- Admission-rejected terminal/job requests start no child work.
- All workers terminate within one tested shutdown budget, including blocked streams and provider rebind.
- Critical completion state remains queryable even when event delivery drops/disconnects.

### P1.4 Define event classes and generate the event catalog

Actions:

- Declare event name, payload schema, scope key, ordering, loss/backpressure, replay, and final-state policy in one catalog.
- Generate/validate Go publication, Wails forwarding, TypeScript runtime decoders, and contract tests.
- Add explicit overflow markers/cursors for logs and terminal streams.
- Separate critical completion from best-effort telemetry.

Findings: BE-004, BE-022 through BE-024, BE-069, BE-073; FE-009, FE-010, FE-019 through FE-023, FE-056; OPS-034.

Exit criteria:

- Catalog parity test proves every required backend topic reaches the frontend with a valid payload.
- Slow/disconnected clients have documented bounded behavior and do not lose final operation truth.

### P1.5 Make backup/restore a durable recovery product

Actions:

- Pin helper image by digest and minimize capabilities/mounts/network/root privileges.
- Stop labeling live-volume backup as unconditionally safe; add quiesce/snapshot hooks or a clear crash-consistency contract.
- Allocate archive names atomically, define metadata/ownership/xattr/ACL fidelity, and publish archive+sidecar durably.
- Track helper container identity, cancellation, cleanup, and final state.
- Restore into staging/new volume, validate, then promote; remove/quarantine partial state on failure.
- Add user-facing recovery, corruption, cleanup, and reset runbooks.

Findings: BE-030 through BE-036, BE-075; OPS-039.

Exit criteria:

- Concurrent plans never share a path.
- Fault injection at every IO/process phase never reports an unusable backup as complete.
- Round trips validate declared filesystem metadata across supported backends.
- Cancelled/failed operations leave no unknown helper containers or silently partial target volume.

### P1.6 Correct provider, terminal, and forwarding semantics

Actions:

- Move backend path-list/path/shell/sudo/credential semantics into the provider capability descriptor.
- Fail path mapping closed and apply saved terminal/Linux settings end to end.
- Reserve terminal capacity before process creation and reap every rejected/closed process.
- Probe effective default container user and represent root state as root/non-root/unknown.
- Make port-forward exposure explicit, preserve IPv4/IPv6/bind semantics, include bind in identity, cap listeners/sessions, and evict failed UDP state.
- Set/test explicit named-pipe ACL and quotas; surface bridge failure.

Findings: BE-025 through BE-029, BE-049, BE-055 through BE-057, BE-066, BE-068, BE-079, BE-080; FE-013, FE-014, FE-018, FE-030.

Exit criteria:

- Provider matrix tests assert exact backend environment/path behavior.
- Terminal failures are visible and no process/handle leaks under concurrency.
- Forwarding tests cover loopback/wildcard/concrete IPv4/IPv6/link-local and UDP failure recovery.
- Pipe access is limited to intended principals and connection counts.

### P1.7 Correct update, lineage, and mutation health behavior

Actions:

- Fix latest-check grouping and atomic generations.
- Rebind/revalidate plans at apply and make health rollback compare current baselines correctly, including no-healthcheck stability.
- Make finalization/shutdown/expiry reliable and expose partial history.
- Use a conformant Dockerfile parser with correct ARG scope/parser directives/target errors.
- Clean RunImage partial containers and avoid reporting external success as failure due only to local cache/audit writes.

Findings: BE-008, BE-009, BE-043 through BE-046, BE-051, BE-058, BE-059, BE-081.

Exit criteria:

- Stale/duplicate update rows cannot drive a plan.
- Invalid Dockerfile targets and parser uncertainty are explicit, never silently mapped to the last stage.
- Health rollback tests use before/after restart baselines and no-healthcheck services.
- Every update/run outcome is idempotent or explicitly partial.

## Phase 2 — Frontend usability, accessibility, and performance

Target: begin in parallel after P0 async-target primitives are established; finish before accessibility/performance release claims.

### P2.1 Repair data/status and stream UX contracts

Actions:

- Preserve last-known-good inventory slices on partial refresh failure; give each slice independent status/generation.
- Make log initial subscription ordering, cursor uniqueness, pause/unpinned counts, wrap virtualization, search selection, and export scope match labels.
- Surface terminal write/paste/resize/close errors and stream-stop cleanup failures.
- Represent settings-load failure as failure, serialize/coalesce saves, and avoid remount keys that disrupt caret/focus.
- Show port-forward, diagnostics, clipboard, notification, and “open folder” outcomes honestly; remove no-op controls.

Findings: FE-007 through FE-030, FE-055, FE-067; BE-022, BE-023.

Exit criteria:

- Fault-injected UI tests prove last-known-good data remains distinguishable from stale/failed data.
- Log UI/export/counter behavior is identical at 50k cap, pause, wrap, filters, and duplicate timestamps.
- Every user-triggered RPC failure produces an accessible, actionable state.

### P2.2 Establish accessible component primitives

Actions:

- Fix muted contrast in both themes and re-enable Axe color contrast.
- Implement modal/palette focus containment, Escape ownership, initial focus, and restoration.
- Implement WAI-ARIA tab keyboard/focus/panel semantics in shared Tabs and terminal tabs.
- Make DataTable column controls keyboard/touch accessible and virtual rows expose position/count.
- Apply consistent focus-visible styling, live regions/status roles, persistent labels, reduced-motion behavior, and keyboard loader skip.
- Require accessible names for icon-only buttons at the type/lint/test layer.

Findings: FE-031 through FE-043, FE-058, FE-059, FE-062.

Exit criteria:

- Axe runs with no globally disabled rules on every route/modal/theme/size fixture.
- Keyboard-only and screen-reader smoke covers navigation, destructive dialogs, settings, table customization, Agent, logs, and terminal tabs.
- Contrast, 200% zoom/reflow, reduced motion, and high-contrast mode pass documented criteria.

### P2.3 Split high-frequency state and bundle boundaries

Actions:

- Extract route feature controllers/reducers from `App.tsx` and isolate logs/metrics/terminals from unrelated rerenders.
- Dispose/suspend hidden xterm renderers and use measured variable-height virtualization for wrapped content.
- Avoid repeated full-array metrics/history work and preserve newest samples.
- Replace per-frame React loader state/O(n²) linking with CSS/canvas or bounded non-React animation; cancel/dedupe version calls and report only checks actually performed.
- Lazy-load Monaco, charts, terminal, Agent, and other heavy routes; define bundle and route latency budgets.

Findings: FE-044 through FE-051.

Exit criteria:

- Main initial chunk meets an agreed budget and heavy routes load on demand.
- Seeded 100-container/5,000-line workflows meet frame/latency/memory budgets on release hardware.
- Hidden terminals and route changes release resources predictably.

### P2.4 Make frontend tests high-signal and native-contract aware

Actions:

- Give chart fixtures real dimensions and eliminate routine warning noise.
- Add deferred async/lifecycle tests for every P0 target workflow.
- Enforce frontend coverage thresholds focused on branch/state-machine coverage, not only aggregate lines.
- Expand release UI across major routes, themes, sizes, keyboard flows, degraded states, and committed goldens.
- Add native packaged Wails/webview smoke; keep mock-browser tests for speed but do not call them host integration.
- Fix the existing Prettier failure.

Findings: FE-052, FE-059 through FE-066; OPS-024, OPS-034.

Exit criteria:

- Unit tests have no expected warning flood.
- Visual goldens run by default where declared and failures upload diffs.
- A packaged app smoke proves bindings/events/navigation on each supported release OS.
- `npm run typecheck`, lint, format, unit, build, audit, and release UI all pass cleanly.

## Phase 3 — Delivery, operations, documentation, and maintainability

Target: complete before final stable release sign-off; many items can run in parallel with Phases 1–2.

### P3.1 Make CI bounded, isolated, and reproducible

Actions:

- Add job/step timeouts, race jobs, project coverage policy, structured Go/Vitest/Playwright artifacts, and cancellation-safe cleanup.
- Isolate real-Docker integration from lint; do not expose a world-writable daemon socket to broad dependency/tool execution.
- Make local CI run the same tool versions/commands/matrices as hosted CI.
- Repair the Go/npm module boundary so `go list ./...` cannot discover `node_modules`.
- Align cross-build image Node/frontend behavior with actual production builds.

Findings: OPS-014, OPS-021 through OPS-026; FE-063, FE-065.

Exit criteria:

- CI cannot hang unbounded and always uploads machine-readable failure evidence.
- Race/coverage/security jobs are isolated and required.
- Local reproduction uses the same pinned toolchain and frozen dependency graph.
- Expected first-party Go package list is asserted exactly.

### P3.2 Validate real packages and supported platforms

Actions:

- Define exact OS/distribution/architecture support and artifact naming.
- Add real package structure, install, launch, migration, signature, uninstall, and update tests.
- Resolve macOS ARM64-only/ambiguous naming and move off deprecated runner images.
- Map SemVer prerelease versions into valid native package versions.
- Test next/prerelease paths rather than hardcoded 1.0.0 only.

Findings: OPS-017 through OPS-020, OPS-031, OPS-037.

Exit criteria:

- Every advertised platform/architecture has clean-system evidence for the exact release SHA.
- Package metadata/version/signature matches the tag and installed payload.
- Unsupported combinations are rejected or documented, not silently shipped.

### P3.3 Replace evidence prose with enforceable release state

Actions:

- Convert release criteria/evidence to machine-readable records with commit SHA, artifact digest, environment, result, waiver/expiry, and links.
- Require `pass` for mandatory items; “blocked,” “in progress,” TODO wording, and stale commit evidence must fail release readiness.
- Upload evidence with the release and retain it independently of mutable logs.
- Initialize rotating structured logging and add a state/log backup, migration-recovery, and reset runbook.

Findings: OPS-015, OPS-016, OPS-027, OPS-028, OPS-039.

Exit criteria:

- Release readiness is computed, not asserted in prose.
- Evidence for the tag target is complete, immutable, downloadable, and independently verifiable.
- Operators can locate logs/state, back them up, recover a failed migration, and reset safely.

### P3.4 Close governance and documentation gaps

Actions:

- Add an explicit license, SECURITY.md, supported-version/reporting policy, contribution guide, code of conduct as appropriate, and third-party notices process.
- Reconcile stale feature/tool/version claims and clearly distinguish desktop, experimental server, and remote Agent trust.
- Document privacy/data flows, retention, support boundaries, backup fidelity, forwarding exposure, provider permissions, and platform limitations.
- Configure dependency automation for Docker images and workflow-installed tools.

Findings: OPS-030, OPS-031, OPS-033, OPS-038 through OPS-040.

Exit criteria:

- A new user/contributor can determine legal terms, supported systems, security contact, data egress/retention, and recovery steps from the repository.
- Automated docs/version checks prevent known drift classes.

### P3.5 Reduce maintenance concentration

Actions:

- Split `App.tsx`, `App.test.tsx`, AgentPage, SettingsPage, and TerminalPage along state-machine/feature boundaries.
- Define dependency direction rules so Wails services adapt to domain APIs instead of accumulating logic.
- Add lint/architecture checks for maximum dependency cycles, forbidden layer imports, event/catalog parity, and generated output cleanliness.
- Remove dead settings or wire them end to end.

Findings: FE-047, FE-067; BE-054, BE-057; OPS-014, OPS-034.

Exit criteria:

- High-frequency feature changes no longer require editing the root application component.
- Settings have one schema defining defaults, validation, UI, runtime application, and tests.
- Runtime/version/event contract drift is caught in CI.

## Release gates by milestone

| Gate                   | Must be true                                                                                                                                        |
| ---------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------- |
| Server containment     | Server artifact absent from stable release, or all authenticated/authorized isolated-server tests and independent security review complete.         |
| Critical correctness   | Registry realm trust and all wrong-target frontend regressions fixed; destructive operation manifest/policy tests pass.                             |
| Data integrity         | Scoped identity migration, transactional generations, partial-success contract, and backup/file atomicity tests pass.                               |
| Lifecycle/resource     | Race suite, adversarial resource suite, and one-deadline shutdown suite pass with measured ceilings.                                                |
| Desktop boundary       | CSP/navigation/path capability/native-Wails contract tests pass on each supported OS.                                                               |
| Accessibility          | No suppressed mandatory Axe rules; keyboard/focus/live-region/contrast/reduced-motion matrix passes.                                                |
| Supply chain           | Exact-SHA CI gate, isolated signing, inner signature verification, immutable signed provenance/checksums/SBOM pass.                                 |
| Packaging/platform     | Clean install/launch/migrate/uninstall evidence exists for every advertised OS/architecture.                                                        |
| Operations/governance  | SECURITY/license/notices/support/recovery/privacy/retention docs complete and checked.                                                              |
| Clean quality baseline | Go format/vet/test/race/coverage policy and frontend typecheck/lint/format/test/build/audit/release-UI all pass without unexplained warnings/skips. |

## Suggested issue breakdown and ownership

| Workstream                  | Suggested owner profile              | First deliverable                                                      |
| --------------------------- | ------------------------------------ | ---------------------------------------------------------------------- |
| Server decision/security    | Security + backend architecture      | ADR and stable-release containment change.                             |
| Identity/operation protocol | Backend/data + frontend platform     | ResourceKey and Operation schemas with migration/test plan.            |
| Registry/credentials        | Backend security                     | Issuer binding and atomic config/credential test suite.                |
| Async UI integrity          | Frontend platform                    | Scoped query/operation primitive applied to Project and Agent.         |
| Lifecycle/resource limits   | Backend runtime                      | Root task group/admission API and one representative stream migration. |
| Persistence/reconciliation  | Data/backend                         | Transactional generation for update checks and Compose snapshots.      |
| Backup/recovery             | Backend + operations                 | Durable backup state machine and fault-injection harness.              |
| Accessibility/design system | Frontend + design QA                 | Correct shared Modal/Tabs/Button/DataTable contracts.                  |
| CI/release supply chain     | Build/release engineering + security | Exact-SHA gated isolated signing pipeline.                             |
| Package/platform validation | Release QA                           | Machine-readable support matrix and clean-system harness.              |
| Documentation/governance    | Maintainers + security/legal         | License/SECURITY/support/privacy/recovery baseline.                    |

## Backlog hygiene

- Preserve BE/FE/OPS IDs in issue titles and commits so remediation is traceable.
- Mark an item resolved only when the recommended regression/acceptance test is merged and passes in the appropriate platform matrix.
- When combining findings into one implementation, keep each ID as a checklist item; do not close adjacent findings merely because a shared abstraction landed.
- Record accepted risks with owner, rationale, affected deployment modes, compensating controls, evidence, expiry, and next review date.
- Re-run the source and executable review after Phase 0 and again before stable release; several findings interact, and a local fix can move risk to another layer.

## Report references

- [Executive summary and report index](./00-review-index-and-executive-summary.md)
- [Architecture and cross-cutting gaps](./01-system-architecture-and-cross-cutting-gaps.md)
- [Backend/data/concurrency detailed findings](./02-backend-data-correctness-concurrency.md)
- [Security/privacy/data-integrity lens](./03-security-privacy-and-data-integrity.md)
- [Frontend/UX/accessibility/performance detailed findings](./04-frontend-correctness-ux-accessibility-performance.md)
- [Build/test/release/docs detailed findings](./05-build-test-ci-release-documentation.md)
- [Validation evidence and limitations](./07-validation-and-review-method.md)
- [Complete finding register](./08-complete-finding-register.md)
