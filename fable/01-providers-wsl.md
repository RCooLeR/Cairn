# Providers — Windows WSL & stdio transport

Files: `internal/providers/windows_wsl.go`, `stdio_conn.go`, `stdio_tracker.go`, `exec.go`, `docker_apt.go`, `manager.go`

Methodology note: because `wsl.exe` re-parses commands through the distro's default shell, every claim
here about command behavior was tested with a Go probe that replicates the app's exact
`exec.Command("wsl.exe", "-d", <distro>, "--", ...)` invocation (Go's `syscall.EscapeArg` quoting), with
destructive commands swapped for `echo`. See "Verified non-bugs" at the bottom — several suspicious
patterns turned out to work correctly and should *not* be "fixed".

---

<a name="w-1"></a>
## W-1 · HIGH — `Start`/`Stop`/`Restart` run `systemctl` without root and always fail

**File:** `internal/providers/windows_wsl.go:394-407`

```go
func (p *WindowsWSLProvider) Start(ctx context.Context) error {
	_, err := p.runWSL(ctx, p.configuredDistro(), "systemctl", "start", "docker")
	return err
}
```

`runWSL` builds `wsl.exe -d <distro> -- systemctl start docker` — no `-u root`. Inside a stock Ubuntu
WSL distro the default user is uid 1000 and there is **no polkit agent**, so systemd refuses:

```
$ wsl.exe -d Ubuntu -- systemctl start docker
Failed to start docker.service: Interactive authentication required.
exit=1
```

**Empirically verified on this machine** (probe T7b). `Stop` and `Restart` hit the same polkit action and
fail identically. The engine Start/Stop/Restart actions surfaced through
`providers.Manager.Start/Stop/Restart` → `ProviderService` therefore *cannot succeed* on any standard
WSL setup. Notably the code already knows the right form: install step 9 runs
`wsl -d <distro> -u root -- sh -lc "systemctl enable --now docker"`, and Detect's repair hint says
`sudo systemctl start docker`.

**Fix:** run these through root, exactly like the install steps do:

```go
func (p *WindowsWSLProvider) Start(ctx context.Context) error {
	_, err := p.runWSLAsRoot(ctx, p.configuredDistro(), "systemctl", "start", "docker")
	return err
}
// runWSLAsRoot prepends {"-u", "root"} before "--" in runWSLWithOptions.
```

`runWSLWithOptions` currently hardcodes `wslArgs := append([]string{"-d", distro, "--"}, args...)`; add a
variant that inserts `-u root`. Apply to `Start`, `Stop`, `Restart`.

Related (LOW, same family): `LinuxNativeProvider.Start/Stop/Restart`
(`internal/providers/linux_native.go:296-309`) also invoke `systemctl start docker` as the app user. On
desktop Linux a polkit agent will usually pop an interactive auth dialog (so it can work), but on
headless/agent-less setups it fails the same way. Consider `pkexec systemctl ...` or documenting the
polkit requirement. Colima's lifecycle is user-level and fine.

---

<a name="w-2"></a>
## W-2 · MEDIUM — backend IP resolution via `hostname -I` is fragile

**File:** `internal/providers/windows_wsl.go:456-480`

```go
if output, ok := p.runWSLText(ctx, p.configuredDistro(), "sh", "-lc", "hostname -I"); ok {
	for _, field := range strings.Fields(output) {
		if ip := net.ParseIP(strings.TrimSpace(field)); ip != nil && ip.To4() != nil && !ip.IsLoopback() {
```

Two concrete failure modes:

1. **Mirrored networking** (`networkingMode=mirrored` in `.wslconfig`, increasingly common): inside the
   distro `hostname -I` reports the *Windows host's own* adapter IPs. The forwarder then dials the host
   itself on the very port it is listening on — best case connection-refused loops, worst case the
   relay connects to *its own listener* and ping-pongs bytes to itself. In mirrored mode forwarding is
   unnecessary (ports are already mirrored); the provider should detect mirrored mode
   (`wslinfo --networking-mode` or `/etc/wsl.conf` heuristics) and report `Supported=false` so the
   manager stays inert.
2. **Multiple IPs**: with Docker's own bridges absent from `hostname -I` this usually works, but a
   distro with an extra NIC (e.g. Tailscale, `usbipd`) can order a non-routable address first;
   `hostname -I` gives no interface affinity. Preferring the address on the default route
   (`ip route get 1.1.1.1`) is deterministic.

Also note the 30s `wslBackendIPCacheTTL` means UDP sessions dial a stale IP for up to 30s after a WSL
restart (TCP recovers via the dial-failure retry in `dialBackendPort`; UDP "dials" never fail). Minor,
self-heals.

---

## W-3 · LOW — `parseWSLListVerbose` skips any distro whose name starts with "NAME"

**File:** `internal/providers/windows_wsl.go:794`

```go
if line == "" || strings.HasPrefix(strings.ToUpper(line), "NAME") {
	continue
}
```

The header-line filter also filters real distros named e.g. `NameServer` / `named-anything`. Match the
header more precisely (`NAME` followed by whitespace then `STATE`), or skip only the first non-empty
line.

---

## W-4 · LOW — `commandStdioConn.Close()` can block a `Read` caller for up to 4s

**File:** `internal/providers/stdio_conn.go:82-118`

`Read` calls `c.Close()` inline when the first-read heuristic detects a WSL startup failure; `Close`
synchronously waits up to `2s + 2s` (graceful + force timeouts) for the child to exit. That stall runs
inside an HTTP transport read. Not incorrect — but consider hard-killing immediately on the
startup-failure path since the process is already known to be broken.

Also `truncateSingleLine` (`stdio_conn.go:195-204`) slices at byte offsets and can split a UTF-8
sequence mid-rune in the error message (WSL errors decode to non-ASCII text). Cosmetic.

---

## W-5 · INFO — `Detect` mutates configuration as a side effect

**File:** `internal/providers/windows_wsl.go:171`

`p.SetDistro(selected.Name)` inside `Detect` persists nothing but rewrites the in-memory distro and
clears the IP cache. Harmless today (manager re-applies saved settings before every use), but it makes
`Detect` non-idempotent and racy against a concurrent `SetDistro` from Settings. Prefer resolving the
name into a local without mutating provider state.

---

## Verified non-bugs — do not "fix" these

Probe results (Go-quoted invocations, exactly as the app issues them):

| Suspicion | Verdict |
|---|---|
| `escapeWSLCommandDollars` + embedded `"..."` in install step 7 (docker group) breaks parsing | **Works.** `user="\$(getent ...)"` survives wsl.exe's re-quoting and runs `usermod -aG docker roman` correctly (probe T1). The unescaped form would break (probe T1b) — the escaping is both necessary and sufficient with Go's arg quoting. |
| Install step 6 codename detection (`dockerAptSourceWriteCommand`) mangled by outer-shell `$` expansion | **Works** when escaped: `RESULT-codename=[noble]` (probe T4); raw form yields empty (T4b), confirming `escapeWSLCommandDollars` must stay. |
| `wslSystemdEnabledCommand` awk regex (`...$/{...}`) eaten by the outer shell | **Works** (probe T2). `$/` is not a valid parameter reference, bash leaves it intact. |
| NVIDIA runtime grep `([[:space:]]|$)` anchor corrupted (escaped or not) | **Works both ways** (probes T5/T5b). |
| `sh -lc "command -v docker ..."` always exits 0 through wsl.exe re-splitting | **Works** — missing binary exits 127 (probe T6); Docker-missing detection is sound. |
| PowerShell-style quoting breaks these commands | Only when invoked *from PowerShell 5.1* (unescaped embedded quotes). The app uses Go's `EscapeArg`, which round-trips correctly. Keep this in mind for docs/scripts that tell users to run the same commands manually. |

One real caveat found while probing: the **CLI shim** (`docker_cli_shim_windows.go:150-157`) forwards
`$args` through *PowerShell*, which does mangle embedded double quotes on 5.1 — e.g.
`docker run -e 'A=he said "hi"' ...` via the shim will corrupt the argument. LOW; noted in
[07-misc.md](07-misc.md).
