# Cairn full project review and dependency refresh

- Review date: 2026-08-17
- Branch: `codex/review-remediation`
- Base commit: `a223bfb462037969d42277ef20dcae28468e5f8e`
- Scope: current dirty remediation tree, including backend, frontend, persistence, Docker/provider boundaries, backup/update workflows, tests, build tooling, CI, release, and dependency state

## Outcome

The reviewed source tree is materially safer and more reproducible than the 2026-07-28 checkpoint. This pass closed the newly reachable Go standard-library vulnerabilities, removed all known npm audit findings, hardened confirmation plans against runtime and target changes, completed missing project-operation exclusion, corrected backup and update-health integrity gaps, and fixed the highest-impact frontend accessibility and variable-height table defects.

This is a source and automated-validation acceptance, not an unrestricted production-release certification. The Wails prerelease migration, signed installers, native clean-machine upgrade/uninstall tests, and native package smoke on all three operating systems remain separate release gates. Unsupported Wails server mode remains quarantined by policy checks.

## Review method

The review traced the following paths rather than relying only on linters:

- provider/context rebind through Docker confirmation-plan creation and apply;
- project and Compose mutation admission, cancellation, deletion, and stale-plan behavior;
- SQLite migration backup publication under a busy WAL reader;
- backup sidecar parsing and backup-artifact deletion under replacement, alias, and special-file attacks;
- update apply health across replicas, restart history, healthcheck/no-healthcheck services, and stability windows;
- frontend overlays, focus restoration, startup accessibility, event payloads, terminal events, toasts, and large tables;
- direct and transitive Go/npm dependency state, vulnerability scanners, compiler/tool pins, container downloads, GitHub Actions, and release publication controls;
- unit, race, browser, accessibility, production-build, task/policy, and configuration checks.

## Fixed findings

| ID | Severity | Area | Resolution |
| --- | --- | --- | --- |
| RR-2026-048 | Critical | Docker confirmation plans | Every Docker plan is bound to the active provider/context scope, apply rejects scope changes before audit or Docker side effects, and runtime detach invalidates outstanding stores. |
| RR-2026-049 | High | Docker target identity | Container operations retain inspected IDs; image/network deletion executes against inspected immutable IDs; push plans revalidate tag-to-image identity; locally resolved run-image plans reject tag retargeting. Volume plans retain their existing incarnation fingerprint. |
| RR-2026-050 | High | SQLite migration backups | `wal_checkpoint(FULL)` now consumes and validates SQLite's result row and refuses to publish a main-file-only backup when the checkpoint is busy, incomplete, or malformed. |
| RR-2026-051 | High | Project mutation lifecycle | Project lifecycle and Compose service mutations now share the scoped project-operation generation/gate, propagate its cancellable context, exclude concurrent work, and reject superseded plans. |
| RR-2026-052 | High | Project plan target binding | Project plan apply revalidates the reviewed project/Compose target and configuration input fingerprint before external side effects. |
| RR-2026-053 | High | Backup integrity | Restore sidecars are regular, stable files capped at 64 KiB with strict JSON EOF and bounded semantic fields; delete plans bind archive/metadata identities and preserve replacement files plus DB rows on conflict. |
| RR-2026-054 | Medium | Update health | Apply requires all expected replicas, measures restart deltas from pre-update baselines, and requires a continuous stability interval for healthchecked and no-healthcheck services. |
| RR-2026-055 | Medium | External file bounds | Dockerfile lineage reads are regular-file, identity-stable, and capped at 512 KiB instead of unbounded `ReadFile`. |
| RR-2026-056 | High | Overlay accessibility | The command palette cannot stack over another modal, uses the shared focus trap, stops Escape propagation, and restores focus to the invocation point. |
| RR-2026-057 | High | Startup accessibility/performance | The covered application is inert and hidden from assistive technology until the loader leaves. The presentation no longer acts as a multi-second backend gate and remains inside the first-meaningful-render budget. |
| RR-2026-058 | Medium | Toast accessibility | Actionable notifications persist until action/dismiss, every toast has an accessible dismiss control, and passive notifications keep bounded expiry behavior. |
| RR-2026-059 | High | Table reachability | Fixed-height virtualization is disabled when a visible column can wrap; all variable-height rows remain reachable while fixed-line datasets keep windowing. |
| RR-2026-060 | Medium | Runtime event boundary | Object payloads and array fields are checked before use; malformed log/stat/terminal payloads are ignored instead of reaching unsafe array/string operations. |
| RR-2026-061 | Critical | Dependency vulnerabilities | Go moved to 1.26.6, closing the reachable `net/url`, TLS, ASN.1, and `net/http` standard-library findings that the obsolete scanner missed. npm resolutions now contain patched DOMPurify, brace-expansion, and nanoid releases. |
| RR-2026-062 | High | CI/release supply chain | Actions and the cross-build base image are digest/SHA pinned, Zig archives are checksum-verified, release jobs rerun source gates, signing secrets are step-scoped, and release assets are no longer deleted/replaced. |
| RR-2026-063 | Medium | Tool reproducibility | govulncheck, golangci-lint, GoReleaser, and Garble use explicit current versions; local Task lint uses the same golangci-lint pin as hosted CI; PowerShell tasks select the correct executable per OS. |

## Second adversarial pass

The follow-up pass re-read the already-remediated tree instead of treating the first green run as final. It exercised delayed asynchronous workers, same-name Docker resource races, replacement files, malformed runtime events, hidden terminal ownership, and the secret-bearing release steps. The resulting additional fixes are:

| ID | Severity | Area | Resolution |
| --- | --- | --- | --- |
| RR-2026-064 | High | Update/rollback confirmation integrity | Update and rollback plans now bind the verified transitive Compose input closure as well as stored configuration. Apply revalidates it under the project-operation gate, and delayed workers revalidate again immediately before every pull, build, up, image retag, and rollback up mutation. Oversized health-log tails fail closed instead of silently hiding a fatal signature beyond the inspection prefix. The plan store prunes expired records and caps pending confirmations at 128. |
| RR-2026-065 | High | Backup restore/delete integrity | Restore plans bind runtime scope, held archive/metadata identities, archive checksum, and overwrite-target incarnation. New targets receive a cryptographically random per-plan ownership label, are inspected after create, and are restored into or cleaned up only while both that label and incarnation still match. Source artifacts are checked again immediately before the helper runs. Delete plans also retain both reviewed artifact handles: Windows deletes the exact held objects rather than resolving their paths again, while POSIX holds each inode through the final verified unlink. Plan admission is capped at 128 and every rejection, expiry, apply failure, worker exit, and shutdown releases retained handles/reservations. |
| RR-2026-066 | High | Runtime events and terminal lifecycle | Notification, provider/job/image, object, log, statistics, GPU, and terminal events now have bounded identifiers, strings, batches/arrays, finite numerics, and parseable timestamps before use. Terminal base64 is size/syntax checked before decoding; a visited terminal remains mounted while hidden, but provider/context rebinding disposes old xterm/listener ownership. Hidden-terminal focus is recovered and stale command-palette requests cannot win after close/reopen. |
| RR-2026-067 | Medium | Untrusted input allocation | GitHub release metadata, Ollama process data, native `nvidia-smi` output, Docker one-shot/streamed statistics, and Linux/macOS autostart files now have explicit byte/session limits, strict JSON EOF where applicable, and regular/stable file checks. |
| RR-2026-068 | High | Release signing and publication | Windows now signs and verifies the application before rebuilding, signing, and verifying the installer; the raw executable is not published. macOS uses an isolated temporary keychain, verifies signatures/notarization/stapling, and removes certificate/key material. Secret-bearing steps are separated from repository-controlled packaging and later third-party actions, and same-ref releases serialize instead of cancelling an active publication. |
| RR-2026-069 | Medium | Hosted/cross-build reproducibility | Hosted Node is exactly pinned through `.node-version`, NSIS is version-pinned, current GitHub Action majors remain immutable-SHA pinned, Docker Dependabot coverage was added, Debian smoke uses a digest, cross-build ownership repair reuses the already-required local cross image instead of mutable `alpine`, and NSIS assembly regenerates the WebView2 bootstrapper from the pinned Wails module. |

## Dependency refresh

### Go and build tools

| Dependency/tool | Previous | Reviewed value | Decision |
| --- | ---: | ---: | --- |
| Go toolchain | 1.26.5 | 1.26.6 | Updated; required for reachable standard-library fixes. |
| `docker/go-connections` | 0.7.0 | 0.8.1 | Updated. |
| `x/crypto` | 0.53.0 | 0.55.0 | Updated. |
| `x/sys` | 0.46.0 | 0.47.0 | Updated. |
| `modernc.org/sqlite` | 1.52.0 | 1.56.0 | Updated and migration/WAL tests rerun. |
| govulncheck | 1.3.0 | 1.7.0 | Updated; the old scanner produced a false-clean result for Go 1.26.5. |
| golangci-lint | 2.6.2 / local PATH | 2.12.2 | Updated and pinned consistently. |
| GoReleaser | floating `~> v2` | 2.17.1 | Pinned to the current published stable release. |
| Garble | 0.16.0 | 0.17.0 | Updated for Go 1.26 support and current fixes. |

The only outdated direct Go module is Wails v3. The current `v3.0.0-alpha2.103` backend embeds runtime `3.0.0-alpha.91`, while npm only published selected alpha builds and Cairn currently consumes `3.0.0-alpha.79`. The available latest line is `v3.0.0-beta.9`; moving to it changes backend, CLI/cache pins, generated bindings, frontend runtime, and platform packaging together. That migration is intentionally deferred to a dedicated three-OS branch rather than silently folded into this security patch.

### Frontend

Compatible updates include Recharts 3.10.1, Zustand 5.0.15, Axe Playwright 4.13.0, Playwright 1.62.1, Vite/plugin-react 8.2.1/6.0.5, ESLint 10.8.1, TypeScript-ESLint 8.67.0, Autoprefixer 10.5.4, PostCSS 8.5.26, Prettier 3.9.6, and Vitest 4.1.10. The lock now resolves DOMPurify 3.4.13, brace-expansion 5.0.9, and nanoid 3.3.18.

Remaining npm updates are major migrations: React/React DOM 19, Tailwind 4, Xterm 6, TypeScript 7, jsdom 30, Lucide 1, globals 17, jest-dom 7, and the coordinated Wails beta. They are not security blockers in the audited lock and should be handled with focused migration and native-WebView evidence.

## Validation

Final integrated results are recorded here after the last source edits:

- Frontend lint, Prettier, and TypeScript: passed.
- Frontend unit tests: 30 files / 357 tests passed.
- Production Vite build: passed; main chunk 510.27 kB (127.38 kB gzip), retaining the known 500 kB warning.
- Ladle component build: passed.
- Playwright release UI: 16 passed, 1 opt-in committed-golden comparison skipped; Axe route/modal scans, degraded mode, overflow reachability, and responsiveness budgets passed.
- npm audit at moderate threshold: zero vulnerabilities. Registry signatures verified for all 668 installed packages; 128 packages also supplied verified attestations.
- Backend unit tests: every platform-neutral package passed in the digest-pinned Go 1.26.6 image; the Windows Wails shell compiled and its complete test binary passed when launched directly. A production-tag Windows build also passed. On the final tree, WSL independently passed every internal package except the GTK/WebKit-dependent shell package.
- High-risk race suites passed for Docker plans/security/services/shell, Compose/project mutation admission, store migration backup, backups, and update health. The final WSL race run passed backups, updates, services, metrics, and Compose; the same-content backup replacement regression passed 100 consecutive Linux runs. Windows direct normal/race backup binaries and the deterministic exact-handle late-swap tests passed.
- `go vet -unsafeptr=false ./...`, `go mod verify`, `go mod tidy -diff`, and final Go formatting passed. WSL independently vetted the non-shell internal package set, and pinned golangci-lint 2.12.2 reported zero issues across the platform-neutral package set.
- govulncheck 1.7.0 found no blocking reachable vulnerability. It reported only the documented no-fix Moby advisories GO-2026-4883 and GO-2026-4887, which remain explicitly allowlisted by the repository wrapper.
- actionlint, static toolchain policy, Docker input policy, and server-mode containment: passed.

## Residual risks and next gates

1. Complete the coordinated Wails beta/runtime/CLI migration and run Windows, Linux, and macOS native package/launch smoke before changing the current prerelease pins.
2. Produce signed Windows/macOS artifacts and test clean install, upgrade, rollback, uninstall, shortcuts/icons, trust chain, and timestamping. No installer acceptance is claimed by this source review.
3. Split the 510.27 kB main frontend chunk and reduce `App.tsx` ownership concentration; browser performance passes today, but the main chunk remains above Vite's 500 kB warning threshold.
4. Add bounded durable terminal replay/query state for process restart or renderer failure. The visited terminal host now remains mounted across ordinary route navigation and is correctly replaced on provider/context rebind, but output is still process-memory state rather than durable history.
5. Replace the remaining generic runtime-event casts and persisted payloads with shared generated/handwritten schemas. The second pass validates and bounds the renderer's active notification, provider/job/image, object, log, statistics/GPU, and terminal event paths, but does not claim exhaustive schema coverage for all future events.
6. Coalesce or reject repeated full update checks while one is already active. Pending update/rollback and backup/restore plans are now globally pruned/capped, but repeated full checks can still create redundant jobs that serialize behind per-project locks.
7. Server mode is containment-only. It must remain excluded from stable outputs until it has an independently authenticated, authorized, TLS-protected, rate-limited API surface.
8. Upgrade the monolithic Docker module when an upstream release fixes GO-2026-4883 and GO-2026-4887, then remove their narrow scanner allowlist. No fixed module version exists in the reviewed dependency line.
9. POSIX has no portable atomic compare-and-unlink operation for a regular file. Backup-delete plans now keep the reviewed inode open, eliminating the observed inode-reuse failure, and recheck it immediately before unlink; a malicious same-user actor can still swap the public pathname in that final instruction window. Archive and metadata deletion is also not transactional: if the second artifact changes or fails after the first is removed, Cairn preserves the replacement and database row but cannot restore the first artifact. Windows uses exact-handle deletion and does not retarget a replacement pathname.
