# Frontend (React / TypeScript)

Coverage: `tsc --noEmit` passes. This branch's UI changes (PortForwardingPanel, SettingsPage
diagnostics section) were read in full; **all ~19,900 lines of `App.tsx`** plus `hooks/`, stores, and
Button/Modal were read by a dedicated reviewer that also cross-checked suspicious behavior against the
Go backend (updates executor, logsvc export, metrics manager). AgentPage/TerminalPage/DataTable remain
uncovered (see [07-misc.md](07-misc.md)).

General quality: the code is disciplined about the classic React failure modes — every `Events.On`
subscription has cleanup, stream identity is gated through refs, cross-render updates use functional
`setState`. The bugs below are state-machine gaps and stale-async races, not systemic sloppiness.

---

<a name="f-4"></a>
## F-4 · HIGH — "Check now" on the Updates page spins and stays disabled forever after the first check

**File:** `frontend/src/App.tsx:2056` (also `1265, 3363, 10113, 10141-10150`), `Button.tsx:46`

```tsx
setUpdateCheckJobID(payload.jobID ?? null);
...
if (payload.done && payload.total && payload.done >= payload.total) {
  setLastUpdateCheckAt(Date.now());
  window.setTimeout(() => setUpdateCheckProgress(null), 1200);   // progress cleared...
  void refreshUpdateSurfaces();                                   // ...but jobID never cleared
}
// UpdatesPage (10113):
const checking = Boolean(checkProgress || checkJobID);
<Button ... loading={checking} onClick={onCheckNow}>Check now</Button>
```

`updateCheckJobID` is set by `checkAllUpdates` and by every `updates:check:progress` event, but **never
reset to `null` anywhere** (grep-verified; the backend payload always carries `jobID`). On completion
only `updateCheckProgress` is cleared, so `checking` stays `true` for the rest of the session and
`Button` treats `loading` as disabled. Concrete scenario: open Updates → click "Check now" once → the
button spins forever until app restart. Bonus: with `total === 0` (no projects) the completion branch
never fires, so the "Checking updates" progress row lingers too.

**Fix:** clear the job in the completion branch
(`setTimeout(() => { setUpdateCheckProgress(null); setUpdateCheckJobID(null); }, 1200)`), and also
clear on a matching `job:done` / when `payload.total === 0`.

---

<a name="f-5"></a>
## F-5 · MEDIUM — failed update/rollback job renders as a green success box; backend error discarded

**File:** `frontend/src/App.tsx:2120-2131` (display at `16049-16058`)

The `job:done` handler stores `payload.result ?? payload.message ?? "done"` and **ignores
`payload.error`**; the modal renders any result in ok-styled green. The updates executor publishes
`result: "failed" | "manual_needed" | "rolled_back"` plus `error: "<detail>"`
(`internal/updates/executor.go:852-861`) — so a failed `docker compose up` shows a green
"Result: failed" box and the actual error text appears nowhere. **Fix:** map `payload.error` into
`state.error`, and tone the result box by result value.

<a name="f-6"></a>
## F-6 · MEDIUM — inspect modal reopens after close / shows the wrong item on out-of-order responses

**File:** `frontend/src/App.tsx:4655-4694` (containers), `4753-4777` (images), `4796-4820` (volumes)

The raw-inspect `.then`/`.catch` callbacks call `setInspect(... open: true ...)` unconditionally.
Close the modal before a WSL-slow `docker inspect` returns → it pops back open; inspect A then B
quickly → the slower response overwrites the modal with the wrong item's data. The adjacent lineage
callback already demonstrates the intended guard
(`current.open && current.subtitle === subtitle ? ... : current`) — these three callbacks just lack it.
**Fix:** apply the same identity guard; never force `open: true` from an async completion.

<a name="f-3"></a>
## F-3 · MEDIUM — stats for stopped/removed containers are never evicted → inflated project CPU/RAM, frozen Overview rows

**File:** `frontend/src/App.tsx:18929-18938` (merge), `2359-2401` (use), `2407-2419` (only reset),
`8340` (Overview), `1470-1477` (re-applied in `refreshProjects`)

```tsx
function mergeStatsSamples(current, samples) {
  const next = { ...current };
  for (const sample of samples) next[sample.containerID] = sample;  // add/overwrite only
  return next;
}
```

The backend prunes stopped containers from its `latest` map and stops publishing them
(`internal/metrics/manager.go:254-264`) — no zeroing sample ever arrives, and the frontend map only
grows (cleared solely when Docker disconnects). Every publish frame feeds **all** merged samples into
`applyStatsSamplesToProjects` / `applyStatsSamplesToProjectDetail`, and `refreshProjects` re-applies
them to fresh data. Concrete: project runs `web` (40% CPU) + `worker` (30%); stop `worker` → project
cards keep reporting ~70% CPU and combined memory indefinitely; the Overview "Container Status" table
keeps showing 30% CPU with frozen uptime for the exited container; `compose down && up` (new IDs)
double-counts. **Fix:** for the "all"-scope stream, replace instead of merge — or evict IDs absent from
the current inventory / absent for N consecutive frames before aggregating.

<a name="f-7"></a>
## F-7 · MEDIUM — Hub search: selecting a result re-triggers the search; in-flight responses never invalidated

**File:** `frontend/src/App.tsx:2153-2192, 2218-2257` (effects), `5883-5891, 5910-5917` (select)

Selecting a result writes the name back into `query`, which re-arms the 300ms debounce effect — the
dropdown deterministically reappears over the completed form (both Pull and Run modals). And cleanup
clears only the timer, not in-flight requests: type "ngin", pause, type "x" — if the first response
resolves last, the list shows "ngin" results under an "nginx" query. **Fix:** skip the search when the
query equals the just-selected name, and stamp requests with a token, applying only the latest.

---

<a name="f-1"></a>
## F-1 · LOW — PortForwardingPanel can stay hidden/stale across provider rebinds

**File:** `frontend/src/components/settings/PortForwardingPanel.tsx:44-60`

`GetStatus` reports `supported: false` both for non-WSL providers **and** while the runtime is
rebinding (`Manager == nil`); the panel refreshes only on mount and on `portforward:changed`. Switch
provider/distro while on the Settings page and the panel stays `null` (or stale) until the section
remounts, since `portforward:changed` fires only when a forward actually changes. Subscribe to
`provider:changed` (and optionally `docker:connected`) as extra refresh triggers. Related: until
backend P-1 is fixed, the On/Off badge is misleading after a restart (the disable was never persisted).

## F-8 · LOW — log-export "Range" selector has no effect (and format is extension-derived)

**File:** `frontend/src/App.tsx:9016-9052, 9792-9807`

`ExportLogs` sends only `{scope, ids, path}` — `ExportLogsRequest` has no range field
(`internal/models/models.go:612-616`) and the backend always exports with `Tail: -1`
(`internal/logsvc/manager.go:119-124`). "Current buffer" vs "tail" changes nothing; a user choosing
"tail" for a small file gets the full dump. Format works only via the path extension. **Fix:** remove
the control or add `tail` to the request and honor it server-side.

## F-9 · LOW — dashboard chart range buttons (5m / 1h / 24h) do nothing

**File:** `frontend/src/App.tsx:7970-7984, 8040-8045, 18865`

`range` only toggles the button highlight; the chart always renders the same rolling ~300-frame
(~10 min) buffer, so "1h"/"24h" silently show the wrong window and 24h of data is impossible (older
points are trimmed). Filter/aggregate by range with down-sampled retention, or remove the selector.

## F-10 · LOW — any settings change resurrects a dismissed app-update notification

**File:** `frontend/src/App.tsx:1878-1905` (with `2605`)

The app-update check effect depends on the whole `appSettings` object; `saveSetting` replaces that
object on every save, so toggling the theme (or anything) re-runs `CheckAppUpdate` and resets
`appUpdateNotificationRead` to `false` — the "Cairn X.Y.Z is available" notification reappears unread
after the user marked all read. **Fix:** depend on the extracted `updates.notify` boolean and only
reset the read flag when `notice.version` changes.

## F-11 · LOW — debounced `objects:changed` refresh dropped when the callback identity changes

**File:** `frontend/src/hooks/useDebouncedRuntimeEvent.ts:10-22` (used at `App.tsx:2008-2012`)

The hook's cleanup discards a pending debounced event; the callback is recreated whenever
`activeProjectID` changes, tearing down and re-registering the subscription. A container action's
`objects:changed` that lands within the 500ms window around opening/closing a project detail is
silently dropped → stale lists until the next event. **Fix:** flush the pending callback in cleanup, or
hold the callback in a ref so the subscription doesn't re-register.

## F-12 · LOW — network detail merges stale snapshot over live inventory

**File:** `frontend/src/App.tsx:15272-15287` (with `4855`)

`{ ...current, ...container }` spreads the *detail-time snapshot* last, so its `state`/`health` beat
the live inventory values, and `openNetworkDetail` skips reloading when a cached detail exists. Stop an
attached container elsewhere → the network's Containers tab still shows a green "running" badge until a
manual refresh. **Fix:** invert the merge for lifecycle fields (`{ ...container, ...current }` keeping
network-specific fields explicitly), or always reload on open.

## F-2 · INFO — diagnostics section notes (new on this branch)

- Field names line up with Go JSON tags (`ageMs` ↔ `AgeMS \`json:"ageMs"\``) — checked.
- `formatDiagnosticDurationMS` floors sub-second ages to "0s"; panel is manual-refresh only — fine.
- The `section === "advanced"` effect clears its bootstrap timeout correctly.

## Verified non-findings (kept to prevent re-work)

- `submitPushImage` setting success right after `ApplyPushImagePlan` is correct — Go's `PushImage`
  blocks until the push completes (`internal/docker/create.go:101-120`).
- UpdatePlan/CommandPlan array fields are defaulted by generated binding constructors — no nil `.map`
  crashes.
- All `Events.On` subscriptions, stream starts, and debounce timers in `App.tsx` have correct cleanup;
  push/pull/stats stream lifecycles use cancelled-flags + refs correctly.
- `data-theme="system"` is handled in CSS.

## Not covered this pass

`frontend/src/agent/AgentPage.tsx`, `frontend/src/components/terminal/TerminalPage.tsx`,
`frontend/src/components/ui/DataTable.tsx`, overview components — the original reviewers for these hit
session usage limits. The terminal page (xterm lifecycle, resize, reconnect) carries the highest
residual risk.
