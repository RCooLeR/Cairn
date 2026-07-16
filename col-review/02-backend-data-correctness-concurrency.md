# Backend, Data Correctness, Concurrency, and Security Review

Review date: 2026-07-15

Scope: Go backend and cross-cutting runtime architecture, including shell/application lifecycle, providers, Docker access, Compose/project discovery, persistence, registry authentication, updates, backups, port forwarding, logs, metrics, terminal, Agent integration, lineage, the Windows Docker bridge, and the pinned Wails server transport.

Review posture: read-only source audit. No application source was changed. The worktree was already dirty and contained unrelated untracked material; it was preserved. An aggregate Go test invocation became suspended by the Windows host process runner and left exited/Initialized process records. That is treated as an environment/tooling artifact, not as a Cairn defect. Independent Ubuntu WSL validation subsequently passed, as detailed below. Findings are supported by source behavior unless explicitly labeled as a risk or coverage gap.

## Validation results

- All non-shell internal Go package tests passed under Ubuntu WSL.
- Race-enabled tests passed for all non-shell internal packages.
- CGO_ENABLED=0 go test -tags server . ./internal/shell passed, validating the server-tag entry point and shell package.
- Aggregate non-shell test coverage was 68.1%.
- internal/services coverage was 46.4%.
- internal/shell coverage was 15.9%.

These passing results are useful evidence against broad regressions, but they do not invalidate the source-confirmed findings below. In particular, the low shell and service coverage aligns with the missing server-authentication, event-forwarding, lifecycle, and cross-service contract tests identified by this review.

## Severity model

- Critical: direct credential loss or unauthenticated control of Docker/host-equivalent capabilities.
- High: likely security, data-loss, cross-context action, serious correctness, or practical denial-of-service defect.
- Medium: material reliability, lifecycle, performance, policy, or defense-in-depth defect.
- Low: limited-impact hygiene issue or very low-likelihood failure mode.

## Executive priorities

The highest-priority fixes are:

1. Disable or secure server mode before deployment: it currently exposes all bound desktop services without application authentication, authorization, TLS, CSRF/origin protection, or request limits.
2. Bind registry credentials to explicitly trusted token realms.
3. make project and cache identities include provider and Docker context, and enforce that scope in every service.
4. Repair update-check generations and metrics tier queries so stale or missing data cannot drive actions.
5. Put every background job and stream under root lifecycle ownership with caps, cancellation, waiting, and durable final state.
6. Close confirmation bypasses around imported Compose deployment and local-driver bind volumes.
7. Bound every network body, file read, child-process output, log line, session count, and public service concurrency path.

---

## Findings

### BE-001 — Server mode exposes an unauthenticated remote-control API

- Severity: Critical
- Confidence: High
- Type: Security / authorization / deployment
- Status: Confirmed when Cairn is built with the server tag; impact depends on network reachability and mounted host/Docker resources.

Evidence:

- Cairn registers provider, Docker, project, Compose, metrics, logs, terminal, updates, lineage, backup, port-forward, registry, Agent, settings, and notification services with Wails at E:/Development/projects/apps/rcooler/Cairn/internal/shell/app.go:141-168.
- The same application options contain no authentication middleware, authorization layer, or Server TLS configuration at E:/Development/projects/apps/rcooler/Cairn/internal/shell/app.go:141-190.
- The server container explicitly sets WAILS_SERVER_HOST=0.0.0.0 at E:/Development/projects/apps/rcooler/Cairn/build/docker/Dockerfile.server:43-48.
- The documented Docker run task publishes host port 8080 without a loopback address at E:/Development/projects/apps/rcooler/Cairn/build/Taskfile.yml:270-281, which makes Docker bind all host interfaces by default.
- The pinned Wails server reads that environment variable, defaults to port 8080, and serves plain HTTP when TLS is nil at C:/Users/roman/go/pkg/mod/github.com/wailsapp/wails/v3@v3.0.0-alpha2.103/pkg/application/application_server.go:66-85,107-136.
- Wails exposes the assets/runtime handler and event WebSocket at C:/Users/roman/go/pkg/mod/github.com/wailsapp/wails/v3@v3.0.0-alpha2.103/pkg/application/application_server.go:202-226.
- The HTTP transport routes /wails/runtime directly to numeric service object/method dispatch at C:/Users/roman/go/pkg/mod/github.com/wailsapp/wails/v3@v3.0.0-alpha2.103/pkg/application/transport_http.go:130-165,247-321. It does not authenticate a caller, authorize an operation, check Origin, require a CSRF token, restrict HTTP method, or require a JSON content type.
- The event WebSocket explicitly skips origin verification at C:/Users/roman/go/pkg/mod/github.com/wailsapp/wails/v3@v3.0.0-alpha2.103/pkg/application/websocket_server.go:60-95 and broadcasts each event to every connected client at :124-136.

Scenario and impact:

An unauthenticated client that can reach port 8080 can discover the public runtime JavaScript/object IDs and invoke Cairn's Docker, provider, terminal, backup, registry, Agent, settings, and file-oriented methods with the server process's authority. It can also subscribe to logs, terminal output, job events, and metrics. If a Docker socket or host paths are mounted into the server container, this is effectively host compromise. Even without those mounts, it exposes Cairn data and process resources.

This is also vulnerable to cross-site request attacks. The runtime parser ignores Content-Type, so a malicious website can send a simple cross-origin text/plain body containing valid JSON even if it cannot read the response. The WebSocket accepts every Origin. Binding only to localhost is therefore useful network reduction but is not, by itself, a complete browser-origin defense.

Recommendation:

- Do not ship or expose server mode until mandatory authentication and per-user authorization exist.
- Separate server-safe APIs from desktop-only host terminal, arbitrary file, credential, provider-install, and Docker-host capabilities.
- Require TLS directly or require an authenticated reverse proxy while binding the backend to loopback/private IPC only.
- Enforce Host and Origin allowlists, CSRF protection, strict methods and content types, secure sessions/tokens, event isolation, rate limits, concurrency quotas, and audit identity.
- Default Docker publication to 127.0.0.1 and fail startup on wildcard bind unless an explicit secure-server configuration is present.

Regression test:

Start the server build and assert that unauthenticated and cross-origin HTTP runtime calls and WebSocket upgrades are rejected. Authenticate two users and assert least-privilege method authorization and event isolation. Verify wildcard startup fails without TLS/auth configuration.

### BE-002 — Ordinary Wails runtime request bodies are unbounded

- Severity: High
- Confidence: High
- Type: Security / denial of service / dependency integration
- Status: Confirmed in the pinned Wails dependency.

Evidence:

- Cairn pins github.com/wailsapp/wails/v3 v3.0.0-alpha2.103 at E:/Development/projects/apps/rcooler/Cairn/go.mod:17.
- The ordinary /wails/runtime path copies the complete request body into a growing bytes.Buffer without http.MaxBytesReader or an application-defined cap at C:/Users/roman/go/pkg/mod/github.com/wailsapp/wails/v3@v3.0.0-alpha2.103/pkg/application/transport_http.go:144-165.
- Only the optional chunk path is capped: 1 MiB per chunk and 64 MiB assembled at C:/Users/roman/go/pkg/mod/github.com/wailsapp/wails/v3@v3.0.0-alpha2.103/pkg/application/transport_http.go:30-39,170-244.
- Server read timeout is 30 seconds by default, but that limits duration rather than total bytes at C:/Users/roman/go/pkg/mod/github.com/wailsapp/wails/v3@v3.0.0-alpha2.103/pkg/application/application_server.go:87-118.

Scenario and impact:

An unauthenticated server-mode client sends a very large non-chunked body quickly enough to fit within the timeout. The buffer repeatedly grows and retains the full body before parsing, allowing memory exhaustion or process termination. Desktop webview traffic also lacks an ordinary-body application limit, so a renderer defect can cause the same failure locally.

Recommendation:

Add a size-limiting middleware before the Wails transport, reject oversized Content-Length early, wrap the body in http.MaxBytesReader, return HTTP 413, and cap aggregate concurrent body memory. Patch or upgrade Wails so the cap is part of the transport rather than relying only on Cairn middleware.

Regression test:

Send a body one byte below and one byte above the configured limit on both ordinary and chunked paths. Assert bounded allocation, a 413 response for overflow, chunk-store cleanup, and unaffected subsequent requests.

### BE-003 — A registry can redirect Cairn's stored credentials to an attacker-selected Bearer realm

- Severity: Critical
- Confidence: High
- Type: Security / credential exfiltration
- Status: Confirmed.

Evidence:

- Cairn accepts and parses the registry-provided WWW-Authenticate challenge at E:/Development/projects/apps/rcooler/Cairn/internal/registry/auth.go:217-251.
- It accepts an arbitrary realm URL and validates only HTTPS, or the explicitly permitted local-HTTP case, at E:/Development/projects/apps/rcooler/Cairn/internal/registry/auth.go:254-286.
- It sends the stored identity token or Basic username/password to that realm at E:/Development/projects/apps/rcooler/Cairn/internal/registry/auth.go:281-285.
- The HTTP client's redirect behavior is not constrained to the approved auth origin.

Scenario and impact:

A malicious or compromised custom registry responds with WWW-Authenticate: Bearer realm="https://attacker.example/token". Cairn sends the configured registry identity token or Basic credentials to attacker.example. Real registries can legitimately use a different auth host, so a simplistic same-host check would break valid deployments, but the current implementation has no trust binding at all.

Recommendation:

Create an explicit registry-to-auth-realm trust policy. Preconfigure known official realm relationships, require informed approval for new cross-origin realms, avoid sending credentials to an untrusted realm, and reject credential-bearing redirects outside the approved origin set. Canonicalize hosts and ports and log realm changes without logging secrets.

Regression test:

Use a test registry that challenges with same-origin, approved cross-origin, unapproved cross-origin, downgrade, userinfo, IP-literal, and redirecting realms. Assert credentials are sent only to approved TLS origins.

### BE-004 — Three backend event topics required by the UI are never forwarded

- Severity: High
- Confidence: High
- Type: Correctness / API contract
- Status: Confirmed.

Evidence:

- TopicImagePushProgress, TopicUpdatesCheckProgress, and TopicUpdatesApplied are declared at E:/Development/projects/apps/rcooler/Cairn/internal/bus/bus.go:27-32.
- The shell forwarding list at E:/Development/projects/apps/rcooler/Cairn/internal/shell/app.go:216-235 omits all three topics.
- The frontend subscribes to update-check progress at E:/Development/projects/apps/rcooler/Cairn/frontend/src/App.tsx:2055, update-applied at :2091, and image-push progress at :2230.
- CheckAll publishes its update-specific progress but does not provide an independent durable result that repairs a lost completion at E:/Development/projects/apps/rcooler/Cairn/internal/updates/manager.go:472-487.

Scenario and impact:

The backend publishes a push or update event, but the Wails bridge never subscribes, so the UI never receives it. Progress can remain stuck and completion-dependent refreshes do not occur.

Recommendation:

Forward every public event using one declarative topic-to-event registry. Make final job state queryable so events are hints rather than the sole source of truth.

Regression test:

Enumerate public bus topics and assert each intended frontend topic crosses the shell bridge with the correct name and payload. Run push/check/apply flows end to end and verify final UI state after deliberately dropping an intermediate event.

### BE-005 — Project identity omits Docker context and import path

- Severity: High
- Confidence: High
- Type: Data integrity / multi-context isolation
- Status: Confirmed.

Evidence:

- ProjectID is derived only from provider ID and project name at E:/Development/projects/apps/rcooler/Cairn/internal/compose/ids.go:18-24.
- Detector snapshots use that ID at E:/Development/projects/apps/rcooler/Cairn/internal/compose/detector.go:320-341.
- Import derives the same identity at E:/Development/projects/apps/rcooler/Cairn/internal/services/project_service.go:113-117,174-178.
- projects.id is a global primary key at E:/Development/projects/apps/rcooler/Cairn/internal/store/migrations/0001_v1_schema.sql:33-46.
- SaveSnapshot handles the collision by rewriting context_name at E:/Development/projects/apps/rcooler/Cairn/internal/store/projects.go:102-123.

Scenario and impact:

Two Docker contexts contain a project with the same Compose project name. Reconciling context B overwrites/moves context A's row. Two imported folders with the same basename/project name also collide even in one context.

Recommendation:

Migrate project identity and related foreign keys to provider + context + stable project identity. For imports, include a user-selected logical name or canonical project path discriminator. Preserve a separate display name.

Regression test:

Persist same-named projects in two contexts and two distinct import folders. Reconcile and act on each in alternating order; assert all rows, services, lineage, update checks, and histories remain isolated.

### BE-006 — Stale project cleanup is scoped only to provider, not context

- Severity: High
- Confidence: High
- Type: Data loss / reconciliation
- Status: Confirmed.

Evidence:

- Stale cleanup methods filter by provider but not context at E:/Development/projects/apps/rcooler/Cairn/internal/store/projects.go:133-141,173-180.
- Project IDs already omit context as described in BE-005.

Scenario and impact:

While active in context A, a scan does not observe projects that exist only in context B. Provider-wide stale cleanup can delete B's persisted projects and dependent state.

Recommendation:

Require provider and context on every reconciliation and cleanup repository method. Perform snapshot upsert and stale deletion in a single context-scoped transaction and reject an empty/unknown context.

Regression test:

Seed contexts A and B, reconcile A with a deliberately partial result, and assert no B row is updated or deleted.

### BE-007 — Compose, update, and lineage APIs accept project IDs outside the active provider/context

- Severity: High
- Confidence: High
- Type: Authorization / context isolation
- Status: Confirmed.

Evidence:

- ProjectService contains the expected active provider/context guard at E:/Development/projects/apps/rcooler/Cairn/internal/services/project_service.go:899-911.
- ComposeService has no ProviderID or ContextName scope fields at E:/Development/projects/apps/rcooler/Cairn/internal/services/services.go:116-124.
- Compose Config, Ps, and action paths load arbitrary stored project IDs at E:/Development/projects/apps/rcooler/Cairn/internal/services/project_service.go:355-391,534-586.
- Update planning/execution accepts arbitrary project/history IDs at E:/Development/projects/apps/rcooler/Cairn/internal/updates/manager.go:524-533 and E:/Development/projects/apps/rcooler/Cairn/internal/updates/executor.go:140-164,241-276,777-788.
- Lineage reads/replaces by direct IDs without an active-scope guard at E:/Development/projects/apps/rcooler/Cairn/internal/lineage/manager.go:53-78,121-149.

Scenario and impact:

A stale or known project/history ID from context B is supplied while context A is active. Cairn can disclose B's stored configuration or run B's Compose paths through A's active provider, and can replace lineage/update data across contexts.

Recommendation:

Centralize a required scope-authorization helper and make repository methods require provider/context. Include scope in every plan and revalidate it on apply after acquiring the runtime lock.

Regression test:

Create resources in two providers/contexts and call every ID-based Compose, update, rollback, and lineage method with the other scope active. Assert a consistent not-found/forbidden result and no side effect.

### BE-008 — Update “latest” grouping retains stale and duplicate checks

- Severity: High
- Confidence: High
- Type: Data correctness / persistence
- Status: Confirmed.

Evidence:

- The latest-check query groups by container_id, image_ref, and base_image_ref at E:/Development/projects/apps/rcooler/Cairn/internal/store/updates.go:485-500.
- Container IDs and image/base references change during normal recreation and configuration updates.
- CheckProjectUpdates inserts checks individually at E:/Development/projects/apps/rcooler/Cairn/internal/updates/manager.go:165-171 and never invalidates the previous project check generation.

Scenario and impact:

A service container is recreated or its reference changes. The previous row remains the latest member of its old group while a new row becomes latest in another group. Plans can include both stale and current updates and create duplicate histories.

Recommendation:

Introduce a transactional check-run generation. Atomically replace the current logical service/update set, key it by stable service/update identity rather than ephemeral container ID, and keep historical checks separately with retention.

Regression test:

Run checks before and after container recreation and reference changes. Assert exactly one current row/action per logical service and that a failed partial check cannot replace a complete generation.

### BE-009 — Update checks are persisted non-atomically and grow without a lifecycle boundary

- Severity: Medium
- Confidence: High
- Type: Persistence / atomicity / retention
- Status: Confirmed.

Evidence:

- CheckProjectUpdates inserts each result in a loop at E:/Development/projects/apps/rcooler/Cairn/internal/updates/manager.go:165-171 even though the repository provides a batch insertion path.
- No project-generation invalidation accompanies those inserts.
- No retention cleanup for image update checks was found in the store/runtime.

Scenario and impact:

Database failure or cancellation midway through a check leaves a partial “current” result set. Repeated checks grow the table indefinitely and make latest-selection behavior increasingly ambiguous.

Recommendation:

Commit one complete run transactionally, mark it successful only after every row is written, keep the prior complete generation on failure, and expire historical generations by age/count.

Regression test:

Inject a failure after N rows and assert readers still see the prior complete generation. Run thousands of generations and assert retention bounds row count.

### BE-010 — Metrics range queries discard newer retention tiers

- Severity: High
- Confidence: High
- Type: Data correctness / time series
- Status: Confirmed.

Evidence:

- Retention creates raw data for the newest hour, one-minute rollups through 24 hours, and fifteen-minute rollups for older data at E:/Development/projects/apps/rcooler/Cairn/internal/store/metrics.go:144-168,231-322.
- ResolutionForRange selects one resolution for the entire requested range at E:/Development/projects/apps/rcooler/Cairn/internal/store/metrics.go:171-185.
- QuerySeries filters exclusively to that selected tier at E:/Development/projects/apps/rcooler/Cairn/internal/store/metrics.go:74-141.

Scenario and impact:

A two-hour request chooses one-minute data and omits the newest raw hour. A two-day request chooses fifteen-minute data and omits the newest 24 hours. Charts end in the past even though fresh samples exist.

Recommendation:

Split the requested interval at retention cutoffs, query each applicable tier, stitch chronologically, and deduplicate boundary buckets.

Regression test:

Seed all tiers and query ranges of 59 minutes, 61 minutes, 23 hours, 25 hours, and two days. Assert continuous coverage through the newest sample and no duplicate boundaries.

### BE-011 — Raw project metrics group on exact timestamps instead of time buckets

- Severity: High
- Confidence: High
- Type: Data correctness / aggregation
- Status: Confirmed.

Evidence:

- Project-series raw aggregation sums rows grouped by exact sampled_at at E:/Development/projects/apps/rcooler/Cairn/internal/store/metrics.go:88-105.
- Samples from different containers are collected at slightly different timestamps.

Scenario and impact:

Container A samples at 12:00:00.010 and B at 12:00:00.032. They become separate groups, so a project chart labeled as a total displays one-container values rather than the sum.

Recommendation:

Bucket raw timestamps at the requested display resolution before summing, or align all container samples to one collection timestamp. Define missing-container semantics explicitly.

Regression test:

Insert skewed timestamps for multiple containers and assert each project bucket contains the expected sum and count.

### BE-012 — Provider installation is detached from application lifetime

- Severity: High
- Confidence: High
- Type: Lifecycle / concurrency
- Status: Confirmed.

Evidence:

- ApplyInstall launches a goroutine using context.WithoutCancel at E:/Development/projects/apps/rcooler/Cairn/internal/services/provider_service.go:75-86.
- ProviderService exposes no job cancellation/wait lifecycle.
- App shutdown cancels the root, stops the runtime, then closes bus and database at E:/Development/projects/apps/rcooler/Cairn/internal/shell/app.go:169-175.

Scenario and impact:

Cairn shuts down while a privileged 20-minute installation is running. The install continues after root cancellation and can publish progress or write audit state after the bus/database close.

Recommendation:

Own installs in a root job registry. Derive their context from the root plus explicit user cancellation, track a waitgroup and durable status, and cancel/wait within a bounded shutdown budget.

Regression test:

Start a blocking install, trigger shutdown, and assert the child command is terminated/reaped, the job records canceled state before database close, and no event is published after bus close.

### BE-013 — Provider install plans can be applied concurrently, out of order, and after abandonment

- Severity: High
- Confidence: High
- Type: Concurrency / privileged operation planning
- Status: Confirmed.

Evidence:

- Plan lookup and execution are not an atomic claim at E:/Development/projects/apps/rcooler/Cairn/internal/providers/manager.go:299-322.
- Linux, macOS, and Windows step handlers delete a plan only after the last step and do not enforce a strict next-step state at E:/Development/projects/apps/rcooler/Cairn/internal/providers/linux_native.go:271-301, E:/Development/projects/apps/rcooler/Cairn/internal/providers/macos_colima.go:303-333, and E:/Development/projects/apps/rcooler/Cairn/internal/providers/windows_wsl.go:371-401.
- Failed or abandoned plans remain in the manager map at E:/Development/projects/apps/rcooler/Cairn/internal/providers/manager.go:266-321.

Scenario and impact:

Two ApplyInstall calls read the same plan before either removes it and run apt, brew, or WSL installation concurrently. A caller can also skip/replay steps, and abandoned plans leak or remain replayable.

Recommendation:

Atomically Take or mark a plan in-flight, store the exact next permitted step, enforce single use and expiry, and remove/terminally close the plan on success, failure, cancellation, or timeout.

Regression test:

Race two applies against one plan, try out-of-order/replayed steps, and advance a fake clock past expiry. Assert only one command sequence ever starts.

### BE-014 — ImportProject can auto-deploy Compose content without a bound confirmation plan

- Severity: High
- Confidence: High
- Type: Security / confirmation bypass
- Status: Confirmed.

Evidence:

- ImportProject validates and stores a project, then auto-deploys when no containers exist at E:/Development/projects/apps/rcooler/Cairn/internal/services/project_service.go:153-250, specifically :236-238.
- The background helper calls runProjectAction with ProjectActionDeploy and a nil plan at E:/Development/projects/apps/rcooler/Cairn/internal/services/project_service.go:1310-1333.

Scenario and impact:

A direct ImportProject call points to Compose content with privileged containers, host mounts, builds, exposed ports, or destructive startup commands. Cairn starts it without requiring a single-use plan bound to the reviewed file content.

Recommendation:

Make import/save and deploy separate operations. Require an expiring single-use review token containing canonical path, provider/context, file hashes, environment inputs, and summarized effects. Rehash and revalidate at apply time.

Regression test:

Import a project requiring deployment without a plan and assert no Docker/Compose command runs. Modify the file after planning and assert application is rejected as stale.

### BE-015 — Local volume driver options bypass dangerous bind-mount confirmation

- Severity: High
- Confidence: High
- Type: Security / policy bypass
- Status: Confirmed.

Evidence:

- RunImage risk classification inspects bind mount specs at E:/Development/projects/apps/rcooler/Cairn/internal/services/docker_commands.go:70-85.
- Runs without recognized binds execute directly at E:/Development/projects/apps/rcooler/Cairn/internal/services/services.go:325-353.
- CreateVolume accepts arbitrary Driver and DriverOpts without a confirmation plan at E:/Development/projects/apps/rcooler/Cairn/internal/services/services.go:741-758.
- Docker forwards those options at E:/Development/projects/apps/rcooler/Cairn/internal/docker/create.go:320-353.

Scenario and impact:

Create a local volume with type=none, o=bind, device=/, then mount that named volume into a container. The run request contains a named volume rather than a bind, so it bypasses the host-filesystem warning while exposing the host root.

Recommendation:

Disallow local bind-style driver options or classify/plan them exactly like binds. Canonicalize every mount mechanism, including plugins and propagation modes, before policy evaluation.

Regression test:

Attempt bind-equivalent local volumes with common option spellings and assert the same high-risk confirmation and path details as a direct bind mount.

### BE-016 — Arbitrary image runs bypass planning when no direct bind mount is present

- Severity: High
- Confidence: High
- Type: Security policy / resource control
- Status: Confirmed design gap.

Evidence:

- A run without a recognized direct bind is classified safe and executes immediately at E:/Development/projects/apps/rcooler/Cairn/internal/services/docker_commands.go:70-85 and E:/Development/projects/apps/rcooler/Cairn/internal/services/services.go:325-353.
- The resulting HostConfig sets ports, restart policy, mounts, and related options at E:/Development/projects/apps/rcooler/Cairn/internal/docker/create.go:532-579, but there is no mandatory plan that summarizes the image, published address scope, persistence, networks, or resource budgets.

Scenario and impact:

A caller pulls and starts an arbitrary image, publishes a port on every interface, uses a persistent restart policy, or starts an unbounded CPU/memory/PID workload. Because it has no direct bind mount, Cairn does not require a reviewed plan.

Recommendation:

Require a server-issued plan for every container run. Risk-level the plan, but always bind it to immutable request content and show image provenance, ports and bind addresses, networks, restart policy, privileges, mounts, and CPU/memory/PID limits. Supply conservative defaults.

Regression test:

Assert every RunImage request needs a matching single-use plan and that mutation of ports, image, restart policy, network, mounts, or limits after planning invalidates it.

### BE-017 — Private image pulls ignore configured registry credentials

- Severity: High
- Confidence: High
- Type: Functional correctness / authentication
- Status: Confirmed.

Evidence:

- Pull uses ImagePull with an empty image.PullOptions value at E:/Development/projects/apps/rcooler/Cairn/internal/docker/create.go:439-475.
- Push does encode RegistryAuth at E:/Development/projects/apps/rcooler/Cairn/internal/docker/create.go:478-489.

Scenario and impact:

Registry Login succeeds and credentials are stored, but PullImage or RunImage attempts an anonymous pull and fails for private repositories. Users receive behavior inconsistent with the successful login state.

Recommendation:

Resolve the registry from the normalized image reference, load credentials through the configured helper, encode RegistryAuth, and distinguish credential-helper failures from anonymous-denied responses.

Regression test:

Use an authenticated test registry. Assert direct pull and pull-before-run succeed after login, fail after logout, and never send credentials to a different registry.

### BE-018 — Docker object cache primary keys are not provider/context scoped

- Severity: High
- Confidence: High
- Type: Data integrity / multi-provider isolation
- Status: Confirmed.

Evidence:

- images_cache.id and networks_cache.id are global primary keys at E:/Development/projects/apps/rcooler/Cairn/internal/store/migrations/0001_v1_schema.sql:83-93,108-120.
- Container cache uses the same global-ID model at E:/Development/projects/apps/rcooler/Cairn/internal/store/migrations/0001_v1_schema.sql:64-80.
- Image save uses ON CONFLICT(id) and rewrites provider_id at E:/Development/projects/apps/rcooler/Cairn/internal/store/object_cache.go:248-263.
- Network save does the same at E:/Development/projects/apps/rcooler/Cairn/internal/store/object_cache.go:350-369.
- Docker context is absent from the cache key.

Scenario and impact:

The same content-addressed image digest exists on two backends. Reconciling backend B changes the one row's provider_id, making the image disappear from A's cache. Reused network/container IDs can produce the same effect, and contexts within one provider overwrite one another.

Recommendation:

Use composite provider_id + context_name + object_id primary keys and update every index/foreign key. If the cache is intentionally active-context-only, make it explicitly ephemeral and clear it atomically on context changes rather than pretending to persist multiple scopes.

Regression test:

Reconcile identical image IDs and deliberately colliding network/container IDs across two providers and contexts. Assert independent rows and scoped reads.

### BE-019 — Log-stream sessions, readers, and rings have no global or per-client cap

- Severity: High
- Confidence: High
- Type: Resource exhaustion / lifecycle
- Status: Confirmed.

Evidence:

- Log manager session storage and creation have no capacity limit at E:/Development/projects/apps/rcooler/Cairn/internal/logsvc/manager.go:33-47,369-448.
- Each session can attach a reader goroutine per selected container.
- The default ring can retain 50,000 lines per session at E:/Development/projects/apps/rcooler/Cairn/internal/logsvc/types.go:20-25.

Scenario and impact:

A buggy or hostile caller repeatedly starts streams and never stops them. Memory, Docker log connections, goroutines, and bus traffic scale with session count times container count.

Recommendation:

Set per-client, per-scope, and global stream/reader limits; deduplicate equivalent streams; expire disconnected/idle sessions; charge ring capacity against a global byte budget rather than line count alone.

Regression test:

Attempt to exceed every quota, disconnect clients without cleanup, and run a soak test across many containers. Assert bounded goroutines, memory, and Docker readers.

### BE-020 — Log export materializes unbounded Docker history in memory

- Severity: High
- Confidence: High
- Type: Denial of service / export correctness
- Status: Confirmed.

Evidence:

- ExportLogs maps an unspecified tail to all available history and collects results before writing at E:/Development/projects/apps/rcooler/Cairn/internal/logsvc/manager.go:110-194.

Scenario and impact:

Exporting long-lived or very noisy container logs requests all history across containers and builds a large in-memory slice. The process can exhaust memory before the first output byte is durably written.

Recommendation:

Stream each reader through filtering/formatting directly to a temporary output file, enforce maximum bytes/time/lines unless the user explicitly raises them, report truncation, fsync, and atomically rename on success.

Regression test:

Export a synthetic multi-gigabyte stream through bounded fakes and assert stable memory, correct cancellation cleanup, and an explicit truncation result at configured limits.

### BE-021 — A newline-free Docker log record can grow without bound

- Severity: High
- Confidence: High
- Type: Parser robustness / denial of service
- Status: Confirmed.

Evidence:

- The framed Docker parser appends fragments until it sees a newline without a maximum assembled-record size at E:/Development/projects/apps/rcooler/Cairn/internal/logsvc/parse.go:109-155.
- The plain scanner path has a token cap, so behavior differs by stream format.

Scenario and impact:

A container emits an indefinitely long line through Docker's framed protocol. Cairn continuously grows the pending buffer and can be OOM-killed.

Recommendation:

Set a strict maximum assembled byte length, emit a truncated record with metadata, discard until the next delimiter, and count/report truncation. Apply equivalent semantics to framed and plain paths.

Regression test:

Feed fragmented records below, at, and far above the limit, including no newline before EOF. Assert bounded allocation and deterministic truncated output.

### BE-022 — Log pagination cursors are not unique and can skip duplicate records

- Severity: Medium
- Confidence: High
- Type: Data correctness / pagination
- Status: Confirmed.

Evidence:

- Cursor ordering/identity uses timestamp, container, stream, and text at E:/Development/projects/apps/rcooler/Cairn/internal/logsvc/ring.go:65-164.
- Docker can emit repeated identical lines with the same timestamp.

Scenario and impact:

Two or more identical records share the complete cursor tuple. If a page ends among them, the next page cannot distinguish which duplicate was consumed and skips one or more records.

Recommendation:

Assign a monotonic per-stream sequence number when accepting each record and use it as the cursor. Preserve the sequence through filtering and include oldest/newest/dropped sequence metadata.

Regression test:

Insert many byte-identical records with one timestamp, paginate at every boundary, and assert every sequence appears exactly once.

### BE-023 — A transient log-reader failure permanently blocks reattachment

- Severity: High
- Confidence: High
- Type: Reliability / stream lifecycle
- Status: Confirmed.

Evidence:

- The manager marks a container attached before starting the reader at E:/Development/projects/apps/rcooler/Cairn/internal/logsvc/manager.go:402-448.
- The attached marker is not removed when that producer exits.

Scenario and impact:

A Docker log connection fails transiently. The producer exits, but later reconciliation sees the permanent attached marker and never starts another reader. The stream silently stops for that container.

Recommendation:

Remove the marker in a producer defer, record terminal reason, and reattach transient failures with capped exponential backoff and cancellation. Treat EOF for a stopped container separately.

Regression test:

Make the first reader fail, the second succeed, and assert exactly one bounded retry chain, resumed lines, and no retry after Stop.

### BE-024 — The event bus silently drops terminal and completion events

- Severity: High
- Confidence: High
- Type: Concurrency / IPC correctness
- Status: Confirmed.

Evidence:

- Publish silently discards the oldest queued event for each slow subscriber at E:/Development/projects/apps/rcooler/Cairn/internal/bus/bus.go:145-160.
- Shell forwarding uses buffers of only 32 for logs, stats, and job events and 4096 for terminal events at E:/Development/projects/apps/rcooler/Cairn/internal/shell/app.go:447-465.

Scenario and impact:

Brief renderer or event-loop backpressure fills a queue. Dropping terminal byte events corrupts the interactive stream. Dropping job.done leaves a job permanently busy because there is no guaranteed replay. Log drop counters do not cover this second bus hop.

Recommendation:

Use a backpressure-aware ordered byte channel for terminal data, reserve/control-prioritize terminal close and job completion, make jobs queryable, and attach monotonic sequence/gap metadata to lossy telemetry/log streams.

Regression test:

Block the consumer, exceed each buffer, then resume. Assert terminal bytes remain ordered/intact, completion is always observable, and lossy streams report exact gaps.

### BE-025 — Default WSL port forwarding can expose services to LAN without resource limits

- Severity: High
- Confidence: High
- Type: Network security / denial of service
- Status: Confirmed behavior and design risk.

Evidence:

- Port forwarding defaults enabled at E:/Development/projects/apps/rcooler/Cairn/internal/store/settings.go:41.
- Runtime starts the manager at E:/Development/projects/apps/rcooler/Cairn/internal/shell/runtime.go:168-172.
- All-interface publishes map to a Windows 0.0.0.0 listener at E:/Development/projects/apps/rcooler/Cairn/internal/portforward/bind.go:15-58.
- Each TCP connection creates relay work and a backend dial with no global/per-IP cap at E:/Development/projects/apps/rcooler/Cairn/internal/portforward/manager.go:288-357.
- UDP stores one source-address session for 60 seconds without a quota at E:/Development/projects/apps/rcooler/Cairn/internal/portforward/udp.go:20-90.

Scenario and impact:

A workload published on all interfaces becomes reachable from the LAN through the Windows mirror. A LAN peer opens many TCP connections or sends UDP from many source tuples, exhausting sockets, goroutines, backend dials, or memory.

Recommendation:

Default mirrors to loopback or require explicit LAN consent with a firewall warning. Add global/per-forward/per-IP connection and UDP-session limits, dial/idle/read/write deadlines, rate limits, and exposure telemetry.

Regression test:

Verify fresh-install bind policy, then flood TCP and spoof many UDP source tuples. Assert limits, cleanup, warning state, and healthy service for legitimate clients.

### BE-026 — IPv6 all-interface publishing is widened to IPv4

- Severity: Medium
- Confidence: High
- Type: Network correctness / unintended exposure
- Status: Confirmed.

Evidence:

- The bind classifier treats both 0.0.0.0 and :: as all-interface at E:/Development/projects/apps/rcooler/Cairn/internal/portforward/bind.go:15-18.
- The selected Windows listener for that class is 0.0.0.0 at E:/Development/projects/apps/rcooler/Cairn/internal/portforward/bind.go:46-57.

Scenario and impact:

A container intentionally published only on IPv6 unspecified address is mirrored onto all IPv4 interfaces, broadening reachability beyond the requested address family.

Recommendation:

Preserve address family and requested bind semantics. Use [::] for IPv6, control dual-stack behavior explicitly, and never infer IPv4 exposure from an IPv6-only publish.

Regression test:

Cover IPv4 wildcard, IPv6 wildcard, IPv4/IPv6 loopback, and concrete addresses with dual-stack enabled and disabled. Assert exact listener families.

### BE-027 — Loopback, concrete IPv6, and link-local publishes are silently not forwarded

- Severity: Medium
- Confidence: High
- Type: Functional correctness / observability
- Status: Confirmed.

Evidence:

- unsupportedForwardBind rejects loopback and IPv6 forms at E:/Development/projects/apps/rcooler/Cairn/internal/portforward/bind.go:29-39.
- desiredForwards silently skips them at E:/Development/projects/apps/rcooler/Cairn/internal/portforward/bind.go:89-95.
- The feature's type comments describe mirroring publish interfaces at E:/Development/projects/apps/rcooler/Cairn/internal/portforward/types.go:6.

Scenario and impact:

A container published to 127.0.0.1, ::1, or a concrete IPv6 interface does not receive a Windows mirror, with no visible unsupported/error record.

Recommendation:

Implement these address forms using a backend dial that reaches the matching backend interface, or explicitly show an unsupported forward with reason/remediation rather than dropping it.

Regression test:

Feed every supported and unsupported bind form and assert either a functioning exact mirror or an observable diagnostic row.

### BE-028 — Port-forward identity omits bind address

- Severity: Medium
- Confidence: High
- Type: Network correctness / reconciliation
- Status: Confirmed.

Evidence:

- forwardKey contains only protocol and host port at E:/Development/projects/apps/rcooler/Cairn/internal/portforward/bind.go:11-13.
- Reconciliation uses that key and chooses one candidate at E:/Development/projects/apps/rcooler/Cairn/internal/portforward/bind.go:89-120.

Scenario and impact:

Different containers or interfaces publish the same protocol/port on distinct concrete addresses. Cairn collapses them into one forward and can route an address to the wrong backend workload.

Recommendation:

Include normalized bind address/family and backend target identity in the key. Detect true conflicts explicitly rather than selecting a broadest winner.

Regression test:

Publish the same port on two concrete interfaces and two containers. Assert independent forwarding or a deterministic conflict without misrouting.

### BE-029 — Failed UDP backend sessions remain cached and blackhole traffic

- Severity: Medium
- Confidence: High
- Type: Network reliability / resource lifecycle
- Status: Confirmed.

Evidence:

- UDP session creation/pump and error handling are at E:/Development/projects/apps/rcooler/Cairn/internal/portforward/udp.go:69-116.
- Backend pump failure does not synchronously evict the source session, so it remains until idle expiry.

Scenario and impact:

The WSL/backend UDP connection errors. Subsequent datagrams from the same source reuse a dead session and disappear for up to the idle timeout.

Recommendation:

Give each session a close-once callback that removes itself on either pump error, refresh deadlines correctly, and optionally retry a bounded number of idempotent datagrams.

Regression test:

Force the backend connection to fail, immediately send another datagram from the same source, and assert a new session is established or an explicit error is surfaced.

### BE-030 — Concurrent backup plans can allocate the same archive path

- Severity: High
- Confidence: High
- Type: Data corruption / concurrency
- Status: Confirmed.

Evidence:

- Backup planning derives second-resolution names and checks availability using os.Stat at E:/Development/projects/apps/rcooler/Cairn/internal/backups/manager.go:191-219,934-976.
- Planning does not reserve the archive or metadata path.

Scenario and impact:

Two PlanBackup calls for one volume occur in the same second before either archive exists. Both plans receive the same filenames. Concurrent tar helpers write one archive; metadata O_EXCL and cleanup paths can then fail or delete shared artifacts.

Recommendation:

Reserve a unique archive/metadata pair atomically with O_EXCL during planning, or atomically allocate at application and persist the reservation owner. Write through a unique staging name and atomically publish only after digest/metadata success.

Regression test:

Synchronize many concurrent plans to one timestamp and apply them together. Assert unique paths, valid independent archives, and cleanup limited to the owning job.

### BE-031 — Backup helper uses a mutable image with excessive privileges

- Severity: High
- Confidence: High
- Type: Supply chain / least privilege
- Status: Confirmed design gap.

Evidence:

- The helper image is the mutable tag alpine:3 at E:/Development/projects/apps/rcooler/Cairn/internal/backups/manager.go:27-33.
- The helper run grants the source volume read access and the complete backup directory read/write access while retaining default root, network, capabilities, and writable root filesystem at E:/Development/projects/apps/rcooler/Cairn/internal/backups/manager.go:844-866.

Scenario and impact:

The tag is replaced or registry/DNS transport is compromised. The helper can read and exfiltrate the volume and can alter or delete every archive visible in the backup directory.

Recommendation:

Pin and verify an image digest or ship a minimal audited helper. Add network=none, read-only root, cap-drop=ALL, no-new-privileges, non-root where possible, and a unique job staging directory mount rather than the entire backup root.

Regression test:

Inspect the exact generated container config and assert a digest-pinned image, disabled network, dropped capabilities, read-only root, and only job-specific mounts.

### BE-032 — Live-volume backups are classified safe despite consistency risk

- Severity: High
- Confidence: High
- Type: Data integrity / safety policy
- Status: Confirmed.

Evidence:

- Running containers using the volume generate a warning, but the backup plan remains safe/executable at E:/Development/projects/apps/rcooler/Cairn/internal/backups/manager.go:196-203,900-924.

Scenario and impact:

A database or stateful service writes while tar walks the volume. The archive can contain mutually inconsistent files/pages and be unusable even though Cairn presents the operation as safe/successful.

Recommendation:

Support application quiesce hooks, stop/snapshot workflows, or explicit crash-consistent mode. Require informed confirmation for in-use volumes and record consistency mode plus active consumers in metadata.

Regression test:

Simulate a changing multi-file dataset and assert that a safe plan is not issued without quiescence or explicit crash-consistency acknowledgement.

### BE-033 — Backup format has no defined metadata-fidelity contract

- Severity: Medium
- Confidence: Medium
- Type: Data fidelity / portability
- Status: Risk requiring platform tests.

Evidence:

- Backup and restore use the Alpine/BusyBox tar path at E:/Development/projects/apps/rcooler/Cairn/internal/backups/manager.go:844-898.
- The command does not explicitly preserve or verify xattrs, ACLs, Linux capabilities, sparse extents, or all platform-specific metadata.

Scenario and impact:

A restored volume appears complete by file count but loses ACLs, extended attributes, executable capabilities, ownership semantics, or sparse-file layout. Applications can fail or security properties can change.

Recommendation:

Define the supported metadata contract, select a tool/format that preserves it, record format/tool versions, and verify archive contents before reporting success.

Regression test:

Round-trip owners/groups, permissions, symlinks, hard links, FIFOs if supported, ACLs, xattrs, Linux file capabilities, large IDs, Unicode paths, and sparse files on every supported backend.

### BE-034 — Canceling the Docker CLI may not cancel the daemon-side backup/restore container

- Severity: High
- Confidence: Medium
- Type: Lifecycle / cancellation
- Status: High-confidence risk based on Docker run process semantics.

Evidence:

- Backup/restore launches docker run without a tracked container name or ID at E:/Development/projects/apps/rcooler/Cairn/internal/backups/manager.go:844-898.
- Cancellation can terminate the client process while the created daemon-side container remains independently runnable.

Scenario and impact:

Cairn reports a job canceled or shuts down after killing the docker CLI, but the helper continues writing an archive or overwriting a target volume in the daemon.

Recommendation:

Use the Docker SDK and track the helper container ID, or allocate a unique name/cidfile. On cancellation, stop/kill/remove the helper and wait for terminal state before completing the job.

Regression test:

Block a helper, cancel after container creation, and assert the daemon contains no running or leftover helper and no file/volume changes occur after cancellation completion.

### BE-035 — Failed restore into a newly created volume leaves partial state

- Severity: Medium
- Confidence: High
- Type: Cleanup / partial failure
- Status: Confirmed.

Evidence:

- Restore creates a new target volume and returns on later failure without removing the new target at E:/Development/projects/apps/rcooler/Cairn/internal/backups/manager.go:530-547.

Scenario and impact:

Archive validation or extraction fails after target creation. A partial or empty volume remains, potentially colliding with a retry and appearing usable.

Recommendation:

Track ownership of newly created targets and remove them on any failure/cancellation unless the user explicitly requests preservation for forensics. Never remove a pre-existing volume.

Regression test:

Inject failure after create and midway through extraction; assert newly owned targets are removed while pre-existing targets remain untouched.

### BE-036 — Backup shutdown does not wait and abandoned plans are never globally pruned

- Severity: Medium
- Confidence: High
- Type: Lifecycle / memory retention
- Status: Confirmed.

Evidence:

- StopAll cancels backup jobs but does not wait for them at E:/Development/projects/apps/rcooler/Cairn/internal/backups/manager.go:148-162.
- Plan lookup removes only the requested plan when it notices expiry at E:/Development/projects/apps/rcooler/Cairn/internal/backups/manager.go:586-610.

Scenario and impact:

Shutdown closes Docker/database resources while job cleanup is still running. Clients that create plans and never apply them grow the in-memory map indefinitely.

Recommendation:

Wait for all jobs within the shared shutdown deadline and run a periodic global expiry janitor. Cap active plans per client/scope and expose counts.

Regression test:

Start blocking jobs, initiate shutdown, and assert cleanup finishes before dependency close. Create many abandoned plans under a fake clock and assert all expire without lookup.

### BE-037 — Agent endpoint is an unrestricted SSRF and data-exfiltration destination

- Severity: High
- Confidence: High
- Type: Security / privacy / SSRF
- Status: Confirmed design gap.

Evidence:

- Agent defaults enabled at E:/Development/projects/apps/rcooler/Cairn/internal/store/settings.go:49.
- Configuration accepts an arbitrary endpoint string at E:/Development/projects/apps/rcooler/Cairn/internal/services/agent_service.go:726-752.
- Chat context can include Docker inventory, logs, and project files at E:/Development/projects/apps/rcooler/Cairn/internal/services/agent_service.go:866-930.
- The HTTP client follows redirects without an origin/private-address policy at E:/Development/projects/apps/rcooler/Cairn/internal/services/agent_service.go:932-992.

Scenario and impact:

A mistaken or malicious endpoint points to a remote collector, cloud metadata/internal control plane, or a local service that redirects the POST elsewhere. Cairn sends operational and project context to that destination.

Recommendation:

Default to loopback-only, require explicit informed consent for remote destinations, require HTTPS/authentication, resolve and validate addresses, restrict redirects to an approved origin set, and preview exactly what context will leave the machine.

Regression test:

Test loopback, private, link-local/metadata, DNS-rebinding, HTTPS downgrade, cross-origin redirect, and approved remote endpoint cases. Assert sensitive POST data reaches only the approved final origin.

### BE-038 — Agent and import-review file reads are unbounded and accept special files

- Severity: High
- Confidence: High
- Type: Denial of service / filesystem safety
- Status: Confirmed.

Evidence:

- readAgentProjectFiles calls os.ReadFile before truncation at E:/Development/projects/apps/rcooler/Cairn/internal/services/agent_service.go:1500-1562.
- PlanFileEdit reads the complete existing file despite the edit-size cap at E:/Development/projects/apps/rcooler/Cairn/internal/services/agent_service.go:564-581.
- Project import review also uses unbounded reads at E:/Development/projects/apps/rcooler/Cairn/internal/services/project_service.go:1428-1494.

Scenario and impact:

A giant project file is read entirely and OOMs the process; a FIFO or device path blocks a service call indefinitely. Truncating only after os.ReadFile does not reduce the read allocation.

Recommendation:

Lstat and require regular files, validate size before open, read through io.LimitReader with one-byte overflow detection, cap aggregate bytes, and use context-aware file handling where platform support permits.

Regression test:

Exercise small, exactly capped, oversized, sparse, symlink, FIFO, directory, and device inputs. Assert early rejection, bounded memory, and cancellation.

### BE-039 — Agent redaction and max-context-lines do not bound or reliably sanitize payloads

- Severity: High
- Confidence: High
- Type: Privacy / secret handling / resource bounds
- Status: Confirmed gap.

Evidence:

- Redaction is line/key pattern based at E:/Development/projects/apps/rcooler/Cairn/internal/services/agent_service.go:2155-2176.
- Multiline secrets such as a PEM body have continuation lines without the sensitive key.
- marshalAgentData JSON-escapes file content at E:/Development/projects/apps/rcooler/Cairn/internal/services/agent_service.go:1984-1989.
- agentContextText later limits line count at E:/Development/projects/apps/rcooler/Cairn/internal/services/agent_service.go:1034-1051, but escaped file content can occupy one logical JSON line regardless of its byte/token size.

Scenario and impact:

A private key header is redacted while its body remains, or a token stored under an innocuous name is sent. A huge escaped file bypasses the line-count budget and causes a very large Agent request.

Recommendation:

Do not send .env values by default. Use structured allowlists, secret scanners appropriate to each format, and a user-visible preview. Enforce total and per-tool byte/token budgets before JSON marshaling.

Regression test:

Use multiline PEM, opaque tokens, quoted/multiline env values, Unicode, and a huge one-line file. Assert no secret fragments leave and all byte/token caps hold.

### BE-040 — Agent file replacement is non-atomic and path validation is raceable

- Severity: High
- Confidence: High for non-atomic write; Medium for adversarial symlink exploitation
- Type: File integrity / TOCTOU
- Status: Confirmed defect plus platform-dependent risk.

Evidence:

- Existing files are overwritten with os.WriteFile, which truncates in place, at E:/Development/projects/apps/rcooler/Cairn/internal/services/agent_service.go:1918-1937.
- Symlink/path validation and the eventual open/write are separate operations at E:/Development/projects/apps/rcooler/Cairn/internal/services/agent_service.go:625-665.

Scenario and impact:

A crash, disk-full event, or short write leaves the configuration truncated. In a shared/adversarial directory, a path component can be swapped between validation and open so the write reaches another target.

Recommendation:

Create a no-follow temporary file in the same directory, preserve mode/ACL, write and fsync, revalidate target identity, atomically rename, and fsync the directory. Prefer handle-relative/no-follow APIs for containment.

Regression test:

Inject failures at create/write/fsync/rename and assert the old file remains intact. Race symlink swaps and assert no external target changes.

### BE-041 — Registry token and index JSON bodies are unbounded

- Severity: High
- Confidence: High
- Type: Network denial of service
- Status: Confirmed.

Evidence:

- Token response JSON is decoded directly from the response body at E:/Development/projects/apps/rcooler/Cairn/internal/registry/auth.go:302-307.
- Registry index/manifest response JSON is decoded directly at E:/Development/projects/apps/rcooler/Cairn/internal/registry/resolve.go:133-145.

Scenario and impact:

A malicious registry or auth realm streams an enormous valid/invalid JSON body within the timeout. The decoder grows allocations until Cairn exhausts memory.

Recommendation:

Validate Content-Length where present, wrap every response in a strict limit, reject overflow and trailing data, and place semantic caps on token/tag/manifest fields.

Regression test:

Serve below-limit, above-limit, chunked infinite, misleading Content-Length, and compressed oversized responses. Assert bounded memory and a typed size error.

### BE-042 — Credential-helper preparation can fail open to inline credentials

- Severity: High
- Confidence: High
- Type: Credential storage / transactional integrity
- Status: Confirmed.

Evidence:

- Login prepares helper configuration and then runs docker login at E:/Development/projects/apps/rcooler/Cairn/internal/registry/auth.go:35-64.
- ensureCredentialHelper treats ProviderNotReady as success at E:/Development/projects/apps/rcooler/Cairn/internal/registry/credentials.go:83-88.
- A verification/finalization failure after login returns without transactional rollback/logout or guaranteed removal of inline auth.
- Credential lookup errors are ignored at E:/Development/projects/apps/rcooler/Cairn/internal/registry/auth.go:94 and E:/Development/projects/apps/rcooler/Cairn/internal/registry/resolve.go:55.

Scenario and impact:

The user selected docker-helper storage, but helper setup is unavailable. docker login stores base64 credentials inline. Later finalization fails, Cairn reports an error, and the credential remains in config. Other malformed-helper errors silently become anonymous registry requests.

Recommendation:

Fail closed before login when secure storage cannot be prepared. Back up configuration transactionally and scrub/logout/restore on every post-login failure. Propagate helper lookup errors distinctly.

Regression test:

Inject ProviderNotReady, helper command failure, config write failure, login success followed by verification failure, and rollback failure. Assert no residual inline secret and an accurate final account state.

### BE-043 — CheckAll is not coalesced and has no reliable completion state

- Severity: Medium
- Confidence: High
- Type: Concurrency / job semantics
- Status: Confirmed.

Evidence:

- All-project checking ignores individual project errors and has no in-flight coalescing at E:/Development/projects/apps/rcooler/Cairn/internal/updates/manager.go:131-143,472-487.
- Its completion depends on the update-specific event topic omitted by the shell bridge in BE-004.

Scenario and impact:

Repeated UI/API calls launch concurrent registry checks over the same projects, increasing rate-limit and database load. Partial failures disappear, and the caller has no durable success/partial/failure result to query.

Recommendation:

Use one in-flight check per provider/context with join/cancel semantics, collect per-project outcomes, persist a final job summary, and publish generic durable job completion.

Regression test:

Race multiple CheckAll calls and inject mixed project failures. Assert one underlying run, identical joined result, and a queryable partial-failure summary.

### BE-044 — Update health rollback uses historical restart count and does not observe no-healthcheck stability

- Severity: High
- Confidence: High
- Type: Correctness / rollback safety
- Status: Confirmed.

Evidence:

- Rollback treats an absolute restart count of at least two as failure at E:/Development/projects/apps/rcooler/Cairn/internal/updates/executor.go:548-600, specifically :575-576.
- A running container without a healthcheck returns success-with-warning immediately at E:/Development/projects/apps/rcooler/Cairn/internal/updates/executor.go:581-600.

Scenario and impact:

A previously stable container with two historical restarts is updated and immediately rolled back despite no new restart. Conversely, an updated process without a healthcheck is reported successful before the configured observation window can reveal crash-looping or early exit.

Recommendation:

Capture baseline/container identity and compare restart delta for the new instance. For no-healthcheck services, observe running state and restart delta for a real stabilization interval and report lower confidence explicitly.

Regression test:

Cover historical restarts with zero new delta, a new restart during watch, container replacement, healthy/unhealthy transitions, no-healthcheck crash loop, and cancellation.

### BE-045 — Update plans are not rebound to current Docker and source state at application

- Severity: High
- Confidence: High
- Type: Stale plan / time-of-check-time-of-use
- Status: Confirmed.

Evidence:

- Planning/execution paths at E:/Development/projects/apps/rcooler/Cairn/internal/updates/executor.go:241-335 and :362 onward do not revalidate the current container identity, image digest/reference, Dockerfile/Compose content, or provider/context fingerprint against the planned snapshot.
- Direct project/history scope weaknesses are described in BE-007.

Scenario and impact:

The container, Compose file, Dockerfile, image tag, context, or registry digest changes after planning. Applying the old plan updates or rolls back a different state than the user reviewed.

Recommendation:

Store immutable provider/context/project, container/image digest, source-file hashes, and expected current state in the plan. Reacquire/compare all values immediately before mutation and fail stale on any difference.

Regression test:

Mutate each bound value independently between plan and apply. Assert no Docker/Compose command runs and the user receives a stale-plan result.

### BE-046 — Update history finalization, shutdown, and plan expiry are not reliable

- Severity: High
- Confidence: High
- Type: Persistence / lifecycle
- Status: Confirmed.

Evidence:

- FinishHistory errors are discarded at E:/Development/projects/apps/rcooler/Cairn/internal/updates/executor.go:685-688,716-754.
- Update StopAll cancels but does not wait at E:/Development/projects/apps/rcooler/Cairn/internal/updates/manager.go:115-129.
- Finalization uses a detached bounded context that can race database shutdown at E:/Development/projects/apps/rcooler/Cairn/internal/updates/executor.go:757-762.
- The custom plan map prunes only an accessed expired plan and has no global janitor at E:/Development/projects/apps/rcooler/Cairn/internal/updates/executor.go:791-819.

Scenario and impact:

Docker mutation succeeds, but history remains “started” because final persistence fails. Shutdown closes the DB while finalizers run. Abandoned plans accumulate and remain in memory until directly accessed.

Recommendation:

Treat final history state as durable job state using retries/outbox semantics, surface persistence degradation separately from mutation outcome, wait for workers at shutdown, and periodically prune every expired plan.

Regression test:

Inject finalization failures and shutdown during completion; assert eventual correct terminal history or an explicit repair record. Create abandoned plans and verify fake-clock janitor cleanup.

### BE-047 — WSL starts duplicate full-inventory polling loops

- Severity: Medium
- Confidence: High
- Type: Performance / duplicate work
- Status: Confirmed.

Evidence:

- Process-backed Docker event handling falls back to objectPollLoop at E:/Development/projects/apps/rcooler/Cairn/internal/docker/objects.go:359-379.
- Runtime independently starts StartReconcileLoop for the client at E:/Development/projects/apps/rcooler/Cairn/internal/shell/runtime.go:157-159.
- StartReconcileLoop performs its own periodic scans at E:/Development/projects/apps/rcooler/Cairn/internal/docker/objects.go:320-343.

Scenario and impact:

On WSL/process-backed operation, two minute-based loops list containers/images/networks and write cache state, doubling baseline Docker/DB load and increasing race opportunities.

Recommendation:

Make one component the sole reconciliation scheduler and let transport type select events versus one poll fallback.

Regression test:

Use a counting process-backed fake for several intervals and assert exactly one scan cadence per object kind.

### BE-048 — Docker event bursts can launch unbounded overlapping reconciliations

- Severity: High
- Confidence: High
- Type: Concurrency / backpressure
- Status: Confirmed.

Evidence:

- Event flush starts a new goroutine for each reconcile kind at E:/Development/projects/apps/rcooler/Cairn/internal/docker/objects.go:497-520.
- There is no per-kind in-flight gate, generation ordering, or backlog bound.

Scenario and impact:

An event storm triggers repeated list/inspect operations while earlier 60-second calls remain blocked. Goroutines and database writes accumulate; an older scan can commit after a newer scan and regress cache state.

Recommendation:

Use per-kind dirty-bit coalescing/singleflight: at most one scan runs, events set dirty, and one follow-up scan runs if needed. Commit by generation and record backlog/duration.

Regression test:

Block reconciliation, emit thousands of events, then release. Assert bounded goroutines and at most one current plus one coalesced follow-up scan, with newest state winning.

### BE-049 — Docker bridge startup failure is silently hidden

- Severity: Medium
- Confidence: High
- Type: Reliability / degraded-mode observability
- Status: Confirmed.

Evidence:

- Docker bridge Start errors are logged only at Debug and provider rebind still succeeds at E:/Development/projects/apps/rcooler/Cairn/internal/shell/runtime.go:160-163.

Scenario and impact:

The named pipe cannot be created or the proxy fails to initialize. Cairn reports the provider active while Docker-CLI compatibility features depending on the bridge are absent, with no actionable status.

Recommendation:

Represent bridge health explicitly in provider/runtime status, emit a warning/notification, and fail rebind when a configured required feature depends on it.

Regression test:

Inject listen/start failure and assert visible degraded status, remediation detail, and required-mode failure.

### BE-050 — Shutdown has no shared deadline and can scale to minutes or hang

- Severity: High
- Confidence: High
- Type: Lifecycle / availability
- Status: Confirmed.

Evidence:

- App calls runtimeController.StopAll before closing resources at E:/Development/projects/apps/rcooler/Cairn/internal/shell/app.go:169-175.
- Log StopAll closes sessions sequentially at E:/Development/projects/apps/rcooler/Cairn/internal/logsvc/manager.go:598-617, and individual close may wait up to 15 seconds at :65-76.
- Metrics shutdown waits for workers without an explicit upper bound at E:/Development/projects/apps/rcooler/Cairn/internal/metrics/manager.go:59-90.
- Session counts are not globally bounded.
- Database Close errors are discarded at E:/Development/projects/apps/rcooler/Cairn/internal/shell/app.go:174.

Scenario and impact:

Many blocked log/metrics sessions make quit take N times an individual timeout or wait forever. Other detached jobs can race the eventual bus/database close.

Recommendation:

Pass one shutdown context/deadline to every manager, stop independent sessions concurrently with a cap, wait deterministically, force-close after the deadline, and surface resource-close errors.

Regression test:

Create many deliberately blocked sessions and jobs, trigger shutdown, and assert total duration remains within one global budget with every process/connection reaped.

### BE-051 — GPU usage remains stale after probe loss

- Severity: Medium
- Confidence: High
- Type: Metrics correctness
- Status: Confirmed.

Evidence:

- When the current GPU probe is unavailable, collection returns without clearing the prior gpuUsage at E:/Development/projects/apps/rcooler/Cairn/internal/metrics/manager.go:606-617.
- Sample construction continues copying retained values at E:/Development/projects/apps/rcooler/Cairn/internal/metrics/manager.go:464-478.

Scenario and impact:

GPU tooling/device becomes unavailable after a successful probe. Cairn displays the last utilization indefinitely as a current measurement.

Recommendation:

Clear values or emit explicit unavailable/stale state with last-success timestamp and probe error. Expire data after a short age.

Regression test:

Return one successful GPU sample followed by unavailable/error results and assert subsequent samples contain no current usage and expose staleness.

### BE-052 — Metrics retention errors are ignored and suppress retry for an hour

- Severity: Medium
- Confidence: High
- Type: Persistence / error handling
- Status: Confirmed.

Evidence:

- The manager advances lastRetain before calling retention and discards the returned error at E:/Development/projects/apps/rcooler/Cairn/internal/metrics/manager.go:547-560.

Scenario and impact:

A transient SQLite error prevents downsampling/deletion. Because the timestamp was already advanced, the manager does not retry until the next hour and emits no operational signal.

Recommendation:

Advance last-success time only after a successful transaction, log/metric failures, and retry with bounded exponential backoff.

Regression test:

Fail the first retention call and make the next succeed. Assert prompt retry, exactly one successful timestamp update, and visible failure telemetry.

### BE-053 — Metrics streaming sessions are unbounded

- Severity: Medium
- Confidence: High
- Type: Resource exhaustion
- Status: Confirmed.

Evidence:

- The metrics session map and creation path have no global, per-client, or per-scope cap at E:/Development/projects/apps/rcooler/Cairn/internal/metrics/manager.go:92-113.

Scenario and impact:

Repeated starts create unlimited session state/subscriptions and fan-out work, especially damaging through exposed server mode.

Recommendation:

Deduplicate equivalent subscriptions and cap per-client/global sessions. Expire disconnected/idle owners and charge history buffers against a byte budget.

Regression test:

Open beyond configured quotas from one and many clients, disconnect without Stop, and assert bounded session count and cleanup.

### BE-054 — Metrics interval and retention settings are saved but not applied

- Severity: Medium
- Confidence: High
- Type: Functional gap / configuration
- Status: Confirmed by repository usage search.

Evidence:

- metrics.retention_raw_minutes and metrics.sample_interval_seconds are defined/defaulted at E:/Development/projects/apps/rcooler/Cairn/internal/store/settings.go:44-45.
- Runtime constructs metrics behavior with hardcoded values at E:/Development/projects/apps/rcooler/Cairn/internal/shell/runtime.go:185-192,364-375.
- Retention windows are fixed at E:/Development/projects/apps/rcooler/Cairn/internal/store/metrics.go:156-165.
- No backend consumer of those setting keys was found outside settings/tests.

Scenario and impact:

Users change and save sampling or raw-retention values, but behavior does not change. The UI and persisted configuration make a false operational promise.

Recommendation:

Wire validated values into manager/repository configuration with safe live-reload semantics, or remove/disable the controls until supported.

Regression test:

Change each setting, restart/rebind, and assert observed sampling cadence and retention cutoff change exactly as configured.

### BE-055 — Windows-to-WSL Compose terminals use the host path-list separator

- Severity: Medium
- Confidence: High
- Type: Cross-platform functional correctness
- Status: Confirmed.

Evidence:

- Multi-file terminal setup joins COMPOSE_FILE with os.PathListSeparator at E:/Development/projects/apps/rcooler/Cairn/internal/terminal/manager.go:186-205.
- On Windows the separator is semicolon, while WSL/Linux Compose interprets colon.

Scenario and impact:

A multi-file WSL project terminal receives one malformed COMPOSE_FILE value, so Compose commands use the wrong or no configuration.

Recommendation:

Ask the provider for backend environment path-list semantics and use the backend separator after path mapping.

Regression test:

Create two-file projects for Windows/WSL, Linux, and macOS providers and assert exact COMPOSE_FILE values and Compose config resolution.

### BE-056 — Terminal capacity is checked after process creation and rejected Windows sessions can leak handles

- Severity: High
- Confidence: High
- Type: Resource lifecycle / concurrency
- Status: Confirmed.

Evidence:

- PTY/process creation precedes registration/capacity validation at E:/Development/projects/apps/rcooler/Cairn/internal/terminal/manager.go:120-146,229-240,410-422.
- Registration failure only calls Close at E:/Development/projects/apps/rcooler/Cairn/internal/terminal/manager.go:398-400.
- Windows Close terminates the process, while process-handle cleanup occurs through Wait at E:/Development/projects/apps/rcooler/Cairn/internal/terminal/pty_windows.go:92-130,155-190.

Scenario and impact:

Repeated opens above the maximum still spawn and terminate processes. The rejected path does not reliably wait/reap, leaking handles and imposing process-start cost.

Recommendation:

Atomically reserve a slot before spawning, roll back the reservation on all failures, and always Wait/reap after Close/termination.

Regression test:

Fill the limit and issue many concurrent excess opens on Windows. Assert no child starts for rejected requests and process/handle counts remain flat.

### BE-057 — Terminal and Linux provider settings are dead; default sudo flow cannot prompt

- Severity: Medium
- Confidence: High
- Type: Functional gap / installation reliability
- Status: Confirmed by usage search and command construction.

Evidence:

- terminal.default_shell, linux.socket_path, and linux.sudo_mode are stored/presented, but no runtime consumer of terminal.default_shell was found.
- Default provider construction passes empty LinuxNativeOptions at E:/Development/projects/apps/rcooler/Cairn/internal/shell/app.go:423.
- ApplySavedSettings handles WSL/Colima but not those Linux options at E:/Development/projects/apps/rcooler/Cairn/internal/providers/manager.go:518-545.
- Linux install plans issue plain sudo at E:/Development/projects/apps/rcooler/Cairn/internal/providers/linux_native.go:438-482.
- The command runner supplies no interactive stdin/TTY at E:/Development/projects/apps/rcooler/Cairn/internal/providers/exec.go:48-60.

Scenario and impact:

Saved shell/socket/sudo choices do nothing. A GUI-driven installation using the default ask mode fails unless sudo credentials are already cached or passwordless.

Recommendation:

Wire each setting end to end, implement pkexec/askpass or explicit rootless/group modes, and reject unsupported modes before planning. Remove UI controls until behavior exists.

Regression test:

For every setting and sudo mode, assert exact provider/terminal behavior. Simulate an uncached password requirement and verify a supported secure prompt or actionable preflight failure.

### BE-058 — RunImage leaves a stopped container after start failure

- Severity: Medium
- Confidence: High
- Type: Partial failure / cleanup
- Status: Confirmed.

Evidence:

- RunImage creates the container, then returns the start error without removing or returning the partial container at E:/Development/projects/apps/rcooler/Cairn/internal/docker/create.go:154-160.

Scenario and impact:

Start fails because of a port, mount, runtime, or hook issue. The stopped named container remains. A retry then fails on name conflict or accumulates orphaned objects.

Recommendation:

Remove the newly created container on start failure when safe, or return a typed partial-success result containing its ID and explicit cleanup/retry choices.

Regression test:

Inject start failure after create and assert automatic removal or a returned partial object with idempotent cleanup; retries must not accumulate containers.

### BE-059 — External mutations can succeed while Cairn reports failure because audit/cache persistence failed

- Severity: High
- Confidence: High
- Type: State consistency / idempotency
- Status: Confirmed.

Evidence:

- Volume/network creation mutates Docker before inspect/cache persistence at E:/Development/projects/apps/rcooler/Cairn/internal/docker/create.go:338-353,382-405.
- Multiple Docker service methods return audit errors after mutation at E:/Development/projects/apps/rcooler/Cairn/internal/services/services.go:301-323,398-412,487-510,631-648,741-758,785-802.
- Compose action paths do likewise at E:/Development/projects/apps/rcooler/Cairn/internal/services/project_service.go:442-468,746-793.
- Registry login/logout can return a post-mutation audit/finalization error at E:/Development/projects/apps/rcooler/Cairn/internal/registry/auth.go:43-85.

Scenario and impact:

Docker/Compose/login succeeds, then SQLite/audit/cache update fails. The UI receives an error and retries an already completed operation, causing duplicates, conflicts, unintended repeat actions, or misleading credentials state.

Recommendation:

Separate side-effect outcome from persistence warning in the result contract. Make operations idempotent, persist audit/cache repair through an outbox/reconciliation path, and never represent a confirmed external success as a simple failure.

Regression test:

Inject failure after each external mutation. Assert returned status identifies success plus degraded persistence, retries do not duplicate, and repair eventually converges.

### BE-060 — Renderer APIs can read or overwrite arbitrary host files

- Severity: High in server mode; Medium as desktop defense in depth
- Confidence: High
- Type: Filesystem trust boundary
- Status: Confirmed.

Evidence:

- SaveImage accepts a caller-provided path at E:/Development/projects/apps/rcooler/Cairn/internal/services/services.go:667-685 and writes/truncates it at E:/Development/projects/apps/rcooler/Cairn/internal/docker/create.go:186-232.
- LoadImage reads a caller-provided path at E:/Development/projects/apps/rcooler/Cairn/internal/docker/create.go:235-283.
- ExportLogs creates caller-provided directories/files with 0755/0666 modes at E:/Development/projects/apps/rcooler/Cairn/internal/logsvc/manager.go:133-160.

Scenario and impact:

A compromised renderer, malicious server-mode caller, or unsafe navigation supplies any process-accessible path, reads an arbitrary archive into Docker, or overwrites a user file. Sensitive log exports may also be broadly readable subject to umask.

Recommendation:

Use OS file-dialog capability tokens or explicitly approved roots, canonicalize and revalidate targets, require overwrite confirmation or O_EXCL, write atomically, and use 0700/0600 defaults for sensitive exports.

Regression test:

Attempt unapproved absolute, traversal, symlink, existing-target, device, and approved capability paths. Assert only the exact user-approved target is accessible and permissions are restrictive.

### BE-061 — Integer settings have no key-specific range validation

- Severity: Medium
- Confidence: High
- Type: Input validation / configuration safety
- Status: Confirmed.

Evidence:

- Generic integer setting parsing and writing accepts any representable integer at E:/Development/projects/apps/rcooler/Cairn/internal/store/settings.go:283-313,339-358.
- The values control resource sizes, intervals, retention, and Agent context behavior.

Scenario and impact:

A negative or huge value is persisted for Colima resources, sampling/update intervals, retention, or Agent limits. Downstream commands fail, loops run pathologically, or memory/resource use becomes excessive.

Recommendation:

Define a backend schema for every setting: type, minimum, maximum, enum, default, sensitivity, and restart requirement. Validate before persistence and repair/quarantine invalid existing rows during migration.

Regression test:

Table-test each key at min/max and just outside both bounds, including overflow strings and invalid pre-existing database values.

### BE-062 — Resolved build-argument values are persisted as ordinary metadata

- Severity: High
- Confidence: High
- Type: Secret handling / data minimization
- Status: Confirmed.

Evidence:

- Compose parser captures resolved build args at E:/Development/projects/apps/rcooler/Cairn/internal/compose/parse.go:324-329.
- Detector stores them in service metadata at E:/Development/projects/apps/rcooler/Cairn/internal/compose/detector.go:466-470.
- Lineage copies and persists them at E:/Development/projects/apps/rcooler/Cairn/internal/lineage/manager.go:151-198,409-425 and E:/Development/projects/apps/rcooler/Cairn/internal/store/lineage.go:18-38,192-201.
- Update history persists BuildArgs at E:/Development/projects/apps/rcooler/Cairn/internal/store/updates.go:319-341.

Scenario and impact:

A resolved build argument contains a package token, license key, proxy credential, or other secret. It becomes durable SQLite state, appears in histories/diagnostics, and propagates into database backups.

Recommendation:

Persist argument names and redacted/hash metadata only. Treat BuildKit secrets separately and never materialize them into general metadata. Migrate/scrub existing rows and minimize audit/error details.

Regression test:

Parse a project with secret-valued build args and inspect every service, lineage, update, audit, and exported record. Assert no raw or reversibly encoded value remains.

### BE-063 — Import review returns raw Compose and env files, including paths outside the project root

- Severity: High
- Confidence: High
- Type: Secret exposure / path containment
- Status: Confirmed.

Evidence:

- Review/import returns raw Compose and environment file content through helpers at E:/Development/projects/apps/rcooler/Cairn/internal/services/project_service.go:103-150,1428-1494.
- readImportEnvFiles accepts absolute paths and joins relative paths without enforcing containment at E:/Development/projects/apps/rcooler/Cairn/internal/services/project_service.go:1457-1494.
- Compose config output can contain interpolated secret values.

Scenario and impact:

An env-file entry references an absolute path or ../../ file. Cairn reads it and sends raw content to the renderer. Even normal .env and resolved Compose content can expose credentials more broadly than required for review.

Recommendation:

Return filenames and redacted structured previews by default. Require explicit user approval for reading external env paths, enforce canonical project containment otherwise, and never return fully resolved secret values unless a narrowly scoped reveal action is confirmed.

Regression test:

Use in-root, traversal, absolute, symlinked, and secret-bearing env/Compose inputs. Assert external reads are blocked/confirmed and default DTOs are redacted.

### BE-064 — Audit, notification, update-check, and update-history tables have no retention

- Severity: Medium
- Confidence: High
- Type: Persistence growth / privacy
- Status: Confirmed by repository-wide cleanup search.

Evidence:

- These durable tables are created in E:/Development/projects/apps/rcooler/Cairn/internal/store/migrations/0001_v1_schema.sql.
- Metrics has explicit retention, but no age/count cleanup path was found for audit_log, notifications, image_update_checks, or update_history.

Scenario and impact:

Long-lived installations accumulate operational history indefinitely, increasing database size, backup time, query cost, and exposure of historical sensitive-adjacent data.

Recommendation:

Define configurable age/count retention with safe defaults, required legal/audit exceptions, indexed batched deletion, and a measured VACUUM/checkpoint strategy.

Regression test:

Seed old/new rows beyond retention volume, run cleanup under cancellation and concurrent reads, and assert only eligible rows are removed with bounded lock duration.

### BE-065 — Compose reconciliation can race and commit an older snapshot last

- Severity: Medium
- Confidence: High
- Type: Concurrency / stale write
- Status: Confirmed design gap.

Evidence:

- Detector reconciliation has no per-scope mutex, singleflight, or commit generation at E:/Development/projects/apps/rcooler/Cairn/internal/compose/detector.go:43-90.
- Multiple Wails/action paths trigger detection at E:/Development/projects/apps/rcooler/Cairn/internal/services/project_service.go:241,299,462,787,840,1331.
- The shared runtime lock allows concurrent readers, so it does not serialize these scans.

Scenario and impact:

Two scans overlap. The newer scan observes state B and commits, then the slower older scan observes/commits stale state A and performs stale cleanup last.

Recommendation:

Serialize or coalesce detection per provider/context and attach a monotonic generation to commits. Perform upsert plus stale cleanup atomically only if the generation is still current.

Regression test:

Gate two scans so the older result completes last. Assert only the newer snapshot commits and stale cleanup cannot regress it.

### BE-066 — Windows Docker bridge lacks an explicit least-privilege pipe ACL and connection quotas

- Severity: High if the default ACL admits unintended principals; otherwise Medium hardening
- Confidence: Medium on default-ACL exploitability, High on missing explicit policy/quotas
- Type: Local security / denial of service
- Status: Risk requiring runtime ACL verification.

Evidence:

- Bridge creation calls winio.ListenPipe with an empty PipeConfig at E:/Development/projects/apps/rcooler/Cairn/internal/dockerbridge/bridge_windows.go:21-23.
- The bridge proxies Docker API-equivalent authority.
- Accept/handler creation is unbounded at E:/Development/projects/apps/rcooler/Cairn/internal/dockerbridge/manager.go:138-199.

Scenario and impact:

If the inherited/default DACL includes another local principal, that principal gains Docker and therefore host-equivalent control. Even an authorized local process can create many connections and exhaust bridge/backend resources.

Recommendation:

Set explicit SDDL limited to the current user and only required SYSTEM/administrator identities, verify connecting client identity, reject remote clients, and impose connection/request/byte/time quotas.

Regression test:

Inspect the real named-pipe ACL and attempt connection as current user, another standard user, anonymous/network, SYSTEM, and administrator. Flood accepted connections and assert bounded resource use.

### BE-067 — Provider command execution buffers unlimited output and returns raw output in errors

- Severity: Medium
- Confidence: High
- Type: Resource exhaustion / secret exposure
- Status: Confirmed.

Evidence:

- ExecRunner uses unbounded stdout and stderr buffers at E:/Development/projects/apps/rcooler/Cairn/internal/providers/exec.go:39-80.
- Command failure details include raw captured output at E:/Development/projects/apps/rcooler/Cairn/internal/providers/exec.go:110-123.

Scenario and impact:

A verbose or malicious command produces unlimited output and exhausts memory. Command output containing tokens, paths, environment values, or credential-helper messages is surfaced in diagnostics/UI/audit details.

Recommendation:

Capture a bounded head/tail with explicit truncation, stream safe progress separately, redact known secret arguments/output, and keep full logs only in a deliberately protected diagnostic sink.

Regression test:

Generate output far beyond the cap on both streams with embedded secrets. Assert bounded memory, useful head/tail diagnostics, and complete redaction.

### BE-068 — Stdio-backed Docker connections advertise deadlines that do nothing

- Severity: Medium
- Confidence: High
- Type: Transport correctness / cancellation
- Status: Confirmed.

Evidence:

- dialCommandStdio wraps a child process as net.Conn at E:/Development/projects/apps/rcooler/Cairn/internal/providers/stdio_conn.go:34-80.
- SetDeadline, SetReadDeadline, and SetWriteDeadline are no-ops at E:/Development/projects/apps/rcooler/Cairn/internal/providers/stdio_conn.go:147-157.

Scenario and impact:

Docker HTTP transport sets connection deadlines expecting stalled I/O to unblock, but a stuck WSL/stdio process ignores them. Calls can block beyond intended transport timeout until some broader cancellation closes the process.

Recommendation:

Implement deadline timers that close/cancel the relevant pipe/process operation, propagate timeout errors, and guarantee Wait/reaping on all close/error paths.

Regression test:

Block reads and writes, set near deadlines, and assert timely net.Error timeouts, process termination/reap, and no goroutine leak.

### BE-069 — Background bus subscriptions leak their cleanup goroutine after bus close

- Severity: Low
- Confidence: High
- Type: Goroutine lifecycle
- Status: Confirmed; current production impact appears limited.

Evidence:

- Subscribe starts cleanup that waits only on subscriber context at E:/Development/projects/apps/rcooler/Cairn/internal/bus/bus.go:86-108.
- A context.Background subscription therefore does not end its cleanup goroutine merely because Bus.Close was called.

Scenario and impact:

A component uses a non-canceling context and the bus closes. Channel state is closed, but the cleanup goroutine remains blocked for process lifetime.

Recommendation:

Select on both subscriber context and bus done, and make unsubscribe/close idempotent.

Regression test:

Subscribe with Background, close the bus, and assert the cleanup goroutine exits without an external context cancellation.

### BE-070 — Plan ID generation panics on entropy-source failure

- Severity: Low
- Confidence: High
- Type: Error handling / availability
- Status: Confirmed, very low-likelihood trigger.

Evidence:

- Random plan ID generation panics if crypto/rand fails at E:/Development/projects/apps/rcooler/Cairn/internal/security/containers.go:245-250.

Scenario and impact:

An OS entropy/provider failure during a plan request terminates the entire Cairn process instead of returning a recoverable operation error.

Recommendation:

Return an error through every plan-construction API and inject the random reader for deterministic failure tests.

Regression test:

Provide a failing reader and assert a typed internal error, no partial plan insertion, and continued process operation.

### BE-071 — Plan stores have inconsistent ownership, expiry, and shutdown semantics

- Severity: Medium
- Confidence: High
- Type: Lifecycle / architecture
- Status: Confirmed.

Evidence:

- Cairn creates five security plan stores at E:/Development/projects/apps/rcooler/Cairn/internal/shell/app.go:61-65.
- Shutdown closes only agentFilePlans at E:/Development/projects/apps/rcooler/Cairn/internal/shell/app.go:172.
- Provider, backup, and update implementations use differing claim/expiry/pruning behavior described in BE-013, BE-036, and BE-046.

Scenario and impact:

Janitor goroutines and stale plans have unclear owners; some stores atomically consume, others allow replay, and abandoned custom-map plans persist. Security guarantees vary by feature.

Recommendation:

Create one root-owned plan registry abstraction with atomic single-use claim, immutable request fingerprint, provider/context binding, consistent expiry, periodic pruning, capacity quotas, and Close/wait semantics.

Regression test:

Run a shared conformance suite against every plan type: concurrent apply, replay, mutation, wrong context/user, expiry without lookup, cancellation, and shutdown.

### BE-072 — Provider Stop bypasses the confirmation policy used for Restart

- Severity: Medium
- Confidence: High
- Type: Safety policy consistency
- Status: Confirmed.

Evidence:

- Provider stop is directly executable while restart uses a plan/confirmation path at E:/Development/projects/apps/rcooler/Cairn/internal/services/provider_service.go:213-250,311-329.

Scenario and impact:

A direct call stops the active backend and disrupts every workload, terminal, stream, and background operation without the explicit impact review required by restart.

Recommendation:

Apply a consistent plan/confirmation policy to stop, including affected workloads/jobs, or document and enforce a deliberate authorization distinction.

Regression test:

Assert Stop requires a current single-use plan bound to active provider/context and affected-resource summary, including direct API invocation.

### BE-073 — Server event broadcasting has unbounded client/fan-out work and unsynchronized map reads

- Severity: High in exposed server mode
- Confidence: High
- Type: Dependency concurrency / denial of service
- Status: Confirmed in the pinned Wails dependency.

Evidence:

- WebSocket clients are admitted without authentication, origin validation, or a count limit at C:/Users/roman/go/pkg/mod/github.com/wailsapp/wails/v3@v3.0.0-alpha2.103/pkg/application/websocket_server.go:60-105.
- register and unregister read len(b.clients) after releasing the mutex at C:/Users/roman/go/pkg/mod/github.com/wailsapp/wails/v3@v3.0.0-alpha2.103/pkg/application/websocket_server.go:97-120, racing concurrent map writes.
- Each event launches one goroutine per connected client at C:/Users/roman/go/pkg/mod/github.com/wailsapp/wails/v3@v3.0.0-alpha2.103/pkg/application/websocket_server.go:124-136, with no backpressure or per-client queue bound.

Scenario and impact:

An attacker opens many sockets and triggers or waits for high-rate events. Every event creates many concurrent writers/goroutines, allowing CPU/memory exhaustion. Concurrent connects/disconnects also produce a Go data race and may cause runtime map failures.

Recommendation:

Patch/upgrade Wails or replace this broadcaster: authenticate before upgrade, validate Origin, cap clients, maintain one bounded writer queue/goroutine per client, disconnect slow consumers, and keep all map access under lock.

Regression test:

Run the race detector while rapidly connecting/disconnecting and broadcasting. Load-test above the client/queue limits and assert bounded goroutines/memory and deterministic slow-client eviction.

### BE-074 — Public services lack a coherent request, session, and concurrency budget

- Severity: High in server mode; Medium for desktop robustness
- Confidence: High
- Type: Cross-cutting architecture / denial of service
- Status: Confirmed design gap.

Evidence:

- Independent unbounded paths include Wails ordinary body buffering (BE-002), log/metrics sessions and log bodies (BE-019 through BE-021 and BE-053), provider output (BE-067), registry bodies (BE-041), background install/check/reconcile work (BE-012, BE-013, BE-043, BE-048), and WebSocket fan-out (BE-073).
- Public service DTOs commonly accept arbitrary string/slice cardinality without a shared caller or aggregate budget.

Scenario and impact:

A buggy renderer or unauthenticated server client composes individually legal operations to exhaust memory, disk, child processes, registry requests, Docker connections, goroutines, or database write capacity.

Recommendation:

Add a central service budget layer with authenticated caller identity, maximum request bytes, aggregate string/slice limits, per-operation semaphores, per-client/global sessions and jobs, rate limits, disk quotas, and consistent retry-after/resource-exhausted errors. Keep stricter feature-specific limits beneath it.

Regression test:

Build an adversarial load suite that mixes all public APIs from one and many clients. Assert fixed upper bounds on memory, goroutines, open files/connections, child processes, disk growth, and queue latency while legitimate work remains responsive.

### BE-075 — Backup sidecar success is reported without checking Close or durable flush

- Severity: Medium
- Confidence: High
- Type: Crash consistency / file durability
- Status: Confirmed.

Evidence:

- writeSidecar creates the metadata file with O_EXCL and mode 0600, but defers and ignores Close, never calls Sync, and returns success immediately after JSON Encode at E:/Development/projects/apps/rcooler/Cairn/internal/backups/manager.go:1014-1027.

Scenario and impact:

A delayed filesystem error, removable/network storage failure, power loss, or abrupt process termination occurs after buffered writes. Cairn records a successful backup while its required metadata/checksum sidecar is incomplete or absent after restart, making restore validation fail.

Recommendation:

Write metadata to a same-directory temporary file, check Encode, Sync, and Close errors, atomically rename it, then fsync the directory. Apply an explicit durability policy to the archive before publishing both artifacts as complete.

Regression test:

Inject write, Sync, Close, and rename failures and simulate restart between each stage. Assert no backup is marked complete unless a valid archive and sidecar pair is durably published.

### BE-076 — The server container runs as root by default

- Severity: Medium independently; amplifies BE-001 to Critical
- Confidence: High
- Type: Container hardening / blast radius
- Status: Confirmed.

Evidence:

- The final distroless stage has no USER directive at E:/Development/projects/apps/rcooler/Cairn/build/docker/Dockerfile.server:34-48, so the server uses the image's default root user.
- The same image binds the unauthenticated service to all interfaces as described in BE-001.

Scenario and impact:

An attacker exploiting the exposed runtime API or another application defect receives root privileges inside the container and full root access to any mounted Docker socket, configuration, backup, or host paths. Root is not the cause of the remote API flaw, but materially increases its blast radius.

Recommendation:

Run as a fixed high-numbered non-root UID/GID, use a read-only root filesystem, drop all capabilities, enable no-new-privileges/seccomp, and mount only narrowly required writable paths. Do not mount a Docker socket into an unauthenticated server.

Regression test:

Inspect/run the production image and assert non-root identity, read-only filesystem, empty capability set, only required writable mounts, and startup failure when permissions are insufficient rather than silently broadening them.

### BE-077 — Registry login changes the caller's secret by trimming whitespace

- Severity: Medium
- Confidence: High
- Type: Credential correctness / input mutation
- Status: Confirmed.

Evidence:

- Login applies strings.TrimSpace to req.Secret before validation and before passing it to docker login at E:/Development/projects/apps/rcooler/Cairn/internal/registry/auth.go:21-40.
- Usernames and registry identifiers may reasonably be normalized, but passwords and access tokens are opaque byte sequences; leading and trailing whitespace can be valid credential material.

Scenario and impact:

A registry account has a password or token whose first or last character is whitespace. Cairn submits a different credential, reports authentication failure, and provides no indication that it mutated the value. Repeated attempts or password resets cannot make that account work without changing the credential itself.

Recommendation:

Treat secrets as opaque. Reject only a zero-length value, do not trim or Unicode-normalize it, and ensure diagnostics never interpolate it. If the UI wants to warn about accidental pasted whitespace, show a non-destructive warning and require the user to choose whether to remove it.

Regression test:

Pass secrets containing leading/trailing ASCII whitespace, tabs, newlines, non-breaking spaces, and ordinary internal whitespace through the service and fake Docker runner. Assert byte-for-byte preservation and complete redaction from errors/audits.

### BE-078 — Docker credential configuration updates are non-atomic read/modify/write transactions

- Severity: High
- Confidence: High
- Type: Credential integrity / lost update / crash consistency
- Status: Confirmed.

Evidence:

- ensureCredentialHelper reads the complete backend config, decodes and mutates it in memory, then rewrites the complete file at E:/Development/projects/apps/rcooler/Cairn/internal/registry/credentials.go:65-113.
- Linux and WSL write directly with cat > config.json, and Windows uses WriteAllText on the final path, at E:/Development/projects/apps/rcooler/Cairn/internal/registry/config.go:79-106,165-178.
- There is no interprocess lock, compare-and-swap check, temporary-file rename, Sync/Close durability check, or permission/ownership preservation around this shared Docker CLI file.

Scenario and impact:

Docker, another credential tool, or a second Cairn operation updates config.json between Cairn's read and write. Cairn overwrites that update, potentially deleting credentials, plugins, contexts, or unrelated custom fields. A cancellation, disk-full event, or process crash during the direct truncating write can leave invalid or empty JSON and break Docker authentication globally for the user.

Recommendation:

Avoid editing shared Docker configuration when a supported credential-helper command can perform the operation. If editing is unavoidable, serialize Cairn writers per backend, use an advisory/interprocess lock where available, re-read and compare the file identity/hash before commit, preserve metadata, write to a same-directory 0600 temporary file, check write/Sync/Close, atomically replace, and fsync the directory. On conflict, abort with recovery guidance instead of overwriting.

Regression test:

Race two independent writers and inject failure/cancellation at read, encode, partial write, Sync, Close, and rename. Assert unrelated keys and concurrent edits survive, the old file stays valid on every pre-commit failure, and the final file remains restrictively permissioned.

### BE-079 — Container terminals report the default root user as non-root

- Severity: Medium
- Confidence: High
- Type: Security UX / privilege-state correctness
- Status: Confirmed.

Evidence:

- OpenContainerTerminal asks containerUser to determine IsRoot at E:/Development/projects/apps/rcooler/Cairn/internal/terminal/manager.go:267-293.
- containerUser returns (false, "") immediately whenever the request does not explicitly name a user at E:/Development/projects/apps/rcooler/Cairn/internal/terminal/manager.go:505-509.
- Docker exec with no explicit user uses the image/container default, which commonly is UID 0.

Scenario and impact:

An operator opens a normal terminal into a root-default container. Cairn marks the session as non-root even though every command has root authority inside the container. Any warning, badge, policy, or future telemetry derived from IsRoot understates the privilege and can mislead the operator before destructive commands.

Recommendation:

When no user is requested, determine the effective exec user from container configuration or execute the existing id -u probe without a User override. Represent probe failure as unknown rather than false, and expose that three-state value through the UI.

Regression test:

Cover root-default, numeric/non-root default, named non-root, explicit root, explicit non-root, and failed-probe containers. Assert the displayed state is root, non-root, or unknown as appropriate.

### BE-080 — Terminal path-mapping errors fail open to paths in the wrong operating-system namespace

- Severity: Medium
- Confidence: High
- Type: Cross-provider correctness / error handling
- Status: Confirmed.

Evidence:

- Backend terminal working-directory mapping ignores every MapPathToBackend error and retains the original path at E:/Development/projects/apps/rcooler/Cairn/internal/terminal/manager.go:149-164.
- Project terminal mapping does the same for every Compose file and for the working directory at E:/Development/projects/apps/rcooler/Cairn/internal/terminal/manager.go:189-213.
- BE-055 separately covers the host path-list separator used after mapping.

Scenario and impact:

A Windows path cannot be mapped into WSL, or a backend path is malformed. Cairn still opens the backend shell with a Windows path in cwd/COMPOSE_FILE. The shell may start in a fallback directory and Compose may operate on no file or a coincidentally named different path, while the UI presents the intended project terminal.

Recommendation:

Fail closed when a nonempty requested path cannot be mapped, include actionable provider/path repair details, and never mix host and backend namespaces in a command environment. Return a typed mapping error and keep the terminal unopened.

Regression test:

Make mapping fail independently for cwd and each Compose file across native, WSL, and context providers. Assert no process starts and the user receives the exact failing path; also verify successful multi-file backend separators.

### BE-081 — Dockerfile lineage parsing can select the wrong base image and persist incorrect update advice

- Severity: Medium
- Confidence: High
- Type: Parser correctness / lineage integrity
- Status: Confirmed limitations with user-visible downstream effects.

Evidence:

- ParseDockerfile keeps one ARG map for the entire file and mutates it for every ARG at E:/Development/projects/apps/rcooler/Cairn/internal/lineage/dockerfile.go:46-64, rather than distinguishing global pre-FROM arguments from per-stage scope/redeclaration.
- logicalDockerfileLines hardcodes backslash continuation and treats backslash as its escape character at E:/Development/projects/apps/rcooler/Cairn/internal/lineage/dockerfile.go:131-200; it does not honor a # escape=` parser directive.
- resolveFinalStageIndex silently returns the last stage when a configured numeric or named target is invalid at E:/Development/projects/apps/rcooler/Cairn/internal/lineage/dockerfile.go:355-371. The tests explicitly encode this silent fallback at internal/lineage/dockerfile_test.go:35.
- Lineage output is persisted and used by update/rebuild workflows, so these are not presentation-only parse differences.

Scenario and impact:

A multi-stage Dockerfile redeclares an ARG, uses Windows backtick continuations, or specifies a misspelled build target. Cairn attributes the service to a different external base image, misses or invents an update, and can produce a rebuild recommendation that does not correspond to Docker's actual build graph.

Recommendation:

Use a Dockerfile parser whose syntax and scoping track the supported Docker/BuildKit grammar, including parser directives and stage-local ARG behavior. Treat an invalid configured target as an explicit unresolved/error state rather than silently selecting the last stage. Store parser version and uncertainty with lineage records.

Regression test:

Add conformance fixtures for global/stage ARG shadowing, ARG declared after FROM, # escape=`, quoted/escaped comments, heredocs, named/numeric invalid targets, BuildKit platform args, and multi-stage inheritance. Compare the selected stage/base against docker buildx build --print/metadata or another authoritative build parse.

### BE-082 — Successful Agent file edits can lose their audit record and failed edits are not audited

- Severity: Medium
- Confidence: High
- Type: Audit integrity / security observability
- Status: Confirmed.

Evidence:

- ApplyFileEdit records only the post-write success path and explicitly discards recordFileEditAudit's error at E:/Development/projects/apps/rcooler/Cairn/internal/services/agent_service.go:625-673.
- Validation, stale-hash, directory, and write errors return before an audit attempt in the same method.
- The audit helper can return a database failure at E:/Development/projects/apps/rcooler/Cairn/internal/services/agent_service.go:1959-1979, but the caller suppresses it.

Scenario and impact:

An Agent-generated edit changes a project file, but SQLite/audit insertion fails; the user receives success with no durable security trail. Repeated rejected or failing edit attempts also leave no evidence, weakening incident reconstruction and making the audit UI's implied completeness false.

Recommendation:

Define the audit contract explicitly. Record attempt, actor/session, target, plan fingerprint, outcome, and safe error code for every apply path. For a successful external file mutation followed by audit failure, return a typed partial-success result and persist a recoverable outbox record before mutation where possible; never pretend the file was not changed. Monitor and surface audit-pipeline degradation.

Regression test:

Inject audit failure before/after a successful write and trigger every validation/write failure. Assert every attempt has one durable outcome or a visible partial-success/outbox state, with no file content or secret leaked into the record.

### BE-083 — Registry cache, limiter, and circuit maps grow with attacker-controlled host keys

- Severity: Medium in desktop mode; High in exposed server mode
- Confidence: High
- Type: Memory retention / denial of service
- Status: Confirmed.

Evidence:

- Registry manager owns process-lifetime cache, registryGate, and circuit maps at E:/Development/projects/apps/rcooler/Cairn/internal/registry/types.go:117-129.
- registryLimiter creates and retains a channel for each normalized registry host without eviction at E:/Development/projects/apps/rcooler/Cairn/internal/registry/resolve.go:266-300.
- circuit entries are created for arbitrary failed hosts and removed only after a later success at E:/Development/projects/apps/rcooler/Cairn/internal/registry/resolve.go:302-342.
- Cache entries expire only when the exact key is looked up again; storeCache has no size cap or global sweep at E:/Development/projects/apps/rcooler/Cairn/internal/registry/resolve.go:243-264.

Scenario and impact:

A caller submits many syntactically valid unique registry/image hosts. Every request can retain limiter and failure state, and successful unique image references retain cache entries until revisited. An unauthenticated server client can turn this into steady process-memory growth; a buggy desktop integration can cause the same issue over a longer interval.

Recommendation:

Validate registries against configured/observed sources where possible, cap unique host and cache cardinality, use bounded LRU/TTL structures with periodic eviction, remove idle limiter entries safely, and charge all entries to the shared resource budget from BE-074.

Regression test:

Resolve tens of thousands of unique failing and successful hosts under a small configured cap. Assert map/cache cardinality and memory plateau, active limiters remain safe during eviction, and legitimate hot entries retain expected behavior.

---

## Recommended remediation sequence

### Immediate release blockers

1. BE-001, BE-002, and BE-073: disable server artifacts/tasks by default or add the complete secure-server boundary.
2. BE-003: prevent credential forwarding to untrusted auth realms.
3. BE-005 through BE-007 and BE-018: migrate identities and enforce provider/context scope before more persisted data accumulates.
4. BE-014 through BE-016: close operation-planning and mount-policy bypasses.
5. BE-017, BE-041, BE-042, BE-077, and BE-078: repair registry authentication, opaque-secret handling, response bounds, and shared Docker-config integrity.

### Next correctness and data-safety milestone

1. BE-008 through BE-011 and BE-043 through BE-046: make update/metrics data generation- and state-correct.
2. BE-030 through BE-036 and BE-075: make backups uniquely allocated, least-privileged, consistency-aware, cancelable, durable, and fidelity-tested.
3. BE-059, BE-078, and BE-082: introduce partial-success/idempotent mutation, atomic publication, and durable-audit contracts.
4. BE-062 through BE-064: minimize secrets and bound durable history.
5. BE-079 through BE-081: make terminal privilege/path state truthful and align lineage parsing with Dockerfile semantics.

### Lifecycle and scale milestone

1. BE-012, BE-013, BE-019 through BE-024, BE-047, BE-048, BE-050, BE-053, BE-056, BE-071, BE-074, and BE-083: root-own and budget every job, stream, and retained runtime map.
2. BE-025 through BE-029: make port-forward exposure explicit and bounded.
3. BE-037 through BE-040 and BE-082: constrain Agent destination, context, file operations, and audit outcomes.
4. BE-065 through BE-069: serialize reconciliation, secure/limit local bridges and process transports, and close lifecycle leaks.

## Cross-cutting regression suite to add

- Multi-provider and multi-context fixture suite with intentionally colliding project/object names and IDs.
- Plan conformance suite for immutable fingerprints, context/user binding, expiry, concurrent apply, replay, and stale source state.
- Fault-injection suite that fails audit/cache/database operations after each external Docker/Compose/registry mutation.
- Lifecycle suite that blocks every worker/stream, then cancels, rebinds provider, and shuts down under one deadline while checking processes, goroutines, sockets, and DB state.
- Resource-limit suite for large HTTP/network bodies, project files, process output, Docker logs, session counts, connection counts, plans, and event fan-out.
- Backup round-trip suite covering concurrent plans, in-use state, cancellation, damaged archives, and filesystem metadata.
- Server security suite covering unauthenticated access, authorization, CSRF, Host/Origin, CORS, WebSocket isolation, TLS/proxy assumptions, body limits, and multi-client quotas.
- Race-detector suite around Wails server broadcaster, Docker reconciliation, Compose scans, provider plan claims, and service rebind/shutdown.

## Positive observations

- ProjectService already contains an active provider/context validation pattern at E:/Development/projects/apps/rcooler/Cairn/internal/services/project_service.go:899-911; it can be generalized rather than invented.
- The Wails chunked transport path has explicit per-chunk and assembled-size caps; the ordinary path should adopt the same discipline.
- Agent HTTP responses are capped to 4 MiB at E:/Development/projects/apps/rcooler/Cairn/internal/services/agent_service.go:949-975, although callers should still detect overflow explicitly rather than treating truncated JSON as a generic decode error.
- Registry plaintext HTTP is limited to explicit/local cases, and the correction needed for BE-003 is trust binding for cross-origin auth rather than weakening TLS policy.
- Existing plan abstractions, audit repositories, and runtime controller provide useful foundations for consistent security and lifecycle behavior once their semantics are unified.

## Review caveats

- BE-033 is a fidelity risk until tested against the exact Alpine tar/tool version and each supported filesystem/backend.
- BE-034 is based on Docker client/daemon cancellation semantics and should be verified with an integration test that observes the helper container.
- BE-066 deliberately does not claim a proven unauthorized-user exploit: the missing explicit SDDL and quotas are confirmed, but the effective default named-pipe ACL must be inspected on supported Windows versions.
- Server-mode findings are conditional on building/deploying the server artifact. The repository explicitly supplies build and Docker run tasks for that mode, so it is part of the reviewed project surface.
- The suspended aggregate test process was an execution-host artifact. It should not be entered into Cairn's defect backlog.
