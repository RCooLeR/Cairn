# Port forwarding (the feature under review on this branch)

Files: `internal/portforward/*`, `internal/shell/runtime.go`, `internal/services/service_wrappers.go`,
`frontend/src/components/settings/PortForwardingPanel.tsx`

Overall: the manager is a clean design (spec-diff reconcile, per-forward closer tracking, serialized
reconciles, deliberate leave-in-place on transient list failures). The bugs below are mostly lifecycle
and Windows-specific edge cases.

---

<a name="p-1"></a>
## P-1 · HIGH — the Enabled toggle is never persisted; every rebind/restart force-enables forwarding

**Files:** `internal/shell/runtime.go:168-172`, `internal/services/service_wrappers.go:67-75`

```go
// runtime.go — on every provider (re)bind:
portForwardManager = portforward.NewManager(dockerClient, dialer, r.events,
	portforward.Options{Enabled: true})          // ← hardcoded

// service_wrappers.go — the Settings toggle:
func (s *PortForwardService) SetEnabled(_ context.Context, enabled bool) error {
	...
	s.Manager.SetEnabled(enabled)                 // ← in-memory only
	return nil
}
```

`SetEnabled` writes nothing to the settings store, and `RebindProvider` recreates the manager with
`Enabled: true` unconditionally. Concrete failure: the user clicks **Disable forwarding** (it works),
then restarts Cairn — or anything triggers a rebind (changing the WSL distro in Settings, switching
provider/context, the app's own recovery) — and all host ports are silently bound again. For a user who
disabled forwarding *because of a port conflict with a Windows service*, this re-breaks that service on
every launch. The UI toggle looks persistent (it lives in Settings) but is session-scoped.

**Fix:**
1. Add a settings key, e.g. `portforward.enabled` (default `true`).
2. `PortForwardService` gets `Settings *store.SettingsRepository`; `SetEnabled` persists then applies.
3. `RebindProvider` reads it: `Enabled: settingsBoolOr(ctx, r.db.Settings(), "portforward.enabled", true)`.

---

<a name="p-2"></a>
## P-2 · HIGH — UDP relay dies permanently on `WSAECONNRESET` (stray ICMP), forward still shown "active"

**File:** `internal/portforward/udp.go:52-56`

```go
for {
	n, src, err := host.ReadFrom(buffer)
	if err != nil {
		return                                    // ← any error kills the whole UDP forward
	}
```

On Windows, a UDP socket that previously sent a datagram to a peer that answered with ICMP
"port unreachable" gets **`WSAECONNRESET` returned from the next `recvfrom`** — this is delivered
through `host.ReadFrom` as an error on the very socket serving *all* clients (this is the classic Go-on-
Windows UDP gotcha; DNS servers written in Go all special-case it). Concrete scenario: a game/DNS client
talks through the forward, the client process exits, the janitor-driven `host.WriteTo` (or a late reply
pump write) hits the now-closed client port → ICMP → next `ReadFrom` errors → `serveUDP` returns. The
forward's status remains `"active"` (nothing updates it), yet the host port drops every datagram until a
container change forces a rebind.

`pumpUDPReplies` (`udp.go:89-97`) has the same issue on the backend-connected socket: one reset kills
the reply pump while the session stays cached for up to 60s — a one-way relay window.

**Fix:** treat connection-reset errors as non-fatal in both loops:

```go
n, src, err := host.ReadFrom(buffer)
if err != nil {
	if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
		return
	}
	if isConnReset(err) { // windows.WSAECONNRESET / syscall.ECONNRESET
		continue
	}
	fwd.fail(err) // and publish, see P-6
	return
}
```

---

<a name="p-3"></a>
## P-3 · MEDIUM — `StopAll` doesn't serialize with an in-flight reconcile → leaked bound port

**File:** `internal/portforward/manager.go:76-100` vs `190-242`

`StopAll` cancels the context, drains `m.forwards`, stops the drained forwards, then `m.wg.Wait()`.
But `reconcileOnce` (running inside `reconcileLoop`, which `m.wg` *is* waiting on) may already be past
its `ctx.Err()` check and past the map snapshot. Interleaving:

1. `reconcileOnce` binds a new listener via `startForward` (succeeds — cancel hasn't happened yet).
2. `StopAll` runs: cancels, drains the map (the new forward isn't inserted yet), stops the drained set.
3. `reconcileOnce` resumes and inserts the new forward into `m.forwards`; `reconcileLoop` then exits on
   the cancelled ctx, releasing `wg.Wait()`.

Result: `StopAll` returns while a live listener stays bound — and nothing will ever close it, because
context cancellation alone closes nothing (only `fwd.stop()` does, and the entry sits in a map nobody
drains again; a later `Start` doesn't clear it either). On a provider switch the old runtime keeps a
host port bound for the process lifetime, which then collides with the new runtime's forward for the
same port (it binds `error: address already in use`).

**Fix:** have `StopAll` take `reconcileMu` before draining (serializes with reconcile), and/or re-check
`m.started` under `m.mu` inside the insert loop of `reconcileOnce`, stopping the fresh forward instead
of inserting when stopped:

```go
m.mu.Lock()
if !m.started { m.mu.Unlock(); fwd.stop(); continue }
m.forwards[key] = fwd
m.mu.Unlock()
```

---

<a name="p-4"></a>
## P-4 · MEDIUM — forwards in `error` state are never retried

**File:** `internal/portforward/manager.go:217-237`

```go
for key, fwd := range current {
	if want, ok := desired[key]; ok && want == fwd.spec {
		continue                                   // ← kept, even if fwd.status == "error"
	}
```

Spec equality is the only criterion; a forward whose bind failed (port temporarily taken by another app
— very common on Windows, e.g. `svchost` ephemeral reservations, or WSL's own localhost relay) is kept
as a permanent `error` row and never re-attempted, even after the conflicting app releases the port.
Only stopping/starting the container (spec change) or toggling the feature clears it.

**Fix:** in the teardown/keep loop, treat `status == statusError` as "not satisfied" so it is stopped
and re-created next pass; add a small backoff (e.g. retry every Nth reconcile) to avoid hammering a
genuinely occupied port. Requires reading `fwd.status` — take `fwd`'s publication into account (status
is written pre-publication only, so reading it under `m.mu` after insert is safe today; keep it that
way or move status under `fwd.mu`).

---

<a name="p-5"></a>
## P-5 · MEDIUM — loopback-published container ports relay to the distro eth0 IP and can't connect

**Files:** `internal/portforward/manager.go:300-309`, `internal/providers/windows_wsl.go:426-454`,
`internal/portforward/bind.go:76-107`

A container published as `-p 127.0.0.1:8080:80` produces a host listener on `127.0.0.1:8080` (bind
mirroring — good), but `relayTCP` dials `DialStream(ctx, 8080)` → the provider dials
`<distro-eth0-IP>:8080`. Inside the distro, dockerd bound that publish to `127.0.0.1` *in the distro's
namespace* and its DNAT rule is scoped to destination 127.0.0.1 — a connection arriving at the eth0
address is refused. Net effect: the forward binds, shows **active**, accepts connections, and instantly
drops every one (client sees ECONNRESET/EOF). This misleads exactly the way Docker Desktop parity is
supposed to prevent.

(Additional wrinkle: on NAT-mode WSL, WSL's own `localhostForwarding` may already relay
`127.0.0.1:8080` → distro loopback, in which case Cairn's listener either fails to bind (→ error row)
or shadows a mechanism that already worked.)

**Fix options**, in order of preference:
1. Skip loopback-bound publishes (`isLoopbackHost(binding.HostIP)`) in `desiredForwards` on WSL-NAT —
   document that WSL localhostForwarding covers them — or
2. give the Dialer an address-aware variant (`DialStreamAddr(ctx, host, port)`) and relay loopback
   publishes through a stdio bridge (`wsl -d X -- socat - TCP:127.0.0.1:8080`) the way the Docker API
   transport already works.
Either way, add an integration test: publish on `127.0.0.1`, connect through the forward, assert bytes
flow — the current test matrix only exercises injected in-memory listeners.

---

<a name="p-6"></a>
## P-6 · LOW — accept-loop death leaves a zombie "active" forward; `::1` publishes mirrored as IPv4

1. `serveTCP` (`manager.go:276-298`): a non-timeout `Accept` error that isn't ctx-cancellation (e.g.
   `EMFILE`) returns silently. The forward stays in the map with `status: "active"`, is never retried
   (spec unchanged — see P-4), and never accepts again. Mark the forward failed and publish
   `portforward:changed` so the UI shows the error row.
2. `bindAddrFor` (`bind.go:33-45`): an IPv6 loopback publish (`::1`) maps to `"127.0.0.1"`, and any
   IPv6-specific bind (e.g. `fe80::1`) is mirrored verbatim into a `net.Listen("tcp", "[addr]:port")`
   that will bind an interface family the relay's IPv4 distro dial can't serve. All-interfaces `::`
   maps to `0.0.0.0`, losing v6 reachability that Docker Desktop would provide. Low priority; document
   or normalize deliberately.

---

## Notes (fine as-is)

- `relay()` closes both conns exactly once via `sync.Once`, unblocking both `io.Copy`s — correct.
- Widest-bind-wins dedup for the same host port across containers is deterministic (first container in
  Docker's stable list order wins ties) — no flapping observed in the logic.
- The 64 KiB UDP buffer and per-source session map with idle reap are sound; `session.lastSeen` only
  advances on client→backend traffic, so a server-push-heavy flow can be reaped at 60s idle — acceptable
  for UDP, mentioned for awareness.
- `PortForwardService.GetStatus` returning `Supported:false` while the runtime is rebinding is
  intentional; see [06-frontend.md](06-frontend.md#f-1) for the UI-side consequence.
