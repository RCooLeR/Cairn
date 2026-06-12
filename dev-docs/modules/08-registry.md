# Module 08 — Registry Client

`internal/registry` — remote digest resolution for update checks. Read-only: HEAD/GET manifests, never push.

## 1. Responsibilities

Parse image references; resolve remote digest for (ref, platform); handle Docker Hub + generic OCI registries; auth via existing Docker credential chain; rate-limit awareness; caching; never store passwords.

## 2. Reference parsing

Use `github.com/distribution/reference`. Normalization: bare names → `docker.io/library/<name>:latest`; explicit digests (`name@sha256:…`) marked pinned (no remote check). Output: `{Registry, Repository, Tag?, Digest?}`.

## 3. Digest resolution protocol

```text
1. GET /v2/ → 401 with WWW-Authenticate → token (anonymous or basic from credential helper)
2. HEAD /v2/<repo>/manifests/<tag>
   Accept: index/list + manifest media types (OCI + Docker v2)
3. Docker-Content-Digest header = remote digest of the tag
4. If digest is an index/list: GET index, select platform (daemon's os/arch from Info),
   record both index digest and platform manifest digest.
```

Comparison rule (normative): compare against the digest kind we can resolve locally — local `RepoDigests` are index digests for pulled multi-arch images, so compare index-to-index; when local digest absent (locally built/`local-only`) → status `local_only_image`/`built_locally`, not an error.

## 4. Auth

Chain: explicit none → `~/.docker/config.json` auths + `credHelpers`/`credsStore` (invoke helper binary via provider exec — credentials live on the backend OS for WSL!). 401 after auth attempt → `auth_required` status. Helpers' output never persisted, used in-memory per request.

### 4a. Account management (login/logout)

Backs `RegistryService` ([04-api-contracts.md §5a]):
- `Login`: provider-exec `docker login <registry> -u <user> --password-stdin`, secret piped (never argv); on success re-read config.json to refresh account list; immediately `TestAuth` (token grant for a known-public repo or `/v2/` authorized ping).
- `Logout`: `docker logout <registry>`.
- Account listing: parse backend `~/.docker/config.json` (`auths`, `credHelpers`, `credsStore`); usernames only — secrets are never read back into Cairn.
- Plaintext-store detection: no helper configured → account row flagged "stored unencrypted by Docker" warning per [05-security.md §4].
- Effect on update checks: §3 token requests use these credentials automatically; a successful login clears `auth_required` statuses on next check.

## 5. Politeness & resilience

- Per-registry concurrency cap: 3; global: 8. Jittered scheduling for bulk checks.
- 429 → status `rate_limited`, exponential backoff (base 30 s), Retry-After honored; per-registry circuit breaker (5 consecutive failures → 10 min open).
- Result cache: (ref, platform) → digest, TTL 1 h (configurable); bulk "Check now" bypasses cache.
- Timeouts: 10 s per request; total budget per check 30 s.

## 6. Errors → statuses

| Condition | UpdateStatus |
|---|---|
| 401/403 after auth chain | auth_required |
| 429 / circuit open | rate_limited |
| network/5xx/timeout | error (CheckFailed) |
| 404 manifest | error with hint "tag no longer exists" |
| pinned digest ref | pinned_digest (no request made) |

## 7. Tests

Unit: ref normalization corpus (≥ 25), WWW-Authenticate parsing, platform selection from index fixtures, backoff/circuit logic (fake clock). Integration: local `registry:2` (push two digests for one tag, verify change detection); httptest server simulating Hub auth, 429, index→manifest; WSL credential-helper invocation path (fixture-faked).
