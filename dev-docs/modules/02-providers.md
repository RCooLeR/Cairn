# Module 02 — Platform Providers

`internal/providers` — the most important architectural boundary. The core never touches OS specifics; providers own detection, installation, lifecycle, command execution, path mapping, and terminal spawning for their backend.

## 1. Interface (normative)

```go
type PlatformProvider interface {
    ID() string; DisplayName() string; Type() ProviderType; Platform() Platform

    Detect(ctx) (*ProviderStatus, error)
    PlanInstall(ctx, InstallOptions) (*CommandPlan, error)
    ExecuteInstallStep(ctx, planID string, step int, progress chan<- InstallProgress) error

    Start(ctx) error; Stop(ctx) error; Restart(ctx) error

    DockerHost(ctx) (string, error)      // connection string for Engine API
    DockerContext(ctx) (string, error)

    RunDocker(ctx, args ...string) (*CommandResult, error)
    RunCompose(ctx, workdir string, args ...string) (*CommandResult, error)

    HostShellCommand(opts TerminalOptions) (argv []string, err error)     // consumed by terminal manager
    BackendShellCommand(opts TerminalOptions) (argv []string, err error)

    MapPathToBackend(hostPath string) (string, error)
    MapPathToHost(backendPath string) (string, error)
}
```

Rules: all commands are argv arrays (no shell string interpolation); every call has a timeout; `CommandResult{Command, Stdout, Stderr, ExitCode, Duration}` is always captured; install runs as a CommandPlan so the UI previews every step.

`ProviderStatus` and `ProviderProblem{Code, Message, RepairHint, Recoverable}` per [04-api-contracts.md §1]. Problem codes are stable strings (e.g. `WSL_MISSING`, `UBUNTU_MISSING`, `SYSTEMD_OFF`, `DOCKER_MISSING`, `DOCKERD_DOWN`, `PERM_SOCKET`, `COMPOSE_MISSING`, `COLIMA_MISSING`, `BREW_MISSING`, `CTX_UNREACHABLE`) — the UI maps them to repair actions.

## 2. Provider manager

- Constructs the provider set for the current OS + saved configs (`providers` table).
- `DetectAll` in parallel (5 s budget each). Active selection: saved healthy → best detected (native > wsl/colima > existing context) → none (onboarding).
- Switching active provider: cancel streams → swap docker client → full refresh → `provider:changed`.

## 3. Detection state machine (shared shape)

Each provider implements detection as ordered checks producing a `ProviderStatus`; first hard failure short-circuits but later independent checks still run when possible so the UI shows the full picture.

## 4. Linux native provider

Checks: docker CLI on PATH → socket path exists (`linux.socket_path`, default `/var/run/docker.sock`; rootless: `$XDG_RUNTIME_DIR/docker.sock`) → socket permission (open test) → daemon ping → compose plugin version → buildx → systemd available.
Install plan (Ubuntu/Debian v1): apt prerequisites → Docker GPG key + repo → `docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin` → enable+start service → verify. Each step is a previewed command; apt steps run via `pkexec`/`sudo -A` prompt.
Permissions: never auto-add to docker group; expose three modes per [05-security.md §5]; sudo-per-action wraps RunDocker with `sudo -n` after an authenticated prompt session.
Start/Stop: `systemctl start|stop docker` (sudo path) — risk `needs_confirmation` for stop.

## 5. Windows WSL Ubuntu provider

Target chain: Cairn → `wsl.exe -d Ubuntu --` → Docker Engine inside Ubuntu.

### 5.1 Detection
`wsl.exe --status` (exists, default version 2) → `wsl.exe -l -v` (parse UTF-16LE!, find Ubuntu-family distros; multiple → use `windows.wsl_distro` setting, ask in onboarding) → distro is WSL2 → `wsl -d U -- test -d /run/systemd/system` (systemd) → docker/compose/buildx inside → `wsl -d U -- docker info`.

Exclusions & coexistence: `docker-desktop` and `docker-desktop-data` distros are NEVER candidates (Docker Desktop internals). Custom-named distros (e.g. `cairn-dev` via `wsl --install <distro> --name <name>` or `wsl --import`) are first-class candidates. If Docker Desktop's WSL integration is enabled for the selected distro (detectable via `/usr/bin/docker` symlinking to `/mnt/wsl/docker-desktop/...`), report problem `DESKTOP_INTEGRATION_CONFLICT` with the repair hint to disable integration for that distro — Cairn must manage a real engine inside the distro, not Desktop's proxy. Cairn never modifies the Windows-side Docker context, so an existing Docker Desktop installation keeps working untouched.

### 5.2 Install flow (each a previewed plan step)
1. Enable WSL (`wsl --install --no-distribution`; needs elevation + possible reboot → plan pauses with resume marker).
2. Install Ubuntu (`wsl --install -d Ubuntu`), first-run user creation guided.
3. Ensure WSL2 (`wsl --set-version Ubuntu 2`).
4. Enable systemd (`/etc/wsl.conf` `[boot] systemd=true`) → `wsl --shutdown` → restart.
5. Docker apt repo + packages inside Ubuntu (same as Linux plan, executed via wsl exec).
6. Enable/start service, verify versions, hello-world test.

### 5.3 Command execution & paths
`RunDocker/RunCompose` = `wsl.exe -d <distro> -- docker …` with workdir mapped. Path mapping: `C:\Users\x\p` ↔ `/mnt/c/Users/x/p` (drive lowercase, backslashes, spaces, UNC `\\wsl$\Ubuntu\home\…` ↔ native paths). Projects under `/mnt/*` get a performance warning; recommend `~/projects` inside WSL ([ui/02 §4]).

### 5.4 Docker connection
The Engine API connection tunnels through WSL interop with a custom dialer — the Go Docker client receives a `net.Conn` backed by a spawned process:

1. **Primary (dependency-free):** `wsl.exe -d <distro> -- docker system dial-stdio` — official CLI mechanism that proxies stdio to the daemon socket.
2. **Fallback:** `wsl.exe -d <distro> -- socat UNIX-CONNECT:/var/run/docker.sock -` (socat added during install only if dial-stdio probing fails).

Never expose the daemon on TCP for this. The chosen transport is hidden behind `DockerHost()`/the provider dialer so the core stays transport-agnostic.
Edge cases: distro not initialized; no sudo password; docker group needs WSL restart; VPN/firewall vs published ports (warning only).

### 5.5 Terminals
Host: `powershell.exe` (or `pwsh` if present). Backend: `wsl.exe -d <distro>`. ConPTY via Wails-compatible pty lib.

## 6. macOS Colima provider

Detection: brew → docker CLI → compose (plugin or standalone) → colima binary → `colima status` (profile from settings) → context `colima` exists/selected → daemon ping.
Install: brew install docker docker-compose colima (each previewed) → `colima start --cpu N --memory N --disk N` → context select → verify. No Homebrew → guide user to install it (link + command preview), do not script the curl-pipe install silently.
Lifecycle: `colima start|stop|restart -p <profile>`; resource changes require restart (warn).
Paths: identity mapping (Colima mounts $HOME by default); warn when project outside mounted dirs.
Terminals: host `zsh`; backend `colima ssh -p <profile>`.

## 7. Existing-context provider (all OS)

List contexts (`docker context ls --format json`), ping selected, use its host for Engine API, run compose with `--context`. Never modifies the user's setup. Unencrypted `tcp://` host → permanent warning badge. Works with Docker Desktop, OrbStack, Rancher Desktop, remote contexts.

## 8. Remote SSH provider (engine in v1, UI post-MVP)

Create/validate docker context `host=ssh://user@host`; reuse existing-context machinery; status includes SSH reachability. No key management in v1 (system ssh-agent only).

## 9. Tests

Unit: every parser (wsl -l -v fixtures incl. UTF-16/BOM, colima status JSON, context ls), path mappers (≥ 20 cases incl. UNC/spaces/unicode), problem-code mapping. Integration: Linux full; WSL/Colima via VM matrix ([06-testing.md §2]); install flows on clean VMs with failure injection (no network, no systemd, no sudo). Contract: the full Phase 1–3 integration suite runs against every provider.
