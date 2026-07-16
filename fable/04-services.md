# Services layer (Wails-bound services, agent, CLI shim, project lifecycle)

Files: `internal/services/*` — full-file review performed by a dedicated review agent; the two most
severe findings (S-1, S-2) were **independently re-verified against the source** during report
assembly. Architecture context: service structs guard a swappable runtime behind `RuntimeMu`
(read-locked per call, write-locked by `shell/runtime.go` during rebind); destructive operations are
plan-gated through `internal/security` stores. Locking discipline and plan-gating are consistently
good; the defects cluster in the Windows PATH shim, agent redaction/limits, and one background
goroutine.

---

<a name="s-1"></a>
## S-1 · HIGH — PATH shim rewrites `HKCU\Environment\Path` as `REG_SZ`, breaking `%VAR%` entries

**File:** `internal/services/docker_cli_shim_windows.go:166-186`

```go
current, _, err := key.GetStringValue("Path")     // ← value TYPE discarded
...
next := dir
if strings.TrimSpace(current) != "" {
	next += ";" + current
}
return key.SetStringValue("Path", next)           // ← always writes REG_SZ
```

Stock Windows stores the user `Path` as `REG_EXPAND_SZ` (its default content is the unexpanded
`%USERPROFILE%\AppData\Local\Microsoft\WindowsApps`). `GetStringValue` returns the raw (unexpanded)
string and its type; the type is discarded and the merged value is written back with
`SetStringValue` → `REG_SZ`. From then on Windows stops expanding every `%VAR%` entry in the user PATH,
so those directories silently vanish from PATH in all new sessions (WindowsApps aliases, `%USERPROFILE%`
-relative tool dirs, etc.).

This is **not opt-in**: `maybeAutoInstallWindowsDockerCLIShim` (`internal/shell/app.go:240-252`) runs at
every startup when the WSL provider is active and `docker` isn't on PATH — the most common
Cairn-on-Windows configuration — and the corruption outlives Cairn's uninstall.

**Fix:** capture and preserve the type; safest is to always write expandable:

```go
current, valType, err := key.GetStringValue("Path")
...
if valType == registry.EXPAND_SZ || strings.Contains(next, "%") {
	return key.SetExpandStringValue("Path", next)
}
return key.SetStringValue("Path", next)
```

(Any uninstall/remove path that rewrites `Path` needs the same treatment.)

---

<a name="s-2"></a>
## S-2 · MEDIUM — `redactText` erases whole lines (wrong submatch index) → agent redrafts silently drop env vars

**File:** `internal/services/agent_service.go:2109, 2121-2123`

```go
secretLinePattern = regexp.MustCompile(`(?i)^(\s*[-\w.]+\s*[:=]\s*)("?)`)
...
if match := secretLinePattern.FindStringSubmatchIndex(line); match != nil {
	lines[i] = line[:match[2]] + "[REDACTED]"
	continue
}
```

`FindStringSubmatchIndex` returns pairs: `match[2]` is the **start** of group 1, which is always `0`
because the pattern is `^`-anchored. The intended `DB_PASSWORD=[REDACTED]` becomes just `[REDACTED]` —
the key name is destroyed too (verified by the reviewing agent with a live regex repro: even
`AUTH_URL=https://example.com` collapses to `[REDACTED]` because the key merely contains "auth").

Concrete damage: `DraftProjectFile` (`agent_service.go:527-545`) feeds the redacted file to the LLM as
"current content" and asks for a full replacement; the model cannot know `DB_PASSWORD`/`AUTH_URL` exist,
so its redraft omits them, and `ApplyFileEdit` **replaces the whole file** — the user's `.env`/compose
keys are silently deleted by an apparently successful agent edit.

**Fix:** keep the key prefix — use group 1's *end*: `lines[i] = line[:match[3]] + "[REDACTED]"`.

---

<a name="s-3"></a>
## S-3 · MEDIUM — imported-project background deploy holds the runtime read-lock, uncancellable → app-wide freeze

**File:** `internal/services/project_service.go:1310-1318` (spawned at `:236-238`)

```go
go func() {
	unlock := s.lockRuntime()
	defer unlock()
	if err := s.runProjectAction(ctx, security.ProjectActionDeploy, projectID, false, nil); ...
```

Importing a project with no existing containers spawns a deploy (`docker compose up -d` — image pulls
and builds, unbounded minutes) on a `context.WithoutCancel` context while holding `RuntimeMu.RLock` the
whole time. If the user then switches provider or quits: `RebindProvider`/`StopAll`
(`internal/shell/runtime.go:134-144, 263-273`) block in `serviceMu.Lock()`; with a writer pending,
**every** other frontend call's `lockRuntime()` queues behind it → the entire app freezes until the
build completes; on quit, `OnShutdown`'s `cancel()` can't cancel it and the process hangs.

**Fix:** make the deploy cancellable and lock-scoped: derive from the runtime lifecycle
(`ctx, stop := context.WithCancel(context.WithoutCancel(ctx))`, register `stop` so
rebind/shutdown can cancel before taking the write lock), and/or take the read lock per sub-operation
instead of across the whole deploy.

---

<a name="s-4"></a>
## S-4 · MEDIUM — `agent.max_context_lines` can never truncate

**File:** `internal/services/agent_service.go:1033-1051`

`result.Data` (a `json.MarshalIndent` blob — all containers, all images, up to 28 project files × 64 KiB
≈ 1.7 MB) is appended to `lines` as a **single element**, so `len(lines)` is ~25 no matter what and the
configured cap (default 400, exposed in Settings) never triggers. A project-scoped chat ships a
multi-megabyte prompt to the local model; Ollama truncates from the front (dropping the system prompt)
or stalls.

**Fix:** split before counting: `lines = append(lines, strings.Split(result.Data, "\n")...)` so the
existing cap and the `"... context truncated ..."` marker work.

---

<a name="s-5"></a>
## S-5 · MEDIUM — PATH registry edit never broadcasts `WM_SETTINGCHANGE`

**File:** `internal/services/docker_cli_shim_windows.go:166-186` (status text at `:47-54`)

After writing `HKCU\Environment\Path` the code sends no `WM_SETTINGCHANGE ("Environment")` broadcast
(what `setx` does). Explorer keeps its stale environment, so every "new PowerShell window" launched
from the taskbar/Start inherits the old PATH — while the shim status deliberately instructs exactly
that ("Open a new PowerShell window so the updated PATH is loaded"). Following the app's own repair
hint does nothing until sign-out/sign-in; the feature looks broken on first install.

**Fix:** after a successful write:

```go
windows.SendMessageTimeout(windows.HWND_BROADCAST, windows.WM_SETTINGCHANGE, 0,
	uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr("Environment"))),
	windows.SMTO_ABORTIFHUNG, 5000, nil)
```

---

<a name="s-6"></a>
## S-6 · LOW — `ApplyFileEdit` create-mode skips the conflict check

**File:** `internal/services/agent_service.go:637-663`

The `OriginalHash` guard protects edits, but when the file didn't exist at plan time
(`CreateFile=true`, hash empty) apply writes with no existence check — a file created within the plan's
10-minute TTL (by the user, by compose, or by a second pending create-plan) is silently clobbered.
**Fix:** for create plans, fail with the same Conflict error if `os.Stat` succeeds, or open with
`os.O_CREATE|os.O_EXCL`.

<a name="s-7"></a>
## S-7 · LOW — `DraftProjectFile` reads the whole target file into the prompt

**File:** `internal/services/agent_service.go:527-530`

Every other agent ingestion path is capped (64 KiB per project file, 256 KiB for `PlanFileEdit`), but
the draft flow `os.ReadFile`s the target unbounded — a large artifact in the project root produces a
memory spike and an oversized local-LLM request instead of a clean "file too large" error. **Fix:** stat
first and refuse/truncate above the existing 256 KiB limit.

---

## Explicitly investigated and cleared (agent's negative results, kept to prevent re-work)

- Lazy `planStore()` initializers look racy but are dead code — all stores are wired at construction in
  `shell/app.go:61-68`.
- `ComposeService.Config`'s duplicated error branch is dead but harmless (`compose.Client.Config` sets
  `API.Valid=false` on every error path).
- `executeStaleProjectContainerAction` calling `s.Docker.Stop/RemoveContainer` under the project RLock
  is **not** a recursive-lock deadlock — `ProjectService.Docker` is the raw `dockercore.Client`, not the
  locking `*DockerService` (wired in `runtime.go:224`).
- `KillContainer`/`PushImage`/`Restart` returning ConfirmationRequired unconditionally is intended
  plan-gating, enforced consistently by `RequireConfirmation`/`validateStoredPlan`.
- Agent file paths are symlink-checked against the project root; container file listings pass user
  paths via env var rather than shell interpolation — both sound.
