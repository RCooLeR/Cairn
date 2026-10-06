# Ladle story discovery

This local package replaces only Ladle 5.1.1's `globby` dependency. A top-level
development dependency anchors the portable file path, and the Ladle-scoped
override refers to it through `$globby`. All six Ladle
call sites use the async `globby(patterns)` export without options. The adapter
uses Node 24.21's stable filesystem glob API, returns files with forward-slash
paths, follows symbolic links, supports exclusions and explicit dot paths, and
expands literal directories. Unsupported options fail explicitly.

The scoped override removes Ladle's `globby` → `fast-glob` → `micromatch` →
`braces` dependency chain. As of 2026-10-06, the published `braces` package has no
patched release for [GHSA-vfj7-8cjw-p6xm](https://github.com/advisories/GHSA-vfj7-8cjw-p6xm).
The upstream maintainer disputes the advisory, but the full npm audit gate remains
enabled. This adapter contains no vendored braces code or fabricated package
version.

Fixture expectations were compared with `globby@14.1.0`, including relative and
absolute exclusion behavior. `npm test` runs the contract checks before Vitest,
resolving the installed adapter from Ladle so broken dependency links also fail.
Windows tests use directory junctions; file symlinks are also checked on hosts
that permit their creation. Run `npm run ladle:build` when changing this adapter
or upgrading Ladle. Remove the override when Ladle has an audited dependency
chain or the upstream advisory is authoritatively corrected.
