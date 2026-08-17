# 2026-07-28 Registry Credential-Helper Remediation and Final Acceptance

- Follow-up date: 2026-07-28
- Re-review base: `a223bfb462037969d42277ef20dcae28468e5f8e`
- Branch: `codex/review-remediation`
- Scope: registry credential policy, helper discovery, WSL failure classification, login transaction/audit lifecycle, frontend error presentation, secret ownership, single-instance/tray lifecycle, cross-platform build/CI invariants, and final Windows package acceptance
- Relationship to prior reports: append-only follow-up to `11-2026-07-24-full-rereview-and-fix-status.md`
- Current status: final source validation and exact standalone Windows binary/WebView acceptance passed; unrestricted release, installer, supply-chain, and cross-platform gates remain open

## Executive outcome

The reported error was produced by Cairn's registry-login flow. It was not an IPv6-listener error, an Omada WLAN/VLAN error, a Docker project-detection failure, or proof that the active Docker provider was unavailable.

The active Windows WSL backend was reachable, but none of the Docker credential-helper candidates was installed and responsive inside that backend. Cairn was configured to require helper-backed credential storage, so refusing to run `docker login` was the correct fail-closed behavior. Two parts of the resulting behavior were incorrect:

1. Cairn classified an absent credential helper as `E_PROVIDER_NOT_READY`, falsely suggesting that Docker/WSL itself was unavailable.
2. The renderer displayed the serialized Wails `RuntimeError` envelope and discarded the structured repair hints instead of presenting an actionable registry-authentication error.

The end-to-end trace then found six additional defects in the same flow: WSL transport failures and helper absence were not distinguished precisely; pre-command failures were missing from the audit trail; login could be audited as successful before verification/finalization completed; audit failure could leave an ambiguous credential result; the frontend could offer or retain a secret while the credential policy was disabled or unknown; Test Auth hid structured diagnostics; and a secret could remain attached after the registry identity changed.

`RR-2026-031` through `RR-2026-047` now record seventeen defects. The first eight cover the registry/helper flow. Final native validation then exposed `RR-2026-039`: relaunching a tray-resident Cairn started a second privileged runtime, which contended for the first process's private named pipe and failed with Access denied. The independent release audit exposed `RR-2026-040`: native Linux/macOS build tasks still permitted build-time module mutation and icon generation from stale artwork despite the corrected Windows path, and `RR-2026-041`: hosted/local vet tasks still excluded repository build tooling. A final lifecycle and dependency pass then exposed six more issues: project deletion/update/rollback admission and stale-plan races (`RR-2026-042` through `RR-2026-044`), incomplete image-load post-mutation reconciliation (`RR-2026-045`), a late terminal-open PTY leak (`RR-2026-046`), and five newly published npm advisories in the resolved dependency graph (`RR-2026-047`). The working tree fixes all seventeen delta findings and adds focused, stress, race, browser, audit, or task-graph regression evidence.

The final frozen source passed complete Go and frontend validation, targeted race tests, production-browser validation, resource-aware Windows compilation, PE/build-info inspection, and native flow smoke. The exact standalone binary launches a nonblank WebView, detects the active WSL provider and both projects, renders the missing-helper failure as structured `E_REGISTRY_AUTH` guidance, and redirects a second launch to the existing window without starting another Cairn runtime. RR-2026-029 is therefore closed for this exact standalone binary/WebView gate.

This does **not** mean every original `BE-*`, `FE-*`, or `OPS-*` finding is closed or that Cairn is ready for unrestricted production release. No NSIS installer was produced, the final main bundle remains over the 500 kB warning threshold, and the architectural, supply-chain, signing, installer, external-writer, and non-Windows platform residuals remain.

## Trigger and observed host evidence

### User-visible failure before this remediation

The Wails boundary returned a structured error whose relevant fields were:

```text
code: E_PROVIDER_NOT_READY
message: Docker credential helper is not available
detail: Cairn is set to Docker credential helper mode, but none of these
        helpers responded: wincred.exe, desktop.exe, desktop, pass,
        secretservice.
```

The error also carried three useful repair hints, but the affected form rendered the serialized JSON envelope rather than the structured message, detail, hints, and code.

### Read-only environment probes

| Probe | Observed result | Interpretation |
| --- | --- | --- |
| Active backend | An active WSL 2 distro was running | The WSL provider was available enough to execute backend probes. |
| Docker config | Present in the backend user profile | Absence of a config file was not the cause. |
| `docker-credential-wincred.exe` | Not available in the active distro | No Windows-credential helper was bridged into this WSL backend. |
| `docker-credential-desktop.exe` | Not available in the active distro | No Docker Desktop `.exe` helper was visible from WSL. |
| `docker-credential-desktop` | Not available in the active distro | No native/bridged Desktop helper alias was available. |
| `docker-credential-pass` | Not available in the active distro | The common standalone WSL `pass` backend was not installed/initialized. |
| `docker-credential-secretservice` | Not available in the active distro | No Secret Service helper was installed/available. |
| Conventional Docker Desktop helper location | Helper not present on the Windows host | Cairn was not merely overlooking the conventional Docker Desktop helper path. |

The operational condition was therefore real: helper-backed login could not proceed on the active backend. The application defect was the taxonomy and presentation of that condition, not the refusal to put a credential inline into Docker's config.

No package, password-store, keyring, or Docker configuration change was made automatically. Installing and initializing a credential helper changes the user's security/storage environment and remains an explicit operator action.

## End-to-end flow reviewed

| Stage | Reviewed boundary | Required behavior after remediation |
| --- | --- | --- |
| 1. Settings load | `registry.credentials_mode` crosses store/service/Wails/UI boundaries | The UI treats the policy as unknown until settings have loaded. Only exact `docker_helper` enables Cairn-managed login. |
| 2. Login entry points | Settings Registries, Push Image, and the shared login modal | Disabled or unknown policy cannot open a secret-entry form. A policy refresh to disabled closes the form and clears its secret. |
| 3. Request validation | Registry, username, secret, and secret kind | Registry/username are normalized as identifiers; the secret remains opaque. Missing inputs fail before any Docker command. |
| 4. Provider resolution | Active provider and stdin-capable Docker runner | A true provider/transport failure remains `E_PROVIDER_NOT_READY`; a storage-helper problem does not invalidate the provider. |
| 5. Transaction lock | Cairn process lock plus backend Docker-config lock | Login preparation, Docker mutation, verification, finalization, audit, and rollback are serialized for Cairn writers. |
| 6. Credential policy | `none`, `docker_helper`, empty/default, or invalid value | `none` blocks Cairn login; empty/default fails closed to required helper mode; invalid policy fails visibly. |
| 7. Helper selection | Exact `credHelpers`, global `credsStore`, normalized alias, candidate detection | A configured helper must respond. Missing candidates yield `E_REGISTRY_AUTH`; cancellation/deadline and WSL transport failures retain their own taxonomy. |
| 8. Prior-state snapshot | Matching inline auth, helper mapping, and prior helper record | Cairn records enough prior state to restore the affected registry identity without exposing either old or submitted secrets. |
| 9. Docker login | `docker login <registry> -u <username> --password-stdin` | The secret is sent only over stdin, omitted from the audit command, and redacted from returned diagnostics. |
| 10. Verification | Authenticated registry HTTP check with the submitted in-memory credential | Docker exit zero alone is not logical success. Verification failure enters the rollback/failure path. |
| 11. Finalization | Enforce helper mapping and remove matching inline auth | Finalization must complete before success is audited. Failure restores the prior helper/config state. |
| 12. Terminal audit | One `registry.login` outcome after the logical result is known | The code attempts exactly one terminal outcome record. Preparation failures are included. Audit failure blocks or rolls back login and returns explicit `E_INTERNAL`/partial-state metadata. |
| 13. UI presentation | Wails error envelope to visible alert | Title, body, detail, bounded repair hints, and code render without exposing the raw JSON envelope. |
| 14. Secret cleanup | Close, identity changes, policy changes, and retry edits | Registry/preset/username/secret-kind changes clear the old secret and stale error; secret edits clear only the stale error. |

## Delta finding register

### RR-2026-031 - Missing credential helper was classified as provider failure

- Severity: **Medium**
- Related findings: BE-042, FE-018, FE-035
- Affected path: `internal/registry/credentials.go`

**Failure**

When none of the helper candidates responded, Cairn returned `E_PROVIDER_NOT_READY`. That code is intended for a Docker/provider/backend that cannot service the operation. In the observed case, WSL was running and backend commands executed successfully; only secure registry credential storage was unavailable.

**Impact**

- The UI and user were directed toward Docker/provider repair even though containers and projects could continue to work.
- Provider-health automation could interpret a registry storage dependency as a broader runtime outage.
- The error obscured the safe alternatives: install/initialize a helper or disable Cairn-managed login and manage credentials externally.

**Remediation**

- Missing, uninitialized, or nonresponsive helpers now return `E_REGISTRY_AUTH`.
- The detail lists the attempted candidates.
- Repair hints explain backend-local helper setup, the WSL-specific alternatives, and the `none` credential-policy option.
- The provider remains usable for non-registry flows.

**Regression coverage**

- Default helper-policy login now expects `E_REGISTRY_AUTH`.
- Direct helper detection/check tests assert that helper storage failures never become `E_PROVIDER_NOT_READY`.
- Tests assert Docker login is not invoked when the helper preflight fails.

### RR-2026-032 - WSL helper absence and WSL transport failure were collapsed

- Severity: **Medium**
- Related findings: BE-042, BE-067
- Affected path: `internal/registry/credentials.go`

**Failure**

An error returned by the WSL command runner was insufficient to distinguish:

- `docker-credential-pass: not found` with exit 127 inside a healthy distro;
- failure to launch `wsl.exe`;
- a nil command result;
- a provider-level `E_PROVIDER_NOT_READY`;
- WSL service/queue/buffer failures; and
- caller cancellation or deadline expiry.

Treating all of them alike either invalidated a healthy provider or risked continuing candidate probes after the backend itself had failed.

**Impact**

- Healthy WSL providers could be reported unavailable because one executable did not exist.
- A genuinely unavailable WSL transport could be mislabeled as a registry credential problem.
- Cancellation/deadline intent could be lost behind a generic helper error.

**Remediation**

- Caller cancellation and deadline are checked before the probe and after a failed command and remain `E_CANCELLED`/`E_TIMEOUT`.
- WSL nil results, launch failures/exit `-1`, provider-not-ready errors, and known WSL service/queue/buffer diagnostics remain `E_PROVIDER_NOT_READY`.
- Exit 127 with an in-distro helper-not-found diagnostic remains `E_REGISTRY_AUTH`.
- Detection stops immediately when the backend is unavailable, but tries the bounded candidate list when only an individual helper is absent.
- WSL NUL-separated diagnostics are normalized before safe bounded presentation.

**Regression coverage**

- Missing WSL helper tests require every bounded candidate to be tried without invalidating the provider.
- Nil-result, launch-failure, and WSL-service diagnostic cases require one probe followed by `E_PROVIDER_NOT_READY`.
- Already-cancelled and already-expired contexts require no backend command and preserve their typed code.

### RR-2026-033 - Structured Wails repair guidance was discarded or rendered as JSON

- Severity: **Medium**
- Related findings: FE-018, FE-035, FE-056
- Affected paths: `frontend/src/api/errors.ts`, `frontend/src/App.tsx`

**Failure**

The frontend parsed a subset of the nested Wails error envelope but omitted `repairHints`. Several form-error surfaces treated the error as plain text, so the operator saw the serialized `RuntimeError` JSON or a generic message instead of the intended guidance.

**Impact**

- The error looked like an internal crash rather than a recoverable configuration condition.
- The most useful operator actions were hidden.
- Raw envelopes are noisy and can encourage copying more diagnostic data than necessary.

**Remediation**

- `ParsedAppError` now carries `repairHints`.
- Nested `cause` and top-level envelopes are both supported.
- Hints are accepted only from arrays, restricted to strings, trimmed, deduplicated, capped at eight, and capped at 2,048 characters per item.
- Shared form and global-action errors render title, body, distinct detail, a "How to fix" list, and code in an accessible alert.
- The raw serialized envelope is not rendered when structured parsing succeeds.

**Regression coverage**

- Parser tests cover nested/top-level hints, malformed values, trimming, deduplication, count limits, and length limits.
- App tests feed the original Wails-shaped error and require readable detail/hints/code with no raw JSON.
- A non-registry Compose action test proves the shared global surface also preserves structured detail and repair guidance.

### RR-2026-034 - Login preparation failures were absent from the audit trail

- Severity: **High**
- Related findings: BE-059, BE-064, BE-082
- Affected path: `internal/registry/auth.go`

**Failure**

Failures before the Docker command returned directly. Examples included a provider that could not accept stdin, Docker-config lock failure, helper-policy failure, helper discovery failure, and prior-state snapshot failure. No `registry.login` failed outcome was attempted for those security-relevant login attempts.

**Impact**

- The audit UI could imply that no login attempt occurred.
- Repeated helper/policy failures could not be reconstructed during support or incident review.
- Audit database failure and operation failure were not combined into one explicit fail-closed outcome.

**Remediation**

- Every validated login attempt that reaches provider preparation now attempts a terminal failed audit even if Docker login never runs.
- Audit uses a detached, bounded five-second context so caller cancellation does not silently erase the outcome.
- The audit command includes only registry, username, and the literal `--password-stdin`; it never includes the secret.
- If the failed preparation cannot be audited, Cairn returns `E_INTERNAL`, explicitly states that Docker login was not run, and instructs the operator to restore audit database health.

**Regression coverage**

- Helper-preparation failure creates one failed `registry.login` entry with the expected target/command.
- The submitted secret is absent from the audit entry and serialized returned error.
- A closed audit store blocks the operation, returns `E_INTERNAL`, and proves Docker login was not invoked.

### RR-2026-035 - Audit success preceded logical login completion and audit failure left ambiguous state

- Severity: **High**
- Related findings: BE-059, BE-078, SEC-14
- Affected paths: `internal/registry/auth.go`, `internal/registry/credentials.go`

**Failure**

Docker process exit was treated too closely as success. The audit could record success before Cairn completed authenticated HTTP verification and helper-only finalization. A later verification/finalization failure could therefore contradict the durable audit. Conversely, audit failure after a successful external login could leave the new credential installed while returning a generic failure. Command failure plus audit failure also did not preserve the complete failure context clearly.

**Impact**

- A durable success record could describe a credential Cairn later rejected or rolled back.
- Retrying after an audit error could operate on an already changed external credential state.
- Support could not distinguish completed rollback from partial credential/config mutation.
- Secret-bearing command diagnostics required defense-in-depth redaction.

**Remediation**

- Logical outcome is computed across Docker command, authenticated verification, and helper/config finalization.
- On logical failure, Cairn restores the previous helper record and matching config entries before attempting one final failed audit.
- On logical success, Cairn attempts one final successful audit only after verification and finalization.
- If successful-login auditing fails, Cairn restores the previous credential/config state and returns `E_INTERNAL`.
- Completed audit-failure rollback returns partial metadata with state `rolled_back_audit_missing` and `cleanupRequired: false`.
- Incomplete rollback returns explicit partial metadata (`restore_incomplete_audit_missing` or `audit_missing_restore_incomplete`) with `cleanupRequired: true`.
- Original operation, restore, and audit failures are joined without including submitted or prior secret values.

**Regression coverage**

- Verification failure produces exactly one failed outcome and restores the prior credential.
- Finalization failure produces exactly one failed outcome and restores the prior state.
- Command failure plus audit-store failure returns `E_INTERNAL` without losing the underlying failure or leaking the secret.
- Success-audit failure restores both the original helper credential and original Docker config and reports the completed rollback state.

### RR-2026-036 - Login UI could collect secrets while credential policy was disabled or unknown

- Severity: **High**
- Related findings: BE-042, FE-018, FE-056
- Affected paths: `frontend/src/App.tsx`, `frontend/src/settings/SettingsPage.tsx`

**Failure**

Registry login entry points did not derive availability from a known, successfully loaded credential policy. The UI could offer a login form while the mode was `none`, still loading, stale after a refresh, failed to load, or contained an unknown value. The backend would eventually reject some of these requests, but the renderer had already collected a secret it had no valid reason to request.

**Impact**

- Cairn could solicit a password/token despite the user's explicit "No Cairn-managed credentials" policy.
- A stale modal could keep a secret after the policy changed.
- UI and backend policy could visibly disagree.

**Remediation**

- Login is enabled only when settings are in a loaded state and the exact normalized mode is `docker_helper`.
- `none`, unknown values, loading, and load failure disable login with a specific reason.
- Settings Registries and Push Image pass the same disabled policy/reason to their login controls.
- The open and submit handlers enforce the policy defensively in addition to button disabling.
- If a settings refresh disables login while the modal is open, Cairn invalidates the request owner, closes the modal, clears the secret, and reports why.
- Settings copy now says "Require Docker credential helper"; it no longer implies a best-effort preference or inline fallback.

**Regression coverage**

- Mode `none` disables both Settings and Push Image login and never renders a secret field.
- An unresolved settings request disables login until policy is known.
- A refresh from `docker_helper` to `none` closes the open modal, clears it, and prevents service invocation.

### RR-2026-037 - Test Auth hid the actionable structured failure

- Severity: **Medium**
- Related findings: FE-018, FE-035
- Affected path: `frontend/src/components/settings/SettingsTables.tsx`

**Failure**

The registry account table collapsed a rejected `RegistryService.TestAuth` call into a generic "Auth failed" badge/message. Structured code, detail, and repair hints from helper, provider, or registry errors were discarded.

**Impact**

- The operator could not tell whether to repair a helper, reconnect the provider, change credentials, or retry a registry.
- The detailed backend taxonomy added little value on this primary diagnostics surface.

**Remediation**

- Failed status text is parsed through the shared structured error parser.
- The row renders title/body, distinct detail, repair hints, and error code in an accessible alert.
- Verified state is exposed as a status region rather than only visual text.

**Regression coverage**

- A Wails-shaped `E_REGISTRY_AUTH` failure must show detail, hints, and code without raw JSON.
- A verified account must expose its result through the expected live status semantics.

### RR-2026-038 - Registry identity changes retained a secret for the previous target

- Severity: **High**
- Related findings: FE-003, FE-053, SEC-09
- Affected path: `frontend/src/App.tsx`

**Failure**

Changing the registry preset, custom registry URL, username, or secret kind retained the previously entered secret. That secret was collected under a different target/identity interpretation but could then be submitted to the new registry/account.

**Impact**

- A password/token intended for one registry could be sent to another registry.
- A token intended for one username or authentication kind could be reused under another identity.
- A stale error could appear to describe newly edited credentials.

**Remediation**

- Registry preset, registry URL, username, and secret-kind changes clear the secret and stale error.
- Selecting Custom clears the preset-derived URL so the operator must name the target explicitly.
- Editing the secret clears the stale error while preserving the new secret value.
- Closing or policy-disabling the modal clears all login state.

**Regression coverage**

- One interaction test exercises registry URL, username, secret-kind, retry-secret, and Custom preset transitions and asserts the required clearing semantics.
- Policy-refresh coverage proves a secret cannot survive an administrative mode change.

### RR-2026-039 - Relaunch created a competing Cairn runtime instead of restoring the tray window

- Severity: **High**
- Related findings: BE-050, BE-069, BE-071, FE-033, OPS-017, OPS-024
- Affected paths: `internal/shell/app.go`, `internal/shell/single_instance_test.go`

**Failure**

Closing Cairn's main window leaves the application running in the system tray by design. Launching `cairn.exe` again did not activate that existing application; it started a second complete Cairn process. The second process initialized provider/runtime resources and attempted to own the same private Windows Docker bridge named pipe as the first process. Native diagnostics then reported Access denied because the original Cairn runtime still owned the pipe.

**Impact**

- The user could see a degraded second window even though the original runtime and Docker provider were healthy.
- Two privileged service graphs could access shared settings/database/provider state concurrently.
- The second runtime could contend for private named pipes, jobs, watchers, notification/tray ownership, and other process-scoped resources.
- Relaunching a hidden/minimized tray application did not implement the expected "show the existing app" desktop behavior.

**Remediation**

- Wails `SingleInstanceOptions` now uses the stable application identifier `app.cairn.desktop`.
- `OnSecondInstanceLaunch` restores the existing main window: it calls Show, unminimizes only when required, and focuses the window.
- `application.New` now acquires the Wails process-wide single-instance lock before Cairn binds the initial provider runtime, starts the private bridge, or acquires other provider-owned resources. A rejected second launcher therefore exits before resource contention.
- The main-window reference is protected by an `RWMutex` because the second-instance callback can race window construction; a nil window is handled safely.
- System-tray activation and second-instance activation share the same tested restore helper rather than carrying divergent show/focus behavior.

**Regression and native coverage**

- Unit tests assert Show / IsMinimised / UnMinimise / Focus order for a minimized window.
- A visible window is shown/focused without an unnecessary unminimize call.
- A nil/racing pre-window callback is safe.
- Complete Go tests and vet pass; race-enabled `./internal/shell ./internal/registry` passes.
- Native first launch established the WSL runtime and project inventory.
- Launching the exact binary again left exactly one `cairn.exe` process, restored/focused the existing window, produced no private-pipe/Access denied error, and preserved the detected projects.

### RR-2026-040 - Native Linux and macOS builds bypassed dependency and artwork invariants

- Severity: **High**
- Related findings: OPS-013, OPS-014, OPS-017, OPS-025, OPS-037; RR-2026-024
- Affected paths: `build/Taskfile.yml`, `build/linux/Taskfile.yml`, `build/darwin/Taskfile.yml`, `build/windows/Taskfile.yml`

**Failure**

The earlier release-task remediation was incomplete outside Windows:

- native Linux and macOS builds still depended on `common:go:mod:tidy`;
- their native `go build`/`garble build` flags omitted `-mod=readonly`; and
- Linux/macOS icon generation invoked `common:generate:icons` without first synchronizing canonical source artwork, while only the Windows `.syso` path explicitly ran asset synchronization; and
- the shared synchronization task passed `-SkipLinuxIcons`, so a direct Linux DEB/RPM package task could retain stale `build/linux/icons/hicolor/**` payloads even after the canonical icon changed.

This contradicted the documented claim that build-time module mutation had been removed and that canonical artwork was coupled to resource generation.

**Impact**

- A native hosted package job could mutate `go.mod` or `go.sum` while producing an artifact, so the packaged source state would no longer be demonstrably identical to the reviewed checkout.
- A standalone Linux/macOS package build could embed stale branding even when canonical artwork had changed.
- Windows and Docker-cross builds enforced stronger integrity rules than native Linux/macOS builds, creating platform-dependent release behavior.

**Remediation**

- Removed `common:go:mod:tidy` from both native build dependency graphs.
- Prepended `-mod=readonly` to both development and production native Linux/macOS build flags.
- Made `common:generate:icons` depend on `common:sync:assets`, so every platform synchronizes canonical artwork before icon generation.
- Removed `-SkipLinuxIcons` from that shared synchronization path, so direct Linux packages also regenerate their hicolor payload set.
- Removed the now-redundant Windows-only explicit synchronization step; Windows `.syso` generation inherits the same shared invariant.
- Docker cross-builds retain their existing `-mod=readonly` enforcement.

**Validation and remaining scope**

- `task --list-all` parses the complete task graph after the change.
- A static fail-closed assertion confirms neither native task references `common:go:mod:tidy` and both declare `-mod=readonly`.
- `task --dry common:generate:icons` shows `common:sync:assets` before `wails3 generate icons`.
- A real full asset synchronization regenerated the Linux icon set successfully and produced no unexpected tracked artwork diff.
- Workflow `actionlint`, modified PowerShell parsing, and `git diff --check` pass.
- This closes the task-graph defect. It does not substitute for producing and exercising signed native Linux/macOS packages on those operating systems.

### RR-2026-041 - Hosted and local vet tasks omitted repository tooling

- Severity: **Medium**
- Related findings: OPS-013, OPS-015, OPS-025; RR-2026-024
- Affected paths: `.github/workflows/ci.yml`, `Taskfile.yml`

**Failure**

Go tests had been broadened to `./...`, and final manual evidence used full-tree vet, but the hosted CI job and top-level `go:lint` task still ran:

```text
go vet -unsafeptr=false . ./internal/...
```

That pattern omitted the repository's `tools/iconset` package even though the now-validated nested npm module boundary makes full-root discovery deterministic.

**Impact**

- A static defect in release/build tooling could pass both the advertised local lint task and hosted CI.
- Local, manual, and hosted definitions of "full Go vet" disagreed.
- The review could cite stronger evidence than the repeatable pipeline actually enforced.

**Remediation and validation**

- Hosted CI and `task go:lint` now run `go vet -unsafeptr=false ./...`.
- The exact broadened vet command passes on the final tree.
- `actionlint` accepts both workflows and `task --list-all` parses the complete task graph.

### RR-2026-042 - Project removal did not cancel and join project-owned mutations

- Severity: **High**
- Related findings: BE-005, BE-043 through BE-046, BE-059, BE-065, BE-071; RR-2026-003, RR-2026-007, RR-2026-011
- Affected paths: `internal/store/project_operations.go`, `internal/store/projects.go`, `internal/updates/executor.go`

**Failure**

Project update and rollback workers were application-owned, but deletion was not coordinated with work owned by the same scoped project. `ForgetAndDelete`, explicit delete, and stale detector cleanup could remove a project snapshot while an admitted update/rollback was still invoking Compose or writing final update history. A delete followed by redetection/import of the same project ID created an ABA risk: late work from the old incarnation could finish against state that now represented the new incarnation.

Stale cleanup had an additional time-of-check/time-of-use problem. It discovered stale rows before the transaction, but its later SQL deletion was still expressed as a broad stale predicate. A row that became stale after the pre-scan could therefore be removed without having acquired that project's lifecycle fence.

**Impact**

- Compose mutation could continue after the user removed the project.
- Old-incarnation completion/history could become associated with a newly detected same-ID project.
- A detector reconciliation could delete a project it had not leased and drained.
- Canceling the deletion caller could reopen the gate while canceled workers were still unwinding.

**Remediation**

- Added one process-local lifecycle gate keyed by canonical provider, context, and project ID.
- Every update/rollback worker registers a cancellable project operation and releases it only after the background worker exits.
- Project deletion marks the key deleting, advances its incarnation revision, cancels active operations, and joins them before destructive database work begins.
- If the deletion caller is canceled while waiting, a detached join retains the deletion fence until all canceled workers actually release; new work cannot overlap that unwind interval.
- Stale cleanup leases a sorted, exact list of pre-scanned project IDs. The transaction revalidates each exact ID and predicate before deleting project-owned mutable state and the row. There is no later broad ungated delete.
- Read-only generation probes for unknown IDs use implicit revision zero and do not retain arbitrary map entries.

**Validation**

- `TestProjectOperationGateCanceledDeletionStaysFencedUntilOperationsExit`
- `TestDeleteStaleDetectedProjectsOnlyDeletesLeasedCandidates`
- stale/gate stress runs repeated 50 times
- full store/update suites, `go vet`, and race-enabled store/update suites passed

### RR-2026-043 - Concurrent same-project update/rollback plans were not exclusive

- Severity: **High**
- Related findings: BE-014 through BE-016, BE-043 through BE-046, BE-071
- Affected paths: `internal/store/project_operations.go`, `internal/store/projects.go`, `internal/updates/executor.go`, `internal/updates/manager_test.go`

**Failure**

Two independently confirmed plans for one project could both pass ordinary plan validation and start. Their pulls, builds, tags, Compose up operations, rollback-history transitions, health checks, and compensating rollbacks could then interleave. A second plan created before the first mutation could also remain apparently valid after the first operation had changed the project.

**Remediation**

- Mutation admission is exclusive per scoped project.
- Successful admission advances the project lifecycle revision before external work starts.
- Plan generation probes fail with typed `E_CONFLICT` while a mutation owns the project.
- A second concurrent apply/rollback fails with typed `E_CONFLICT`.
- Any plan captured before a completed or admitted mutation presents the old revision and fails with typed `E_PLAN_EXPIRED`.
- Plan generation is sampled before reading project-owned state. Rollback history is re-read after the sample, closing the delete/reimport ABA window.
- Peer-context cancellation is observed synchronously at worker handoff as well as through `context.AfterFunc`, so an already-canceled deletion peer cannot briefly start new mutation work.

**Validation**

- `TestProjectOperationGateMutationIsExclusiveAndAdvancesRevision`
- update/rollback concurrency and pre-existing-plan expiration regressions
- mutation/cancellation stress runs repeated 20 times
- race-enabled store/update suites passed

### RR-2026-044 - Confirmed update and rollback plans did not bind the stored Compose configuration

- Severity: **High**
- Related findings: BE-014 through BE-016, BE-043 through BE-046
- Affected paths: `internal/updates/project_config_fingerprint.go`, `internal/updates/executor.go`, `internal/updates/project_config_fingerprint_test.go`, `internal/updates/manager_test.go`

**Failure**

Apply re-read enough state to find the project and working directory, but it did not prove that the stored project/service configuration still matched what the user reviewed. Between plan and apply, detector/import refresh could change Compose files, service identity, image reference, build context, Dockerfile, target, or stable service metadata while the old confirmation remained usable.

**Remediation**

- Update and rollback plans capture a versioned SHA-256 fingerprint over the stored project target and stable service configuration.
- The fingerprint includes scoped project identity, name, working directory, ordered Compose-file set, Compose project name, and deterministically ordered service IDs/names/image/build fields and metadata.
- Volatile status, health, replica counters, pin state, and last-seen timestamps are intentionally excluded so ordinary observation refresh does not invalidate a safe confirmation.
- Apply re-reads project plus services after exclusive admission and compares the fingerprint before starting the job.
- Rollback also re-reads the exact scoped history row and requires it to remain rollback-available.
- A mismatch fails with `E_PLAN_EXPIRED` and instructs the user to create and review a fresh plan.

**Validation**

- deterministic/reordered/volatile fingerprint tests
- mutations of every bound project/service field change the fingerprint
- update and rollback reject changed confirmed configuration
- volatile snapshot changes remain admissible
- full, stress, vet, and race suites passed

**Residual**

This fingerprint covers Cairn's stored normalized configuration. It does not hash the current bytes of Compose files, Dockerfiles, referenced env files, or other transitive on-disk inputs. An external editor can therefore change file contents after planning without changing the stored rows. Closing that residual requires a bounded, canonical dependency-closure digest captured during planning and revalidated immediately before Compose execution.

### RR-2026-045 - Image load errors lost the post-mutation outcome and reconciliation contract

- Severity: **High**
- Related findings: BE-024, BE-050, BE-060, BE-074; RR-2026-016, RR-2026-017
- Affected paths: `internal/docker/create.go`, `internal/docker/client.go`, `internal/docker/client_test.go`

**Failure**

Calling Docker `ImageLoad` crosses the mutation boundary: the daemon may import one or more images even if the upload call, local archive revalidation, response decoding, response close, or caller context later fails. Some post-call failures returned without reconciling the image cache or publishing invalidation, and the returned error did not distinguish a daemon-confirmed loaded image from an unknown mutation outcome. A terminal-looking line such as `Loaded image:` with no image identity could also be treated as completion.

**Remediation**

- Every return after invoking `ImageLoad` performs best-effort image reconciliation from a cancellation-detached bounded path and then publishes image invalidation.
- Errors after the mutation boundary carry typed partial-resource metadata:
  - `state=loaded` plus the image identity when a nonempty terminal load record was observed;
  - `state=unknown`, `id=unknown` when the daemon outcome cannot be proven.
- Job completion carries the confirmed result only when completion was actually observed.
- Response read, close, upload, archive close, and archive identity failures are joined without dropping the underlying cause.
- `Loaded image:` and `Loaded image ID:` count as terminal only when the trimmed identifier is nonempty.
- Existing response byte/message/result limits and strict JSON/error semantics remain fail closed.

**Validation**

- post-upload transport loss, response-close failure, malformed trailing data, archive replacement, daemon error, empty/progress-only response, excessive size/count, and empty terminal-identity regressions
- each post-mutation failure proves one reconciliation and one image invalidation
- focused Docker tests, race tests, full Go tests, vet, and `golangci-lint` passed

**Residual**

Reconciliation is best effort. If the Docker backend is simultaneously unavailable, Cairn reports the accurate partial state and publishes invalidation, but it cannot prove the daemon's final inventory until a later successful refresh.

### RR-2026-046 - A terminal opened after navigation could leak an invisible backend PTY

- Severity: **High**
- Related findings: BE-050, BE-056, BE-071; FE-013, FE-014, FE-064; RR-2026-012, RR-2026-020
- Affected paths: `frontend/src/components/terminal/TerminalPage.tsx`, `frontend/src/components/terminal/TerminalPage.lifecycle.test.tsx`

**Failure**

Opening a host/backend/project/container terminal mutates backend state before the promise returns. If the user navigated away while that RPC was pending, the unmounted page ignored the late session object. A newly mounted Terminal page could already have completed its initial enumeration, leaving the live PTY with no visible tab and no local owner to close it.

**Remediation and validation**

- All open paths pass returned sessions through one adoption helper.
- A mounted current page adopts the session normally.
- An unmounted page immediately asks the backend to close a late-created session instead of silently dropping it.
- Errors and busy state are not written after unmount.
- The initial session list continues to merge sessions added/removed while enumeration is pending, preserving the earlier RR-2026-020 fix.
- A lifecycle regression opens a host terminal, unmounts/navigates, remounts, resolves the old request, and proves `CloseTerminal` is issued for the invisible late session.
- Complete frontend tests and the release-browser suite passed.

### RR-2026-047 - Newly published frontend dependency advisories were present in the final lock

- Severity: **High**
- Related findings: SEC-14, OPS-011, OPS-013, OPS-025
- Affected paths: `frontend/package.json`, `frontend/package-lock.json`

**Failure**

A live final audit on 2026-07-28 found five advisories after the earlier source review:

- brace-expansion CPU denial of service, `GHSA-3jxr-9vmj-r5cp`;
- brace-expansion memory-exhaustion denial of service, `GHSA-mh99-v99m-4gvg`;
- DOMPurify custom-element hook-policy bypass with a possible second-order XSS gadget, `GHSA-c2j3-45gr-mqc4`;
- js-yaml quadratic CPU consumption through merge-key chains, `GHSA-52cp-r559-cp3m`;
- PostCSS source-map path traversal and arbitrary `.map` file disclosure, `GHSA-r28c-9q8g-f849`.

The first attempted global brace-expansion override reached patched `5.0.8` but broke legacy minimatch 3's callable CommonJS contract (`expand is not a function`). A zero-audit lock that breaks the linter is not a valid repair.

**Remediation**

- Upgraded the supported parent toolchain from ESLint `9.39.4` to `10.8.0` and `@eslint/js` `9.39.4` to `10.0.1`.
- Existing `typescript-eslint 8.61.0` explicitly supports ESLint 10.
- The resolved graph now uses minimatch `10.2.6`, which natively consumes brace-expansion `5.0.8`; no compatibility override remains.
- Raised the DOMPurify override floor to `3.4.12`.
- Raised the direct PostCSS floor to `8.5.18`; the lock resolves `8.5.24`.
- The ESLint 10 graph removes js-yaml from the installed dependency graph.

**Validation**

- clean `npm ci`: 670 packages installed and the Go-module boundary hook passed;
- final `npm audit --json`: zero vulnerabilities at every severity;
- ESLint 10, TypeScript, Prettier, 28 files / 343 Vitest tests, production Vite, and Playwright release UI all passed;
- the production chunk-size warning remains unrelated and explicitly residual.

Primary advisory records:

- <https://github.com/advisories/GHSA-3jxr-9vmj-r5cp>
- <https://github.com/advisories/GHSA-mh99-v99m-4gvg>
- <https://github.com/advisories/GHSA-c2j3-45gr-mqc4>
- <https://github.com/advisories/GHSA-52cp-r559-cp3m>
- <https://github.com/advisories/GHSA-r28c-9q8g-f849>

## Result taxonomy after remediation

| Condition | Typed result | Docker login run? | Credential state | Audit behavior |
| --- | --- | ---: | --- | --- |
| Policy is `none` | `E_CONFLICT` | No | Unchanged | Failed preparation outcome is attempted. |
| Policy is empty/default | Helper mode is required | Only after helper preflight | Helper-backed only | Normal terminal outcome contract. |
| Policy is unknown/invalid | `E_INTERNAL` | No | Unchanged | Failed preparation outcome is attempted. |
| All helpers are absent/uninitialized | `E_REGISTRY_AUTH` | No | Unchanged/restored | One failed preparation outcome is attempted. |
| WSL/provider transport is unavailable | `E_PROVIDER_NOT_READY` | No | Unchanged/restored | One failed preparation outcome is attempted when provider identity is available. |
| Caller cancels helper probe | `E_CANCELLED` | No | Unchanged/restored | Bounded detached failed outcome is attempted. |
| Helper probe deadline expires | `E_TIMEOUT` | No | Unchanged/restored | Bounded detached failed outcome is attempted. |
| Docker login exits nonzero | Typed command/auth error | Yes | Previous state restored when possible | One terminal failed outcome after rollback. |
| HTTP verification rejects credential | `E_REGISTRY_AUTH` or typed transport result | Yes | Previous state restored when possible | One terminal failed outcome after rollback. |
| Helper-only finalization fails | Typed finalization result | Yes | Previous state restored when possible | One terminal failed outcome after rollback. |
| Failure audit cannot be written | `E_INTERNAL` | Depends on failure stage | Restored when possible; partial metadata if not | Audit outage is explicit; no false success. |
| Success audit cannot be written | `E_INTERNAL` | Yes | Previous state restored when possible | Completed or incomplete rollback is explicit. |
| Command, verification, finalization, and audit succeed | Success | Yes | New helper-backed credential retained; matching inline auth removed | Exactly one successful terminal outcome. |

## Final validation evidence

### Final frozen-source validation

| Area | Final result | Evidence |
| --- | --- | --- |
| Go tests | Passed | Native Go 1.26.5 `go test ./...` passed on the final source tree. |
| Go static/security analysis | Passed | `go vet -unsafeptr=false ./...` passed on the same final source tree; `golangci-lint run --timeout=5m` reported **0 issues**; and the pinned `govulncheck` wrapper reported **no reachable vulnerabilities**. The identical full-tree vet scope is enforced by hosted CI and `task go:lint`. |
| Race-sensitive stateful paths | Passed | Race-enabled suites passed for store, updates, Docker, registry, shell, terminal, backups, bus, Compose, logs, metrics, providers, runtime scope, security, services, Docker bridge, lineage, logging, port forwarding, and soak status. This includes project deletion/admission/fingerprint, single-instance, registry transaction, image-load, and terminal lifecycle changes. |
| Frontend unit/component tests | Passed | Vitest passed **28 files / 343 tests**, including structured repair hints, registry policy/secret ownership, Test Auth, Agent refresh/approval ownership, terminal late-open cleanup, and other re-review regressions. |
| Frontend dependency / Go module boundary | Passed | A real clean `npm ci` installed 670 packages and ran `frontend/scripts/ensure-go-module-boundary.mjs`. Final `npm audit --json` reports **0 vulnerabilities**. The generated `frontend/node_modules/go.mod` declares `github.com/RCooLeR/Cairn/frontend/node_modules`; root `go list ./...` returned 24 packages and zero packages beneath `frontend/node_modules`. This closes OPS-014 for the final tree. |
| TypeScript | Passed | Final-tree TypeScript compilation/typecheck passed. |
| ESLint | Passed | Final-tree ESLint **10.8.0** passed after the supported parent upgrade removed the vulnerable legacy minimatch/js-yaml chain. |
| Prettier | Passed | Final-tree Prettier check passed. |
| Production frontend build | Passed with retained size warning | The final production build completed. The main application chunk is **501.66 kB** (**124.82 kB gzip**) and remains above the configured 500 kB warning threshold. |
| Production browser / accessibility smoke | Passed | Playwright release UI passed **16 tests**, with the committed-golden comparison remaining an intentional opt-in skip (**1 skipped**). The suite includes current-preview enforcement, nonempty root, uncaught-`pageerror` failure, route/degraded accessibility, layout, and performance checks. |
| Cross-platform task graph | Passed for static/task validation | `task --list-all` parsed; native Linux/macOS tasks no longer invoke build-time tidy, enforce `-mod=readonly`, and the icon dry run synchronizes canonical artwork before generation. Native packages remain an open platform gate. |
| Windows build/resources | Passed for standalone PE | The repository-pinned Wails CLI/runtime path produced the final production executable and architecture-correct resource object. The Windows binary checker passed. |

### Final acceptance checklist

- [x] Native Go 1.26.5 complete tests pass on the frozen tree.
- [x] Targeted race-enabled shell and registry tests pass.
- [x] `go vet ./...` passes.
- [x] Complete frontend suite passes: 28 files / 343 tests.
- [x] TypeScript, ESLint, and Prettier pass.
- [x] Fresh production Vite build passes; the 501.66 kB / 124.82 kB gzip main-chunk warning is retained as a residual.
- [x] Release-UI production boot/page-error/accessibility suite passes: 16 passed, 1 opt-in golden skipped.
- [x] Repository-pinned Wails `v3.0.0-alpha2.103` is reflected in the final binary dependency metadata.
- [x] Canonical Windows resources and `wails_windows_amd64.syso` are regenerated and hashed.
- [x] The exact production executable is stamped with version, commit, and UTC build date.
- [x] PE checker verifies AMD64, icon/group-icon, manifest, `asInvoker`, per-monitor-v2 DPI, version, company, product, and description metadata.
- [x] `go version -m`, file length, and SHA-256 are recorded below.
- [x] The exact executable launches a nonblank native WebView.
- [x] Native startup detects Ubuntu WSL, Docker Engine, inventory, and both canonical projects.
- [x] Native missing-helper login displays structured `E_REGISTRY_AUTH` detail/hints without raw RuntimeError JSON or secret disclosure.
- [x] Native relaunch leaves exactly one process, restores the same window, avoids private-pipe contention, and retains projects.
- [x] Clean `npm ci` runs the nested Go-module postinstall boundary; root `go list ./...` reports 24 packages and zero beneath `frontend/node_modules` (OPS-014).
- [x] Native Linux/macOS task graphs are readonly, omit build-time tidy, and synchronize canonical artwork before icon generation (RR-2026-040).
- [x] Hosted CI and local lint vet the complete root module, including repository tooling (RR-2026-041).
- [ ] No NSIS installer was produced; install, upgrade, uninstall, installed-payload, shortcut/icon, and Authenticode checks remain open.
- [ ] Exact-SHA hosted release gating, isolated signing, immutable publication/provenance, and macOS/Linux native package validation remain open.

## Final Windows binary evidence

| Evidence | Final value |
| --- | --- |
| Executable | `bin/cairn.exe` |
| App version | `v1.0.0` |
| Source stamp | `a223bfb46203-dirty` |
| Build date (UTC) | `2026-07-28T11:32:05Z` |
| File size | `23,080,448` bytes |
| SHA-256 | `FA617C648FDF370A4A20F9456809185556299A7661EFEC32C5D93700DDE4C220` |
| Go build info | `go1.26.5`, `GOOS=windows`, `GOARCH=amd64`, `CGO_ENABLED=0`, production tag, trimpath |
| Wails build CLI | Repository-pinned `.cache\tools\wails3.exe`, `v3.0.0-alpha2.103` |
| Wails dependency | `github.com/wailsapp/wails/v3 v3.0.0-alpha2.103` |
| Resource object | `wails_windows_amd64.syso` (repository-root transient build input; removed after linking and verification) |
| Resource object size | `67,580` bytes |
| Resource object SHA-256 | `5D2C2662D550AF07F57863A9E54851BB7513D1C79035629009C75D812EFF257A` |
| PE architecture | AMD64 - passed |
| Icon/group-icon resources | Present - passed |
| Manifest | Present with `asInvoker` execution level - passed |
| DPI awareness | Per-monitor-v2 - passed |
| FileVersion / ProductVersion | `1.0.0.0` / `1.0.0` - passed |
| Company / product | `Cairn` / `Cairn` - passed |
| File description | `A clean Compose-first Docker manager for Windows, macOS, and Linux` |
| `scripts/check-windows-binary.ps1` | Passed against the exact executable |
| Release-artifact/installer checker | Not claimed: no NSIS installer or complete release artifact set was produced |

### Native first-launch proof

- The exact executable launched a visible, nonblank native Wails WebView.
- Active provider: WSL 2.
- Docker Engine responded successfully, and container, image, and volume inventories loaded.
- Multiple canonical projects were detected across partially running and stopped states.
- This proves the concrete WSL scope-casing/project-detection regression remains fixed in the final executable; it does not close BE-005's general global identity redesign.

### Native registry-helper proof

- With no supported helper available in the active WSL backend, the login modal rendered `E_REGISTRY_AUTH` as structured user guidance.
- The message included the actionable detail and repair hints.
- The UI did not show the raw Wails `RuntimeError` JSON envelope.
- No submitted secret appeared in the rendered error.
- The provider, container inventory, and projects remained available because missing credential storage no longer invalidates Docker provider health.

### Native single-instance/relaunch proof

- Relaunching the exact executable while Cairn was resident left exactly one `cairn.exe` process.
- Wails restored/focused the existing native window.
- No second private named-pipe owner was created and no Access denied/pipe error appeared.
- The detected projects remained present after relaunch.

### Acceptance interpretation

RR-2026-029 is **closed for the exact standalone Windows executable and native WebView boot** identified by the hash above. RR-2026-039 is fixed and native-validated for relaunch of that executable. RR-2026-040 and RR-2026-041 are fixed and task/CI validated; platform-native Linux/macOS package acceptance remains open.

This evidence does not certify an installer or unrestricted production release. No NSIS package was produced, so installed payloads, shortcuts/icons, upgrade/uninstall, WebView bootstrap, Authenticode chain/timestamp, and clean-machine behavior remain untested. It also does not close the architectural, supply-chain, signing/provenance, external Docker-config-writer, macOS, or Linux residuals.

## Operator resolution for this machine

Two supported policies are intentionally distinct:

1. **Cairn-managed registry login**
   - Keep Settings > Registries > Credential mode set to **Require Docker credential helper**.
- Install and initialize a compatible helper in the active WSL backend.
   - For standalone WSL this commonly means a correctly initialized `pass` or Secret Service setup; a Docker Desktop helper is valid only if Docker Desktop actually supplies it to that backend.
   - Re-run login only after `docker-credential-<helper> list` responds successfully in the same backend Cairn uses.

2. **Externally managed registry login**
   - Set Credential mode to **No Cairn-managed credentials**.
   - Manage `docker login` and credential storage outside Cairn in the active backend.
   - Cairn intentionally disables its own login form in this mode and will not collect a registry secret.

The missing helper does not disable project discovery, container inventory, logs, terminal, or other non-registry flows. If those fail, diagnose their provider/scope/network paths independently.

## Residual risks and recommendations

The remediation above is bounded to the reviewed registry login/helper flow. The following remain:

- **External Docker config writers:** BE-078 remains partial. Cairn writers honor Cairn's process/backend lock and compare before publication, but Docker CLI, Docker Desktop, or another process does not honor that lock. A narrow external-writer compare/replace race remains.
- **Crash durability:** The login flow now has coherent in-process outcome/rollback/audit ordering, but there is no durable pre-mutation operation intent/outbox that can reconcile a process or host crash during the external Docker/helper mutation.
- **Audit availability:** Cairn fails closed or rolls back when the audit store is unavailable, but an unavailable audit database cannot contain the attempted terminal record. Operations/health UI should expose persistent audit degradation globally.
- **Helper lifecycle and supply chain:** Cairn probes helpers but does not install, initialize, upgrade, attest, or recover the user's password store/keyring. Documentation should define supported helpers per backend and safe initialization/recovery.
- **Real integration matrix:** Fake-provider regression coverage is strong, but final evidence should include prepared real Docker/WSL/helper test hosts without using production credentials.
- **Credential-policy UX:** The UI now prevents secret collection under disabled/unknown policy. Future entry points must use the same centralized policy rather than duplicating a permissive default.
- **Runtime validation:** RR-2026-028 remains open for unrelated Wails events and restored-state payloads; this repair only strengthens the shared application-error boundary.
- **Global identity:** BE-005 remains open beyond the concrete WSL casing fix. The canonical project/import-origin identity migration and resolver are still required.
- **Frontend bundle budget:** The final main application chunk is 501.66 kB (124.82 kB gzip), so the configured 500 kB warning remains. Strict execution ordering prevents the prior blank-window failure, but root-component concentration and limited budget headroom remain.
- **Plan/source-file freshness:** RR-2026-044 binds stored normalized project/service configuration, but does not yet hash the current transitive Compose, Dockerfile, or env-file contents. External on-disk edits between plan and apply require a fresh plan by policy, but are not automatically detected unless discovery changes the stored rows.
- **Image-load reconciliation availability:** RR-2026-045 accurately reports confirmed-versus-unknown partial state and attempts a detached refresh after every mutation-boundary error. A simultaneous Docker outage can still postpone authoritative inventory reconciliation until the next successful refresh.
- **Single-instance platform scope:** RR-2026-039 has Windows unit/race/native proof for the exact binary. macOS/Linux activation behavior still requires platform-native package smoke.
- **Installer:** No NSIS installer was produced. Standalone-PE acceptance does not prove installation, upgrade, uninstall, shortcuts/icons, installed WebView bootstrap, or installed-payload signatures.
- **Broader release gates:** Exact-SHA CI enforcement, signing isolation, immutable publication/provenance, clean-machine installer validation, macOS/Linux native package evidence, and the other residuals in reports 05, 09, and 11 are unchanged.
- **Network issues remain independent:** Omada WLAN isolation, Norton filtering, Ollama bind/firewall policy, and unsupported IPv6 forwarding are not resolved by registry credential-helper changes.

## Bottom line

The original error represented a real missing security dependency on the active WSL backend, but Cairn described and displayed it incorrectly. The final source now keeps provider health, helper availability, cancellation, and registry authentication distinct; refuses to collect secrets when policy does not authorize Cairn login; binds secrets to the current registry identity; presents actionable structured guidance; and gives login one coherent verification/finalization/rollback/audit outcome.

The exact hashed standalone Windows binary passed its final source, build/resource, dependency-boundary, browser, native WebView, provider/project, registry-error, and single-instance relaunch gates. It is the accepted working snapshot requested in this remediation task. That bounded acceptance must not be generalized into an unrestricted release claim: helper installation/operator policy, bundle size, installer/signing/publication, clean-machine, architectural, external-writer, and non-Windows evidence remain open.
