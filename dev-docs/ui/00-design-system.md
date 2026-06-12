# UI 00 — Design System

Feel: clean, fast, calm, technical, trustworthy, project-first. A polished local control center — not an enterprise console. Visual reference: `codex-docs/ui-ideas/*.png` (dark, teal accent, card grid, soft panels).

## 0. Brand assets (normative)

Use the existing assets from the repo's **`assets/`** folder — do not generate or substitute new artwork:

- `assets/cairn-logo.png` — full logo: sidebar header, onboarding welcome, About page.
- `assets/cairn-icon.png` — icon mark: window/taskbar/dock icon, installer icons, notifications, favicon for the Wails webview.

Derive required platform formats from these sources at build time (`.ico` for Windows, `.icns` for macOS, PNG set for Linux desktop files) — see [../08-packaging-release.md §1]. The accent color token (`--accent`) is sampled from the logo's teal.

## 1. Theme & tokens (Tailwind config + CSS vars)

Dark-first; light theme ships in v1 (toggle in Settings; `system` follows OS).

```text
--bg-app        #0D1117-family deep neutral       --bg-panel    raised surface
--bg-card       card surface (1 step lighter)     --bg-inset    wells/tables
--border        subtle 1px (≈8% white)            --border-strong focus/hover
--text-primary  high contrast                     --text-secondary 60%
--text-muted    40%                               --accent      teal/green (brand, from logo)
--ok #2DD4A7    --warn #F5B83D    --error #F0605D    --info #4D9FFF    --neutral #8B949E
```
Radius: cards 12px, controls 8px, badges 999px. Spacing scale 4px base. Elevation by background steps + 1px borders, almost no shadows. Typography: Inter (UI), JetBrains Mono (code/logs/terminal/digests); sizes 12/13/14/16/20/24.

## 2. Status color semantics (normative everywhere)

green = healthy/running/up-to-date · yellow = warning/checking/update available · red = failed/unhealthy/error/dangerous · gray = stopped/unknown/disabled · blue = informational/actionable.

## 3. Core components

| Component | Key states/notes |
|---|---|
| Button | primary (accent) / secondary (outline) / ghost / danger (red); loading spinner state; disabled with tooltip reason |
| StatusDot | 8px dot + optional pulse for `starting/checking` |
| Badge | status colors; count badges; update badges (§6) |
| Card | header (title, status, kebab menu) / body / footer-actions |
| DataTable | virtualized (≥100 rows), sortable columns, column show/hide, sticky header, row hover actions, multi-select checkboxes, bulk-action bar appears on selection |
| Tabs | underline style; lazy-render panes; keyboard arrows |
| Modal | sizes sm/md/lg; danger variant (red header accent); focus trap; Esc closes (not during running job) |
| ConfirmModal | effects list, command preview block (§5), risk banner, optional typed-name input that enables Confirm only on exact match |
| Toast | 4 levels, auto-dismiss 5s (errors sticky), action link slot |
| EmptyState | icon + title + body + primary action ([ui pages define copy]) |
| SearchInput | debounced 150ms, `/` shortcut focuses, clear button |
| FilterChips | single/multi select chip row, count per chip |
| Sparkline | 60-sample mini line, no axes |
| ChartPanel | line/area charts with time-range picker (5m/1h/24h/7d), hover tooltip with exact values, restrained colors (one hue per series) |
| Donut | disk usage / status distribution |
| CodeBlock | mono, copy button, optional syntax highlight (yaml/json), used for command previews |
| Monaco viewer | read-only in v1; yaml/json highlight; find widget |
| LogViewer / Terminal | specialized, see ui/07, ui/08 |
| KeyValueGrid | inspect-style 2-col fact sheets |
| Skeleton | shimmer placeholders for all async panels |

## 4. Interaction rules

- Every async action button: ≤ 100 ms feedback (spinner/disable). Optimistic UI only for start/stop toggles with rollback on error.
- Row actions: hover-revealed icon buttons + kebab overflow; all also in context menu (right-click) and command palette.
- Keyboard: `/` search, `Ctrl/Cmd-K` palette, `Esc` close, arrows in tables/tabs, `Enter` default action. Visible focus rings.
- Destructive affordances always red and physically separated from safe ones (menu bottom group).
- Tooltips on truncation and on all icon-only buttons (300 ms delay).
- Numbers: bytes humanized (KiB/MiB/GiB), rates `/s`, percents 1 decimal; digests truncated `sha256:ab12…ef89` with copy-on-click; timestamps relative (<24 h) with absolute tooltip.

## 5. Command preview block (trust pattern)

Used in every plan/confirm modal and pre-action toasts:

```text
┌ Commands ────────────────────────────── [copy] ┐
│ $ docker compose pull api                      │
│ $ docker compose up -d api                     │
│ workdir: ~/stacks/apps      context: colima    │
└────────────────────────────────────────────────┘
```
Mono font, `$` prefix, workdir+context footer, copy copies plain commands.

## 6. Update badges (normative set)

`Image update` (yellow) · `Base update` (yellow, layered-square icon) · `Rebuild needed` (orange-yellow, hammer icon) · `Pinned digest` (gray, pin) · `Unknown base` (gray, ?) · `Up to date` (green, check) · `Checking…` (pulse) · `Auth required` / `Rate limited` / `Error` (red/info as per §2). Badges always show counts when aggregated (e.g. `2 image · 1 base · 1 rebuild`).

## 7. Charts

One accent hue per metric family (CPU teal, memory violet, net rx/tx paired blues, disk amber). Grid lines at 10% opacity. Live charts roll left, 1 s cadence, max 300 visible points (downsampled). Pause-on-hover with crosshair tooltip.

## 8. Loading / error / empty triad

Every data panel implements all three: Skeleton → content | EmptyState | InlineError (message + retry + details expander with `AppError.Detail`). No infinite spinners: 15 s → timeout error state.

## 9. Accessibility baseline

Contrast ≥ 4.5:1 for text (verify accent on dark); all interactive elements keyboard-reachable; aria-labels on icon buttons; status conveyed by icon+text, never color alone; reduced-motion media query disables pulses/chart animations.
