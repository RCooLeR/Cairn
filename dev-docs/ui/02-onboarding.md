# UI 02 — Onboarding & Provider Setup

Shown on first launch, when no healthy provider exists, or via Settings → Providers → "Set up new backend".

## 1. Flow

```text
1 Welcome → 2 Choose backend → 3 Checks → 4 Install missing (plan) → 5 Verify → 6 Detect projects → 7 Done
```
Stepper at top; back navigation allowed until install starts; flow resumable (state persisted) — critical for WSL reboot case.

## 2. Step 1 — Welcome

Logo, one-paragraph promise ("clean control center for Docker & Compose — using the Docker you already trust"), [Get started], link "I already have Docker running" → jumps to existing-context path.

## 3. Step 2 — Choose backend (OS-aware cards)

Windows: **Ubuntu on WSL2** (recommended) · Existing Docker context · Remote host (disabled v1-MVP, tooltip).
Linux: **Native Docker Engine** (recommended) · Existing context · Remote.
macOS: **Colima** (recommended) · Existing Docker context (auto-detected contexts listed inline with ping status) · Remote.
Each card: icon, 2-line description, "what will be installed" expander, Detected/Not detected chip (live from `DetectAll`).

## 4. Step 3 — Checks

Live checklist (each row: spinner→✓/✕/—): per-provider detection items ([modules/02 §4–7]), e.g. WSL installed, Ubuntu present, WSL2, systemd, Docker, Compose, Buildx, daemon ping, permission, disk space ≥ 5 GB, test container run. Failures show `RepairHint` inline. Outcome: all green → skip to 5; fixable gaps → 4; unfixable (e.g. no WSL2 hardware virt) → blocking explanation with docs link.
WSL path-performance note shown here when relevant: recommend `~/projects` inside WSL over `/mnt/c/...` for heavy projects.

## 5. Step 4 — Install missing (CommandPlan UX)

Plan list: every step with its real command (CodeBlock), status (pending/running/done/failed), live output expander. [Install] starts; elevation prompts explained before they appear ("Windows will ask for administrator approval for: Enable WSL"). Failure: step turns red, output shown, [Retry step] [Open docs] [Back]. Reboot-needed (WSL): explicit pause card "Restart Windows, then reopen Cairn — setup will continue automatically."
Linux permission choice (when socket denied): three radio options per [05-security.md §5] with honest consequence text; docker-group option labeled "convenient, less isolated".

## 6. Step 5 — Verify & summary

Result card: Docker version, Compose version, provider/backend, context, socket/host, storage location, resource settings (Colima sliders editable here: CPU/RAM/disk). Hello-world test result. [Continue].

## 7. Step 6 — Detect projects

Auto-detection results ("Found 3 Compose projects"); list with checkboxes (default all); [Add folder…] for manual import; skippable. → Dashboard with success toast.

## 8. Tests

E2E per OS branch with mocked provider statuses: happy path, each failure path renders hint, resume-after-restart (state restore), existing-context shortcut, permission tri-choice persists to settings.
