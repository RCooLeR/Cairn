# Complete Finding Register

- Review date: 2026-07-15
- Purpose: compact navigation across every primary detailed finding. The full evidence, impact, recommendation, and regression test are in the domain reports.

This register contains **190 catalog entries**. Severity below is normalized to the highest stated deployment severity except “Medium independently; amplifies another Critical finding,” which remains Medium. Cross-report overlap is intentional and these entries are not all independent root causes.

| Severity  |   Count |
| --------- | ------: |
| Critical  |       5 |
| High      |      91 |
| Medium    |      88 |
| Low       |       6 |
| **Total** | **190** |

## Backend, data, concurrency, and security

Source: [02-backend-data-correctness-concurrency.md](./02-backend-data-correctness-concurrency.md)

| ID     | Severity | Finding                                                                                            |
| ------ | -------- | -------------------------------------------------------------------------------------------------- |
| BE-001 | Critical | Server mode exposes an unauthenticated remote-control API                                          |
| BE-002 | High     | Ordinary Wails runtime request bodies are unbounded                                                |
| BE-003 | Critical | A registry can redirect Cairn's stored credentials to an attacker-selected Bearer realm            |
| BE-004 | High     | Three backend event topics required by the UI are never forwarded                                  |
| BE-005 | High     | Project identity omits Docker context and import path                                              |
| BE-006 | High     | Stale project cleanup is scoped only to provider, not context                                      |
| BE-007 | High     | Compose, update, and lineage APIs accept project IDs outside the active provider/context           |
| BE-008 | High     | Update “latest” grouping retains stale and duplicate checks                                        |
| BE-009 | Medium   | Update checks are persisted non-atomically and grow without a lifecycle boundary                   |
| BE-010 | High     | Metrics range queries discard newer retention tiers                                                |
| BE-011 | High     | Raw project metrics group on exact timestamps instead of time buckets                              |
| BE-012 | High     | Provider installation is detached from application lifetime                                        |
| BE-013 | High     | Provider install plans can be applied concurrently, out of order, and after abandonment            |
| BE-014 | High     | ImportProject can auto-deploy Compose content without a bound confirmation plan                    |
| BE-015 | High     | Local volume driver options bypass dangerous bind-mount confirmation                               |
| BE-016 | High     | Arbitrary image runs bypass planning when no direct bind mount is present                          |
| BE-017 | High     | Private image pulls ignore configured registry credentials                                         |
| BE-018 | High     | Docker object cache primary keys are not provider/context scoped                                   |
| BE-019 | High     | Log-stream sessions, readers, and rings have no global or per-client cap                           |
| BE-020 | High     | Log export materializes unbounded Docker history in memory                                         |
| BE-021 | High     | A newline-free Docker log record can grow without bound                                            |
| BE-022 | Medium   | Log pagination cursors are not unique and can skip duplicate records                               |
| BE-023 | High     | A transient log-reader failure permanently blocks reattachment                                     |
| BE-024 | High     | The event bus silently drops terminal and completion events                                        |
| BE-025 | High     | Default WSL port forwarding can expose services to LAN without resource limits                     |
| BE-026 | Medium   | IPv6 all-interface publishing is widened to IPv4                                                   |
| BE-027 | Medium   | Loopback, concrete IPv6, and link-local publishes are silently not forwarded                       |
| BE-028 | Medium   | Port-forward identity omits bind address                                                           |
| BE-029 | Medium   | Failed UDP backend sessions remain cached and blackhole traffic                                    |
| BE-030 | High     | Concurrent backup plans can allocate the same archive path                                         |
| BE-031 | High     | Backup helper uses a mutable image with excessive privileges                                       |
| BE-032 | High     | Live-volume backups are classified safe despite consistency risk                                   |
| BE-033 | Medium   | Backup format has no defined metadata-fidelity contract                                            |
| BE-034 | High     | Canceling the Docker CLI may not cancel the daemon-side backup/restore container                   |
| BE-035 | Medium   | Failed restore into a newly created volume leaves partial state                                    |
| BE-036 | Medium   | Backup shutdown does not wait and abandoned plans are never globally pruned                        |
| BE-037 | High     | Agent endpoint is an unrestricted SSRF and data-exfiltration destination                           |
| BE-038 | High     | Agent and import-review file reads are unbounded and accept special files                          |
| BE-039 | High     | Agent redaction and max-context-lines do not bound or reliably sanitize payloads                   |
| BE-040 | High     | Agent file replacement is non-atomic and path validation is raceable                               |
| BE-041 | High     | Registry token and index JSON bodies are unbounded                                                 |
| BE-042 | High     | Credential-helper preparation can fail open to inline credentials                                  |
| BE-043 | Medium   | CheckAll is not coalesced and has no reliable completion state                                     |
| BE-044 | High     | Update health rollback uses historical restart count and does not observe no-healthcheck stability |
| BE-045 | High     | Update plans are not rebound to current Docker and source state at application                     |
| BE-046 | High     | Update history finalization, shutdown, and plan expiry are not reliable                            |
| BE-047 | Medium   | WSL starts duplicate full-inventory polling loops                                                  |
| BE-048 | High     | Docker event bursts can launch unbounded overlapping reconciliations                               |
| BE-049 | Medium   | Docker bridge startup failure is silently hidden                                                   |
| BE-050 | High     | Shutdown has no shared deadline and can scale to minutes or hang                                   |
| BE-051 | Medium   | GPU usage remains stale after probe loss                                                           |
| BE-052 | Medium   | Metrics retention errors are ignored and suppress retry for an hour                                |
| BE-053 | Medium   | Metrics streaming sessions are unbounded                                                           |
| BE-054 | Medium   | Metrics interval and retention settings are saved but not applied                                  |
| BE-055 | Medium   | Windows-to-WSL Compose terminals use the host path-list separator                                  |
| BE-056 | High     | Terminal capacity is checked after process creation and rejected Windows sessions can leak handles |
| BE-057 | Medium   | Terminal and Linux provider settings are dead; default sudo flow cannot prompt                     |
| BE-058 | Medium   | RunImage leaves a stopped container after start failure                                            |
| BE-059 | High     | External mutations can succeed while Cairn reports failure because audit/cache persistence failed  |
| BE-060 | High     | Renderer APIs can read or overwrite arbitrary host files                                           |
| BE-061 | Medium   | Integer settings have no key-specific range validation                                             |
| BE-062 | High     | Resolved build-argument values are persisted as ordinary metadata                                  |
| BE-063 | High     | Import review returns raw Compose and env files, including paths outside the project root          |
| BE-064 | Medium   | Audit, notification, update-check, and update-history tables have no retention                     |
| BE-065 | Medium   | Compose reconciliation can race and commit an older snapshot last                                  |
| BE-066 | High     | Windows Docker bridge lacks an explicit least-privilege pipe ACL and connection quotas             |
| BE-067 | Medium   | Provider command execution buffers unlimited output and returns raw output in errors               |
| BE-068 | Medium   | Stdio-backed Docker connections advertise deadlines that do nothing                                |
| BE-069 | Low      | Background bus subscriptions leak their cleanup goroutine after bus close                          |
| BE-070 | Low      | Plan ID generation panics on entropy-source failure                                                |
| BE-071 | Medium   | Plan stores have inconsistent ownership, expiry, and shutdown semantics                            |
| BE-072 | Medium   | Provider Stop bypasses the confirmation policy used for Restart                                    |
| BE-073 | High     | Server event broadcasting has unbounded client/fan-out work and unsynchronized map reads           |
| BE-074 | High     | Public services lack a coherent request, session, and concurrency budget                           |
| BE-075 | Medium   | Backup sidecar success is reported without checking Close or durable flush                         |
| BE-076 | Medium   | The server container runs as root by default                                                       |
| BE-077 | Medium   | Registry login changes the caller's secret by trimming whitespace                                  |
| BE-078 | High     | Docker credential configuration updates are non-atomic read/modify/write transactions              |
| BE-079 | Medium   | Container terminals report the default root user as non-root                                       |
| BE-080 | Medium   | Terminal path-mapping errors fail open to paths in the wrong operating-system namespace            |
| BE-081 | Medium   | Dockerfile lineage parsing can select the wrong base image and persist incorrect update advice     |
| BE-082 | Medium   | Successful Agent file edits can lose their audit record and failed edits are not audited           |
| BE-083 | High     | Registry cache, limiter, and circuit maps grow with attacker-controlled host keys                  |

## Frontend correctness, UX, accessibility, performance, and tests

Source: [04-frontend-correctness-ux-accessibility-performance.md](./04-frontend-correctness-ux-accessibility-performance.md)

| ID     | Severity | Finding                                                                                   |
| ------ | -------- | ----------------------------------------------------------------------------------------- |
| FE-001 | Critical | Project-detail responses can display and operate on the wrong project                     |
| FE-002 | Critical | Agent analysis/draft/preview results can cross project boundaries                         |
| FE-003 | High     | Restore-volume backup loading can populate a different volume's modal                     |
| FE-004 | High     | Provider installation can continue after its UI/session is discarded                      |
| FE-005 | High     | Provider setup responses are not correlated with the setup session/backend                |
| FE-006 | High     | Agent chat persists across navigation but request ownership does not                      |
| FE-007 | High     | A partially failed full inventory refresh erases last-known-good slices                   |
| FE-008 | High     | Partial inventory refreshes race and share a misleading global status                     |
| FE-009 | High     | Initial log lines can be dropped before the stream ID ref updates                         |
| FE-010 | High     | Paused/unpinned log counters fail once the 50k cap is reached                             |
| FE-011 | High     | “Current buffer/current filters” log export exports different data                        |
| FE-012 | High     | Log “wrap” mode still clips content to a fixed-height virtual row                         |
| FE-013 | High     | Terminal writes, pastes, scheduled commands, and resize failures are unhandled            |
| FE-014 | Medium   | Rapid terminal-close events can select a session that has also closed                     |
| FE-015 | High     | Settings-load failure is represented as successfully loaded defaults                      |
| FE-016 | High     | Settings saves can overlap, duplicate, and clear busy state out of order                  |
| FE-017 | Medium   | Settings diagnostics present unknown/error data as real zero/disabled values              |
| FE-018 | High     | Port-forwarding errors are hidden by an early null return                                 |
| FE-019 | Medium   | Update progress uses an untracked completion timer that can clear a new job               |
| FE-020 | Medium   | Audit-log and notification refreshes accept obsolete responses                            |
| FE-021 | Medium   | Obsolete metrics start failures can restore stale errors                                  |
| FE-022 | Medium   | Stream stop operations discard cleanup failures                                           |
| FE-023 | Medium   | Debounced event-hook cleanup invokes application work during teardown                     |
| FE-024 | Medium   | Bulk selections can retain resources that are no longer visible or present                |
| FE-025 | Medium   | Log export's “Open folder” action only copies a path                                      |
| FE-026 | Medium   | Notification rows expose no-op buttons and do not mark viewed items read                  |
| FE-027 | Medium   | Agent streaming always steals scroll from users reading history                           |
| FE-028 | Medium   | Search-match selection can become out of range as live logs change                        |
| FE-029 | Medium   | DataTable virtual window state survives same-length dataset replacement                   |
| FE-030 | Medium   | Port-forwarding table is likely to clip at narrow widths                                  |
| FE-031 | High     | Muted text fails WCAG AA contrast in both themes                                          |
| FE-032 | High     | Axe explicitly disables the rule that would detect FE-031                                 |
| FE-033 | High     | Command palette is an incomplete modal and can close an underlying dialog                 |
| FE-034 | High     | Terminal tab arrow navigation leaves focus behind and can repeat the same transition      |
| FE-035 | Medium   | Shared Tabs changes selection without moving focus or relating tabs to panels             |
| FE-036 | High     | DataTable column customization is inaccessible to keyboard and touch users                |
| FE-037 | Medium   | Virtual DataTable rows omit absolute row position semantics                               |
| FE-038 | Medium   | Focus indicators do not consistently cover select, textarea, and custom focusables        |
| FE-039 | Medium   | Dynamic errors and progress statuses are generally not announced                          |
| FE-040 | High     | CairnLoader skip behavior is pointer-only                                                 |
| FE-041 | High     | JavaScript loader animation ignores reduced-motion preference                             |
| FE-042 | Medium   | Agent prompt textarea has no persistent accessible name                                   |
| FE-043 | Medium   | Icon-only Button accessibility is not enforced by its type contract                       |
| FE-044 | High     | DataTable fixed-height virtualization is incompatible with wrapped rows                   |
| FE-045 | Medium   | Hidden terminal tabs retain full runtime cost                                             |
| FE-046 | High     | Long-term metric history performs repeated full-array work and can omit the newest sample |
| FE-047 | High     | High-frequency live state is hosted in a 19k-line root component                          |
| FE-048 | High     | CairnLoader performs React updates every animation frame and O(n²) particle linking       |
| FE-049 | Medium   | Loader duplicates version work and does not cancel the underlying call                    |
| FE-050 | Medium   | Loader reports checks that were not actually performed                                    |
| FE-051 | Medium   | Production bundle is a single oversized main chunk                                        |
| FE-052 | Medium   | Chart tests render with invalid dimensions, so chart layout is not meaningfully verified  |
| FE-053 | Medium   | Diagnostics expose raw child-process command strings                                      |
| FE-054 | Medium   | External update URL is opened without frontend scheme/host validation                     |
| FE-055 | Medium   | Clipboard behavior is inconsistent and often silently fails                               |
| FE-056 | Medium   | Wails event and persisted-state boundaries bypass runtime type safety                     |
| FE-057 | Medium   | Frontend CSP and navigation policy are not visible in the document                        |
| FE-058 | Low      | Static Inter Medium file is declared as the entire 400–700 range                          |
| FE-059 | High     | Release UI coverage omits major routes, themes, sizes, and keyboard workflows             |
| FE-060 | High     | Default visual testing does not compare against committed goldens                         |
| FE-061 | High     | Browser E2E uses a mocked Wails runtime, leaving host integration unverified              |
| FE-062 | Medium   | ESLint has no JSX accessibility or UI-contract rules                                      |
| FE-063 | Medium   | Passing unit tests emit enough warnings to obscure real failures                          |
| FE-064 | High     | High-risk asynchronous and lifecycle paths have no focused regression tests               |
| FE-065 | Medium   | No enforced frontend coverage threshold was found                                         |
| FE-066 | Low      | Frontend formatting gate currently fails                                                  |
| FE-067 | Medium   | Settings input remounting can disrupt focus/caret after asynchronous saves                |

## Build, test, CI, release, documentation, and operations

Source: [05-build-test-ci-release-documentation.md](./05-build-test-ci-release-documentation.md)

| ID      | Severity | Finding                                                                                                         |
| ------- | -------- | --------------------------------------------------------------------------------------------------------------- |
| OPS-001 | Critical | Server mode exposes privileged desktop services without authentication or origin protection                     |
| OPS-002 | High     | Advertised server mode does not compile on Windows                                                              |
| OPS-003 | High     | The supplied server container cannot perform core Cairn work and loses state                                    |
| OPS-004 | High     | A release tag can publish without the exact commit passing CI or release gates                                  |
| OPS-005 | High     | All signing secrets are exposed to dependency installation and build processes                                  |
| OPS-006 | High     | Windows signs only the outer installer, leaving the installed executable and uninstaller unsigned               |
| OPS-007 | High     | Public production releases fail open when platform signing is unavailable                                       |
| OPS-008 | High     | Releases and assets are intentionally mutable                                                                   |
| OPS-009 | High     | CI/release binaries are built with a superseded Go security patch                                               |
| OPS-010 | High     | Docker/Moby findings are blanket-allowlisted without waiver governance or client/daemon reachability proof      |
| OPS-011 | High     | Docker build contexts can transmit ignored local secrets                                                        |
| OPS-012 | High     | Actions, images, OS packages, and a Zig executable download are not immutably pinned                            |
| OPS-013 | Medium   | Production builds can rewrite dependency manifests and resolve different frontend packages                      |
| OPS-014 | High     | npm's node_modules is inside the Go package discovery boundary                                                  |
| OPS-015 | High     | “Release validation” accepts blocked/in-progress evidence and validates TODO wording rather than readiness      |
| OPS-016 | High     | v1.0.0 is called release-ready despite explicit pre-v1 blockers and missing proof                               |
| OPS-017 | High     | Windows/macOS/AppImage package validation is mostly a non-empty-file check                                      |
| OPS-018 | Medium   | macOS release is ARM64-only, architecture-obscured, and built on a deprecated runner line                       |
| OPS-019 | High     | SemVer prerelease tags pass validation but generate invalid native package versions                             |
| OPS-020 | Medium   | macOS signing conditions disagree and partial secret configuration fails unpredictably                          |
| OPS-021 | Medium   | No recurring race gate or project-wide coverage policy exists                                                   |
| OPS-022 | Medium   | CI jobs can hang for hours and discard structured test/browser failure evidence                                 |
| OPS-023 | Medium   | Docker integration runs inside the broad lint job with a world-writable daemon socket                           |
| OPS-024 | Medium   | Browser E2E coverage does not exercise native webviews or all shipped visual platforms                          |
| OPS-025 | Medium   | The documented cross-build image can skip the frontend and carries an incompatible Node toolchain               |
| OPS-026 | Medium   | Local “CI” and toolchain-check tasks do not reproduce CI                                                        |
| OPS-027 | Medium   | Rotating structured logging exists but is never initialized by the application                                  |
| OPS-028 | Medium   | Release evidence is stale, partly ignored, and not independently verifiable                                     |
| OPS-029 | Medium   | Database upgrade coverage stops at a release-candidate fixture                                                  |
| OPS-030 | Medium   | The public repository lacks project license, security-reporting, contribution, and third-party notice artifacts |
| OPS-031 | Medium   | Supported operating systems, distributions, and CPU architectures are not stated precisely                      |
| OPS-032 | Medium   | SBOMs/checksums are not signed provenance and the server image has no supply-chain pipeline                     |
| OPS-033 | Low      | Dependency update automation misses Docker images and workflow-pinned Go tools                                  |
| OPS-034 | Medium   | Pinned Wails alpha runtime components are skewed and lack a protocol-contract smoke                             |
| OPS-035 | Medium   | Release tags are not validated for provenance and cancellation can interrupt release work                       |
| OPS-036 | Medium   | Workflow token permissions and checkout credentials are broader than necessary                                  |
| OPS-037 | Medium   | CI hardcodes a stable 1.0.0 smoke and never exercises next/prerelease version paths                             |
| OPS-038 | Low      | Documentation contains stale feature and tool-version claims                                                    |
| OPS-039 | Medium   | There is no user-facing state/log backup, migration-recovery, or reset runbook                                  |
| OPS-040 | Medium   | Local-agent documentation does not explain remote endpoint data disclosure and trust                            |

## Interpretation

- Conditional server findings are normalized to their exposed-server severity because that build/deployment is advertised by the repository.
- FE “Medium-High” entries are normalized High; “Low-Medium” and “Medium-Low” entries are normalized Medium.
- BE-076 remains Medium as an independent root cause; it amplifies BE-001 rather than creating a separate Critical root cause.
- BE-001 and OPS-001 are the same server exposure viewed from runtime and delivery layers. Other architectural themes also intentionally have multiple entries where separate fixes or ownership are required.
