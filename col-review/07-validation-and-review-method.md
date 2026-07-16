# Validation Evidence, Review Method, and Limitations

- Review date: 2026-07-15
- Workspace: `E:\Development\projects\apps\rcooler\Cairn`
- Branch: `feat/port-forwarding-ui`
- Reviewed HEAD: `92230977c065476a74dbb64f8133d8cddff88cfd`

## What was reviewed

This review treats the complete on-disk working tree as the product under review, including the pre-existing modified and untracked source files. The worktree was already substantially dirty when the review began. No existing application change was reverted, formatted, or otherwise rewritten. The only intentional review output is under `col-review/`.

The review covered:

- Go application composition, service bindings, provider lifecycle, Docker access, Compose detection, persistence, metrics, logs, terminals, updates, backups, registry access, port forwarding, Agent behavior, and confirmation-plan infrastructure.
- React/TypeScript state transitions, Wails request/event contracts, destructive-action target identity, streaming lifecycle, settings, inventory refresh, logs, terminals, modal behavior, accessibility, and performance.
- SQLite schema and repository semantics, cross-provider/context identity, retention, atomicity, and secret-adjacent persistence.
- Server-mode and Docker deployment trust boundaries.
- Build scripts, GitHub Actions, packaging/signing, release mutability, dependency/vulnerability gates, documentation, and governance files.
- Existing tests, release-UI tests, formatting, linting, compilation, race detection, and measured coverage.

Line references in the reports point to this reviewed working tree. They will move as the files are edited.

## Review method

The findings combine four kinds of evidence:

1. **Direct defect trace:** the report follows inputs and state transitions through the relevant call chain and identifies a deterministic incorrect outcome.
2. **Trust-boundary analysis:** the report follows credentials, file content, Docker authority, host paths, network listeners, and privileged operations across process/network boundaries.
3. **Lifecycle/concurrency analysis:** the report checks operation identity, cancellation, goroutine ownership, shutdown ordering, event loss, stale completion handling, and persistence ordering.
4. **Executable validation:** builds, tests, race detection, linters, format checks, release-UI tests, and coverage were run where the host permitted them.

Each detailed finding is labeled as a confirmed defect, risk, or gap and includes confidence. A risk is not represented as a proven exploit when exploitability depends on deployment or platform configuration.

## Validation summary

| Area                      | Command or evidence                                                               | Result                                                                                           | Interpretation                                                                                                                                       |
| ------------------------- | --------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------- |
| Frontend type safety      | `npm run typecheck`                                                               | Pass                                                                                             | Current TypeScript tree compiles. This does not validate runtime Wails payloads that are asserted/cast.                                              |
| Frontend lint             | `npm run lint`                                                                    | Pass                                                                                             | Configured ESLint rules pass. JSX accessibility rules are not configured.                                                                            |
| Frontend formatting       | `npm run format:check`                                                            | **Fail**                                                                                         | `frontend/src/settings/SettingsPage.tsx` does not match Prettier.                                                                                    |
| Frontend unit tests       | `npm test -- --reporter=dot`                                                      | Pass: 7 files, 103 tests                                                                         | Tests pass, but emit extensive chart/canvas/Wails warnings and do not cover the highest-risk async identities.                                       |
| Frontend production build | `npm run build`                                                                   | Pass with warning                                                                                | Main JS chunk is approximately 1,274.03 KiB minified / 338.11 KiB gzip; Vite reports the >500 KiB chunk warning.                                     |
| Release UI                | `npm run test:release-ui`                                                         | 15 pass, 1 skip                                                                                  | Default Chromium/mock-runtime scenarios pass. Committed-golden comparison is skipped and major routes/workflows are absent.                          |
| npm dependency audit      | `npm audit --audit-level=moderate`                                                | Pass: 0 reported vulnerabilities in 729 dependencies                                             | Point-in-time registry result; it does not cover Go, release actions, base images, or runtime configuration.                                         |
| Go formatting             | `gofmt -l` over first-party Go files                                              | Pass: no paths returned                                                                          | Reviewed Go source is gofmt-clean.                                                                                                                   |
| Diff whitespace           | `git diff --check`                                                                | Pass                                                                                             | No whitespace errors; Git only warned that `App.tsx` will convert CRLF to LF on a future write.                                                      |
| Windows Go vet            | `go vet -unsafeptr=false . ./internal/...`                                        | Pass                                                                                             | Standard vet analyzers found no issue in the selected packages.                                                                                      |
| Windows desktop build     | `go build -o <temp> .`                                                            | Pass                                                                                             | The current desktop target compiles on Windows.                                                                                                      |
| Windows test compilation  | `go test -c -o <temp-dir> ./internal/...`                                         | Pass                                                                                             | All internal test packages compile for Windows.                                                                                                      |
| Windows server build      | `go build -tags server -o <temp> .`                                               | **Fail**                                                                                         | Pinned Wails has duplicate Windows/server declarations and undefined `windowsApp`; advertised Windows `build:server` is broken.                      |
| Linux server cross-build  | `GOOS=linux CGO_ENABLED=0 go build -tags server -o <temp> .`                      | Pass                                                                                             | Headless Linux binary compiles; this does not make its unauthenticated deployment safe or functionally complete.                                     |
| WSL Go unit tests         | `go test -count=1 -timeout=12m . ./internal/...`                                  | Internal packages pass; root/shell normal Linux build blocked by missing GTK/WebKit dev packages | All domain/internal packages that built executed successfully. The missing GUI libraries are a validation-host dependency, not a Cairn test failure. |
| WSL shell/server tests    | `CGO_ENABLED=0 go test -tags server -count=1 . ./internal/shell`                  | Pass                                                                                             | Root has no tests; shell tests pass under the dependency's headless-compatible configuration.                                                        |
| WSL race detection        | first-party `internal/...` except `internal/shell`, with `go test -race -count=1` | Pass                                                                                             | No race was observed in existing tests. The shell package could not be race-built without unavailable GTK/WebKit libraries.                          |
| Go statement coverage     | non-shell internal packages, atomic coverage                                      | 68.1% total                                                                                      | Coverage is useful but uneven and does not include platform-only paths or most adversarial workflows.                                                |
| Shell coverage            | server-tag shell test                                                             | 15.9%                                                                                            | Application composition, forwarding contracts, and shutdown paths have little exercised coverage.                                                    |
| Go package discovery      | `go list ./...`                                                                   | **Unexpected package**                                                                           | Includes `frontend/node_modules/flatted/golang/pkg/flatted`, demonstrating the Go/npm module-boundary defect.                                        |

## Go coverage detail

Coverage was measured in WSL against the reviewed working tree, excluding `internal/shell` from the aggregate because normal Linux Wails compilation requires GTK/WebKit development packages that are not installed in this environment.

| Package                                    | Statements covered |
| ------------------------------------------ | -----------------: |
| `internal/apperror`                        |              77.5% |
| `internal/backups`                         |              74.7% |
| `internal/bus`                             |              87.6% |
| `internal/compose`                         |              74.5% |
| `internal/docker`                          |              74.6% |
| `internal/dockerbridge`                    |      0.0% on Linux |
| `internal/lineage`                         |              80.8% |
| `internal/logging`                         |              72.0% |
| `internal/logsvc`                          |              74.5% |
| `internal/metrics`                         |              84.5% |
| `internal/portforward`                     |              80.5% |
| `internal/providers`                       |              68.4% |
| `internal/registry`                        |              76.9% |
| `internal/security`                        |              72.2% |
| `internal/services`                        |          **46.4%** |
| `internal/store`                           |              69.9% |
| `internal/terminal`                        |              63.8% |
| `internal/updates`                         |              82.0% |
| `internal/shell` (separate server-tag run) |          **15.9%** |

The two lowest meaningful areas, `services` and `shell`, are precisely where authorization/scope checks, API contracts, service wiring, event forwarding, job ownership, and shutdown composition live. Raising total coverage without targeting those behaviors would not materially reduce the main risks.

## Windows test-runner limitation

Ordinary Windows `go test` invocations compiled successfully but their generated test executables remained at the initial Windows thread state (`Initialized`) with zero CPU. The same occurred when a compiled test binary was launched by the desktop command runner. Compilation, `go vet`, and `go build` continued to work. Exited test processes also remained temporarily visible as stale WMI entries.

This behavior is specific to the Codex desktop execution host and was not counted as a product defect. To obtain executable evidence instead of silently accepting compile-only validation, the suite was rerun inside the configured Ubuntu WSL environment. Those Linux test binaries executed and passed as summarized above.

## Static-tool limitation

`golangci-lint.exe` was present, but this host applied the same non-starting process behavior to that Go-built executable, so a fresh local golangci-lint result could not be obtained. The repository's `go vet` configuration was run directly and passed. CI configuration declares golangci-lint, but the release workflow does not consume CI as a required gate; see the delivery report.

`govulncheck` was not installed as a standalone binary. The repository wrapper uses `go run` and contains an explicit Docker advisory allowlist. Its policy and package-boundary problems were reviewed statically and current advisory/version facts were checked against official upstream sources.

## Platform and integration limits

The following were not claimed as validated by this review:

- Native macOS UI, Colima, codesigning, notarization, DMG installation, and universal/Intel execution.
- Native Linux GTK/WebKit desktop execution and distro-specific packaging beyond repository scripts/evidence.
- Real privileged WSL provider installation or destructive provider lifecycle operations.
- Live private-registry credential-helper behavior across every supported backend.
- LAN/IPv6/firewall behavior for the new Windows/WSL port-forward implementation.
- Signed Windows executable/uninstaller trust-chain verification on an actual produced installer.
- Recovery from power loss during SQLite, Docker config, Agent file, backup, or update writes.
- Multi-user Windows named-pipe ACL behavior at runtime.
- End-to-end browser-to-Wails-to-Docker tests. Playwright uses a mocked Wails runtime.

These are explicit residual validation gaps, not implicit passes.

## Why passing tests did not invalidate the findings

Most high-severity findings require one of the following conditions absent from current tests:

- responses completing out of order after the selected project/backend/modal target changes;
- two Docker contexts containing the same Compose project name;
- a malicious registry challenge or oversized remote response;
- an unauthenticated/cross-origin server-mode caller;
- more than one metrics retention tier in a single query;
- event subscriber backpressure and loss of terminal/job completion data;
- concurrent backup plans created within the same second;
- process shutdown during detached provider/update/backup work;
- a remote Agent endpoint plus multiline or unrecognized secrets;
- log streams at their 50,000-line cap or newline-free records;
- keyboard-only and assistive-technology workflows.

The reports therefore recommend scenario-specific regression tests rather than merely increasing the number of ordinary happy-path assertions.

## Severity interpretation

- **Critical:** credible credential theft, unauthenticated host/Docker-equivalent control, or destructive action against a different target than the UI represents.
- **High:** material data loss/corruption, security/privacy exposure, broken core operation, or severe reliability/accessibility failure under plausible use.
- **Medium:** important correctness, lifecycle, performance, or hardening defect with narrower conditions or recoverability.
- **Low:** maintainability, polish, or low-likelihood defense-in-depth issue.

Severity reflects impact and plausibility in Cairn's intended role. Because Docker authority is commonly host-equivalent, exposed Docker, terminal, host-file, provider-install, or backup capabilities receive higher impact than an ordinary desktop preference bug.

## Recommended revalidation after remediation

At minimum, the remediation branch should run:

1. Full Windows, Linux, and macOS CI from a clean checkout.
2. `go test -race` on all packages on platforms that build their platform-specific code.
3. A required, expiring vulnerability-policy gate with an explicit first-party package list.
4. Frontend typecheck, lint including JSX accessibility, format, unit tests, and coverage thresholds.
5. Real Wails smoke tests for request/event contracts, not only mocked Playwright tests.
6. Cross-context project/cache isolation tests.
7. Adversarial server auth/origin/body-size and registry-realm tests.
8. Failure-injection tests for atomic writes, audit persistence, cancellation, and shutdown.
9. Keyboard/screen-reader/contrast tests without disabling color contrast.
10. Signed-package verification of the installer, embedded executable, generated uninstaller, notarization, checksums, and architecture labels.
