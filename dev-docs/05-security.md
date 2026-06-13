# 05 — Security Specification

Docker access ≈ root-equivalent on the backend. Cairn must be safe by default and transparent about everything it runs.

## 1. Threat model (what we defend against)

| Threat | Mitigation |
|---|---|
| User accidentally destroys data (volumes, prune) | risk-tiered confirmations, typed-name confirmation, command preview |
| Cairn silently escalates privileges | explicit sudo prompts; never silent docker-group add; explained consequences |
| Secrets leakage (registry passwords, SSH keys) | OS keychain only; Docker credential helpers preferred; redaction in logs/audit |
| Exposed Docker daemon | never configure TCP exposure; warn loudly if user's context uses unencrypted TCP |
| Malicious/buggy command injection via names | all provider commands built as argv arrays, never string-concatenated shell; compose project/service names validated against Compose name charset |
| Supply chain (frontend deps) | lockfiles, `npm audit`/`govulncheck` in CI |

Out of scope v1: multi-user access control, network policy management, image vulnerability scanning.

## 2. Destructive-action policy (normative risk mapping)

| Risk | Operations | UX requirement |
|---|---|---|
| `safe` | list/inspect, logs, stats, start/stop/restart container & project, pull image, check updates, run image (wizard), rename container, create volume/network, tag image, save/load image tar | command preview available, no confirmation |
| `needs_confirmation` | kill container, remove stopped container, remove unused image, remove network, push image to registry, service update apply, rebuild apply, restart Docker backend | modal: effects + commands + Confirm |
| `destructive` | remove running container (force), remove image used by containers, prune images/containers/build-cache, `compose down` (no volumes), force recreate, restore volume into new volume | modal with red styling, effects list, explicit checkbox where multiple effects |
| `dangerous` | delete volume, prune volumes, prune system, `compose down --volumes`, restore overwriting existing volume, reset provider/backend, uninstall Docker packages | modal + **type the target name** to enable Confirm; 10-min plan expiry |

Rules:
- Risk evaluation happens in the backend (`internal/security`); the frontend cannot lower a risk level.
- `ApplyX` without a valid confirmed plan → `E_CONFIRMATION_REQUIRED`.
- `security.confirm_destructive` cannot be disabled in v1.
- Volumes used by running containers: deletion blocked outright unless user stops containers or checks an explicit override (which raises risk to dangerous).

## 3. Command transparency

Before any operation that shells out, the plan shows: command(s) argv-joined for display, working directory, provider/context, expected effect, risk label. Audit log records the same (secrets redacted). Example:

```text
Command:  docker compose down --volumes
Workdir:  ~/stacks/observability     Context: linux_native
Risk:     Dangerous — removes containers AND named volumes for this project.
```

## 4. Credentials & secrets

- Registry auth: Cairn provides a registry login UI (Settings → Registries, [ui/10-settings.md §4a]) that **delegates to `docker login` on the backend OS** — credentials are stored by Docker's credential store/helper (Windows Credential Manager / macOS Keychain / pass or plaintext `config.json` under the user's existing Docker setup), never by Cairn. The secret is piped via stdin (`--password-stdin`), never passed as an argument, never written to SQLite/audit/logs (redactor-enforced). Docker Hub with 2FA → access tokens required; UI says so explicitly. If the backend's Docker would store the secret base64-plaintext in `config.json` (no helper configured), Cairn warns and recommends a credential helper before proceeding.
- Push: `docker push` requires confirmed preview/plan semantics (`needs_confirmation`) and shows the exact ref being published. In the current v1 generated API surface, `DockerService.PushImage(ref)` is callable only after the Images Push modal confirms that exact ref; the backend records `image.push` audit rows with `needs_confirmation`.
- Never store: plaintext registry passwords, SSH private keys, tokens — in SQLite, settings, logs, or audit.
- If Cairn must hold a secret (post-v1 remote SSH passphrases): Windows Credential Manager / macOS Keychain / Linux Secret Service via a single `security/secrets.go` abstraction.
- Redaction: audit/command-history writers pass through a redactor that masks `-p/--password/AUTH=` patterns and anything sourced from env marked secret.

## 5. Linux permissions

Detection: socket access test. If denied, present three explicit options (never silently chosen):

```text
A. Use sudo per action (Cairn prompts; sudo password never stored)
B. Add user to docker group  — warning: docker group ≈ root; requires re-login
C. Rootless Docker detected → use rootless socket
```

## 6. Terminal safety

Container terminal header always shows: container, shell path, user, workdir; red "root" badge when uid 0. No automatic privilege escalation. Host/backend terminals are plain user shells.

## 7. Network exposure

Never configure `tcp://0.0.0.0:2375`. If an existing context uses unencrypted TCP, show a persistent warning badge on the provider. Remote management uses SSH-based Docker contexts only.

## 8. Audit log

Every `needs_confirmation+` action and every provider lifecycle action writes audit entries (`started` → `success|failed|cancelled`) with action, target, provider, project, command (redacted), risk, exit code, duration, error. Viewable in Settings → Audit ([ui/10-settings.md §6]). Retention: 90 days or 50 000 rows, whichever first.

## 9. Update safety

No auto-updates by default. Before update: show current/remote digests, exact commands, offer volume backup, record rollback metadata (old image ID/digest/base digest, compose config snapshot, Dockerfile hash). After: health watch (status, healthcheck, restart loop, log scan, 60 s window) → result states `success | success_warn | failed | rolled_back | manual_needed`. Automatic rollback only when previous image ID is still present locally; otherwise manual guidance ([modules/09-updates.md §6]).
