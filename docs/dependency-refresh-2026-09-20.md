# Dependency refresh — 20 September 2026

Versions were checked against the npm registry, Go module proxy, official release notes, GitHub tag references, and Docker Registry manifest digests. The existing source migration to `src/` was preserved. This refresh updates the desktop runtime, every outdated direct application dependency, compatible runtime transitive dependencies, and build/release pins.

## Toolchains and delivery

| Component | Before | After |
| --- | --- | --- |
| Go | 1.27.0 | 1.27.1 |
| Node.js LTS | 24.20.0 | 24.21.0 |
| npm minimum and CI install | 11.19.0 | 12.0.2 |
| Wails Go module, JS runtime, CLI and cache keys | 3.0.0-beta.15 | 3.0.0-beta.23 |
| Garble | v0.17.1-0.20260828084131-08aa939fdc4b | v0.18.0 |
| govulncheck | v1.7.0 | v1.8.0 |
| GoReleaser | v2.18.0 | v2.18.2 |
| Anchore SBOM action | v0.24.0 | v0.24.2 |
| Cross-build Go image | golang:1.27.0-trixie | golang:1.27.1-trixie |
| Debian package-smoke image | stable-slim digest 1710bde3… | stable-slim digest 5bc3287b… |

The Go cross image is pinned to `sha256:433790e515d27dc6003e847e644cc0af956985cf315c1c58a3b73ee2dd305183`. The Debian smoke image is pinned to `sha256:5bc3287b25407c965a30f38e32603dc253a3869e1b12a21ac09bfc27fd8b13ce`. The SBOM action is pinned to its resolved commit `3ad7283483fc7af8ff2b4ea19663c2d5ca935e26`.

Go 1.27.1 includes runtime, compiler, SQL and HTTP fixes. Node 24.21 remains on the established LTS line; Node 26.9 is a Current release. npm 12 is explicitly installed in CI because Node 24.21 bundles npm 11.19.0. [Go release history](https://go.dev/doc/devel/release#go1.27.1), [Node 24.21 release](https://nodejs.org/en/blog/release/v24.21.0), [npm 12 changes](https://github.com/npm/cli/blob/latest/CHANGELOG.md).

GoReleaser 2.18.2 adds secret redaction fixes and release/archive correctness fixes. Garble now has a tagged Go 1.27-compatible version, so the temporary prerelease pin and its obsolete instructions were removed. [GoReleaser release](https://github.com/goreleaser/goreleaser/releases/tag/v2.18.2), [Garble module](https://pkg.go.dev/mvdan.cc/garble@v0.18.0).

Current pins were retained after checking: golangci-lint 2.13.2, Zig 0.16.0, macOS SDK 26.1, NSIS 3.12.0, checkout 7.0.1, setup-go/setup-node 7.0.0, cache 6.1.0, upload-artifact 7.0.1, download-artifact 8.0.1 and goreleaser-action 7.2.3. [NSIS release](https://nsis.sourceforge.io/Download), [Zig releases](https://ziglang.org/download/), [SDK releases](https://github.com/joseluisq/macosx-sdks/releases).

## Backend modules

| Module | Before | After |
| --- | --- | --- |
| github.com/moby/moby/api | v1.55.0 | v1.56.0 |
| github.com/moby/moby/client | v0.5.1 | v0.6.0 |
| github.com/wailsapp/wails/v3 | v3.0.0-beta.15 | v3.0.0-beta.23 |
| golang.org/x/crypto | v0.55.0 | v0.57.0 |
| golang.org/x/sys | v0.47.0 | v0.48.0 |
| modernc.org/sqlite | v1.57.0 | v1.59.0 |
| github.com/dustin/go-humanize | v1.0.1 | v1.1.0 |
| github.com/go-logr/logr | v1.4.3 | v1.4.4 |
| go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp | v0.69.0 | v0.71.0 |
| go.opentelemetry.io/otel, /metric, /trace | v1.44.0 | v1.46.0 |
| modernc.org/libc | v1.74.4 | v1.75.7 |
| modernc.org/memory | v1.11.0 | v1.12.1 |
| github.com/jchv/go-winloader | pseudo-version c1995be93bd1 | removed by Wails |

All other direct Go requirements are already at their latest compatible published versions. `go get -u ./...` refreshed the runtime package graph and `go mod tidy` removed unused requirements. Wails CLI-only module requirements are left under Wails' upstream module contract; forcing every unused tool dependency into the application's `go.mod` would not update an independently installed CLI.

Wails beta.23 incorporates cancellation of aborted Windows/Linux asset requests, a Linux/macOS native allocation leak fix, safer Windows request handling, and binding-generation fixes. This is an update within the beta line already used by Cairn. Wails v3 still has no final stable release. [Wails changelog](https://v3.wails.io/changelog/).

Moby client 0.6 follows API 1.56 and adds a customizable JSON message printer. Cairn's existing API negotiation remains enabled, and its code compiles without an API rewrite. [Moby release](https://github.com/moby/moby/releases/tag/client/v0.6.0).

SQLite 1.59 uses libc routines with improved Linux memory-operation performance and reduces allocation overhead for registered functions. **libc 1.75.7 is intentional:** SQLite explicitly requires the exact libc version used by its own module; blindly selecting current libc 1.77 would break that supported pairing. No custom page cache or database-format change was introduced. [SQLite version documentation and compatibility policy](https://pkg.go.dev/modernc.org/sqlite@v1.59.0).

## Frontend changes and features adopted

Type checking/build scripts now run the native TypeScript 7.0.2 compiler. The `typescript` import is aliased to the supported TypeScript 6.0.2 compatibility package so ESLint and Ladle can continue using the compiler API. This follows Microsoft's supported side-by-side setup. Explicit `vite/client`, `vitest/globals` and Node type inclusion accommodates the new default of excluding ambient type packages. Native type checking passes. [TypeScript 7 migration and aliases](https://devblogs.microsoft.com/typescript/announcing-typescript-7-0/).

React 19.3 supplies independent transition scheduling and fixes deferred-value, hidden Activity/store and effect-event issues. New ViewTransition and Fragment-ref APIs are available, but adding unrelated animations would not address the reported startup problem. [React 19.3](https://react.dev/blog/2026/09/09/react-19-3).

Vitest 5's stricter asynchronous assertion handling and default mock-history clearing are retained. The project already uses an explicit Vite dependency and the supported Node baseline. [Vitest 5 migration](https://vitest.dev/guide/migration/).

The lockfile also refreshes compatible transitive dependencies. This fixes the moderate `decode-uri-component` denial-of-service advisory through the updated `query-string` tree. Ladle's React-18-only `react-inspector` 6 dependency is overridden to version 9, which supports React 19; its three exports used by Ladle were checked. SWC and MSW install-script denials were updated to the resolved versions, preserving the existing policy. [decode-uri-component advisory](https://github.com/advisories/GHSA-vcc3-ghjq-m6fr), [react-inspector release](https://github.com/storybookjs/react-inspector/releases/tag/v9.0.0).

## Validation and remaining limits

- A clean `npm ci` in an isolated directory succeeded using Node 24.21.0 and npm 12.0.2; the dependency lock remained unchanged.
- `npm audit` reports **zero vulnerabilities** across 714 dependency records.
- `govulncheck` 1.8 reports **no vulnerabilities** for the Go application.
- `go mod verify` passed; every Go package compiled against the updated modules.
- The complete static/runtime toolchain contract passed using locally staged tools, without replacing the machine's global tool installations.
- A bug in the toolchain checker was fixed: when multiple copies of a tool exist on PATH, it now validates the first executable instead of passing an array of paths to the metadata reader.
- Full application test/build results and startup/WSL regression validation are recorded in the accompanying project analysis. Cross-platform release packages and the refreshed container images still require the existing CI matrix; downloading registry metadata does not establish cross-platform runtime correctness.

The deprecated `tsconfck` dependency remains owned by the current Ladle release. No registry-approved replacement is assumed. Node Current 26 is not required for this LTS application toolchain, and `@types/node` stays on the latest 24.x release to describe the actual runtime. A fresh `npm outdated` check reports only that intentional Node-types major difference. The TypeScript 6 compatibility API and SQLite libc pairing are deliberate compatibility requirements, not forgotten upgrades.

## Complete direct npm version table

Ranges are declarations in `package.json`; exact resolved versions are committed in `package-lock.json`.

| Package | Before | After |
| --- | --- | --- |
| @monaco-editor/react | ^4.7.0 | ^4.7.0 |
| @wailsio/runtime | 3.0.0-beta.15 | 3.0.0-beta.23 |
| @xterm/xterm | ^6.0.0 | ^6.0.0 |
| lucide-react | ^1.35.0 | ^1.47.0 |
| react | ^19.2.8 | ^19.3.0 |
| react-dom | ^19.2.8 | ^19.3.0 |
| recharts | ^3.10.1 | ^3.10.1 |
| zustand | ^5.0.15 | ^5.0.15 |
| @axe-core/playwright | ^4.13.0 | ^4.13.0 |
| @eslint/js | ^10.0.1 | ^10.0.1 |
| @ladle/react | ^5.1.1 | ^5.1.1 |
| @playwright/test | ^1.62.1 | ^1.63.0 |
| @tailwindcss/postcss | ^4.3.3 | ^4.3.3 |
| @testing-library/jest-dom | ^7.0.1 | ^7.0.1 |
| @testing-library/react | ^16.3.3 | ^16.3.3 |
| @types/react | ^19.2.18 | ^19.3.0 |
| @types/react-dom | ^19.2.5 | ^19.3.0 |
| @vitejs/plugin-react | ^6.1.1 | ^6.1.1 |
| eslint | ^10.9.1 | ^10.11.0 |
| eslint-plugin-react-hooks | ^7.1.1 | ^7.1.1 |
| globals | ^17.11.0 | ^17.12.0 |
| jsdom | ^30.0.1 | ^30.1.0 |
| pixelmatch | ^7.2.0 | ^7.2.0 |
| pngjs | ^7.0.0 | ^7.0.0 |
| postcss | ^8.5.26 | ^8.5.28 |
| prettier | ^3.9.6 | ^3.9.8 |
| tailwindcss | ^4.3.3 | ^4.3.3 |
| typescript | ^5.9.3 | npm:@typescript/typescript6@^6.0.2 |
| typescript-eslint | ^8.68.0 | ^8.70.0 |
| vite | ^8.2.2 | ^8.3.0 |
| vitest | ^4.1.11 | ^5.0.1 |
| @typescript/native | added | npm:typescript@^7.0.2 |
| @types/node | added | ^24.13.6 |
