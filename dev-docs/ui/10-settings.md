# UI 10 — Settings

Left section nav + content pane. Sections: General · Providers · Docker contexts · Updates · Metrics · Terminal · Appearance · Backups · Security & Audit · Advanced · About.

All controls bind to settings keys ([03-data-model.md §7]); changes save immediately with toast; restart-required items labeled.

## 1. General
Theme (dark/light/system) · Launch Cairn at login · Auto-start Docker backend on app launch · Language (en, locked v1).

## 2. Providers
List of configured providers: card per provider (type icon, display name, status from last detect, [Detect again], [Set active], [Repair…] when problems, [Remove] for non-OS-default). [Set up new backend…] → onboarding flow. Active provider card expands platform-specific settings:
- **Windows/WSL:** distro selector (from `wsl -l`), path-mapping info panel, start-Docker-on-launch.
- **Linux:** socket path, permission mode (sudo/group/rootless — change reruns detection), systemd status (read-only).
- **macOS/Colima:** profile, CPU/RAM/disk sliders (restart-required note), Colima autostart.

## 3. Docker contexts
Table (name, host, provider, current ✓, reachable dot); [Use this context]; unencrypted tcp:// rows get permanent red warning chip ([05-security.md §7]).

## 4. Updates
Check interval (manual/6h/daily/weekly) · Notify on available updates · Default modal toggles (backup first / watch health / auto-rollback).

## 4a. Registries
Account table: **Registry** (icon for known: Docker Hub, ghcr.io, GitLab, Quay…) · **Username** · **Storage** (credential helper name, or red "unencrypted (config.json)" warning chip) · **Status** (Verified ✓ / Unverified / Auth failed) · actions([Test], [Log out]).
[+ Log in to registry…] modal: registry picker (presets + custom URL) → username → secret field with kind toggle (password / access token; Docker Hub preselects token + hint "2FA accounts must use an access token" + link); secret piped via stdin, never shown again; [Log in] → verify → row appears. Errors inline (bad credentials / unreachable registry).
Footer note: "Credentials are stored by Docker on the backend, not by Cairn." Effect note: private-image update checks start working after login.

## 5. Metrics
Sample interval (1–10 s) · Retention summary (read-only "1 h raw → 24 h/1 m → 7 d/15 m") · Current DB size + [Compact now].

## 6. Security & Audit
Destructive-action confirmation (locked ON, explainer) · **Audit log viewer:** table (time, action, target, risk chip, status, duration) with filters (range/action/status/project), row drawer (full command redacted, error), [Export CSV]. Retention note (90 d).

## 7. Terminal
Default shell per scope (host/backend) · scrollback size · font size · paste-guard toggle (default on).

## 8. Appearance
Accent preview, density (comfortable/compact tables), reduced motion (follows OS, overridable).

## 9. Backups
Backup directory picker (validated writable; provider-mapped path shown) · existing backups total size · [Open backups folder].

## 10. Advanced
App log level · [Open app logs folder] · binding/diagnostics info · [Reset all caches] (safe; rebuilds from Docker) · danger zone: [Reset Cairn settings…] (dangerous, typed `RESET`).

## 11. About
Version, build, Wails/Go versions, [Check for app updates], licenses, link to docs.

## Tests
E2E: every control round-trips to backend value; provider switch reconnects; audit filters; reset-caches rebuilds lists; typed RESET gate.
