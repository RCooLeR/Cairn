# Build tool audit — 6 October 2026

Published stable releases, action tag commits, and Docker image manifests were checked against their upstream registries. Node is upgraded to the latest Current release as part of the request to update everything; retaining the previous LTS major is not a constraint for this refresh.

| Component | Previous pin | Updated pin |
| --- | --- | --- |
| Node.js | 24.21.0 LTS | 26.10.0 Current |
| npm | 12.0.2 | 12.2.0 |
| golangci-lint | 2.13.2 | 2.14.0 |
| Zig cross compiler | 0.16.0 | 0.17.0 |
| NSIS / Chocolatey package | 3.12.0 | 3.13.0 |
| Go cross image | `golang:1.27.1-trixie@sha256:433790e515d27dc6003e847e644cc0af956985cf315c1c58a3b73ee2dd305183` | `golang:1.27.1-trixie@sha256:0982f930de50a4f1a2b4453d51651f0031082ef2e3a25deb3c763fc39a1094a0` |
| Debian package smoke image | `debian:stable-slim@sha256:5bc3287b25407c965a30f38e32603dc253a3869e1b12a21ac09bfc27fd8b13ce` | `debian:stable-slim@sha256:eb593cf2c358cacef45ca0a424bbc7d30cfa3466265fc2662b9466a0ca6ba1c5` |

Go 1.27.1 remains the latest released Go patch. Node 26.10.0 is the latest Current release, while 24.21.0 remains the latest LTS release. [Go downloads](https://go.dev/dl/?mode=json), [Node releases](https://nodejs.org/dist/index.json).

golangci-lint 2.14.0 supports Go 1.27 and fixes reloading cached analysis facts. Its module requires Go 1.26 or newer, so the project's Go 1.27.1 toolchain satisfies it. No linter configuration changes were required. [Changelog](https://golangci-lint.run/docs/product/changelog/#2140), [module requirements](https://github.com/golangci/golangci-lint/blob/v2.14.0/go.mod).

Zig 0.17.0 upgrades `zig cc` to Clang 22.1.8. The existing Darwin deployment targets and Windows GNU targets remain accepted. Both Linux architecture downloads retain SHA-256 verification using the hashes published in Zig's download index. NSIS 3.13 is released, and its 3.13.0 Chocolatey package is available for the exact CI install command. [Zig download metadata](https://ziglang.org/download/index.json), [Zig release notes](https://ziglang.org/download/0.17.0/release-notes.html), [NSIS release](https://nsis.sourceforge.io/Download), [Chocolatey package](https://community.chocolatey.org/packages/nsis/3.13.0).

Both refreshed Docker references keep their existing tags, and both manifest lists contain Linux amd64 and arm64 images. [Go image metadata](https://hub.docker.com/v2/repositories/library/golang/tags/1.27.1-trixie), [Debian image metadata](https://hub.docker.com/v2/repositories/library/debian/tags/stable-slim).

The other audited pins are current: GoReleaser 2.18.2, Garble 0.18.0, govulncheck 1.8.0, macOS SDK 26.1, and linuxdeploy `1-alpha-20251107-1`. All eight action pins resolve to the latest release's commit: checkout 7.0.1, setup-go 7.0.0, setup-node 7.0.0, cache 6.1.0, upload-artifact 7.0.1, download-artifact 8.0.1, sbom-action 0.24.3, and goreleaser-action 7.2.3.

## Validation

- golangci-lint 2.14.0 passed against the repository with zero issues.
- The checksum-verified Windows Zig 0.17.0 compiler produced C objects for `aarch64-macos.13.3-none`, `x86_64-macos.13.3-none`, `x86_64-windows-gnu`, and `aarch64-windows-gnu`, including the Darwin sanitizer flags used by the wrappers.
- These object compilations validate compiler target and flag compatibility. They do not validate linking the entire Wails application against the macOS SDK. The updated cross image was not built locally; no WSL distribution or Docker daemon was started for this audit.

## Branch preservation

The old `origin/codex/ci-quality-20260930` tip `dd332eb00397fed1907c713698e3f6f535548d30` and PR #48's squash merge `4d94b0e1e5a6a1afb5cf5e12ce93986f753ac491` have the identical tree `fec669137cd525fb14acd07c8f7a64dd327428c4`. Their tree diff is empty, and the squash merge is an ancestor of master. The branch contains no unpreserved changes and can be removed during the requested cleanup. [Merged PR #48](https://github.com/RCooLeR/Cairn/pull/48).
