# Updates, backups, registry

Coverage: all non-test files in `internal/updates`, `internal/backups`, `internal/registry` were read in
full by a dedicated reviewer (after the first attempt hit a session limit), with callers in
`internal/services`/`internal/shell` consulted and the two riskiest hypotheses **verified empirically**
on this machine (wsl.exe argument-relay behavior; `docker volume create` idempotency). The
apply/rollback core of `executor.go` was additionally reviewed directly during report assembly.

Overall: plan/apply flows are carefully built (one-shot plans with TTL, HTTP bodies closed, client
timeouts, guarded maps). The three HIGHs below are behavioral, not structural: a digest comparison that
is wrong for multi-arch images, a WSL shell-expansion break, and a TOCTOU that can destroy volume data.

---

<a name="b-1"></a>
## B-1 · HIGH — multi-arch images always report a false "update available" (index digest vs platform digest)

**Files:** `internal/updates/manager.go:610-625` (also `:344-372, :432-437`),
`internal/registry/resolve.go:99-113`

```go
// remote side: platform-resolved digest
if isIndexMediaType(mediaType) {
	result.IndexDigest = digest
	manifestDigest, err := m.resolvePlatformManifest(...)
	result.ManifestDigest = manifestDigest      // ← child manifest digest returned
}
// local side: docker inspect RepoDigests → the INDEX digest for multi-arch pulls
if digestsEqual(localDigest, remoteDigest) { record.Status = models.UpdateStatusUpToDate }
```

When Docker pulls a multi-arch tag (`nginx:latest`, `postgres:16` — nearly every official image),
`RepoDigests` records the **manifest-list (index) digest**, while `ResolveDigest` returns the
**platform child manifest digest** whenever the registry serves an index. They are never equal, so
`checkServiceImage` reports an update for every up-to-date multi-arch image — and after the user
applies it (`compose pull` + `up`), the local digest is *still* an index digest, so the same false
update reappears immediately: a never-clearing update loop that triggers pointless container
recreations. `DigestResult.IndexDigest` is populated but consumed nowhere (grep-verified). Tests miss
it because stubs return the same value on both sides and the integration test pushes a single-arch
image.

**Fix:** return both digests from `remoteDigest` and treat the image as up to date when the local
digest matches **either** `ManifestDigest` **or** `IndexDigest` (the Watchtower/Diun approach); or
compare against the top-level `Docker-Content-Digest` and only platform-resolve for pinned refs.

---

<a name="b-2"></a>
## B-2 · HIGH — volume restore is always broken on the WSL provider (`$` expanded by the intermediate shell)

**Files:** `internal/backups/manager.go:839-865`, pass-through
`internal/providers/windows_wsl.go:706-715`

```go
const restoreHelperScript = `set -eu
archive=$1
stash_name=".cairn-restore-old-$$"
stash="/restore/$stash_name"
mkdir "$stash"
...`
// reaches WSL as: wsl.exe -d <distro> -- docker run ... sh -c <script> cairn-restore /backup/x.tar.gz
```

`RunDocker` relays raw args through `wsl.exe`, whose default-shell rejoin **expands `$` in the script
before docker runs** — the same mechanism verified with probes in
[01-providers-wsl.md](01-providers-wsl.md) (unescaped `$` expands; that's why
`escapeWSLCommandDollars` exists). Verified again here:
`wsl.exe -d Ubuntu -- printf '[%s]' 'stash="/restore/$stash_name"'` yields `stash="/restore/"`. The
delivered script executes `mkdir ""`, fails under `set -e`, and **every restore job on the
Windows-WSL provider fails** with an opaque "Docker helper command failed". It fails *safely* (before
touching data), but the feature is unusable on the flagship provider. The WSL integration test that
would catch it is double-gated (`wslintegration` tag + env var + a `cairn-dev` distro) and evidently
hasn't run against this script.

**Fix:** escape at the provider boundary — apply `escapeWSLCommandDollars` to args for the WSL
provider only (a blanket escape would corrupt the script on Linux/macOS providers, which exec without
an intermediate shell), or route helper scripts through the properly-quoted `sh -lc` path that
`RunComposeEnv` already uses.

---

<a name="b-3"></a>
## B-3 · HIGH — non-overwrite restore can silently destroy an existing volume's data (TOCTOU)

**File:** `internal/backups/manager.go:283-287, 522-531, 856-859`

Volume existence is validated only in `PlanRestoreVolume`; the plan lives up to 10 minutes
(`DefaultPlanTTL`), and at apply time `docker volume create <name>` **succeeds silently for an existing
volume** (verified: second create exits 0). If the target volume comes into existence inside the plan
window — most plausibly `docker compose up` auto-creating the project's named volume, or a second
restore — `ApplyRestore` proceeds: the helper script stashes the volume's current contents and, on
successful extraction, `rm -rf "$stash"` **permanently deletes them**. Real data is destroyed under a
plan presented as "Creates a new volume" with ordinary confirmation, bypassing the `RiskDangerous` +
typed-name gate the overwrite path requires for exactly this effect.

**Fix:** re-check existence in `runRestore` at apply time and fail non-overwrite with Conflict if the
volume now exists; and/or make the helper script refuse to stash-and-delete when `/restore` is
non-empty in create mode.

---

## B-4 · MEDIUM — interrupted restore leaves a stash that permanently blocks later restores

**File:** `internal/backups/manager.go:852-865`

The helper script has no `trap` and no leftover-stash recovery. If the container dies mid-extraction
(engine restart, OOM, power loss), the volume holds a partial extraction plus the original data hidden
in `/restore/.cairn-restore-old-<pid>` — and since `sh` is PID 1, the stash name is effectively
constant, so any later restore fails at `mkdir "$stash"` ("File exists") with no in-app cleanup path.
The stash dot-dir is also silently included in subsequent backups. **Fix:** detect/recover a
pre-existing stash at script start and add `trap`-based restoration on abnormal exit.

## B-5 · MEDIUM — `Login` deletes the working inline credential before the new login is validated

**File:** `internal/registry/auth.go:35-49` (with `credentials.go:87-100, 167-201`)

In the default `docker_helper` mode, `prepareRegistryLoginStorage` rewrites `config.json` — removing
the registry's inline `auths` entry (the currently *working* credential) — **before** `docker login`
runs. If the new login fails (typo, hiccup, non-functional helper), the old credential is already
destroyed; private pulls start failing, and a show-once token cannot be restored. **Fix:** login and
verify first, then migrate storage — or snapshot the removed entry and restore it on failure.

## B-6 · MEDIUM — background update scheduler dies permanently on a transient error or a disable→re-enable cycle

**File:** `internal/updates/manager.go:487-509, 684-693`

`runScheduler` re-reads the interval each iteration, but both "interval set to 0 (manual)" and *one
transient `GetInt` error* (sqlite busy) hit `return`, killing the goroutine for the runtime's life —
`startOnce` guarantees it never restarts. A user who switches to manual checks and later re-enables a
6-hour interval silently never gets background checks again until app restart. **Fix:** on
`!enabled`, sleep and re-evaluate instead of returning; treat read errors as "keep previous interval".

## B-7 · MEDIUM — update bookkeeping runs on the cancelled job context

**File:** `internal/updates/executor.go:707-744` (cancellation at `internal/shell/runtime.go:304-313`)

On quit/provider-switch, `runtimeHandles.stop()` cancels the job context; the in-flight update fails
with `context.Canceled` and then **all** terminal bookkeeping — `FinishHistory`, `recordAudit`,
`insertNotification`, and any `RollbackOnFailure` attempt — executes against the dead context, every
write failing silently (`_ =`). `update_history` rows stay `result='started'` forever (phantom
in-progress update in the UI) and the audit trail never completes. Same pattern in
`runManualRollback`. **Fix:** do terminal bookkeeping with `context.WithoutCancel` (short timeout) and
log persistence errors.

---

## B-8 · LOW — backup deletion removes the DB record before the files

**File:** `internal/backups/manager.go:435-457` — on Windows a sharing violation (AV scan, open
archive) leaves multi-GB artifacts on disk with the record already gone; retry is impossible ("Backup
was not found"). Delete files first, or mark pending-delete and retry.

## B-9 · LOW — Apply endpoints consume the one-shot plan before validating it

**Files:** `internal/updates/executor.go:116-135, 212-225`; `internal/backups/manager.go:223-236,
342-355, 398-407` — passing the wrong plan type returns Conflict *and* destroys the plan, so the
correct follow-up call fails with "plan expired" and the user must re-plan. Validate before removal
(or re-save on validation failure).

## B-10 · LOW — rollback erases the history row's `new_image_id`/`new_digest`

**File:** `internal/updates/executor.go:672-679` with `internal/store/updates.go:354-368` —
`FinishHistory` unconditionally overwrites `new_*` columns with NULL from `runManualRollback`'s sparse
record, deleting exactly the "what did we update to" evidence a user investigating a bad update needs.
Use a dedicated rollback-status update, or populate the finish record from the existing row.

## B-11 · LOW — restore creates the target volume before re-verifying the archive checksum

**File:** `internal/backups/manager.go:522-531` — a checksum failure or helper-pull failure leaves a
stray empty volume that then blocks re-planning ("Target volume already exists"), pushing the user
toward the dangerous overwrite path. Verify first; delete the just-created volume on pre-extraction
failure.

## B-12 · LOW — registry config access assumes `sh` on the Windows host for existing-context providers

**File:** `internal/registry/config.go:148-178` with `internal/providers/existing_context.go:54-56` —
the `powershell.exe` branch is dead code (only the WSL provider reports `PlatformWindows`, and it is
matched earlier); an existing-context provider on Windows (e.g. Docker Desktop's `desktop-linux`) runs
`sh -lc ...` on the host, which fails without Git-for-Windows — breaking `Login`,
`ListRegistryAccounts`, `TestAuth`, and silently downgrading `ResolveDigest` to anonymous (private
images misreport as `AuthRequired`). Route by host `runtime.GOOS`; when reviving the PowerShell branch
note `Set-Content -Encoding UTF8` writes a BOM under PS 5.1 that docker's JSON parser rejects.

## U-1 · LOW — automatic-rollback compose output published with an empty job ID

**File:** `internal/updates/executor.go:703` — `rollbackHistory` hardcodes
`m.publishComposeOutput("", result)`, so the compose output of the emergency rollback after a failed
update never appears in the job's output panel. Thread `jobID` through.

## U-2 · INFO — rollback semantics worth documenting

`rollbackHistory` re-points the moving tag at the old image ID and `compose up`s the service — correct,
but it leaves the local tag diverged from the registry until the next explicit pull. The `GetImage`
guard correctly degrades to `manual-needed` with a repair hint when the old image was pruned.

## Verified non-findings

- Spaces in backup paths survive the WSL relay (wsl.exe passes the quoted remainder intact) — verified.
- The PowerShell/BOM config-write branch is currently unreachable (see B-12) — the BOM bug only matters
  if that branch is revived.
