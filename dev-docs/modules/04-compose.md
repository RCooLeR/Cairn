# Module 04 — Compose Wrapper & Project Detector

`internal/compose` — official `docker compose` CLI wrapper (never reimplement Compose) + project discovery/grouping.

## 1. CLI wrapper

- Execution always via active provider `RunCompose(workdir, args…)`; args as argv arrays; env passthrough plus `COMPOSE_PROJECT_NAME` only when needed for disambiguation.
- Version detect at provider attach: `docker compose version --format json`; minimum v2.20. Standalone `docker-compose` v1 unsupported → problem `COMPOSE_V1_UNSUPPORTED` with upgrade hint.
- Structured outputs: `--format json` for `ls`, `ps`, `version`; `config` consumed as YAML; lifecycle commands stream plain output to `job:progress`.

Operations: `config [--profiles]`, `ps`, `ls`, `up -d [services…] [--force-recreate] [--pull]`, `down [--volumes]`, `pull [services…]`, `build --pull [services…]`, `restart [services…]`, `stop/start [services…]`, `logs` (only for project-level semantics fallback; primary log path is Engine API, modules/06).

## 2. Output parsing

Parsers are version-tolerant: unknown JSON fields ignored; golden fixtures recorded per supported Compose minor version (`testdata/compose-outputs/v2.2x/…`). `config` parsing extracts per service: `image`, `build.{context,dockerfile,target,args}`, `ports`, `depends_on`, `healthcheck` presence, `env_file`, profiles.

## 3. Project model & IDs

`projectID = providerID + "/" + composeProjectName` (compose name already normalized lowercase). Service ID = `projectID + "/" + serviceName`. Containers map by labels.

## 4. Detection & grouping algorithm

Sources, merged in priority order:
1. **Container labels** (live truth): group running/stopped containers by `com.docker.compose.project`; capture `working_dir`, `config_files` labels.
2. **`docker compose ls -a`**: catches projects with zero containers currently.
3. **Imported projects** (`projects.source = imported`): user-added folders; matched to label projects by normalized name; if a label project later appears with a different workdir → keep label workdir, flag mismatch warning.

Reconciliation rules:
- A project disappears from sources → keep row for 24 h (`last_seen_at`), shown as "inactive" if imported, else dropped.
- Workdir missing on disk → project flagged `E_WORKDIR_MISSING`; lifecycle actions disabled except stop/down (which work via labels without files? — **No:** compose down requires files; offer container-level stop + re-link-folder flow instead).
- Containers without labels → virtual "Ungrouped" bucket (not a project; no compose actions).
- Same project name on two providers → distinct IDs, no merge.

Status derivation: all services running → `running`; none → `stopped`; some → `partial`; any error state → `error`. Health summary aggregates container health.

## 5. Import flow

User picks folder (or compose file): validate existence of `compose.yaml|compose.yml|docker-compose.yml|docker-compose.yaml` (or explicit file list) → run `config` for validation → store project (`source=imported`) → run detection merge → return ProjectDetail. Validation failure returns `E_COMPOSE_INVALID` with CLI output as detail. Path stored host-side; mapped per provider on execution; on WSL warn for `/mnt/*` performance.

## 6. Action execution

All lifecycle ops run from the project working dir with its config files (`-f` flags in stored order). Service-scoped variants pass service names. Everything flows through the command-plan pipeline; safe ops (start/stop/restart/pull) get auto-approved plans but still record audit + show command toast. Long ops stream output lines to `job:progress`.

## 7. Tests

Unit: parser fixtures; grouping table-tests (orphans, zero-container projects, name collisions, workdir mismatch, ungrouped). Integration: all `testdata/projects` detected with expected services (`expected.json`); lifecycle round-trip per project; broken yaml → invalid result; deleted workdir → flagged, re-link works.
