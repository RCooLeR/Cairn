# UI 06 — Images, Volumes, Networks

Three table-centric pages sharing DataTable behaviors ([ui/00 §3]).

## 1. Images page

Header: search, FilterChips [All · In use · Unused · Dangling · Update available], [Pull image…], [Load from .tar…], [Clean up…].

Table: **Repository** · **Tag** · **Image ID** (short, copy) · **Size** · **Created** · **Used by** (count chip → popover with container links) · **Update** (badge: up-to-date/update/pinned/unknown; from latest checks) · actions.
Row actions: **▶ Run…** (run-image wizard, §1a) · Pull (re-pull tag) · Inspect (drawer: full meta, layers/history list, labels, digests) · Check update · kebab(Tag…, Push…, Save to .tar…, Show containers, Copy digest, Remove…).

Pull modal: ref input with validation (`name[:tag]`) **+ Docker Hub search**: typing ≥ 3 chars shows result list (name, ⭐ stars, Official badge, description; Engine `ImageSearch`); selecting fills the ref; tag dropdown defaults `latest`. Progress per layer (`image:pull:progress` bars), cancel. Errors inline (auth/not found); offline → search hidden, manual ref still works.
**Save modal:** multi-select refs (preselected row), destination file picker, size estimate, `job:progress`. **Load modal:** file picker → progress → toast lists imported tags.
**Tag modal:** new ref input (`registry/namespace/name:tag`) with live validation + parsed-parts preview (registry · repository · tag); [Create tag] → new row appears (same image ID).
**Push modal:** ref summary; account check — logged into target registry? green chip with username : red "Not logged in" + [Log in…] (opens Registries login inline, ui/10 §4a); command preview (`docker push <ref>`); needs_confirmation; per-layer progress (`image:push:progress`), cancel; success toast with copyable pull command (`docker pull <ref>`). Auth failure → inline error naming registry + re-login affordance.
Remove: in-use image → destructive modal listing dependent containers; force checkbox raises severity copy.
Clean up modal: checkboxes "Dangling images (1.2 GB)" / "Unused images (3.4 GB)" / "Build cache (2.8 GB)" with reclaimable sizes from DiskUsage; command preview (`docker image prune [-a]`, `docker builder prune`); destructive confirm.

## 1a. Run-image wizard (modal, 2 steps)

**Step 1 — Basics:** image ref (read-only when launched from a row; editable with Hub search otherwise); container name (auto-suggested, uniqueness-validated live); pull-if-missing checkbox.
**Step 2 — Configuration (accordion sections, all optional):**
- **Ports:** rows host-port ↔ container-port/protocol; [auto] button picks a free host port; live conflict check (red "port 8080 in use") .
- **Environment:** key/value rows; paste-multiline `.env` support; secret-pattern keys masked after entry.
- **Volumes:** rows: named volume (picker + create-new inline) or bind path (host folder picker) → container path, RW/RO.
- **Network:** dropdown (bridge default + existing networks).
- **Advanced:** restart policy (no / on-failure / unless-stopped / always), command override, user.
Footer: equivalent `docker run …` command preview (CodeBlock, updates live) · [Cancel] [Run]. On success → toast with [View container]; failure → inline error, form preserved.

## 2. Volumes page

Header: search, FilterChips [All · In use · Unused], [+ Create volume…], [Clean up…].
**Create volume modal:** name (validated), driver (local default; free text for plugins), driver options key/value (collapsed), labels (collapsed); [Create] → row appears.

Table: **Name** · **Driver** · **Size** (est., "—" unknown) · **Project** (link) · **Used by** (containers popover) · **Created** · **Mountpoint** (backend path, copy, tooltip "path inside backend") · **Backups** (count → project Backups tab) · actions.
Row actions: Inspect (drawer: facts, labels, usage) · Backup… (plan modal: destination dir display, consistency warning if in use + "stop project first" option) · Restore… (backup picker → target choice existing/new/duplicate → dangerous confirm when overwrite) · kebab(Copy name, Delete…).

Delete volume = **dangerous**: modal "Delete volume \"postgres_data\"? This can permanently remove database files." + typed-name input. In-use volume: delete blocked with explanation + [Stop using containers…] helper unless explicit override checkbox (keeps typed-name).
Clean up: `docker volume prune` — dangerous, typed `prune`.

## 3. Networks page

Header: [+ Create network…]. **Create network modal:** name, driver (bridge default / overlay disabled with tooltip / custom), optional IPAM (subnet CIDR + gateway, validated), Internal + Attachable toggles, labels; [Create].

Table: **Name** · **Driver** · **Scope** · **Subnet** · **Gateway** · **Containers** (count) · **Project** · **Internal** (chip) · actions(Inspect, kebab: Copy ID, Remove…).
Inspect drawer: facts; connected containers table (name link, IP, aliases); IPAM config; options. Default networks (bridge/host/none) marked system, remove disabled with tooltip.
Remove: needs_confirmation; in-use → blocked with container list.
(Topology graph = P2; no placeholder UI in v1.)

## 4. Shared behaviors

All three live-update via `objects:changed`. Empty states: Images "No images yet — pull one or import a project"; Volumes "No volumes — they appear when containers create them"; Networks system-only list is normal (no empty state needed). Stale/degraded watermark when daemon down.

## 5. Tests

E2E: pull with progress + cancel; tag → push to local auth registry (logged-in and not-logged-in paths); remove in-use image flow; volume delete typed-name + audit row; backup→restore round-trip UI; prune wizards previews match executed commands (audit compare, incl. build cache); 500-image seed scroll smoothness.
