# Build, Test, CI, Release, Packaging, Documentation, and Operations Review

- Review date: 2026-07-15
- Workspace reviewed: `E:\Development\projects\apps\rcooler\Cairn`
- Scope: repository hygiene, dependency/toolchain management, build paths, GitHub Actions, release integrity, signing, packaging, test strategy, release evidence, documentation, observability, operational recovery, and the advertised Wails server/container path.

## Executive assessment

The repository has a substantial automated test and packaging foundation, but it is not yet safe to describe the current release path as production-grade. The most serious concerns are:

1. The advertised server/container mode exposes the complete desktop service surface on all interfaces without a Cairn authentication boundary, while the pinned Wails server accepts websocket connections from any origin.
2. That server path is also broken on Windows and operationally incomplete in its supplied container.
3. A pushed version tag can publish artifacts without first proving that the exact commit passed the normal CI, security, generated-binding, and release-validation gates.
4. Release signing credentials are exposed job-wide to dependency installation and build processes.
5. Published release assets are deliberately replaceable, Windows signs only the outer installer, and unsigned public releases are allowed.
6. The Go build toolchain is one security patch behind, while two Docker/Moby vulnerability findings are broadly allowlisted without expiry or client/daemon reachability evidence.
7. Several “release validation” checks validate only that incomplete/blocked evidence is documented; they do not require release readiness.

The issues below use stable IDs. Severity means potential impact, not estimated implementation effort. Confidence is based on direct source inspection and, where shown, a reproduced command.

## Validation snapshot

The review preserved a heavily dirty worktree and did not modify application source. The report therefore describes the current workspace, including the in-progress diagnostics/port-forwarding changes, and distinguishes host/tool limitations from product failures.

### Commands that completed successfully

| Command                                                                               | Result                                                                                                                                                                 |
| ------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `go version`                                                                          | `go version go1.26.5 windows/amd64`                                                                                                                                    |
| `node --version`                                                                      | `v24.15.0`                                                                                                                                                             |
| `npm --version`                                                                       | `11.12.1`                                                                                                                                                              |
| `go mod verify`                                                                       | Passed; all modules verified.                                                                                                                                          |
| `go mod tidy -diff`                                                                   | Passed with no module-file diff.                                                                                                                                       |
| `go vet -unsafeptr=false . ./internal/...`                                            | Passed.                                                                                                                                                                |
| Windows `go build .`                                                                  | Passed.                                                                                                                                                                |
| Windows `go test -c -o <temp-dir> ./internal/...`                                     | Passed; every internal Windows test package compiled.                                                                                                                  |
| `$env:NODE_OPTIONS='--use-system-ca'; npm audit --audit-level=moderate` in `frontend` | Passed with zero known vulnerabilities; `package-lock.json` remained unchanged.                                                                                        |
| `go list ./...`                                                                       | Completed, but unexpectedly included `frontend/node_modules/flatted/golang/pkg/flatted`; see OPS-014.                                                                  |
| `go build -tags server .` on Windows                                                  | Reproduced a compile failure in pinned Wails; see OPS-002.                                                                                                             |
| `GOOS=linux CGO_ENABLED=0 go build -tags server ...`                                  | Independently validated by the coordinating review as passing, which localizes OPS-002 to the Windows/server build-constraint combination.                             |
| WSL `go test -count=1 -timeout=12m . ./internal/...`                                  | Every domain/internal package that built passed. Normal root/shell Linux compilation alone was blocked by GTK/WebKit development packages absent from the review host. |
| WSL `CGO_ENABLED=0 go test -tags server . ./internal/shell`                           | Passed; root has no tests.                                                                                                                                             |
| WSL `go test -race` over all non-shell `internal/...` packages                        | Passed. Shell race compilation requires the absent GTK/WebKit development packages.                                                                                    |
| WSL atomic Go coverage over non-shell `internal/...`                                  | 68.1% statements overall; `internal/services` is 46.4%. Separate server-tag shell coverage is 15.9%.                                                                   |

The first `npm audit` attempt failed with `unable to verify the first certificate`. Re-running with the Windows system CA store passed with zero findings. This is an environment trust-store issue, not evidence of an npm vulnerability.

### Tooling limitations

- Ordinary **Windows** `go test` commands were inconclusive on this review host: every newly generated `%TEMP%\go-build*\*.test.exe` process was created with its primary thread suspended and never entered the test harness. Multiple independent invocations showed the same host behavior. Only reviewer-owned hung process trees were stopped. The coordinating review bypassed that host limitation by executing the suite in Ubuntu WSL; the internal tests and non-shell race suite passed as shown above.
- `go run github.com/golangci/golangci-lint/...` was affected by the same suspended-generated-executable behavior and was therefore inconclusive locally. The repository's configured CI result was not inferred from this host failure.
- These limitations must not be recorded as project test failures. A clean native Windows/macOS/Linux CI rerun is still required for platform-specific test and golangci-lint verdicts; the WSL results are current evidence for the portable domain code, not a substitute for those targets.

## Findings

### OPS-001 — Server mode exposes privileged desktop services without authentication or origin protection

- **Severity:** Critical
- **Confidence:** High
- **Type:** Security / architecture / deployment
- **Evidence:**
  - `Taskfile.yml:138-156` advertises `build:server`, `run:server`, `build:docker`, and `run:docker` as supported tasks.
  - `build/Taskfile.yml:228-281` describes server mode as an HTTP deployment and publishes host port 8080.
  - `build/docker/Dockerfile.server:42-50` exposes port 8080, explicitly sets `WAILS_SERVER_HOST=0.0.0.0`, and starts the server.
  - `internal/shell/app.go:140-168` registers notification, provider, Docker, project, Compose, metrics, logs, terminal, diagnostics, update, lineage, backup, port-forward, registry, agent, and settings services. Many of these services can create/delete containers, edit project configuration, open terminals, manage backups, log into registries, or otherwise exercise Docker-equivalent privilege.
  - No Cairn authentication, authorization, session, CSRF, trusted-origin, reverse-proxy, or TLS configuration was found around this service registration.
  - In the pinned dependency `github.com/wailsapp/wails/v3@v3.0.0-alpha2.103`, `pkg/application/transport_http.go:132-138` routes `/wails/runtime` directly to the runtime handler. `pkg/application/websocket_server.go:61-65` says “Allow connections from any origin in server mode” and sets `InsecureSkipVerify: true`. `pkg/application/application_server.go:112-135` serves ordinary HTTP unless TLS options are supplied; Cairn supplies no server TLS options.
- **Impact:** Any party that can reach the published port can load the UI and potentially invoke the same service bindings trusted by the desktop shell. If a Docker socket or remote context is made available, this becomes a host/container-control boundary failure, not a normal read-only web dashboard exposure. Cross-site websocket access also makes browser-origin isolation unavailable as a compensating control.
- **Recommendation:** Remove the server/container tasks from the supported surface immediately, or bind server mode to loopback and mark it development-only until a deliberate remote architecture exists. A production design needs TLS, explicit authentication, server-side per-method authorization, secure sessions, CSRF/origin enforcement, rate limiting, audit identity, network policy, and a much smaller service allowlist. Do not expose terminal, project-file editing, registry, agent, provider-install, or destructive Docker methods by default.
- **Acceptance test:** From an unauthenticated browser and from a page hosted at an unrelated origin, attempts to open the Wails websocket or invoke every service binding must be rejected. Add positive tests for role/session authorization and negative tests for CSRF, websocket origin, expired session, brute-force/rate limits, and direct service method calls.

### OPS-002 — Advertised server mode does not compile on Windows

- **Severity:** High
- **Confidence:** High
- **Type:** Build bug / unsupported advertised path
- **Evidence:**
  - `Taskfile.yml:138-146` exposes platform-neutral `build:server` and `run:server` commands.
  - Reproduced on the repository's pinned toolchain with `go build -tags server .`:

        # github.com/wailsapp/wails/v3/pkg/application
        clipboard_windows.go:27:6: newClipboardImpl redeclared
        dialogs_windows.go:14:10: undefined: windowsApp
        dialogs_windows.go:88:6: newDialogImpl redeclared
        dialogs_windows.go:98:6: newOpenFileDialogImpl redeclared
        dialogs_windows.go:172:6: newSaveFileDialogImpl redeclared
        single_instance_windows.go:28:6: newPlatformLock redeclared
        systemtray_windows.go:436:6: newSystemTrayImpl redeclared
        webview_window_windows.go:1289:6: newWindowImpl redeclared
        events_common_windows.go:14:10: undefined: windowsApp
        mainthread_windows.go:22:10: undefined: windowsApp

  - The duplicate symbols are between Windows implementations and server stubs in `application_server.go` from Wails `v3.0.0-alpha2.103`.
  - A Linux `GOOS=linux CGO_ENABLED=0` server build passes, proving that the feature is not universally uncompilable.

- **Impact:** `task build:server` and `task run:server` fail on the project's primary Windows development platform. There is no CI job that would detect the regression.
- **Recommendation:** Until the upstream build constraints are fixed or a compatible Wails version is selected, explicitly restrict server tasks to Linux and fail with a clear precondition on other OSes. Prefer removing this surface in conjunction with OPS-001.
- **Acceptance test:** Add a server build matrix for every claimed host/target pair. A task advertised on Windows must compile on `windows-2022`; otherwise its help text and preconditions must clearly state Linux-only support.

### OPS-003 — The supplied server container cannot perform core Cairn work and loses state

- **Severity:** High
- **Confidence:** High
- **Type:** Deployment bug / container hardening
- **Evidence:**
  - `build/docker/Dockerfile.server:34-50` copies only `/server` and frontend files into a distroless image. It contains no Docker CLI, Compose plugin, shell, or helper tools.
  - `internal/providers/linux_native.go:137-199` requires an on-`PATH` `docker` CLI, checks a Docker socket, and invokes `docker compose` and `docker buildx` during detection.
  - `internal/providers/existing_context.go:58-108,191-205` also requires and executes the Docker CLI.
  - `build/Taskfile.yml:267-281` runs only `docker run --rm -p ...`. It mounts neither `/var/run/docker.sock` nor a Docker context/config, project directory, CLI binary, or persistent Cairn data volume.
  - `internal/store/store.go:59-91` stores Linux state at `$XDG_DATA_HOME/cairn/cairn.db` or `~/.local/share/cairn/cairn.db`. The documented `--rm` container has no volume for that path.
  - The runtime stage has no `USER` instruction and therefore runs as the image default (root). It has no `HEALTHCHECK` even though the upstream Wails server exposes a shallow `/health` response.
- **Impact:** The advertised container starts an exposed UI but cannot satisfy provider/Compose prerequisites; it discards settings/audit/history on exit; and any later Docker-socket mount would run a network-facing root process with Docker-equivalent control.
- **Recommendation:** Remove the image until its threat model and operating model are defined. If retained, provide the exact Docker CLI/Compose versions, read-only project mounts, a documented persistent non-secret data volume, explicit socket/context wiring, a non-root UID/GID, read-only root filesystem, dropped capabilities, `no-new-privileges`, resource limits, and liveness/readiness checks. Never silently mount the host Docker socket.
- **Acceptance test:** Run the documented one-line deployment on a clean host and exercise provider detection, Compose list/config/up/down, container logs/terminal, restart persistence, health/readiness, and non-root filesystem behavior. The test must prove both functionality and that no unauthenticated network client can call services.

### OPS-004 — A release tag can publish without the exact commit passing CI or release gates

- **Severity:** High
- **Confidence:** High
- **Type:** Release integrity / CI design
- **Evidence:**
  - `.github/workflows/release.yml:3-6` triggers directly on any `v*.*.*` tag.
  - Its package job runs dependency installation, stamping, packaging, artifact existence checks, SBOM generation, the upgrade fixture, and Linux package tests (`release.yml:102-203`).
  - It does not run frontend audit/lint/format/typecheck/unit/build/catalog checks, generated binding diff, Go unit tests, vet, golangci-lint, the full vulnerability policy, security suite, performance suite, release UI suite, or checklist readiness gate.
  - `publish-release` depends only on `package` (`release.yml:216-250`); it is not tied to a successful run of `.github/workflows/ci.yml` for the same SHA.
  - A tag is not checked to be on protected `main` or to be signed/annotated.
- **Impact:** A red commit, an unreviewed commit, or a tag created from an arbitrary branch can produce a public release. Packaging success is not proof of source correctness or security.
- **Recommendation:** Make CI a reusable workflow called by both PR/push and release, or publish only from a protected `workflow_run`/environment after all required checks for the exact SHA succeed. Require a protected release environment approval, a signed annotated tag reachable from the protected release branch, clean generated bindings, full security validation, and artifact tests before publish.
- **Acceptance test:** Create a test tag on a commit with a deliberately failing unit test, dirty generated bindings, or failed vulnerability gate. No package should reach a release. Then prove a green SHA can publish without rebuilding different source.

### OPS-005 — All signing secrets are exposed to dependency installation and build processes

- **Severity:** High
- **Confidence:** High
- **Type:** Supply chain / credential handling
- **Evidence:**
  - `.github/workflows/release.yml:39-50` places both Windows PFX credentials and all macOS certificate/notary credentials in job-level `env`.
  - Every matrix leg receives both platforms' secrets, including Linux.
  - Those variables are present while checkout, `npm ci`, Wails installation, source generation/build, package tooling, and third-party SBOM actions run (`release.yml:51-189`).
  - `contents: write` is granted workflow-wide at `release.yml:14-16`.
- **Impact:** A compromised npm lifecycle script, Go/Wails tool, build hook, or action can read and exfiltrate both platforms' long-lived signing material and use the write token. Code signing key compromise can make malicious binaries appear publisher-authentic.
- **Recommendation:** Build unsigned artifacts in read-only, secretless jobs. Move signing into isolated platform-specific jobs/environments that download already-built digests, expose only that platform's credential to the exact signing command, contain no dependency installation, and verify input/output hashes. Prefer HSM/cloud signing or short-lived credentials. Grant `contents: write` only to the final publish job.
- **Acceptance test:** Add a harmless canary step before signing that asserts all signing variable names are absent. Confirm only the isolated signing process sees the relevant platform secret and package jobs have read-only tokens.

### OPS-006 — Windows signs only the outer installer, leaving the installed executable and uninstaller unsigned

- **Severity:** High
- **Confidence:** High
- **Type:** Packaging / code signing bug
- **Evidence:**
  - The package is built first at `.github/workflows/release.yml:118-123`.
  - The later signing step finds only `bin/cairn-*-installer.exe` and signs that file (`release.yml:128-136`).
  - Therefore `bin/cairn.exe` was already embedded in NSIS while unsigned.
  - The NSIS finalize/uninstaller signing hooks are commented out at `build/windows/nsis/project.nsi:70-72`.
  - A separate executable signing task exists at `build/windows/Taskfile.yml:165-177` but the release workflow never calls it.
  - `release.yml:205-214` uploads all `bin/*.exe`, so the raw unsigned executable can also become a release asset.
- **Impact:** After an apparently signed installer completes, Windows/EDR/enterprise policies encounter an unsigned application executable and generated uninstaller. Publisher trust and tamper attribution are incomplete; the raw executable is misleadingly adjacent to signed assets.
- **Recommendation:** Sign and verify `cairn.exe` before invoking NSIS. Configure the NSIS uninstaller/finalize hooks to sign the generated uninstaller, then sign the final installer. Do not publish a raw executable unless it is an intentional, signed portable artifact with a distinct name.
- **Acceptance test:** On a clean Windows VM, run `signtool verify /pa /all` against the downloaded installer, the installed application binary, and the generated uninstaller. All must chain to the expected identity and timestamp.

### OPS-007 — Public production releases fail open when platform signing is unavailable

- **Severity:** High
- **Confidence:** High
- **Type:** Release policy / user trust
- **Evidence:**
  - `.github/workflows/release.yml:124-142,144-178` renames unsigned Windows/macOS packages instead of failing a tagged release.
  - `.goreleaser.yaml:39-46` explicitly publishes `-unsigned` artifacts.
  - `docs/release-process.md:66` says unsigned releases are allowed for early public testing.
  - `docs/v1-release-checklist.md:25-31` simultaneously lists production signing/notarization as a current blocker.
- **Impact:** Users can receive unsigned binaries from the primary public release channel for a Docker-management app with highly privileged local capabilities. A suffix is weaker than platform authenticity and encourages bypassing security warnings.
- **Recommendation:** Make protected production tags fail closed unless all required signing/notarization credentials and verifications succeed. Put unsigned builds in a separate nightly/preview channel with prominent warnings and shorter retention.
- **Acceptance test:** Remove one signing secret in a protected release dry run. The production publish job must fail before creating a release; a separately authorized preview workflow may still produce clearly non-production artifacts.

### OPS-008 — Releases and assets are intentionally mutable

- **Severity:** High
- **Confidence:** High
- **Type:** Supply-chain integrity / reproducibility
- **Evidence:**
  - `.github/workflows/release.yml:237-240` deletes any existing release for the tag before rerunning.
  - `.goreleaser.yaml:28-34` sets `replace_existing_artifacts: true` and `mode: replace`.
  - Builds use moving runner images, packages, action tags, and container tags, so a rerun is not guaranteed to reproduce identical bytes.
- **Impact:** The same tag/version can refer to different installers and checksums over time. Audit trails, downstream mirrors, incident response, and user verification become unreliable. A checksum regenerated beside replacement binaries does not prove continuity.
- **Recommendation:** Treat published version assets as immutable. Fail if a release/tag already exists; build a draft, verify/sign/attest all files, then publish once. If repair is necessary, revoke the affected version and issue a new version. Retain signed provenance tying every digest to commit, workflow, builder, and source.
- **Acceptance test:** Attempt to rerun a published tag. The workflow must stop without deleting or replacing any asset, and existing asset digests must remain unchanged.

### OPS-009 — CI/release binaries are built with a superseded Go security patch

- **Severity:** High
- **Confidence:** High
- **Type:** Toolchain security
- **Evidence:**
  - `go.mod:3-5`, `.github/workflows/ci.yml:24-32,181-185`, `.github/workflows/release.yml:55-59`, `build/docker/Dockerfile.cross:16`, and `build/docker/Dockerfile.server:16` pin Go 1.26.4.
  - Local review already has Go 1.26.5.
  - The official [Go release history](https://go.dev/doc/devel/release) records Go 1.26.5 on 2026-07-07 with security fixes in `crypto/tls` and `os` plus compiler/runtime fixes.
- **Impact:** Current CI, release, and container artifacts omit a published standard-library security patch. TLS is directly relevant to registry/update/agent traffic.
- **Recommendation:** Update all pins and toolchain assertions to Go 1.26.5, rerun the entire matrix, and centralize the version in one machine-readable source consumed by workflows, Dockerfiles, local tasks, and documentation. Adopt a defined SLA for Go security patch releases.
- **Acceptance test:** CI must assert the centralized version and inspect the produced binary's Go build info. A repository search should find no stale 1.26.4 build pin; historical evidence text may remain clearly marked historical.

### OPS-010 — Docker/Moby findings are blanket-allowlisted without waiver governance or client/daemon reachability proof

- **Severity:** High
- **Confidence:** High
- **Type:** Vulnerability management
- **Evidence:**
  - `go.mod:12` pins `github.com/docker/docker v28.5.2+incompatible`.
  - `scripts/run-govulncheck.ps1:7-10,80-103` hardcodes GO-2026-4883 and GO-2026-4887 and returns success whenever their trace contains `github.com/docker/docker`.
  - The warning at line 102 states that there is no fixed SDK version and advises updating Docker Engine.
  - As reviewed on 2026-07-15, the official [GO-2026-4883](https://pkg.go.dev/vuln/GO-2026-4883) and [GO-2026-4887](https://pkg.go.dev/vuln/GO-2026-4887) records are still marked unreviewed and list `github.com/docker/docker` as “all versions, no known fixed.” The script's no-known-fixed-module statement is therefore not itself disproved.
  - Separately, upstream Moby reports both Engine issues fixed in 29.3.1: [GHSA-pxq6-2prw-chj9](https://github.com/moby/moby/security/advisories/GHSA-pxq6-2prw-chj9) and [GHSA-x744-4wpc-v9h2](https://github.com/moby/moby/security/advisories/GHSA-x744-4wpc-v9h2). The latter is High/CVSS 8.8.
  - This client-module/daemon-release ambiguity is exactly why an explicit reachability and waiver decision is required; advisory ID plus module name is too broad.
- **Impact:** A newly reachable or changed high-severity trace is silently accepted forever. The text gives reviewers false confidence that no fix exists and conflates the user's daemon version with code shipped from the Docker module.
- **Recommendation:** Capture full call traces and determine whether Cairn actually links/calls daemon-only vulnerable code. Test a Docker module derived from an Engine release containing the upstream fix, but do not assume that alone will clear the current Go database record. If a waiver remains necessary because the module database has no known fixed version or the reachable trace is a scanner/modeling artifact, record owner, rationale, exact symbols, affected scope, supported daemon requirements, compensating controls, creation date, expiry date, and maximum allowed module version. Fail when the trace, advisory, module version, mitigation, or waiver date changes.
- **Acceptance test:** Run govulncheck with no waiver after upgrade. If the Go database still reports the advisory, mutate the trace/version in a fixture and prove the time-bound waiver rejects it rather than matching only advisory ID and module name.

### OPS-011 — Docker build contexts can transmit ignored local secrets

- **Severity:** High
- **Confidence:** High
- **Type:** Secret handling / build isolation
- **Evidence:**
  - `.gitignore:10-14` explicitly treats `.env` and `.env.*` as local secrets.
  - `.dockerignore:1-21` does not exclude those files. It also omits local state such as `.cairn`, `.release-version.env`, `.idea`, `.task`, and current review/work directories.
  - `build/docker/Dockerfile.server:23-25` performs `COPY . .`.
  - `build/Taskfile.yml:251-258` sends the repository root as the Docker build context.
- **Impact:** An ignored `.env` or other secret is sent to the Docker daemon/remote builder and copied into builder layers/cache, even though it is absent from the final distroless stage. Remote builders, cache exporters, and daemon administrators may retain it.
- **Recommendation:** Make `.dockerignore` deny secrets and local state by default, then replace broad `COPY . .` with an allowlisted set of Go module/source/build/frontend inputs. Use BuildKit secret mounts for any credential intentionally needed during a build.
- **Acceptance test:** Place a sentinel secret in an ignored `.env`, build with a context-inspection test/remote mock, and prove the sentinel never enters the context, layer history, cache, or final image.

### OPS-012 — Actions, images, OS packages, and a Zig executable download are not immutably pinned

- **Severity:** High
- **Confidence:** High
- **Type:** Supply-chain security / reproducibility
- **Evidence:**
  - Every action reference in `.github/workflows/ci.yml` and `release.yml` uses a mutable tag such as `actions/checkout@v5`, `goreleaser/goreleaser-action@v7`, or `anchore/sbom-action@v0.24.0` rather than a full commit SHA.
  - `build/docker/Dockerfile.cross:16` and `build/docker/Dockerfile.server:5,16,34` use image tags without digests.
  - Linux apt packages and Windows `choco install nsis` are not version-pinned (`ci.yml:41-47,202-226`; `release.yml:76-100`).
  - `build/docker/Dockerfile.cross:32-37` downloads and executes a Zig archive through a `curl | tar` pipeline with neither `-f` nor checksum/signature verification. The adjacent macOS SDK download correctly verifies SHA-256 (`lines 39-46`), demonstrating the missing control.
  - `build/Taskfile.yml:129-149` can fetch Puppertino CSS and its license from the moving GitHub `main` branch if the vendored file is absent.
- **Impact:** An upstream tag compromise or ordinary dependency drift can change code executed in trusted release jobs and alter artifacts without a repository change.
- **Recommendation:** Pin actions to audited full SHAs with update automation; pin container bases by digest; verify every downloaded executable/archive with a repository-pinned checksum/signature; pin critical packager versions; and emit a build manifest of all resolved versions/digests.
- **Acceptance test:** On two clean builders using the same source and `SOURCE_DATE_EPOCH`, resolved action/image/tool digests must match. A deliberately modified Zig archive must fail checksum validation before extraction.

### OPS-013 — Production builds can rewrite dependency manifests and resolve different frontend packages

- **Severity:** Medium
- **Confidence:** High
- **Type:** Build determinism / repository hygiene
- **Evidence:**
  - `build/Taskfile.yml:4-8` defines `go:mod:tidy`, and `generate:bindings` depends on it at `lines 166-181`.
  - Native Windows, Linux, and macOS production builds also depend on `go:mod:tidy` (`build/windows/Taskfile.yml:33-52`, `build/linux/Taskfile.yml:41-58`, `build/darwin/Taskfile.yml:31-47`).
  - `build/Taskfile.yml:15-26` and `Taskfile.yml:32-36` use `npm install` rather than lockfile-strict `npm ci`.
  - The release workflow happens to run `npm ci` before the task, but does not assert a clean source tree after task-driven generation/build.
- **Impact:** A build can “repair” or rewrite `go.mod`/`go.sum` instead of failing on an untidy repository, and a local/task build can resolve package ranges differently from CI. This blurs the line between dependency update and reproducible build.
- **Recommendation:** Separate maintenance tasks (`tidy`/`npm install`) from build tasks. Production paths should use `go mod download && go mod verify` plus `npm ci`, then fail on `git diff --exit-code` for dependency files and generated bindings.
- **Acceptance test:** Introduce an intentionally untidy module file or stale lockfile in a test branch. The build must fail without modifying it. A clean build must leave `git status --porcelain` unchanged except for declared artifacts.

### OPS-014 — npm's node_modules is inside the Go package discovery boundary

- **Severity:** High
- **Confidence:** High
- **Type:** Build boundary / false-negative and false-positive risk
- **Evidence:**
  - With `frontend/node_modules` installed, `go list ./...` returned `github.com/RCooLeR/Cairn/frontend/node_modules/flatted/golang/pkg/flatted` as part of the Cairn module.
  - `scripts/run-govulncheck.ps1:2,60` scans `./...` by default.
  - `.github/workflows/ci.yml:157-158` runs golangci-lint without an explicit package list; its normal default is the module package pattern.
  - Build-time `go mod tidy` runs after frontend dependencies may already exist (OPS-013).
  - In contrast, unit/vet commands use `. ./internal/...` and omit `tools/iconset` (`ci.yml:97-99,154-158`).
- **Impact:** An npm dependency's incidental Go source can be linted/scanned as first-party code, produce unrelated vulnerabilities or lint failures, and potentially influence `go mod tidy` when that dependency changes. Meanwhile explicit unit/vet patterns omit a real first-party tool package. Results depend on whether `node_modules` exists.
- **Recommendation:** Establish a hard module boundary around the frontend or move it outside the Go module's recursive package space. Define one authoritative first-party package list that includes `.`, `./internal/...`, and `./tools/...` and use it consistently for test/vet/lint/vulnerability commands. Run scans in a clean phase whose filesystem contents are deterministic.
- **Acceptance test:** Compare package lists before and after `npm ci`; they must be identical and contain no `node_modules` path. Add a tiny test package under `tools` and prove every Go gate executes it.

### OPS-015 — “Release validation” accepts blocked/in-progress evidence and validates TODO wording rather than readiness

- **Severity:** High
- **Confidence:** High
- **Type:** Release gate semantics
- **Evidence:**
  - `scripts/check-v1-release-checklist.ps1:137-157` accepts `green`, `in_progress`, and `blocked_by_platform` equally. It requires only known status plus nonempty Evidence and Remaining text.
  - `lines 159-186` verify row identity/count and then print “evidence validated”; no branch requires all rows to be green.
  - `scripts/check-manual-platform-matrix.ps1:43-89` reads the “Full Platform Matrix TODO” prose, and `lines 92-119` checks only that expected phrases occur.
  - `scripts/run-release-validation.ps1:90-104` labels these checks “v1 release checklist evidence ledger” and “manual platform matrix TODO ledger,” making it easy to mistake documentation shape checks for product acceptance.
  - `docs/v1-release-checklist.md:12-14,21` currently contains four `blocked_by_platform` release rows.
- **Impact:** CI can report “release validation” green while all platform acceptance work remains blocked. It protects the existence of a ledger, not the release decision.
- **Recommendation:** Keep documentation-lint as a separate check. Add a `-RequireGreen`/release mode that rejects every non-green normative item, verifies linked machine-readable evidence, and is mandatory for protected production tags.
- **Acceptance test:** Change every checklist status to `blocked_by_platform` with plausible prose. Documentation lint may pass; the release-readiness gate must fail with a list of unresolved items.

### OPS-016 — v1.0.0 is called release-ready despite explicit pre-v1 blockers and missing proof

- **Severity:** High
- **Confidence:** High
- **Type:** Documentation accuracy / release governance
- **Evidence:**
  - `docs/release-notes-v1.0.0.md:1-3` calls v1.0.0 the “first release-ready version.”
  - The same file at `lines 35-42` admits clean Windows, macOS, Debian/AppImage validation and production signing were not done, while characterizing them as external rather than code gaps.
  - `docs/v1-release-validation.md:118-124` says the unresolved clean-platform matrix “must be closed before” the v1 checklist can be fully checked and names proof needed before v1.0.
  - `docs/v1-release-checklist.md:25-31` lists current blockers including installed-app behavior and signing/notarization.
  - The evidence largely references June 2026 commits/run IDs, while the reviewed head is later and includes large in-progress changes.
- **Impact:** Users and maintainers cannot tell whether v1.0.0 is released, a candidate, or a target. Known acceptance gaps can be deprioritized because release notes declare success.
- **Recommendation:** Use unambiguous lifecycle states: target, candidate, validated, released, superseded. Do not label a build release-ready until the normative gate is green. Publish a signed release sign-off manifest that identifies exact commit, artifacts, evidence links, approver, exceptions, and date.
- **Acceptance test:** A documentation/release metadata check must fail if “release-ready” is asserted while the normative machine-readable checklist contains any non-green item or unsigned production artifact.

### OPS-017 — Windows/macOS/AppImage package validation is mostly a non-empty-file check

- **Severity:** High
- **Confidence:** High
- **Type:** Packaging test gap
- **Evidence:**
  - `scripts/check-release-artifacts.ps1:15-25` only finds matching files and rejects zero length.
  - Windows checks only for `cairn-*-installer*.exe` (`lines 28-31`).
  - macOS checks only for a DMG and app executable path (`lines 36-43`).
  - Linux adds meaningful deb install/remove tests, but AppImage remains existence-only (`lines 32-35` and `.github/workflows/ci.yml:262-275`).
  - `docs/manual-platform-validation.md:170-180` leaves clean desktop, AppImage, permission, Windows, and macOS execution rows open.
- **Impact:** A wrong-architecture, unsigned, corrupt, non-installing, non-launching, non-uninstalling, or dependency-incomplete package can pass. Core release behavior is untested on two platforms and for AppImage.
- **Recommendation:** Add clean-VM/sandbox acceptance jobs:
  - Windows: inspect PE architecture/version, verify all signatures, silent install, launch, exercise a health/about/provider smoke, uninstall, and assert no unwanted residue.
  - macOS: mount/copy/launch, verify `codesign --strict`, Gatekeeper, notarization ticket/staple, architecture and minimum OS, then remove.
  - AppImage: inspect architecture, run/extract on every supported clean distro, launch under a real/virtual display, and validate desktop/icon/update metadata and required libraries.
- **Acceptance test:** Corrupt, mis-sign, or architecture-swap one artifact fixture; the platform gate must fail for the precise reason before publication.

### OPS-018 — macOS release is ARM64-only, architecture-obscured, and built on a deprecated runner line

- **Severity:** Medium
- **Confidence:** High
- **Type:** Platform support / release continuity
- **Evidence:**
  - Both workflows use `macos-14` (`ci.yml:19,174`; `release.yml:36`).
  - Current official [GitHub runner documentation](https://github.com/actions/runner-images) maps `macos-14` to ARM64 and announced deprecation beginning 2026-07-06, with full removal planned for 2026-11-02.
  - Release invokes `darwin:package`, which builds the host/default architecture (`build/darwin/Taskfile.yml:16-58,119-125`).
  - A universal task already exists (`build/darwin/Taskfile.yml:91-135`) but is not used.
  - The signing step deletes the architecture-named DMG and creates `bin/cairn-$CAIRN_VERSION.dmg` (`release.yml:164-167`), concealing that it is ARM64-only.
  - `docs/manual-platform-validation.md:178-180` still lists Intel as best-effort and macOS platform validation unresolved.
- **Impact:** Intel Mac users can download a generically named incompatible artifact; the runner label will eventually disappear; documentation overstates broad macOS support.
- **Recommendation:** Move to a supported runner line and deliberately choose either a universal package or separately named `macos-arm64` and `macos-amd64` assets. Put architecture in filenames and release metadata, and test both. If Intel is unsupported, state that prominently.
- **Acceptance test:** `lipo -info`/`file` must match the advertised architecture. An Intel test VM must install/launch a universal or x64 package; an ARM VM must do the same for ARM.

### OPS-019 — SemVer prerelease tags pass validation but generate invalid native package versions

- **Severity:** High
- **Confidence:** High
- **Type:** Versioning / packaging bug
- **Evidence:**
  - `.github/workflows/release.yml:3-6` accepts tags such as `v1.1.0-rc.1`.
  - `scripts/stamp-version.ps1:16-27` accepts one `-` or `+` suffix with a loose regex.
  - `lines 83-95` writes the raw SemVer into NSIS `INFO_PRODUCTVERSION` and both macOS `CFBundleVersion` and `CFBundleShortVersionString`.
  - `build/windows/nsis/project.nsi:39-40` appends `.0` to `INFO_PRODUCTVERSION` for numeric version resources, producing an invalid value such as `1.1.0-rc.1.0`.
  - Raw SemVer prerelease/build syntax is not a valid normal macOS bundle-version tuple.
  - The regex also accepts non-SemVer leading-zero forms and rejects valid combined prerelease-plus-build metadata.
- **Impact:** A permitted prerelease tag can fail packaging/signing/notarization or emit invalid platform metadata. The workflow has no prerelease stamping test and CI always stamps `1.0.0` (`ci.yml:244-250`).
- **Recommendation:** Use a real SemVer parser. Keep display/marketing SemVer separate from monotonic numeric Windows file version and macOS bundle build version. Define prerelease mapping rules and test stable, prerelease, build-metadata, and invalid cases.
- **Acceptance test:** Package `v1.2.3`, `v1.2.3-rc.1`, and `v1.2.3+build.7` on all three platforms; validate native metadata with platform tools. Reject `v01.2.3` and malformed combinations.

### OPS-020 — macOS signing conditions disagree and partial secret configuration fails unpredictably

- **Severity:** Medium
- **Confidence:** High
- **Type:** Workflow correctness
- **Evidence:**
  - The “Note unsigned” condition at `.github/workflows/release.yml:144-146` checks certificate, identity, notary key, key ID, and issuer.
  - The actual signing/notarization step at `lines 148-172` runs whenever only `MACOS_CERTIFICATE_BASE64` is nonempty.
  - The unsigned rename at `lines 174-178` also tests only whether the certificate is absent.
- **Impact:** A partially configured repository can skip the unsigned warning, enter signing, then fail later with an empty identity/notary parameter. Behavior does not match the documented “signed when configured, otherwise unsigned” policy.
- **Recommendation:** Validate all-or-none signing configuration in an early dedicated step and use one shared boolean output for note/sign/rename logic. In production, apply OPS-007 and fail if the complete set is missing.
- **Acceptance test:** Table-test every partial secret combination. Each must fail immediately with a precise missing-variable list; only the complete set may sign/notarize.

### OPS-021 — No recurring race gate or project-wide coverage policy exists

- **Severity:** Medium
- **Confidence:** High
- **Type:** Test strategy
- **Evidence:**
  - Neither GitHub workflow nor repository test scripts contain a `go test -race` gate.
  - `docs/release-notes-v1.0.0.md:28-30` nevertheless says “focused race” checks are green without linking a current command/workflow.
  - No Go coverage profile/threshold is generated in CI.
  - `frontend/vitest.config.mjs:3-9` has no coverage provider, reporter, or threshold.
  - The release document cites one focused package's historical 92.4% coverage (`docs/v1-release-validation.md:115`), not a current repository policy.
- **Impact:** Regressions in concurrent stream/provider/runtime code can merge without dynamic race checking, and broad untested areas can grow silently while selected test counts remain green.
- **Recommendation:** Run `-race` on a supported Linux subset at minimum, with platform-focused race jobs where feasible. Produce Go and frontend coverage reports, set initially realistic ratcheting thresholds per critical package, and track changed-line coverage without treating raw percentage as a substitute for scenario testing.
- **Acceptance test:** Introduce a controlled race fixture and an uncovered critical branch in a test branch; the appropriate CI gates must fail and publish actionable evidence.

### OPS-022 — CI jobs can hang for hours and discard structured test/browser failure evidence

- **Severity:** Medium
- **Confidence:** High
- **Type:** CI reliability / diagnostics
- **Evidence:**
  - Neither `.github/workflows/ci.yml` nor `release.yml` sets job-level `timeout-minutes`.
  - Main Go tests run as one unstructured command (`ci.yml:97-99`), with no `-json`/JUnit output or result upload.
  - `frontend/playwright.config.mjs:8-20` creates an HTML report, test output, and retained-on-failure traces.
  - CI does not upload `playwright-report` or `frontend/test-results` on failure.
  - No SARIF, coverage, or test-result artifacts are uploaded from lint/test jobs.
- **Impact:** A stuck integration/build can consume the platform default timeout; failures are slower to triage and may be impossible to reproduce after ephemeral runners disappear.
- **Recommendation:** Set explicit job/step timeouts, shard slow integration packages, emit Go JSON/JUnit and Vitest/Playwright reports, and upload logs/traces/screenshots/results with `if: always()` and sensible retention. Redact secrets before upload.
- **Acceptance test:** Force one Playwright failure and one timed-out Go integration. CI must terminate within the declared budget and retain trace, screenshot, structured result, and relevant daemon/app logs.

### OPS-023 — Docker integration runs inside the broad lint job with a world-writable daemon socket

- **Severity:** Medium
- **Confidence:** High
- **Type:** CI privilege isolation
- **Evidence:**
  - `.github/workflows/ci.yml:41-56` prepares Docker in the same `lint-test` job that installs frontend dependencies and runs all build/lint/test tools.
  - `line 52` executes `sudo chmod 666 /var/run/docker.sock`.
  - Real daemon-restart, logs, metrics, terminal, backup, and registry integrations run later in the same job (`lines 112-152`).
  - `ci.yml` has no explicit top-level `permissions` declaration.
- **Impact:** Any compromised process in that long job gets unauthenticated root-equivalent control through Docker. World-writable mode is unnecessary even on an ephemeral runner and increases blast radius.
- **Recommendation:** Split unprivileged lint/unit/build from Docker integration. Give integration jobs an explicit read-only repository token, no signing/release secrets, isolated test credentials/network, and least-privilege Docker access through the runner's Docker group or narrowly scoped sudo—not mode 0666.
- **Acceptance test:** In the unprivileged job, access to the Docker socket must fail. The isolated integration job must pass without changing socket mode to world-writable.

### OPS-024 — Browser E2E coverage does not exercise native webviews or all shipped visual platforms

- **Severity:** Medium
- **Confidence:** High
- **Type:** End-to-end test gap
- **Evidence:**
  - `frontend/playwright.config.mjs:33-38` defines Chromium only.
  - Release UI tests run only in the Linux package-smoke path (`ci.yml:232-242,272-275`).
  - The repository contains Windows and Linux screenshot goldens, but no macOS/WebKit golden set.
  - Wails ships through WebView2 on Windows, WebKit/WKWebView on macOS, and WebKitGTK on Linux; a Vite+Chromium mock does not validate native bindings, permissions, window/tray behavior, webview compatibility, or platform CSS/rendering.
- **Impact:** A browser-mock suite can remain green while native packages fail at startup, binding transport, keyboard/clipboard/dialog interactions, or WebKit rendering.
- **Recommendation:** Retain fast Chromium mocked UI tests, but add native packaged-app E2E smokes on each OS. Add Playwright WebKit for standards compatibility where useful and platform-specific screenshot/accessibility coverage. Drive real Wails bindings for a safe smoke subset.
- **Acceptance test:** Launch each installed package, read version/provider state through real bindings, navigate every primary route, perform one safe action, and capture platform-native failure artifacts.

### OPS-025 — The documented cross-build image can skip the frontend and carries an incompatible Node toolchain

- **Severity:** Medium
- **Confidence:** High
- **Type:** Cross-build bug / documentation
- **Evidence:**
  - `build/docker/Dockerfile.cross:7-14` documents direct `docker run` usage.
  - `lines 22-26` install Debian Bookworm's unpinned `nodejs npm` packages.
  - `frontend/package.json:31-34` requires Node >=24 and npm >=11; Bookworm's distribution packages do not guarantee those majors.
  - `Dockerfile.cross:183-186` builds the frontend only if `frontend/dist` does not exist and uses `npm install`.
  - The repository always tracks `frontend/dist/embed-placeholder.txt`, so `frontend/dist` exists on a clean checkout and the documented direct path can skip frontend production build entirely.
- **Impact:** Direct cross-build usage can embed placeholder-only/stale assets or fail against an unsupported Node/npm version. Task-driven builds may mask the defect by building the frontend first.
- **Recommendation:** Either remove frontend building from the cross image and require a verified `dist/index.html` input, or use a pinned Node 24 stage with `npm ci`. Test for the required production entry file/digest, never merely directory existence.
- **Acceptance test:** On a clean checkout containing only the tracked placeholder, run each documented Docker command. It must either build verified production assets or fail clearly before compiling; the final binary must not contain only the placeholder.

### OPS-026 — Local “CI” and toolchain-check tasks do not reproduce CI

- **Severity:** Medium
- **Confidence:** High
- **Type:** Developer experience / gate drift
- **Evidence:**
  - `Taskfile.yml:20-25` says `env:check` “Verifies pinned local toolchain versions,” but only prints `go version`, `node --version`, and `npm --version`. It performs no comparison. This host's Go 1.26.5 is accepted despite the repository's asserted 1.26.4.
  - `Taskfile.yml:97-108` says `ci` runs local CI checks but omits dependency installation/audit, frontend typecheck, generated-binding cleanliness, GoReleaser config check, Docker integrations, packaging, and release validation present in GitHub CI.
  - Local `go:lint` invokes whatever `golangci-lint` is on `PATH` (`Taskfile.yml:73-77`), while hosted CI downloads v2.6.2.
  - `docs/release-process.md:68-76` suggests only `task test`, one platform package, and `goreleaser@latest check` before tagging; `@latest` is itself nondeterministic.
- **Impact:** “Works locally” does not mean the hosted gates ran, and developers can unknowingly use different tool versions or skip release-critical checks.
- **Recommendation:** Create a single versioned local/CI entrypoint or reusable task graph, with explicit fast/full/package modes. Make toolchain checks assert centralized exact/range policy and bootstrap pinned tools. Keep expensive platform integrations clearly reported as skipped rather than silently absent.
- **Acceptance test:** Compare the emitted gate manifest from local full mode and hosted CI; names, versions, package scopes, and skip reasons must match.

### OPS-027 — Rotating structured logging exists but is never initialized by the application

- **Severity:** Medium
- **Confidence:** High
- **Type:** Observability / supportability
- **Evidence:**
  - `internal/logging/logging.go:27-61` implements a rotating JSON `slog` writer.
  - Repository search found no application import/use of `internal/logging` and no `slog.SetDefault`; only `internal/logging/logging_test.go` uses it.
  - `main.go:13-16` reports startup failure only via `log.Fatal`.
  - Production Windows builds use the GUI subsystem (`build/windows/Taskfile.yml:52-58`), so there is normally no terminal to receive standard-error output. Finder-launched macOS apps have a similar supportability problem.
  - Operational code emits `slog.Debug/Info/Warn` in Docker, shell, port-forward, store, and provider paths, but those records go to the default process logger.
  - Wails asset logging is explicitly disabled at `internal/shell/app.go:176-179`.
  - The new runtime diagnostics service (`internal/services/diagnostics_service.go:11-34`) reports in-memory counters only; it is not durable, correlated, exportable, or a crash record.
- **Impact:** Installed-app startup/provider/stream/port-forward failures disappear or are difficult for users to collect. The project has audit-domain records but lacks operational diagnostics for incidents and support.
- **Recommendation:** Initialize the rotating logger before application startup in a private per-user data/log directory, set it as the default, include version/commit/provider/session correlation fields, and apply a tested redaction policy. Add “Open logs” and “Export redacted diagnostics” UI, documented retention/location, startup/crash capture, and panic recovery where safe. Keep telemetry/crash upload opt-in with clear privacy terms.
- **Acceptance test:** Launch an installed GUI app without a console, force startup/provider/stream failures, and verify a size-bounded private log contains correlated redacted records and can be exported. Inject representative secrets and prove none appear.

### OPS-028 — Release evidence is stale, partly ignored, and not independently verifiable

- **Severity:** Medium
- **Confidence:** High
- **Type:** Release evidence / auditability
- **Evidence:**
  - `docs/v1-release-validation.md:3` and `docs/v1-release-checklist.md:3` cite `dev-docs/...` as the normative source, but `dev-docs` is absent from the repository.
  - Both checklist scripts silently fall back to embedded items/phrases when the source is absent (`check-v1-release-checklist.ps1:10-17,113-135`; `check-manual-platform-matrix.ps1:10-17,84-89`).
  - `docs/v1-release-validation.md:66` says final 24-hour soak evidence is under `.scratch/release-soak/...`; `.gitignore:51-54` excludes `.scratch`.
  - Evidence cites bare CI run numbers rather than immutable URLs/artifact digests and is tied to commits such as `0c2a928`/`b51f57f` from June, not the reviewed head.
  - `docs/v1-release-validation.md:134` is a long manual chronology that is difficult to validate mechanically.
- **Impact:** A reviewer cannot retrieve the claimed “final evidence,” confirm which normative specification applied, or prove that the evidence corresponds to current release bytes.
- **Recommendation:** Commit/version the normative spec or link to an immutable version. Generate a machine-readable evidence manifest with source SHA, workflow URL/run ID, environment, commands, results, artifact/report digests, exceptions, and signer/approver. Attach logs/profiles/reports as immutable release evidence with retention matching support policy. Never silently substitute an embedded spec.
- **Acceptance test:** Starting from only a release and public repository, an independent reviewer must be able to fetch every required evidence artifact, verify its digest/signature, and map it to the exact source/artifact SHA.

### OPS-029 — Database upgrade coverage stops at a release-candidate fixture

- **Severity:** Medium
- **Confidence:** High
- **Type:** Data compatibility / recovery test gap
- **Evidence:**
  - `docs/v1-release-validation.md:17,45` says `testdata/dbs/v1.0.0-rc1-seed.sql` represents the release-candidate schema until a first post-v1 fixture is added.
  - The repository has a v1.0.0 tag/release narrative and multiple migrations, but no immutable fixture captured from the actually released v1.0.0 application.
  - Release validation runs only `TestReleaseDBFixtureUpgrade` (`scripts/run-release-validation.ps1:114-117`).
  - No release workflow test was found for interrupted/corrupt migration, disk-full migration, backup-before-migrate, or restore/rollback of Cairn's own SQLite state.
- **Impact:** A post-v1 build can migrate the synthetic RC fixture successfully while failing against actual released data or leaving users without recovery after an interrupted migration.
- **Recommendation:** Capture a sanitized database fixture from every public schema version, test the complete upgrade chain to current, and verify representative records/defaults/indexes. Back up before migration and test failure recovery, newer-schema rejection, corruption, disk-full behavior, and rollback/support procedure.
- **Acceptance test:** For each released fixture, start the packaged new version, migrate, validate data, restart, and restore the pre-migration backup after an injected mid-migration failure.

### OPS-030 — The public repository lacks project license, security-reporting, contribution, and third-party notice artifacts

- **Severity:** Medium
- **Confidence:** High
- **Type:** Governance / compliance / security operations
- **Evidence:**
  - Repository-root inventory found no `LICENSE`, `SECURITY.md`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, `CHANGELOG.md`, `NOTICE`, or third-party license report.
  - `build/linux/nfpm/nfpm.yaml:12-16` declares the package license “Proprietary,” but no EULA/license terms are installed or linked.
  - `frontend/Inter Font License.txt` exists, yet `build/linux/nfpm/nfpm.yaml:19-39` packages only the binary, icons, and desktop file. Windows/macOS package tasks likewise do not add project or third-party notices.
  - An SBOM is generated, but an SBOM is not a substitute for copyright/license obligations.
  - No security disclosure channel or supported-version policy exists for an application with Docker-equivalent local authority.
  - `.github/ISSUE_TEMPLATE/bug_report.md:22` requests logs/screenshots without warning users to redact registry tokens, environment values, project paths, or container data.
- **Impact:** Users and contributors lack clear legal permission/terms and a safe vulnerability-reporting path; packages may omit required notices; public bug reports may leak secrets.
- **Recommendation:** Decide and publish the project license/proprietary terms, add `SECURITY.md` with private reporting and supported versions/SLA, add contribution/governance docs, and generate a reviewed third-party license/notice bundle from both Go and npm dependency graphs. Install the applicable license/notices with every package and add redaction guidance to support templates.
- **Acceptance test:** Package inspection must find the project license/EULA and generated third-party notices; a dependency-license policy job must flag unknown/forbidden licenses and missing attribution.

### OPS-031 — Supported operating systems, distributions, and CPU architectures are not stated precisely

- **Severity:** Medium
- **Confidence:** High
- **Type:** Product support / packaging documentation
- **Evidence:**
  - `README.md:3,98-106` broadly advertises Windows, macOS, and Linux without a support matrix.
  - `build/linux/nfpm/nfpm.yaml:41-44` actually depends on the GTK4/WebKitGTK 6 stack described as Ubuntu 24.04+/Debian 13+.
  - `build/darwin/Info.plist:22-23` sets macOS 12.0 minimum.
  - Windows packaging and CI are x64; release notes list Windows ARM64 as post-v1 (`docs/release-notes-v1.0.0.md:46`).
  - Linux release uses the x64 hosted runner; macOS currently uses ARM64 only (OPS-018). No filenames/support page make this complete matrix obvious.
  - AppImage clean-host portability remains unvalidated (`docs/manual-platform-validation.md:170-175`).
- **Impact:** Users can download an incompatible artifact or expect support on a distribution/architecture the package cannot run on. Support triage lacks a defined baseline.
- **Recommendation:** Publish an explicit matrix of OS versions, distributions, libc/webview prerequisites, architecture, Docker/Compose versions, provider mode, tested/deprecated/unsupported state, and artifact filename mapping. Keep it generated or checked against the workflow matrix and native metadata.
- **Acceptance test:** A documentation test compares declared supported targets with produced artifact architectures and actual clean-host acceptance jobs; no claimed target may lack an artifact and test.

### OPS-032 — SBOMs/checksums are not signed provenance and the server image has no supply-chain pipeline

- **Severity:** Medium
- **Confidence:** High
- **Type:** Artifact provenance / container security
- **Evidence:**
  - `.github/workflows/release.yml:184-189` generates per-platform SPDX SBOMs from `bin`.
  - `.goreleaser.yaml:10-18` includes artifacts/SBOMs in an unsigned SHA-256 checksum file generated by the same mutable workflow that uploads them.
  - No GitHub artifact attestation, SLSA provenance, cosign signature, signed checksum, or independent transparency-log record is configured.
  - `build/docker/Dockerfile.server` is not built, smoke-tested, scanned, SBOMed, signed, or published by either workflow.
  - Scanning only the packaged `bin` directory does not prove that each installer-contained file matches its SBOM or that license metadata is complete.
- **Impact:** Users can detect accidental corruption only if they already trust the same release page; they cannot cryptographically bind artifacts to the expected repository/workflow identity. The advertised server image is entirely outside release security controls.
- **Recommendation:** Generate SBOMs for each final signed artifact/image, verify package contents, create GitHub artifact attestations or SLSA provenance, sign checksums/SBOMs/images with protected identity, and publish verification instructions. If server mode remains, add Dockerfile lint, build, runtime smoke, vulnerability/config scan, SBOM, digest pinning, signature, and deployment policy.
- **Acceptance test:** Verify every downloaded asset offline against a signed identity/provenance statement, then alter one byte and prove verification fails. Map each SBOM digest to exactly one final artifact digest.

### OPS-033 — Dependency update automation misses Docker images and workflow-pinned Go tools

- **Severity:** Low
- **Confidence:** High
- **Type:** Maintenance automation
- **Evidence:**
  - `.github/dependabot.yml:1-14` covers GitHub Actions, root Go modules, and frontend npm only.
  - It has no Docker ecosystem entry for the two Dockerfiles.
  - Wails CLI, golangci-lint, govulncheck, GoReleaser, Garble, Zig, linuxdeploy, NSIS, and macOS SDK versions/checksums are scattered across workflows/tasks/scripts and are not consistently modeled as updateable dependencies.
  - Wails CLI version is repeated in multiple workflow steps/cache keys (`ci.yml:67,194-217`; `release.yml:68-93`), increasing skew risk.
- **Impact:** Security/compatibility updates rely on manual discovery and can leave stale pins or partially update one path.
- **Recommendation:** Centralize tool versions and add Renovate/Dependabot/custom regex automation for Docker bases, workflow tools, binary downloads, and checksums. Require update PRs to run build/package/security compatibility tests.
- **Acceptance test:** A bot dry run should identify an intentionally stale version in each ecosystem and update every reference atomically.

### OPS-034 — Pinned Wails alpha runtime components are skewed and lack a protocol-contract smoke

- **Severity:** Medium
- **Confidence:** High
- **Type:** Dependency compatibility / framework risk
- **Evidence:**
  - `go.mod:17` pins Wails Go `v3.0.0-alpha2.103`.
  - `frontend/package.json:23` and `package-lock.json:2685-2688` pin `@wailsio/runtime 3.0.0-alpha.79`.
  - The pinned Go module's own `internal/runtime/desktop/@wailsio/runtime/package.json` declares runtime `3.0.0-alpha.91`.
  - `docs/v1-release-validation.md:80-83` documents alpha.79 as the published package consumed by bindings, but does not prove protocol compatibility against the Go module's embedded alpha.91 runtime.
  - The project is marketing v1 release readiness while relying on a fast-moving alpha framework; OPS-002 demonstrates one real build-constraint defect in that pin.
- **Impact:** Binding transport/event/runtime fixes can differ across alpha revisions. Compilation and mocked browser tests may pass while packaged runtime communication fails in edge paths.
- **Recommendation:** Define and enforce an approved Wails Go/CLI/JS runtime compatibility tuple, or align to the runtime version shipped by the pinned framework when published/compatible. Add a packaged-app contract smoke covering service invocation, errors, events, websocket/runtime reconnect, dialogs, notifications, and shutdown on every platform. Track upstream alpha risks explicitly.
- **Acceptance test:** CI must fail when any member of the tuple changes alone. A real packaged smoke must round-trip a typed service call and event through the native runtime on Windows, Linux, and macOS.

### OPS-035 — Release tags are not validated for provenance and cancellation can interrupt release work

- **Severity:** Medium
- **Confidence:** High
- **Type:** Release governance / workflow reliability
- **Evidence:**
  - `.github/workflows/release.yml:3-12` accepts a broad tag glob and arbitrary manual version input.
  - The workflow does not require an annotated or signed tag, confirm the tag commit is reachable from a protected branch, or compare tag version to source state.
  - `release.yml:18-20` sets `cancel-in-progress: true` for release concurrency.
  - `publish-release` deletes/replaces existing releases (OPS-008), compounding recovery risk after cancellation.
- **Impact:** A tag from an arbitrary commit can publish, and a rerun/concurrent trigger can cancel a long package/sign/notary process. Release provenance and operational recovery are weak.
- **Recommendation:** Require protected signed annotated tags created from the approved branch/commit after CI, use a protected GitHub environment with approval, set release concurrency to queue rather than cancel, and make publish idempotent/immutable.
- **Acceptance test:** Lightweight/unsigned/off-branch tags must be rejected. Two simultaneous runs for the same version must serialize; neither may delete or partially replace a release.

### OPS-036 — Workflow token permissions and checkout credentials are broader than necessary

- **Severity:** Medium
- **Confidence:** High
- **Type:** CI hardening
- **Evidence:**
  - `.github/workflows/ci.yml` does not declare explicit `permissions` and therefore relies on repository defaults.
  - `.github/workflows/release.yml:14-16` grants `contents: write` to all jobs, including dependency installation/package matrix jobs.
  - Checkout steps generally retain the workflow token in Git credentials; package jobs do not set `persist-credentials: false`.
  - Third-party actions and build processes therefore execute in jobs with more authority than needed.
- **Impact:** A compromised action/dependency has a larger token blast radius and may push/alter repository or release state depending on defaults/job.
- **Recommendation:** Set top-level `permissions: contents: read` (or none) and grant minimal permissions per job. Give write only to the isolated final publisher/attestation job. Disable checkout credential persistence in all non-publishing jobs.
- **Acceptance test:** A package job's token must fail a harmless write-permission probe; only the approved publisher can create a draft release.

### OPS-037 — CI hardcodes a stable 1.0.0 smoke and never exercises next/prerelease version paths

- **Severity:** Medium
- **Confidence:** High
- **Type:** Versioning test gap
- **Evidence:**
  - `.github/workflows/ci.yml:244-250` always runs `stamp-version.ps1 -Version "1.0.0"`.
  - Current source defaults also remain 1.0.0 in `build/config.yml`, `frontend/package.json`, `frontend/src/version.ts`, platform metadata, and `Taskfile.yml:11`.
  - No test enumerates/stamps valid and invalid release inputs (see OPS-019).
- **Impact:** Version-specific filenames/metadata and future release increments can break only after a tag is pushed. Stale hardcoded defaults can leak into unstamped/local packages.
- **Recommendation:** Add unit/fixture tests for the stamper and make package smoke use a generated representative next stable and prerelease version. Assert version consistency across About UI, binary build info, NSIS/PE, plist/DMG, deb metadata, filenames, SBOM, and release tag.
- **Acceptance test:** A matrix of stable/prerelease/build metadata values must produce expected platform-specific versions; every final artifact must report one coherent display version and source commit.

### OPS-038 — Documentation contains stale feature and tool-version claims

- **Severity:** Low
- **Confidence:** High
- **Type:** Documentation quality
- **Evidence:**
  - `docs/release-notes-v1.0.0.md:46` calls tray app and desktop notifications post-v1, while `internal/shell/app.go:212-215,254-309,311-417` implements tray/notification behavior.
  - The same release-notes line lists a container/volume file browser as post-v1 while `README.md:11` advertises a container file browser and `internal/docker/files.go` implements backend file operations. The exact shipped UI scope should be reconciled.
  - `docs/manual-platform-validation.md:72,85,116` cites golangci-lint v2.12.2, while hosted CI pins v2.6.2 (`ci.yml:157-158`).
  - `docs/release-process.md:70-76` recommends GoReleaser `@latest` while hosted workflows use a floating `~> v2` action input.
  - `README.md:122` says v1 “targets” release-ready while v1.0.0 release notes say it already is release-ready.
- **Impact:** Users cannot determine actual shipped scope, and maintainers may reproduce validation with a different toolchain than CI.
- **Recommendation:** Generate/version feature matrices and tool manifests where possible. Treat release notes as immutable historical truth and publish corrections/addenda rather than contradictory current claims. Link CI runs and exact commands/versions.
- **Acceptance test:** A release-documentation review must compare claimed features with registered services/routes and compare claimed tool versions with the resolved CI manifest.

### OPS-039 — There is no user-facing state/log backup, migration-recovery, or reset runbook

- **Severity:** Medium
- **Confidence:** High
- **Type:** Operability / disaster recovery
- **Evidence:**
  - User docs cover Docker volume backups but do not document Cairn's own SQLite location from `internal/store/store.go:59-91`.
  - They do not document log location because operational logging is not initialized (OPS-027), nor how to back up/restore/reset Cairn settings, audit, metrics, history, or provider state.
  - There is no runbook for “database newer than this build” (`internal/store/store.go:37-50`), corrupt DB, failed migration, disk full, or safe downgrade.
- **Impact:** A user facing startup failure or data corruption has no supported recovery path and may delete the wrong files or lose audit/history data.
- **Recommendation:** Document per-OS state/log/cache paths, backup/restore/reset workflows, schema compatibility, downgrade limitations, corruption diagnostics, and support bundle collection. Automate safe pre-migration backup and provide a non-destructive recovery command/UI.
- **Acceptance test:** On each OS, follow only the public runbook to back up state, corrupt or upgrade it, recover to a working app, and verify permissions plus data integrity.

### OPS-040 — Local-agent documentation does not explain remote endpoint data disclosure and trust

- **Severity:** Medium
- **Confidence:** High
- **Type:** Privacy / security documentation
- **Evidence:**
  - `docs/local-agent.md:21-31` allows selecting an “OpenAI-compatible” endpoint.
  - `lines 33-48` says project files, logs, Docker inventory, and config context can be sent after redaction.
  - `lines 68-79,90-103` include container files and project configuration/file-edit workflows.
  - The document calls the feature “local” and does not warn that a configurable non-loopback endpoint may send proprietary source, logs, paths, environment examples, and infrastructure metadata to a third party.
  - It does not define endpoint TLS requirements, certificate validation, authentication-token storage, provider retention policy, redaction limitations, consent boundary, or an audit of exactly what was sent.
- **Impact:** Users may assume context stays on-device and unintentionally disclose sensitive project/infrastructure data. Redaction cannot guarantee removal of domain-specific secrets or confidential source.
- **Recommendation:** Distinguish local loopback from remote providers in UI/docs. Require explicit informed consent before first remote use, show endpoint/TLS and exact context categories, default to HTTPS/non-public allowlists for remote, store credentials in OS keychain, provide per-request context preview/audit, and document provider retention/privacy responsibility and redaction limits.
- **Acceptance test:** Configure a non-loopback/plain-HTTP test endpoint. Cairn must block or strongly gate it, show the exact data disclosure, and prove credentials/context are redacted from operational logs and audit exports.

## Prioritized remediation sequence

1. **Stop exposing unsupported server mode:** remove/disable server and Docker deployment tasks until OPS-001 through OPS-003 are resolved and independently security-tested.
2. **Close the release trust chain:** gate releases on exact-SHA CI, isolate secrets/signing, sign all nested artifacts, require production signatures, make assets immutable, and add provenance (OPS-004 through OPS-008, OPS-032, OPS-035, OPS-036).
3. **Patch and constrain dependencies:** move to Go 1.26.5, review/expire Docker vulnerability waivers, pin executable inputs, isolate node_modules from Go, and align the Wails compatibility tuple (OPS-009 through OPS-014, OPS-033, OPS-034).
4. **Make release validation mean readiness:** require green evidence, close clean-platform/package tests, repair prerelease/platform versioning, and publish an explicit support matrix (OPS-015 through OPS-020, OPS-028 through OPS-031, OPS-037).
5. **Improve failure detection and support:** add race/coverage/native E2E, timeouts/artifacts, privilege isolation, durable redacted logging, recovery runbooks, and privacy guidance (OPS-021 through OPS-027, OPS-039, OPS-040).

## Suggested minimum protected-release gate

A production tag should be publishable only when all of the following are true for the exact tagged SHA:

- protected branch and signed annotated tag provenance verified;
- clean checkout, dependency integrity, generated bindings, format/lint/typecheck, Go vet/lint/vulnerability checks;
- Go unit/race/security/performance tests and frontend unit/accessibility/browser tests;
- isolated real-Docker integration tests;
- Windows, Linux, and macOS native package builds with declared architectures;
- clean-host install/launch/core-smoke/uninstall tests for each artifact;
- Windows executable/uninstaller/installer signatures and macOS strict signing/notarization/staple verified;
- AppImage and deb runtime/dependency checks;
- all normative release checklist rows green with retrievable immutable evidence;
- final artifact SBOM, signed checksums, provenance/attestations, malware/vulnerability scans, and digest inventory;
- draft release reviewed once, then atomically published without replacement.
