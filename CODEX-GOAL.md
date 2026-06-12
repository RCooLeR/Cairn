# GOAL: Build Cairn v1.0 end-to-end

Build **Cairn** — a cross-platform (Windows/macOS/Linux), Compose-first Docker management desktop app — exactly as specified in `dev-docs/`, from empty codebase to a release-ready v1.0.

## Source of truth

`dev-docs/` is the complete, normative specification (33 documents). `codex-docs/` is historical input only — ignore it where it conflicts. Read `dev-docs/README.md` first: it contains the document map, locked tech decisions, conventions, and glossary.

**Locked stack (do not substitute):** Wails v3 (pinned) · **Go 1.26.4 exactly** (`toolchain go1.26.4` in go.mod; CI asserts it) · React 18 + TypeScript strict + Vite + Tailwind · zustand · Recharts · xterm.js · Monaco (read-only) · SQLite via modernc.org/sqlite · Docker Engine Go SDK + official `docker compose` CLI.

## Execution rules

1. **Follow `dev-docs/07-development-plan.md` literally**: Phases 0→10, in order, step by step. Every step has "Do" and "Test" — implement both in the same unit of work. Do not start a phase until the previous phase's **Exit gate** is fully green.
2. **Contracts are law:** all frontend↔backend methods, DTOs, events, and error codes come from `dev-docs/04-api-contracts.md`. TS bindings are generated (`wails3 generate bindings`), never hand-written. The SQLite schema comes from `dev-docs/03-data-model.md`.
3. **Module designs are binding:** before implementing any backend module, read its spec in `dev-docs/modules/` and implement its stated responsibilities, algorithms, edge cases, and test requirements. Same for every screen: `dev-docs/ui/` specifies every element, state, and interaction — including loading/empty/error states, exact confirmation-modal contents, and badge semantics.
4. **Security is non-negotiable:** every mutation goes through the command-plan pipeline (plan → preview → confirm → execute → audit) with the risk mapping in `dev-docs/05-security.md §2`. No destructive action without confirmation; typed-name confirmation for dangerous ones; secrets never persisted by Cairn and always redacted.
5. **Testing per `dev-docs/06-testing.md`:** unit + contract tests on every commit; Docker integration tests against a real daemon; the 14 normative update/lineage/registry cases; performance targets (§7); E2E journeys (§8). Coverage gates: domain core ≥ 80 %, parsers/planners ≥ 95 % branches. Red CI blocks progress.
6. **Platform order:** develop and validate on Linux first (Phases 0–4), then Windows WSL (Phase 5), then macOS Colima (Phase 6). On Windows, ALL local Docker work targets the dedicated **`cairn-dev`** WSL distro (see `dev-docs/README.md` "Development environment (Windows)" and plan step 0.0) — never Docker Desktop, its `docker-desktop*` distros, or the `desktop-linux` context. Where a platform VM is unavailable in your environment, implement fully per spec, keep unit tests green with recorded fixtures, and emit the manual test checklist from `dev-docs/06-testing.md §2` as a TODO report — never skip the implementation.
7. **Branding:** use the existing artwork in `assets/` — `cairn-logo.png` (sidebar, onboarding, About) and `cairn-icon.png` (window/taskbar/dock, installers, favicon). Generate platform icon formats (`.ico`, `.icns`, Linux PNG set) from these at build time. Never generate replacement artwork.
8. **Process:** small commits per step (conventional commits); CI per `dev-docs/08-packaging-release.md §1`; keep a running `PROGRESS.md` mapping plan steps → status. If implementation forces a deviation from dev-docs, update the affected doc in the same change and note it in PROGRESS.md. Ask for human input only when truly blocked (e.g., signing certificates); otherwise choose the spec-compliant option and proceed.

## Definition of done

Every box in the **v1 release checklist** (`dev-docs/06-testing.md §9`) is checked, all 11 exit gates passed, and installers (NSIS, AppImage + deb, dmg) build via CI per `dev-docs/08-packaging-release.md`. The result must satisfy the v1 acceptance criteria in `dev-docs/01-product-spec.md §7`.
