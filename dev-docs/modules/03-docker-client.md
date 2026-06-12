# Module 03 — Docker Client

`internal/docker` — thin, typed wrapper over the Docker Engine Go SDK (`github.com/docker/docker/client`).

## 1. Responsibilities

Containers (list/inspect/create+run/start/stop/restart/kill/rename/remove/exec), images (list/inspect/pull/tag/push/save/load/search/remove/history), volumes (list/inspect/create/remove), networks (list/inspect/create/remove), system (ping/info/version/df/events), streams (logs, stats, exec/attach), prune operations. No Compose semantics here (modules/04) and no business rules (risk/confirmation live in security module).

## 2. Connection management

- Client built from active provider: `DockerHost()` string or custom dialer (WSL case).
- API version negotiation enabled; minimum supported Engine API: 1.41.
- Health loop: ping every 10 s when idle; on failure → `docker:disconnected{reason}` → reconnect with exponential backoff (1 s → 30 s cap) → `docker:connected`. All in-flight streams get cancelled with `E_DOCKER_UNREACHABLE`; managers auto-restart streams after reconnect where the UI still needs them.
- Every unary call: 10 s timeout default (pull/prune: none, they stream progress).

## 3. DTO mapping rules

- Container status normalized to `created|running|paused|restarting|exited|dead` + `health: healthy|unhealthy|starting|unknown` (from `State.Health`).
- Compose labels extracted once into typed fields: `ProjectName, ServiceName, ConfigFiles, WorkingDir, ConfigHash` (keys `com.docker.compose.*`).
- Ports mapped to `PortBinding{HostIP, HostPort, ContainerPort, Protocol}`; dedupe IPv4/IPv6 duplicates for display.
- Image `RepoTags`/`RepoDigests` kept verbatim; `dangling` = no tags.
- Sizes from `system df -v` merged into volume/image summaries when available (cheaper than per-object du).

## 4. Events subscription

`client.Events()` filtered to types container/image/volume/network. Mapping: event → cache invalidation + `objects:changed{kind, ids}` (batched ≤ 250 ms). Restart/health events additionally update `containers_cache.status/health` directly for snappy UI. Subscription auto-resumes after reconnect with `since` = last event time to avoid gaps.

## 5. Exec (terminal backend)

`ContainerExecCreate` with `Tty: true, AttachStdin/out/err`, user/workdir/env from options → `ContainerExecAttach` hijacked conn handed to terminal manager (modules/07). Shell detection: try in order `/bin/bash`, `/bin/sh`, `/busybox/sh` via non-tty `exec test -x`; cache per image ID.

## 6. Pull, tag & push

- Pull: `ImagePull` JSON-message stream parsed → `image:pull:progress{layerID, status, current, total}`; final digest captured for cache. Errors: auth → `E_REGISTRY_AUTH`, not found → `E_NOT_FOUND`.
- Tag: `ImageTag(imageID, newRef)` after ref validation (distribution/reference); emits `objects:changed{image}`.
- Push: `ImagePush(ref)` with auth from the backend's Docker credential chain (encoded auth header built per registry; resolved via provider-side helper invocation, same path as modules/08 §4); JSON progress stream → `image:push:progress`; denied → `E_REGISTRY_AUTH` naming the registry; push runs only through a confirmed plan ([05-security.md §2]).

## 6a. Create/run, rename, save/load, search

- **RunImage:** `ContainerCreate` + `ContainerStart` from `RunImageRequest`; pre-validation: name uniqueness, host-port availability probe, image present (else pull with progress when `PullIfMissing`); created container appears via events. Equivalent `docker run` command shown as preview for transparency.
- **Rename:** `ContainerRename` with name validation (`[a-zA-Z0-9][a-zA-Z0-9_.-]*`); Compose-managed containers warn that Compose may recreate with the original name.
- **Save/Load:** `ImageSave`/`ImageLoad` streamed to/from host path (provider-mapped) with `job:progress` (byte counter); load result lists imported tags.
- **SearchHub:** Engine API `ImageSearch` (Hub-only by API design); 10 s timeout; offline → inline error, manual ref entry always available.

## 7. Edge cases

- Paused containers: surface `paused` distinctly; unpause is part of start action.
- Containers without Compose labels → project NULL (shown under "Ungrouped").
- `docker system df` slow on huge installs → cached 5 min, refreshed async.
- Windows daemon (LCOW etc.) out of scope: if `Info.OSType != "linux"` → unsupported-provider warning.

## 8. Tests

Unit: DTO mappers from recorded inspect JSON fixtures (≥ 1 per object kind incl. healthcheck, compose labels, weird ports). Integration ([06-testing.md §3]): every operation against real daemon; event latency < 1 s; reconnect scenario; exec shell-detection on bash-less image.
