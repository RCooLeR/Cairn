# Release Process

Cairn uses native Wails packaging on GitHub Actions and GoReleaser for the final GitHub Release.

## Why Native Packaging

Cairn is a desktop GUI app. Windows, Linux, and macOS packages are built on matching GitHub-hosted runners so platform packaging tools are available:

- Windows: NSIS installer.
- Linux: AppImage and Debian package.
- macOS: app bundle and DMG.

GoReleaser does not cross-build the Wails GUI app. Instead, `.goreleaser.yaml` publishes the already-built native artifacts, generates release notes, and attaches a SHA-256 checksum file.

## CI

The `CI` workflow runs on pushes and pull requests:

- Frontend install, audit, lint, format check, typecheck, tests, story catalog build, and production build.
- Generated Wails binding check.
- Go tests, vet, vulnerability scan, and golangci-lint.
- Linux Docker integration tests.
- Native package smoke builds on Windows, Linux, and macOS.
- GoReleaser configuration validation on Linux.

## Tagged Release

Push a semver tag to create a release:

```powershell
git tag -a v1.0.0 -m "Cairn v1.0.0"
git push origin v1.0.0
```

The `Release Packages` workflow will:

1. Build native packages on Windows, Linux, and macOS.
2. Sign Windows and macOS artifacts when signing secrets are configured.
3. Rename unsigned Windows and macOS artifacts with an `-unsigned` suffix when signing secrets are missing.
4. Generate SBOM artifacts.
5. Run package validation smoke tests.
6. Publish the GitHub Release with GoReleaser.
7. Attach `cairn_<version>_checksums.txt`.

## Manual Dispatch

The release workflow can also be run manually with a version input. Manual dispatch packages artifacts but does not publish a GitHub Release; publishing is reserved for pushed version tags.

## Signing Secrets

Windows signing uses:

- `WINDOWS_SIGN_PFX_BASE64`
- `WINDOWS_SIGN_PFX_PASSWORD`

macOS signing and notarization use:

- `MACOS_CERTIFICATE_BASE64`
- `MACOS_CERTIFICATE_PASSWORD`
- `MACOS_KEYCHAIN_PASSWORD`
- `MACOS_SIGN_IDENTITY`
- `MACOS_NOTARY_KEY`
- `MACOS_NOTARY_KEY_ID`
- `MACOS_NOTARY_ISSUER_ID`

Unsigned releases are allowed for early public testing, but the artifact names clearly show `-unsigned`.

## Local Checks

Run these before tagging when possible:

```powershell
task test
task windows:package
go run github.com/goreleaser/goreleaser/v2@latest check
```

On Linux and macOS, use `task linux:package` and `task darwin:package` respectively.
