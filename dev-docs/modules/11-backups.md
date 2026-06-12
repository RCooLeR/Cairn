# Module 11 — Volume Backups

`internal/backups` — named-volume backup/restore via temporary helper containers. Engine required in v1 (update flow's "backup first"); full management UI is P2.

## 1. Backup

```text
docker run --rm -v <volume>:/source:ro -v <backupDir>:/backup alpine:3
  tar czf /backup/<volume>-<ts>.tar.gz -C /source .
```
- Helper image `alpine:3` (pulled on demand; offline → clear error).
- Consistency warning when volume is used by running containers; offer stop-project-first option (plan step).
- Sidecar `<name>.json`: volume, project, using containers, created_at, compressed size, sha256, docker context, provider, cairn version, format version.
- Backup dir: `backups.directory` setting (host path, provider-mapped). Row in `backups` table; progress via `job:progress`.

## 2. Restore

Targets: into existing volume (**dangerous** — typed-name confirm, wipe then untar), into new volume, duplicate with suffix.
```text
docker run --rm -v <target>:/restore -v <backupDir>:/backup:ro alpine:3
  sh -c "rm -rf /restore/* /restore/..?* /restore/.[!.]* ; tar xzf /backup/<file> -C /restore"
```
Pre-checks: archive exists + checksum matches sidecar; target used by running containers → warn + offer project stop; overwrite requires typed volume name ([05-security.md §2]).

## 3. Safety & limits

Only named volumes (bind mounts out of scope). Large volumes: no size limit, but free-disk check (backup dir) before start; estimated size from `system df -v` when known. Everything through plan pipeline + audit.

## 4. Tests

Integration round-trip on `app-db` postgres volume: write marker data → backup → wipe → restore → checksum-verify marker; restore-into-new and duplicate paths; checksum-mismatch rejection; running-container warning; typed-name enforcement. Unit: sidecar (de)serialization, free-disk check, filename collision handling.
