# Module 12 — SQLite Store

`internal/store`

## 1. Stack & setup

`modernc.org/sqlite` (CGO-free, single binary). Open with: WAL, `busy_timeout=5000`, `foreign_keys=ON`, `synchronous=NORMAL`. One writer connection + reader pool (SQLite WAL pattern). DB at `<appdata>/cairn.db` (`%AppData%/Cairn`, `~/Library/Application Support/Cairn`, `~/.local/share/cairn`).

## 2. Migrations

Embedded FS `migrations/NNNN_name.sql`, forward-only, sequential, each in a transaction, tracked in `schema_migrations`. Migration 0001 = full v1 schema ([03-data-model.md]). Downgrade unsupported (newer schema → refuse start with clear message). Backup copy of DB file before migrating (keep last 2).

## 3. Repository layer

One repo per aggregate (settings, providers, projects, caches, metrics, lineage, updates, backups, audit, notifications). Plain SQL with typed scan helpers — no ORM. All writes through repo methods; JSON columns validated on write. Bulk upserts for cache reconciliation (`INSERT … ON CONFLICT DO UPDATE`).

## 4. Maintenance

Hourly job: metrics retention (modules/05 §4), audit trim (90 d / 50 000), notifications trim (30 d), `PRAGMA wal_checkpoint(TRUNCATE)` when WAL > 64 MB, monthly `VACUUM` (skipped if DB < 50 MB). Corruption handling: integrity_check on open failure → rename corrupt file, recreate fresh, notify user (caches rebuild automatically; history loss reported honestly).

## 5. Tests

Migration idempotence + fresh-vs-upgraded schema equivalence; concurrent reader/writer smoke (race detector); upsert reconciliation; retention/trim bounds; corruption recovery path; repo round-trip per aggregate.
