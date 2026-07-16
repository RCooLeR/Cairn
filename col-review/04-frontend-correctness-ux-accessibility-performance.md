# Frontend Correctness, UX, Accessibility, Performance, and Test Review

- Review date: 2026-07-15
- Scope: `frontend/` plus the directly coupled log-export/backend command-tracking code needed to verify frontend contracts.
- Method: static code review, targeted source tracing across Wails bindings/events, and execution of the existing frontend validation commands. No application source files were modified.

> Line numbers refer to the reviewed working tree, which already contained uncommitted changes when the review began. They may move as those changes are edited.

## Executive summary

The frontend is in materially better shape at compile time than at runtime: strict TypeScript, ESLint, the unit suite, the production build, and the default release-UI suite all pass. The largest risks are not syntax or ordinary rendering errors. They are asynchronous operation identity, stale-response handling, long-lived Wails stream lifecycle, destructive-action target integrity, and accessibility behavior that the current automated checks either do not exercise or explicitly suppress.

The most urgent items are:

1. Prevent stale project, Agent, restore, and provider-setup responses from becoming actionable under a different target.
2. Preserve last-known-good inventory slices instead of replacing them with empty fallbacks on partial failure.
3. Make log streaming, pause semantics, wrapping, and export behavior match the UI contract.
4. Surface terminal RPC failures and correctly own long-running Agent/terminal requests across navigation.
5. Correct muted-text contrast, keyboard tab behavior, command-palette focus containment, and the mouse-only DataTable column menu.
6. Split the 19k-line root component and isolate high-frequency metric/log state to reduce rerender and bundle costs.

No compile-time Wails binding mismatch was found: the current tree type-checks. The binding/event risk is instead at runtime boundaries where payloads are asserted without validation.

## Severity and classification

- **Critical:** Can direct a destructive or costly operation at the wrong target, or cause severe data/control integrity loss.
- **High:** Material correctness, operability, accessibility, or reliability failure in a plausible normal workflow.
- **Medium:** Important robustness, performance, UX, or standards gap with narrower impact or stronger preconditions.
- **Low:** Defense-in-depth, maintainability, polish, or latent risk.
- **Confirmed defect:** The faulty state transition or mismatch is directly present in the code and can be triggered deterministically.
- **Risk:** The implementation lacks a required guard; impact depends on timing, environment, or backend behavior.
- **Gap:** Missing coverage, validation, or architecture rather than an immediately incorrect output.

## Detailed findings

### FE-001 — Project-detail responses can display and operate on the wrong project

- **Classification:** Confirmed defect
- **Severity:** Critical
- **Confidence:** High
- **Evidence:**
  - `frontend/src/App.tsx:1494-1516` commits every `GetProjectDetail` completion without checking that the requested project is still active.
  - `frontend/src/App.tsx:1625-1633` changes `activeProjectID` and starts the new request without clearing or invalidating the previous `projectDetail`.
  - `frontend/src/App.tsx:4994-5008` continues rendering `detailProject` while the new project is loading.
  - `frontend/src/App.tsx:5031-5045` derives the project ID passed to actions from the detail object, so the stale object remains actionable.
- **Impact / reproduction:** Open project A, then quickly open B while delaying A's detail response. Until B resolves, the page can show A under B's selection. If A resolves after B, A can remain displayed. Update, build, up/down, or related actions can then target A while the operator believes B is selected.
- **Recommendation:** Represent detail state as `{ requestedID, status, data, error, generation }`; invalidate the old data immediately; ignore completions whose ID/generation is not current; render data only when `data.summary.id === activeProjectID`; and disable all project actions while that invariant is false.
- **Regression test:** Use deferred promises for A and B. Resolve B then A and A then B, plus reject each request. Assert that only B is ever actionable after selection changes and that action mocks always receive B.

### FE-002 — Agent analysis/draft/preview results can cross project boundaries

- **Classification:** Confirmed defect
- **Severity:** Critical
- **Confidence:** High
- **Evidence:**
  - Analysis results commit unconditionally at `frontend/src/agent/AgentPage.tsx:289-319`.
  - Draft, preview, and apply operations have no immutable project/path operation key at `frontend/src/agent/AgentPage.tsx:333-413`.
  - The project selector remains enabled at `frontend/src/agent/AgentPage.tsx:653-663`.
  - Configuration inputs remain editable while an operation is in flight at `frontend/src/agent/AgentPage.tsx:939-969`.
- **Impact / reproduction:** Start a draft or preview for project A, switch to B, then allow A's request to resolve. A's content/plan is placed into the state currently presented as B. Applying the stale plan can modify A while the page says B, or show A-derived content in B's editor.
- **Recommendation:** Assign each operation an immutable `{ operationID, projectID, filePath }`; commit only if all three still match; disable target-changing controls during mutations; and display the immutable project/path carried by a plan in the confirmation UI rather than the current selector value.
- **Regression test:** Delay every Agent RPC, switch projects and paths before completion, and verify stale completions neither alter the visible editor nor enable Apply. Assert the applied plan belongs to the visibly confirmed target.

### FE-003 — Restore-volume backup loading can populate a different volume's modal

- **Classification:** Confirmed defect
- **Severity:** High
- **Confidence:** High
- **Evidence:**
  - `frontend/src/App.tsx:4281-4315` loads backups for a captured `volume`, then merges the response into whatever restore state is current.
  - `frontend/src/App.tsx:6103-6111` allows the modal to be closed/reset while loading.
  - `frontend/src/App.tsx:17064-17076` passes only `state.busy` to `Modal`; `state.loading` does not prevent close/reopen.
- **Impact / reproduction:** Open restore for A, close it before `ListBackups(A)` resolves, then open B. When A resolves, A's backups appear in B's modal. The user can choose an archive under the false impression it belongs to B and create an unintended overwrite plan.
- **Recommendation:** Capture a request generation and volume name, and only commit when both still match the current modal. Cancel obsolete loads when possible. Treat loading separately from mutation busy state and clearly label intentional cross-volume restores.
- **Regression test:** Use delayed A/B backup calls with modal close/reopen and reversed completion order. Confirm that B never renders A's list and that stale errors do not replace B's status.

### FE-004 — Provider installation can continue after its UI/session is discarded

- **Classification:** Confirmed lifecycle defect
- **Severity:** High
- **Confidence:** High
- **Evidence:**
  - `ProviderSetupModal` does not pass `busy` to the shared modal at `frontend/src/App.tsx:6617-6623`, even when `setup.installing` is true.
  - `frontend/src/App.tsx:3110-3114` handles close by clearing `providerInstallSessionRef`, deleting persisted setup state, and resetting the modal.
  - The backend installation starts at `frontend/src/App.tsx:3293-3338` and is not cancelled by close.
  - Progress-event filtering relies on the cleared session at `frontend/src/App.tsx:1737-1778`.
- **Impact / reproduction:** Start auto-repair/install and press Escape or Close. The backend continues changing the system, but the progress owner and recovery state are removed. Reopening setup can begin another flow, and the user no longer has an accurate view of the active install.
- **Recommendation:** Either disallow closing while non-cancellable provider work is active or explicitly support background operation with a durable job record and reattachment UI. Do not clear the session until the backend confirms completion/cancellation. Persist stream/job identity independently of modal visibility.
- **Regression test:** Start apply, close via button and Escape, reopen, and emit progress/completion events. Verify the active job remains visible, cannot be duplicated, and reaches a recoverable terminal state.

### FE-005 — Provider setup responses are not correlated with the setup session/backend

- **Classification:** Confirmed race risk
- **Severity:** High
- **Confidence:** High
- **Evidence:**
  - Update/repair planning commits into current setup state at `frontend/src/App.tsx:3025-3099`.
  - Provider checks commit without verifying captured backend/session at `frontend/src/App.tsx:3151-3217`.
  - Install planning does the same at `frontend/src/App.tsx:3228-3282`.
  - Project detection does the same at `frontend/src/App.tsx:3340-3374`.
  - Post-install `Detect` completion updates current setup at `frontend/src/App.tsx:1776-1798`.
- **Impact / reproduction:** Close/reopen setup or change backend while a check/plan/detection call is pending. The old result can populate the new flow, advance the wrong step, or make a plan for a backend other than the one currently shown.
- **Recommendation:** Give each setup opening and backend change a generation. Attach `{ sessionID, backend, planID }` to every operation and reject non-matching completions, including error paths. Clear or cancel old calls on backend switch.
- **Regression test:** Deferred calls for every setup step; close/reopen and change Linux/WSL/Colima selections before completion; assert no stale response changes the new session.

### FE-006 — Agent chat persists across navigation but request ownership does not

- **Classification:** Confirmed lifecycle defect
- **Severity:** High
- **Confidence:** High
- **Evidence:**
  - Conversation messages and prompt are module-global at `frontend/src/agent/AgentPage.tsx:76-128`.
  - `sending`, request handles, and stop state are component-local at `frontend/src/agent/AgentPage.tsx:136-163`.
- **Impact / reproduction:** Send a message, navigate away, and return before it finishes. The request can continue appending to global conversation state, but the remounted page reports `sending=false`, has no Stop handle, and allows a second request. Responses may interleave and model usage can be duplicated.
- **Recommendation:** Move request status, request identity, and cancellation ownership into the durable Agent store. Alternatively, explicitly cancel and invalidate on unmount. Enforce one active conversation request per conversation ID.
- **Regression test:** Navigate away/back during streaming, then exercise Stop and Send. Assert there is one active request, ordering remains deterministic, and the stop control always owns it.

### FE-007 — A partially failed full inventory refresh erases last-known-good slices

- **Classification:** Confirmed defect
- **Severity:** High
- **Confidence:** High
- **Evidence:**
  - `frontend/src/api/inventory.ts:32-65` catches each slice failure and substitutes `[]` or `null`.
  - `frontend/src/state/inventoryStore.ts:85-110` writes every fallback over the existing store in one snapshot commit.
- **Impact / reproduction:** Seed containers/images/volumes, then make only `ListContainers` fail. The refresh still returns a snapshot, but containers are replaced with an empty list, removing visible resources and action context despite having valid cached data.
- **Recommendation:** Return per-slice settled results. Merge only fulfilled values and retain failed slices with `{ stale: true, error, lastSuccessAt }`. Report multiple slice errors rather than only `firstError`.
- **Regression test:** Fail each inventory API independently and in combinations. Verify successful slices update, failed slices remain, and the banner identifies exactly which data is stale.

### FE-008 — Partial inventory refreshes race and share a misleading global status

- **Classification:** Confirmed design defect
- **Severity:** High
- **Confidence:** High
- **Evidence:**
  - Independent, unsequenced refresh functions are at `frontend/src/state/inventoryStore.ts:125-155`.
  - Any successful slice sets the whole inventory to ready and clears the whole error at `frontend/src/state/inventoryStore.ts:172-181`.
  - Any failed slice marks the whole inventory as error at `frontend/src/state/inventoryStore.ts:184-192`.
  - Only full refresh has a deduplication promise at `frontend/src/state/inventoryStore.ts:61,78-123`; partial and full calls are not coordinated.
- **Impact / reproduction:** A slow older event refresh can overwrite a newer action result. A successful image refresh can clear a current container error, or a network failure can mark unrelated container/image data globally failed.
- **Recommendation:** Use per-slice request generations and status/error metadata. Coordinate full and partial refresh versions so older results cannot overwrite newer authoritative state.
- **Regression test:** Resolve overlapping full/partial calls in reverse order and assert the newest result wins per slice; independently fail two slices and verify both errors remain represented.

### FE-009 — Initial log lines can be dropped before the stream ID ref updates

- **Classification:** Confirmed race defect
- **Severity:** High
- **Confidence:** High
- **Evidence:**
  - Log events are accepted only when they match `streamIDRef` at `frontend/src/App.tsx:8841-8873`.
  - `StartLogStream` sets React state, but not the ref, at `frontend/src/App.tsx:8924-8931`.
  - The ref is synchronized later by an effect at `frontend/src/App.tsx:8809-8811`.
- **Impact / reproduction:** If the backend emits initial tail lines immediately after returning the stream handle, those events arrive before React renders and runs the effect, so they are discarded.
- **Recommendation:** Set `streamIDRef.current` synchronously in the start promise before setting state. Clear it synchronously before stop/scope changes. Prefer a generation-keyed central stream state.
- **Regression test:** Make `StartLogStream` resolve and emit a matching event in the same microtask/turn. Assert the first line is retained.

### FE-010 — Paused/unpinned log counters fail once the 50k cap is reached

- **Classification:** Confirmed defect
- **Severity:** High
- **Confidence:** High
- **Evidence:**
  - `frontend/src/App.tsx:8851-8855` caps the buffer by slicing it back to 50,000 records.
  - Pause tracking uses array-length baselines at `frontend/src/App.tsx:8950-8959`.
  - Unpinned tracking also uses length at `frontend/src/App.tsx:9009-9012`.
- **Impact / reproduction:** Fill the buffer to 50,000 and pause or scroll away from the tail. New lines replace old lines while length remains constant, so the UI reports zero new lines. The supposedly paused view can silently change as its oldest records are evicted.
- **Recommendation:** Track monotonic received/dropped sequence numbers. Freeze a sequence range or immutable snapshot for paused display and calculate unread counts from sequence deltas, not array length.
- **Regression test:** Stream beyond 50,000 while paused and unpinned. Assert unread counts increase and the paused visible sequence does not mutate unexpectedly.

### FE-011 — “Current buffer/current filters” log export exports different data

- **Classification:** Confirmed frontend/backend contract defect
- **Severity:** High
- **Confidence:** High
- **Evidence:**
  - The modal calls the option “current buffer” and displays active filter context at `frontend/src/App.tsx:9908-9934`.
  - Submission sends scope, IDs, path, and only an optional tail count at `frontend/src/App.tsx:9120-9128`; query, source, and level filters are not sent.
  - Undefined tail is interpreted as all available Docker history at `internal/logsvc/manager.go:119-129`, rather than the current client buffer.
- **Impact / reproduction:** Apply level/source/search filters, choose current buffer, and export. The result can contain the complete unfiltered history and be far larger or more sensitive than the user was told.
- **Recommendation:** Either export the actual client buffer after filters or extend the API to transmit filter/buffer semantics precisely. If the intended behavior is all history, rename and explain it before writing.
- **Regression test:** Seed known history and a capped/filtered client buffer. Assert exact exported lines for tail, buffer, search, source, level, paused, and empty cases.

### FE-012 — Log “wrap” mode still clips content to a fixed-height virtual row

- **Classification:** Confirmed defect
- **Severity:** High
- **Confidence:** High
- **Evidence:**
  - Wrap mode still selects a fixed 44 px row height at `frontend/src/App.tsx:8996-9008`.
  - `LogRow` uses absolute positioning, fixed height, and overflow clipping at `frontend/src/App.tsx:9797-9839`.
- **Impact / reproduction:** Enable wrap and display a long or multiline log entry. Only the portion fitting approximately 44 px is visible; diagnostic content remains hidden despite the control claiming it is wrapped.
- **Recommendation:** Use measured variable-height virtualization, or disable virtualization in wrap mode behind a documented safe row limit. Provide an expandable full-line detail view as a fallback.
- **Regression test:** Render very long and multiline records in both modes and assert the complete text is visually reachable in wrap mode.

### FE-013 — Terminal writes, pastes, scheduled commands, and resize failures are unhandled

- **Classification:** Confirmed defect
- **Severity:** High
- **Confidence:** High
- **Evidence:**
  - `sendInput` rejects on backend failure at `frontend/src/components/terminal/TerminalPage.tsx:470-480`.
  - XTerm input discards that promise at `frontend/src/components/terminal/TerminalPage.tsx:1252-1254`.
  - Scheduled writes are invoked with `void` at `frontend/src/components/terminal/TerminalPage.tsx:541-565`.
  - Confirmed paste closes its dialog before a fire-and-forget write at `frontend/src/components/terminal/TerminalPage.tsx:942-973`.
  - Resize RPCs are also fire-and-forget at `frontend/src/components/terminal/TerminalPage.tsx:1266-1268`.
- **Impact / reproduction:** Close a session or make `WriteTerminal` reject while typing/pasting. The promise becomes unhandled and the data disappears without UI feedback. Confirmed paste cannot be retried because the dialog is already cleared.
- **Recommendation:** Route terminal operations through an error-reporting wrapper; retain confirmation state until writes succeed; classify expected stale-session failures; and surface actionable reconnect/close status.
- **Regression test:** Reject write/resize calls during typing, paste, scheduling, and teardown. Assert there are no unhandled rejections and the user gets an appropriate recoverable status.

### FE-014 — Rapid terminal-close events can select a session that has also closed

- **Classification:** Race risk
- **Severity:** Medium
- **Confidence:** High
- **Evidence:**
  - The `terminal:closed` subscription depends on and closes over `sessions` at `frontend/src/components/terminal/TerminalPage.tsx:254-275`.
  - Manual close repeats closure-based next-session selection at `frontend/src/components/terminal/TerminalPage.tsx:442-455`.
- **Impact / reproduction:** Deliver two close events before React commits the first update. Both handlers calculate from the same old session array and can set `activeSessionID` to a session removed by the other event.
- **Recommendation:** Use a reducer or one functional state transition that removes the session and derives the next valid active ID from the resulting array. Keep session map and active ID in one atomic state object.
- **Regression test:** Emit simultaneous close events for active and neighboring tabs and verify the final active ID always exists or is null.

### FE-015 — Settings-load failure is represented as successfully loaded defaults

- **Classification:** Confirmed error-state defect
- **Severity:** High
- **Confidence:** High
- **Evidence:** `frontend/src/App.tsx:1676-1724` catches settings-load failure, installs default values, and marks the load complete without exposing a load error/retry state.
- **Impact / reproduction:** Make the initial settings RPC fail transiently. The UI displays plausible defaults as though they came from the backend. A user can edit/save them and overwrite valid persisted configuration they never saw.
- **Recommendation:** Model `idle/loading/ready/error/stale` explicitly. Retain last-known-good values, mark unknown fields, show retry, and disable saving settings that were never successfully read unless the user deliberately chooses a reset-to-defaults action.
- **Regression test:** Fail initial and refresh loads with existing backend values. Verify defaults are not presented as authoritative and saving remains guarded until load or explicit reset.

### FE-016 — Settings saves can overlap, duplicate, and clear busy state out of order

- **Classification:** Confirmed defect
- **Severity:** High
- **Confidence:** High
- **Evidence:**
  - A single global `settingsSaving` boolean covers independent saves at `frontend/src/App.tsx:2653-2720`.
  - Text settings save on Enter but remain eligible to save again on blur at `frontend/src/settings/SettingsPage.tsx:1671-1703`.
  - Number settings have the same pattern at `frontend/src/settings/SettingsPage.tsx:1707-1754`.
  - Agent endpoint Enter calls save and then `.blur()`, triggering its blur save immediately afterward at `frontend/src/agent/AgentPage.tsx:627-638`.
- **Impact / reproduction:** Press Enter in the Agent endpoint or a settings field. Two writes can overlap. If the older request finishes first, its `finally` sets global saving false while the newer request remains active; reversed successes/errors can display stale status and persist a value different from the visible one.
- **Recommendation:** Use per-setting operation generations and keyed pending state, or at minimum a pending counter. Centralize commit logic so Enter marks the draft clean before blur. Roll back optimistic local values on rejection or refetch the authoritative value.
- **Regression test:** Deferred duplicate and multi-field saves resolved in every order. Assert one write per commit, correct busy state until all operations finish, and last-intent-wins status/value.

### FE-017 — Settings diagnostics present unknown/error data as real zero/disabled values

- **Classification:** Confirmed defect
- **Severity:** Medium
- **Confidence:** High
- **Evidence:**
  - Diagnostics refreshes have no request generation or cancellation at `frontend/src/settings/SettingsPage.tsx:286-318`; effect cleanup only clears a zero-delay timer, not the in-flight RPC.
  - `frontend/src/settings/SettingsPage.tsx:752-813` coalesces absent values into `0`/`false` and renders claims such as metrics stopped/native networking state.
  - Docker shim status has the same overlapping-refresh pattern at `frontend/src/settings/SettingsPage.tsx:254-270`.
- **Impact / reproduction:** On first load or refresh failure, the panel presents a diagnosis rather than “unknown.” Clicking refresh repeatedly can let an older result or error overwrite the newest one.
- **Recommendation:** Render separate loading, unknown, stale, ready, and error states; retain the last successful sample with timestamp; generation-key every refresh; and label unavailable values as unavailable rather than false/zero.
- **Regression test:** Delay and reverse multiple refreshes; reject the initial request; verify no diagnostic claim is shown until supported by a successful response.

### FE-018 — Port-forwarding errors are hidden by an early null return

- **Classification:** Confirmed defect
- **Severity:** High
- **Confidence:** High
- **Evidence:**
  - Status/error loading is implemented at `frontend/src/components/settings/PortForwardingPanel.tsx:31-63`.
  - The component returns `null` whenever status is absent or unsupported at `frontend/src/components/settings/PortForwardingPanel.tsx:65-68`.
  - Its error UI at `frontend/src/components/settings/PortForwardingPanel.tsx:116-120` is therefore unreachable after an initial status failure.
- **Impact / reproduction:** Make the first port-forwarding status request reject. The entire section disappears with no error, unsupported explanation, or retry control.
- **Recommendation:** Distinguish `loading`, `error`, `unsupported`, and `ready`. Always render a section shell for loading/error, with retry and the failed operation's message.
- **Regression test:** Cover initial failure, unsupported response, refresh failure after success, and recovery through Retry.

### FE-019 — Update progress uses an untracked completion timer that can clear a new job

- **Classification:** Confirmed race risk
- **Severity:** Medium
- **Confidence:** High
- **Evidence:** `frontend/src/App.tsx:2054-2089` clears the active job ID on completion and schedules an untracked 1.2-second timeout that later clears progress without checking which job is now active.
- **Impact / reproduction:** Finish update check A and start B within 1.2 seconds. A's timer can clear B's progress/status.
- **Recommendation:** Store the timeout in a ref, clear it when a new job starts and on unmount, and capture/verify the completing job ID before clearing UI.
- **Regression test:** Fake timers: complete A, start B before the delay, advance time, and assert B remains untouched.

### FE-020 — Audit-log and notification refreshes accept obsolete responses

- **Classification:** Race risk
- **Severity:** Medium
- **Confidence:** High
- **Evidence:**
  - Audit refresh/filter loading commits without generation checks at `frontend/src/App.tsx:1862-1884`.
  - Notification refresh and mark-all flows can overlap at `frontend/src/App.tsx:1807-1860`.
- **Impact / reproduction:** Change the audit filter twice while the first call is slow; the first response can replace the second. A notification refresh racing with mark-all can restore an obsolete unread list/count.
- **Recommendation:** Attach a generation to filtered reads, serialize or merge notification mutations, and invalidate/refetch after mutation with last-intent-wins semantics.
- **Regression test:** Resolve filtered reads and refresh/mark-all operations in reverse order; assert visible state matches the latest filter and mutation.

### FE-021 — Obsolete metrics start failures can restore stale errors

- **Classification:** Confirmed race defect
- **Severity:** Medium
- **Confidence:** High
- **Evidence:** At `frontend/src/App.tsx:2462-2500`, the metrics start success path checks `cancelled`, but the rejection path does not.
- **Impact / reproduction:** Start metrics, then change engine/provider state so the effect cleans up before the promise rejects. The obsolete catch can set a metrics error after the newer state already cleared it.
- **Recommendation:** Check an effect generation/cancel flag in both success and failure paths. Prefer a reducer keyed by stream generation.
- **Regression test:** Unmount/change provider before a deferred start rejects and verify no obsolete error is rendered.

### FE-022 — Stream stop operations discard cleanup failures

- **Classification:** Reliability risk
- **Severity:** Medium
- **Confidence:** High
- **Evidence:** Stop promises are invoked with `void` and no catch in Overview, Logs, and Metrics at `frontend/src/App.tsx:7641-7656`, `frontend/src/App.tsx:8925-8946`, and `frontend/src/App.tsx:2480-2498`.
- **Impact / reproduction:** If a Wails stop RPC rejects during route change, the rejection may be unhandled and the backend stream may remain active without any diagnostic or retry. Late events can then compete with a newer stream.
- **Recommendation:** Create an idempotent best-effort stream cleanup helper that invalidates the local generation synchronously, catches/logs stop failures, and allows backend-side stale-stream cleanup.
- **Regression test:** Reject each stop call during route/scope/provider changes. Assert no unhandled rejection, old events are ignored, and a new stream starts cleanly.

### FE-023 — Debounced event-hook cleanup invokes application work during teardown

- **Classification:** Confirmed lifecycle defect
- **Severity:** Medium
- **Confidence:** High
- **Evidence:** `frontend/src/hooks/useDebouncedRuntimeEvent.ts:15-37` invokes the pending callback inside effect cleanup before clearing the timer and unsubscribing.
- **Impact / reproduction:** Emit an inventory/provider event and unmount or change event name/delay before the debounce expires. Cleanup triggers the refresh callback while the consumer is tearing down, creating unexpected backend calls and state writes.
- **Recommendation:** Cleanup should cancel pending work by default. If flushing is required for a specific consumer, expose an explicit `flushOnCleanup` option and document the consequence.
- **Regression test:** Fake timers around unmount and dependency change. Assert default cleanup never calls the callback, while an explicit flush mode does so once.

### FE-024 — Bulk selections can retain resources that are no longer visible or present

- **Classification:** Correctness risk
- **Severity:** Medium
- **Confidence:** Medium-High
- **Evidence:** Selection sets are not reconciled with authoritative row changes; bulk actions consume the stored IDs at `frontend/src/App.tsx:3687-3723`.
- **Impact / reproduction:** Select resources, change filter or allow inventory/event updates to remove rows, then execute a bulk action. The request can include hidden or deleted IDs, and the user cannot see the complete target set at confirmation time.
- **Recommendation:** Reconcile selections when authoritative inventory changes, or explicitly preserve them while showing “N total / M visible” and enumerate all targets in confirmation. Remove IDs confirmed deleted.
- **Regression test:** Select rows, filter them out and delete one through an event, then assert confirmation and backend payload contain only the documented target set.

### FE-025 — Log export's “Open folder” action only copies a path

- **Classification:** Confirmed UX defect
- **Severity:** Medium
- **Confidence:** High
- **Evidence:** The post-export action labelled “Open folder” calls clipboard copy rather than a shell/folder-open API at `frontend/src/App.tsx:9135-9144`; failure/success feedback is absent. The default export path can also remain relative around `frontend/src/App.tsx:9335-9339`.
- **Impact / reproduction:** Complete an export and select Open folder. No folder opens; the user receives no explanation that the path was copied. A relative path can make the actual destination ambiguous.
- **Recommendation:** Use a host API that reveals the containing folder and label clipboard fallback as “Copy path.” Resolve and display an absolute destination before write, with success/failure feedback.
- **Regression test:** Mock successful/failed reveal and clipboard APIs and assert label, behavior, and displayed absolute path match.

### FE-026 — Notification rows expose no-op buttons and do not mark viewed items read

- **Classification:** Confirmed UX defect
- **Severity:** Medium
- **Confidence:** High
- **Evidence:** `frontend/src/components/notifications/NotificationCenter.tsx:85-130` renders every notification as a button even when it has no target. Target navigation does not mark the individual item read; only mark-all exists. The popover also remains open after unrelated outside interaction.
- **Impact / reproduction:** Keyboard or mouse users activate a targetless notification and nothing happens. Navigating a real notification leaves its unread indicator/count unchanged, undermining notification-state trust.
- **Recommendation:** Render targetless notifications as non-button content; mark an item read when activated (optimistically with rollback); close and restore focus on navigation/outside dismissal.
- **Regression test:** Cover targetless, target, read, unread, mark failure, outside click, and keyboard dismissal paths.

### FE-027 — Agent streaming always steals scroll from users reading history

- **Classification:** Confirmed UX defect
- **Severity:** Medium
- **Confidence:** High
- **Evidence:** `frontend/src/agent/AgentPage.tsx:210-216` unconditionally scrolls the transcript to the bottom whenever messages or sending state change.
- **Impact / reproduction:** Scroll upward during a long response. Every streamed update jumps back to the newest content, making earlier material effectively unreadable until generation ends.
- **Recommendation:** Auto-follow only if the user is already within a small threshold of the bottom. Otherwise preserve position and show a “new messages”/jump-to-latest control.
- **Regression test:** Simulate streaming while at bottom and while scrolled up; assert follow occurs only in the first case.

### FE-028 — Search-match selection can become out of range as live logs change

- **Classification:** Confirmed state-consistency defect
- **Severity:** Medium-Low
- **Confidence:** High
- **Evidence:** `activeMatch` is reset on query/filter dependency changes at `frontend/src/App.tsx:8820-8822`, but not when live-buffer eviction or new data changes the number/order of matches.
- **Impact / reproduction:** Select a late match, then let capped-buffer eviction remove matches. The counter can report an invalid index and no row may be highlighted until the user cycles again.
- **Recommendation:** Clamp or remap active match whenever the match list changes, preferably by stable log sequence ID rather than array index.
- **Regression test:** Add and evict matching lines around the active result and assert the selection remains valid or resets predictably.

### FE-029 — DataTable virtual window state survives same-length dataset replacement

- **Classification:** Confirmed UX/state defect
- **Severity:** Medium
- **Confidence:** High
- **Evidence:**
  - Virtual-window identity uses only `rows.length` and sort state at `frontend/src/components/ui/DataTable.tsx:79-84`.
  - Scroll reset similarly depends only on row count/sort at `frontend/src/components/ui/DataTable.tsx:270-274`.
- **Impact / reproduction:** Scroll deep into a table, then switch to a different filter/scope that returns the same number of rows. The new dataset opens at the old offset, hiding its beginning and giving no visual indication of the retained position.
- **Recommendation:** Include a caller-provided dataset/query identity in virtual state, clamp the offset on every row replacement, and define when filter changes reset versus preserve scroll.
- **Regression test:** Replace a scrolled dataset with a distinct same-length dataset and assert the documented reset behavior.

### FE-030 — Port-forwarding table is likely to clip at narrow widths

- **Classification:** Responsive-layout risk
- **Severity:** Low-Medium
- **Confidence:** Medium
- **Evidence:** `frontend/src/components/settings/PortForwardingPanel.tsx:132` wraps a five-column table in `overflow-hidden` rather than horizontal scrolling or a responsive card layout.
- **Impact / reproduction:** At compact/mobile widths, later columns/actions can be clipped with no way to reach them. The current release test's narrowest viewport is still 1260 px, so this path is not exercised.
- **Recommendation:** Use `overflow-x-auto` with minimum table width, or switch to stacked rows below a breakpoint. Keep action controls reachable without horizontal page overflow.
- **Regression test:** Visual and interaction coverage at 320, 375, 768, and 1024 px with long addresses/labels.

### FE-031 — Muted text fails WCAG AA contrast in both themes

- **Classification:** Confirmed accessibility defect
- **Severity:** High
- **Confidence:** High
- **Evidence:**
  - `frontend/tailwind.config.js:15-16` implements `text-secondary` and `text-muted` by forcing alpha `.68` and `.44` onto theme colors.
  - Light tokens are defined at `frontend/src/styles/index.css:62-64`; dark tokens are at `frontend/src/styles/index.css:12-14` and `frontend/src/styles/index.css:100-102`.
  - Compositing the `.44` muted color gives approximately 2.09:1 on the light panel, 2.01:1 on light inset, 4.11:1 on the dark panel, and 4.11:1 on dark inset. All are below WCAG AA's 4.5:1 requirement for normal text.
  - Muted text is widely used at `text-xs`/`text-sm`, so the large-text exception does not apply.
- **Impact / reproduction:** Labels, timestamps, help text, empty states, and secondary diagnostic information are substantially harder to read, especially in light mode and for low-vision users.
- **Recommendation:** Replace opacity-derived text colors with opaque semantic tokens tested against every supported surface. Target at least 4.5:1 for ordinary text and keep disabled-state semantics separate from informational muted text.
- **Regression test:** Re-enable Axe color-contrast checks, add token-level contrast assertions for every theme/surface pair, and include visual checks for high-contrast/forced-colors mode.

### FE-032 — Axe explicitly disables the rule that would detect FE-031

- **Classification:** Test gap
- **Severity:** High
- **Confidence:** High
- **Evidence:** `frontend/e2e/release-ui.spec.mjs:359-372` disables Axe's `color-contrast` rule for route accessibility checks.
- **Impact / reproduction:** The accessibility suite passes while a pervasive, measurable WCAG failure remains in production styles. Future contrast regressions are also invisible to the gate.
- **Recommendation:** Fix token contrast, remove the exclusion, and document only narrowly scoped exceptions with issue IDs and expiration dates.
- **Regression test:** Make `color-contrast` mandatory in both light and dark route scans; fail CI on new exclusions.

### FE-033 — Command palette is an incomplete modal and can close an underlying dialog

- **Classification:** Confirmed accessibility/correctness defect
- **Severity:** High
- **Confidence:** High
- **Evidence:**
  - Global Ctrl/Cmd+K always opens the palette at `frontend/src/App.tsx:1432-1452`, even when another modal is active or an input is being edited.
  - The palette claims `aria-modal="true"` but has no focus trap, background inerting, or focus restoration at `frontend/src/components/terminal/TerminalPage.tsx:1093-1177`.
  - Palette Escape calls `onClose` without stopping propagation at `frontend/src/components/terminal/TerminalPage.tsx:1103-1111`.
  - Shared modals also listen on `window` for Escape at `frontend/src/components/ui/Modal.tsx:73-100`.
- **Impact / reproduction:** Open an unsaved modal, press Ctrl+K, then Escape in the palette input. Two modal dialogs coexist at the same z-layer; keyboard focus can reach the background, and the Escape can also close the underlying form, losing input.
- **Recommendation:** Add a central modal-stack/shortcut manager. Do not open global palette/search actions under an active modal. Implement the palette using the shared focus-contained dialog primitive, stop handled Escape propagation, inert background content, and restore focus to the opener.
- **Regression test:** Keyboard-only tests for stacked modal prevention, Tab/Shift+Tab containment, Escape closing only the top layer, and focus restoration to the invoking control.

### FE-034 — Terminal tab arrow navigation leaves focus behind and can repeat the same transition

- **Classification:** Confirmed accessibility/functional defect
- **Severity:** High
- **Confidence:** High
- **Evidence:** `frontend/src/components/terminal/TerminalPage.tsx:174-193` calculates arrow navigation from the button's captured index and updates only active state. Tab buttons at `frontend/src/components/terminal/TerminalPage.tsx:770-779` do not focus the destination.
- **Impact / reproduction:** Focus tab A and press Right. B becomes selected, but DOM focus remains on A. Press Right again: the handler attached to A can select B again rather than advance to C. Focus and `aria-selected` diverge.
- **Recommendation:** Store tab element refs, compute from the current focused/active ID, and focus the destination. Follow the WAI-ARIA tabs pattern consistently for Arrow keys, Home, End, Delete/close, and dynamic removal.
- **Regression test:** Assert both active ID and `document.activeElement` across repeated arrows, Home/End, closing active/non-active tabs, and a single-tab state.

### FE-035 — Shared Tabs changes selection without moving focus or relating tabs to panels

- **Classification:** Confirmed accessibility defect
- **Severity:** Medium
- **Confidence:** High
- **Evidence:** `frontend/src/components/ui/Tabs.tsx:18-79` updates `activeID` on Arrow/Home/End but never focuses the new tab. Tabs have no IDs/`aria-controls`; the panel has no `aria-labelledby`.
- **Impact / reproduction:** After keyboard navigation, focus can remain on an element whose `tabIndex` has become `-1`, and assistive technology cannot directly relate the active tab to its panel.
- **Recommendation:** Focus the destination and generate stable tab/panel IDs with reciprocal ARIA relationships. Decide and document automatic versus manual activation.
- **Regression test:** Test selection, focus, and ARIA relationships for enabled/disabled tabs and dynamic item lists.

### FE-036 — DataTable column customization is inaccessible to keyboard and touch users

- **Classification:** Confirmed accessibility/UX defect
- **Severity:** High
- **Confidence:** High
- **Evidence:**
  - The column menu opens only from `onContextMenu` on the header at `frontend/src/components/ui/DataTable.tsx:356-359`.
  - The menu at `frontend/src/components/ui/DataTable.tsx:518-563` has no visible opener, focus placement/restoration, Escape/outside-click contract, or arrow-key navigation.
  - It declares menu semantics while containing ordinary checkboxes/labels/buttons rather than consistent menuitem roles.
- **Impact / reproduction:** A keyboard-only user cannot discover or open column settings. Many touch environments do not expose a reliable context-menu gesture. Screen readers receive a role model that does not match the contained controls.
- **Recommendation:** Add a visible, labelled “Columns” button. Use an accessible popover containing standard checkbox controls, or fully implement the ARIA menu pattern. Restore focus to the opener.
- **Regression test:** Keyboard, screen-reader-role, pointer, long-press/touch, outside-click, and focus-restoration tests.

### FE-037 — Virtual DataTable rows omit absolute row position semantics

- **Classification:** Accessibility gap
- **Severity:** Medium
- **Confidence:** High
- **Evidence:** `frontend/src/components/ui/DataTable.tsx:337-342` declares `aria-rowcount`, but virtual rows do not receive `aria-rowindex`; spacer rows are hidden and cannot convey position.
- **Impact / reproduction:** Screen-reader users hear only the rendered window and cannot determine that a row is, for example, 350 of 2,000. Navigation context is lost as windows recycle.
- **Recommendation:** Emit one-based absolute `aria-rowindex` for each data row and accurate column indexes where columns can be hidden/reordered. Verify semantics with a real screen reader because virtualized native tables vary across browser engines.
- **Regression test:** DOM assertions for absolute indexes plus NVDA/WebView2 manual coverage on a large dataset.

### FE-038 — Focus indicators do not consistently cover select, textarea, and custom focusables

- **Classification:** Accessibility gap
- **Severity:** Medium
- **Confidence:** High
- **Evidence:** Global focus styling at `frontend/src/styles/index.css:160-165` targets only buttons, anchors, and inputs. Selects and textareas commonly remove native outlines and rely on a border-color change. The focus color is hard-coded as `#2dd4a7` rather than a surface-tested semantic token.
- **Impact / reproduction:** Keyboard users can receive no or only a marginal one-pixel state change on several controls. The ring's 3:1 non-text contrast is not guaranteed across light surfaces/themes.
- **Recommendation:** Apply a theme-aware `:focus-visible` ring to all native/custom interactive elements, including `[tabindex]`, select, and textarea. Preserve forced-colors outlines and verify 3:1 against adjacent colors.
- **Regression test:** Tab through every control type in both themes and forced-colors mode; add computed-style assertions for focus-visible state.

### FE-039 — Dynamic errors and progress statuses are generally not announced

- **Classification:** Accessibility gap
- **Severity:** Medium
- **Confidence:** High
- **Evidence:** Representative dynamic messages are plain `<div>` content without `role="alert"`, `role="status"`, or an `aria-live` region at:
  - `frontend/src/settings/SettingsPage.tsx:360-368`
  - `frontend/src/agent/AgentPage.tsx:665-674`
  - `frontend/src/components/settings/PortForwardingPanel.tsx:116-120`
  - `frontend/src/components/terminal/TerminalPage.tsx:876-882`
- **Impact / reproduction:** A screen-reader user can submit or refresh and receive no announcement that the operation failed, completed, or changed state unless they manually navigate back to the message.
- **Recommendation:** Create shared semantic status/error components. Use polite `role="status"` for progress/success and assertive `role="alert"` for actionable failures; avoid repeatedly announcing high-frequency stream updates.
- **Regression test:** Assert live-region roles and use accessibility integration/manual screen-reader tests for save, refresh, terminal failure, and Agent generation.

### FE-040 — CairnLoader skip behavior is pointer-only

- **Classification:** Confirmed accessibility defect
- **Severity:** Medium-High
- **Confidence:** High
- **Evidence:** `frontend/src/components/CairnLoader.tsx:321-333` makes the progressbar container clickable to skip but does not make it a button, focusable, or keyboard-operable.
- **Impact / reproduction:** Keyboard and switch-device users cannot skip a loader that may remain for up to 12 seconds.
- **Recommendation:** Add a real, separately labelled Skip button. Keep progressbar semantics read-only and expose its value/text independently.
- **Regression test:** Tab/Enter/Space test for skip plus accessible-name and progressbar-role assertions.

### FE-041 — JavaScript loader animation ignores reduced-motion preference

- **Classification:** Confirmed accessibility gap
- **Severity:** Medium-High
- **Confidence:** High
- **Evidence:**
  - Canvas animation runs through `requestAnimationFrame` at `frontend/src/components/CairnLoader.tsx:209-306`.
  - CSS reduced-motion handling at `frontend/src/styles/cairn-loader.css:493+` disables selected CSS animations but cannot disable the canvas particle/ring/scan animation.
- **Impact / reproduction:** Enable “Reduce motion” in the OS and launch the app. Canvas particles and continuous effects still animate, potentially triggering vestibular discomfort.
- **Recommendation:** Check `matchMedia('(prefers-reduced-motion: reduce)')` in the component, react to preference changes, and render a static/minimal canvas state with no continuous RAF.
- **Regression test:** Mock reduced-motion media changes and assert RAF is not scheduled or is stopped; add reduced-motion screenshot coverage.

### FE-042 — Agent prompt textarea has no persistent accessible name

- **Classification:** Confirmed accessibility defect
- **Severity:** Medium
- **Confidence:** High
- **Evidence:** `frontend/src/agent/AgentPage.tsx:727-741` provides placeholder text but no visible label, `aria-label`, or `aria-labelledby` for the prompt textarea.
- **Impact / reproduction:** Placeholder text disappears on input and is an unreliable accessible-name mechanism. Screen-reader/form-control navigation may announce an unnamed edit field.
- **Recommendation:** Add a persistent visible label, or at minimum an explicit accessible name tied to nearby instructions.
- **Regression test:** Assert the textarea has an accessible name before and after entering text.

### FE-043 — Icon-only Button accessibility is not enforced by its type contract

- **Classification:** Type-safety/accessibility risk
- **Severity:** Medium
- **Confidence:** High
- **Evidence:** `frontend/src/components/ui/Button.tsx:76-79` derives hidden text from optional `aria-label`; the prop type permits `size="icon"` without a label or textual child.
- **Impact / reproduction:** A new call site can compile and render a focusable icon button with an empty accessible name. Existing discipline cannot prevent regression.
- **Recommendation:** Use a discriminated prop union requiring `aria-label` for icon size, or require an explicit `accessibleName` prop whenever children contain no text.
- **Regression test:** Type-level negative test for an unlabeled icon button and runtime accessible-name coverage.

### FE-044 — DataTable fixed-height virtualization is incompatible with wrapped rows

- **Classification:** Confirmed correctness/performance defect
- **Severity:** High
- **Confidence:** High
- **Evidence:**
  - Virtual offsets assume a fixed 44 px row at `frontend/src/components/ui/DataTable.tsx:8-11` and `frontend/src/components/ui/DataTable.tsx:136-158`.
  - Cells can enable wrapping at `frontend/src/components/ui/DataTable.tsx:494-498`.
  - App tables request wrapping at `frontend/src/App.tsx:10422`, `frontend/src/App.tsx:14284`, `frontend/src/App.tsx:14319`, `frontend/src/App.tsx:14364`, and `frontend/src/App.tsx:14431`.
- **Impact / reproduction:** Render more than the virtualization threshold with multiline content. Actual row heights diverge from offset math, producing overlap, jump, blank space, or unreachable rows.
- **Recommendation:** Measure/cache variable heights, or make virtual mode strictly single-line/fixed-height and provide expansion/detail UI. Do not expose `wrap` under a fixed-height virtual contract.
- **Regression test:** More than 120 rows containing long and multiline values; scroll to first/middle/last and assert every row is reachable without overlap.

### FE-045 — Hidden terminal tabs retain full runtime cost

- **Classification:** Confirmed performance gap
- **Severity:** Medium
- **Confidence:** High
- **Evidence:** Each `TerminalSurface` retains XTerm state, 10,000-line scrollback, Resize/Mutation observers, and its own global `terminal:data` subscription at `frontend/src/components/terminal/TerminalPage.tsx:851-864` and `frontend/src/components/terminal/TerminalPage.tsx:1219-1310`, including inactive tabs.
- **Impact / reproduction:** Open many terminal tabs. Event-listener dispatch, memory, observers, and DOM/canvas costs grow approximately linearly even though only one terminal is visible.
- **Recommendation:** Subscribe once and dispatch by session ID; suspend observers/renderers for hidden surfaces; cap retained live terminals; consider serializing inactive scrollback outside XTerm.
- **Regression test:** Open 1/10/50 sessions and measure listener count, retained DOM nodes, memory, and event-processing time. Assert inactive terminals do not resize/render.

### FE-046 — Long-term metric history performs repeated full-array work and can omit the newest sample

- **Classification:** Confirmed performance/correctness defect
- **Severity:** Medium-High
- **Confidence:** High
- **Evidence:**
  - Metrics appends create a new array and filter the complete retained history on each update at `frontend/src/App.tsx:2451-2455`.
  - Long-term history retains up to 86,400 points at `frontend/src/App.tsx:19000-19037`.
  - Modulo-index downsampling at `frontend/src/App.tsx:19014-19021` does not guarantee the final/latest point is included and can alias away short spikes.
- **Impact / reproduction:** After a long-running session, each roughly two-second update copies/scans tens of thousands of objects and rerenders chart consumers. The displayed line can lag behind the current reading or hide a spike.
- **Recommendation:** Use a bounded ring buffer and time buckets. Downsample each bucket using min/max/average and always include first/latest endpoints. Move sampling/aggregation outside the root render path.
- **Regression test:** Soak with 86,400 synthetic samples, assert bounded update time/memory, latest timestamp inclusion, and preservation of known narrow spikes.

### FE-047 — High-frequency live state is hosted in a 19k-line root component

- **Classification:** Architecture/performance gap
- **Severity:** High
- **Confidence:** High
- **Evidence:**
  - `frontend/src/App.tsx` is approximately 19,000 lines and exceeds 500 KB, causing Babel's deoptimization warning during lint/test transforms.
  - Live stats update inventory and multiple App-level states at `frontend/src/App.tsx:2400-2458`.
  - Route components, modal state, inline callbacks, table columns, histories, and stream ownership are all created from the same root.
- **Impact / reproduction:** Every metrics event can rerender a broad active-page subtree, recreate referentially unstable props/columns, invalidate memoized sorts, and complicate cancellation boundaries. The monolith also makes race conditions harder to isolate and regression-test.
- **Recommendation:** Split by feature route and domain store. Put high-frequency metrics/log state behind selector-based subscriptions, memoize stable table definitions, lazy-load heavyweight routes/libraries, and use reducers/state machines for multi-step workflows.
- **Regression test:** React profiler budget for idle live-metrics updates, asserting only metric consumers rerender; architecture tests can prohibit direct cross-feature state growth in `App.tsx`.

### FE-048 — CairnLoader performs React updates every animation frame and O(n²) particle linking

- **Classification:** Confirmed performance defect
- **Severity:** Medium-High
- **Confidence:** High
- **Evidence:**
  - `frontend/src/components/CairnLoader.tsx:209-306` runs a canvas RAF loop and calls React `setProg` on every frame.
  - Up to roughly 110 particles are compared pairwise for links at `frontend/src/components/CairnLoader.tsx:279-298`.
- **Impact / reproduction:** Startup can spend CPU on approximately 60 React rerenders/second plus quadratic canvas work while the real application is also initializing. This is most visible on low-power systems, remote desktops, and high-DPI WebViews.
- **Recommendation:** Keep visual progress in refs/canvas, throttle semantic React progress to a few updates per second, pause on `document.hidden`, lower/adapt particle count, and use spatial bucketing or nearest-neighbor limits.
- **Regression test:** Startup performance trace on low-end throttling with render-count, long-task, and frame-budget assertions; ensure animation pauses while hidden.

### FE-049 — Loader duplicates version work and does not cancel the underlying call

- **Classification:** Efficiency/lifecycle risk
- **Severity:** Medium-Low
- **Confidence:** Medium-High
- **Evidence:**
  - Loader calls/retries app-version retrieval at `frontend/src/components/CairnLoader.tsx:127-150`.
  - App independently loads version at `frontend/src/App.tsx:1642-1674`.
  - Loader's hard timer can advance UI readiness, but does not cancel a hanging underlying Wails promise.
- **Impact / reproduction:** Startup performs duplicate RPCs and retry/timer state can outlive the visual need for it. A stuck call remains unresolved in the background.
- **Recommendation:** Load version once in a shared bootstrap resource; add generation/cancellation semantics; make loader presentation consume bootstrap state rather than own backend initialization.
- **Regression test:** Count version RPCs through success, failure/retry, hard-timeout, and unmount paths; assert one logical request owner and no post-unmount state commits.

### FE-050 — Loader reports checks that were not actually performed

- **Classification:** Confirmed UX/content defect
- **Severity:** Low-Medium
- **Confidence:** High
- **Evidence:** `frontend/src/components/CairnLoader.tsx:29-39` contains hard-coded success-like messages such as Docker engine detection and security-policy verification, independent of real provider/engine results.
- **Impact / reproduction:** Launch with Docker unavailable or a provider problem. The boot animation can still imply that these checks passed, reducing trust in later diagnostics.
- **Recommendation:** Bind status text to verified bootstrap results, or use neutral copy such as “Preparing interface” that does not claim operational/security checks.
- **Regression test:** Launch with mocked healthy/unhealthy/unknown provider states and assert only substantiated claims appear.

### FE-051 — Production bundle is a single oversized main chunk

- **Classification:** Confirmed performance/architecture gap
- **Severity:** Medium
- **Confidence:** High
- **Evidence:** `npm run build` produced a 1,274.03 KB minified / 338.11 KB gzip main JavaScript chunk and Vite's >500 KB warning. The frontend contains heavyweight route-specific dependencies such as XTerm, Recharts, and Agent UI but does not materially split them by route.
- **Impact / reproduction:** Every launch parses/compiles code for features the user may never open, increasing WebView startup CPU and memory. A single chunk also reduces cache locality and obscures per-feature budgets.
- **Recommendation:** Route-level `React.lazy`/dynamic imports for Agent, Terminal, chart-heavy views, and infrequent modals; explicit vendor chunking only where measurements show benefit; keep bootstrap shell small.
- **Regression test:** CI bundle manifest budget for entry size and major route chunks, plus cold-launch timing on a representative packaged WebView.

### FE-052 — Chart tests render with invalid dimensions, so chart layout is not meaningfully verified

- **Classification:** Test/performance gap
- **Severity:** Medium
- **Confidence:** High
- **Evidence:** The passing unit run repeatedly emitted Recharts warnings that container width/height were `-1` or `0`. These warnings arise from responsive chart containers in jsdom and recur across tests.
- **Impact / reproduction:** Chart tests can assert surrounding text while never constructing a valid plotted layout. The warning flood also obscures real React errors and makes performance/output diagnosis harder.
- **Recommendation:** Provide deterministic `ResizeObserver`/element geometry in tests, isolate chart rendering adapters, and fail tests on unexpected console warnings. Add browser assertions for plotted SVG/canvas dimensions and visible latest data.
- **Regression test:** Dedicated chart test harness with nonzero container geometry, resize behavior, empty data, large data, dark theme, and reduced motion.

### FE-053 — Diagnostics expose raw child-process command strings

- **Classification:** Security/privacy risk
- **Severity:** Medium
- **Confidence:** Medium-High
- **Evidence:**
  - Active stdio commands are displayed at `frontend/src/settings/SettingsPage.tsx:792-813`.
  - The backend stores a raw joined command line at `internal/providers/stdio_tracker.go:65-68`.
- **Impact / reproduction:** If a provider/tool invocation ever includes credentials, tokens, sensitive paths, registry URLs, or user data in arguments, opening diagnostics exposes the value in plaintext and possibly in screenshots/support captures.
- **Recommendation:** Redact sensitive flags, values, environment assignments, and URL credentials at the backend before storage. Prefer structured executable/argument metadata and display a truncated basename/operation summary, with explicit reveal if necessary.
- **Regression test:** Feed commands containing common secret flag forms, URL credentials, environment tokens, quoted values, and Windows paths; assert no secret reaches the DTO or UI.

### FE-054 — External update URL is opened without frontend scheme/host validation

- **Classification:** Defense-in-depth risk
- **Severity:** Low-Medium
- **Confidence:** Medium-High
- **Evidence:** `frontend/src/App.tsx:5788-5791` opens a backend-provided update URL directly with `window.open(..., 'noopener')`. Backend release data accepts GitHub's `html_url` at `internal/services/settings_service.go:124-130` without an explicit allowlist at this boundary.
- **Impact / reproduction:** The normal source is trusted GitHub TLS data, so exploitation is not currently demonstrated. However, compromised/malformed release data or future source changes could open a non-HTTPS or unexpected origin in the WebView/browser.
- **Recommendation:** Validate `https:` and an explicit trusted-host allowlist on both backend and frontend; use the host's external-browser API and reject all other schemes.
- **Regression test:** Accept canonical GitHub URLs; reject `javascript:`, `file:`, HTTP, credential-bearing URLs, lookalike hosts, and unexpected redirects.

### FE-055 — Clipboard behavior is inconsistent and often silently fails

- **Classification:** Reliability/UX risk
- **Severity:** Medium
- **Confidence:** High
- **Evidence:**
  - Some actions use optional `navigator.clipboard?.writeText` and provide no fallback or result at `frontend/src/App.tsx:14563-14569` and `frontend/src/App.tsx:17827-17838`.
  - Numerous Wails `Clipboard.SetText` calls, including command-palette copy at `frontend/src/components/terminal/TerminalPage.tsx:1152-1157`, are invoked with `void` and no error feedback.
- **Impact / reproduction:** In a WebView without navigator clipboard permission/support, an action can do nothing. Wails clipboard rejection is also invisible, leaving users unsure whether sensitive commands/IDs were copied.
- **Recommendation:** Centralize clipboard access through one host-backed helper returning success/error. Show restrained feedback, and avoid claiming copy success before resolution.
- **Regression test:** Supported, unavailable, denied, rejected, and delayed clipboard paths for every shared copy control.

### FE-056 — Wails event and persisted-state boundaries bypass runtime type safety

- **Classification:** Type-safety/compatibility risk
- **Severity:** Medium
- **Confidence:** High
- **Evidence:**
  - Generic Wails payload adapters assert `event.data as T` at `frontend/src/App.tsx:15299-15304` and `frontend/src/components/terminal/TerminalPage.tsx:1542-1547`.
  - Provider setup restoration uses shallow guards that accept nested provider/status objects at `frontend/src/App.tsx:999-1018`.
  - Generated bindings include broad types such as `time.Time = any` at `frontend/bindings/time/models.ts:51`.
- **Impact / reproduction:** Backend/frontend version skew, malformed events, corrupted/localStorage-edited state, or unexpected null/nested fields pass TypeScript and fail later in rendering/action logic.
- **Recommendation:** Validate events and persisted DTOs at boundaries with small schemas/type guards; version local storage; convert generated DTOs into stricter frontend domain models; use opaque/branded IDs for destructive targets.
- **Regression test:** Fuzz missing/wrong/null fields and older persisted versions; assert safe rejection/reset with diagnostics rather than render exceptions.

### FE-057 — Frontend CSP and navigation policy are not visible in the document

- **Classification:** Security hardening gap
- **Severity:** Low-Medium
- **Confidence:** Medium
- **Evidence:** `frontend/index.html:1-12` contains no CSP metadata, and this frontend review found no document-level navigation restriction. React escaping and the absence of `dangerouslySetInnerHTML` are positive controls.
- **Impact / reproduction:** This is not proof that the packaged Wails host lacks a CSP; it may be enforced in host configuration. If it is not, any future injection bug receives a broader script/connect/navigation environment than necessary.
- **Recommendation:** Verify and document the effective packaged WebView CSP and navigation allowlist. Restrict scripts, styles, connections, images, frames, and external navigation to required origins; test the packaged host, not only Vite.
- **Regression test:** Packaged-app security test that attempts inline script/eval, unapproved network access, framing, and untrusted navigation and confirms they are blocked.

### FE-058 — Static Inter Medium file is declared as the entire 400–700 range

- **Classification:** Confirmed presentation/configuration defect
- **Severity:** Low
- **Confidence:** Medium-High
- **Evidence:** `frontend/src/styles/index.css:131-136` maps `Inter-Medium.ttf` to `font-weight: 400 700`, while `font-synthesis: none` is configured elsewhere in the stylesheet.
- **Impact / reproduction:** Regular, medium, semibold, and bold text may all use the same static face or resolve inconsistently, weakening hierarchy and causing platform-specific typography differences.
- **Recommendation:** Supply the correct static files for each used weight or a genuine variable font whose metadata supports the declared range.
- **Regression test:** Font metadata/build check and cross-platform visual samples for 400/500/600/700.

### FE-059 — Release UI coverage omits major routes, themes, sizes, and keyboard workflows

- **Classification:** Test gap
- **Severity:** High
- **Confidence:** High
- **Evidence:**
  - The route matrix at `frontend/e2e/release-ui.spec.mjs:9-20` excludes Agent.
  - Special overlay coverage includes only Command palette, Notification Center, and Import Project at `frontend/e2e/release-ui.spec.mjs:55-79`.
  - `frontend/playwright.config.mjs:15-22` configures the primary desktop viewport, and `frontend/playwright.config.mjs:33-37` defines only one Chromium project.
  - The compact overflow check uses 1260×720 at `frontend/e2e/release-ui.spec.mjs:102-124`, which is not a mobile/narrow breakpoint.
- **Impact / reproduction:** Agent, dark mode, 320–768 px layouts, modal keyboard journeys, macOS/WebKit differences, packaged WebView2 behavior, and destructive workflows can regress while the release suite remains green.
- **Recommendation:** Add route/state matrices covering Agent, light/dark/system themes, narrow/tablet/desktop sizes, keyboard/focus journeys, long strings, empty/error/loading states, and at least representative packaged Wails/WebView smoke tests on supported OSes.
- **Regression test:** This finding is itself the test plan; maintain a documented coverage matrix and fail CI if required route/theme/viewport projects are skipped.

### FE-060 — Default visual testing does not compare against committed goldens

- **Classification:** Release-process gap
- **Severity:** Medium-High
- **Confidence:** High
- **Evidence:** `frontend/e2e/release-ui.spec.mjs:21-24` and `frontend/e2e/release-ui.spec.mjs:88-99` make committed-golden comparison conditional/skip it unless strict or update flags are explicitly supplied. The ordinary stability check compares the current application to itself over a short interval.
- **Impact / reproduction:** A deterministic spacing, typography, missing-control, or color regression can be perfectly stable and still pass the default suite because there is no approved baseline comparison.
- **Recommendation:** Run strict committed-golden comparison in protected CI for representative routes/themes/platforms. Require explicit reviewed baseline updates and retain a small tolerance only for known platform rendering variance.
- **Regression test:** Deliberately change a stable layout token in a test branch and verify CI fails until a golden update is reviewed.

### FE-061 — Browser E2E uses a mocked Wails runtime, leaving host integration unverified

- **Classification:** Integration-test gap
- **Severity:** High
- **Confidence:** High
- **Evidence:** The release-validation server supplies Wails runtime/service mocks rather than launching the packaged desktop host. Browser tests therefore cannot exercise real event timing, WebView permissions, clipboard/navigation APIs, process lifecycle, or stream cleanup behavior.
- **Impact / reproduction:** The central defects in FE-004, FE-009, FE-013, FE-022, and FE-055 are precisely at the mock/host boundary and can pass all browser tests despite failing in the desktop application.
- **Recommendation:** Keep fast mocked browser tests, but add a small packaged-app smoke/integration tier on each supported OS. Exercise startup, one stream, one terminal session, clipboard, external URL, provider status, and orderly shutdown against real Wails bindings.
- **Regression test:** Packaged-host scenario suite with deterministic test backend or injectable service fixture and captured unhandled errors/events.

### FE-062 — ESLint has no JSX accessibility or UI-contract rules

- **Classification:** Tooling gap
- **Severity:** Medium
- **Confidence:** High
- **Evidence:** `frontend/eslint.config.js:1-22` enables core JavaScript/TypeScript and React Hooks rules, but no `jsx-a11y`, Testing Library, import-boundary, or project-specific accessible-control rules.
- **Impact / reproduction:** Lint passes despite unnamed controls, incomplete tab/focus patterns, clickable non-buttons, and modal semantics described above. Architectural cross-feature coupling can continue growing without a boundary gate.
- **Recommendation:** Add `eslint-plugin-jsx-a11y`, Testing Library lint rules for tests, and targeted local rules/type contracts for icon buttons, fire-and-forget promises, and feature boundaries. Roll out with an explicit debt baseline rather than blanket disables.
- **Regression test:** Add representative invalid fixture snippets and assert lint rejects them.

### FE-063 — Passing unit tests emit enough warnings to obscure real failures

- **Classification:** Test-harness gap
- **Severity:** Medium
- **Confidence:** High
- **Evidence:** The 103-test run passed but produced extensive repeated Recharts invalid-dimension warnings, jsdom canvas `getContext` not-implemented output, and Wails browser-runtime warnings. The output was large enough to be truncated during review.
- **Impact / reproduction:** New React warnings, act violations, resource leaks, or unhandled errors can be lost in expected noise. Developers are conditioned to ignore stderr.
- **Recommendation:** Install explicit deterministic mocks for `ResizeObserver`, geometry, canvas, and Wails runtime. Capture console output and fail tests on any warning/error not matched by a narrow per-test expectation.
- **Regression test:** Add a harness self-test that emits an unexpected console warning and verify the suite fails.

### FE-064 — High-risk asynchronous and lifecycle paths have no focused regression tests

- **Classification:** Test gap
- **Severity:** High
- **Confidence:** High
- **Evidence:** Existing tests are broad and pass, but no focused tests were found for request-generation behavior in project detail, Agent project operations, restore close/reopen, provider setup sessions, initial log delivery, capped pause semantics, rejected terminal writes, debounced-hook unmount, or concurrent settings saves.
- **Impact / reproduction:** Timing bugs can be reintroduced without changing snapshots or synchronous happy-path behavior. Mocked promises commonly resolve immediately, hiding the ordering window entirely.
- **Recommendation:** Establish a reusable deferred-promise/event-stream test toolkit and require race tests for every operation whose result is keyed to current selection/modal/session state.
- **Regression test:** See the dedicated test-gap matrix below; make these tests part of normal CI, not an opt-in suite.

### FE-065 — No enforced frontend coverage threshold was found

- **Classification:** Quality-gate gap
- **Severity:** Medium-Low
- **Confidence:** Medium-High
- **Evidence:** The frontend scripts run Vitest but do not enforce a visible statement/branch/function threshold for the reviewed suite. Large components such as `App.tsx`, `AgentPage.tsx`, `SettingsPage.tsx`, and `TerminalPage.tsx` contain many unexercised failure branches despite green tests.
- **Impact / reproduction:** Test count can rise while critical conditional/error paths remain untouched; coverage can fall silently as the monolith grows.
- **Recommendation:** Collect branch coverage, set an honest initial baseline, and ratchet it upward. More importantly, set feature-specific branch thresholds for reducers/state machines and destructive-operation orchestration rather than relying only on a global percentage.
- **Regression test:** CI should fail when coverage drops below the checked-in baseline or when changed high-risk modules reduce branch coverage.

### FE-066 — Frontend formatting gate currently fails

- **Classification:** Confirmed quality-gate failure
- **Severity:** Low
- **Confidence:** High
- **Evidence:** `npm run format:check` reports `frontend/src/settings/SettingsPage.tsx` as not conforming to Prettier in the reviewed working tree.
- **Impact / reproduction:** A standard repository gate is red, creating CI/review noise and making unrelated diffs harder to inspect. Because the tree was already dirty, this report does not attribute authorship.
- **Recommendation:** Format that file after preserving/confirming the in-progress changes, and require the formatting check before merge.
- **Regression test:** Existing `format:check` is sufficient once it is run as a required CI gate.

### FE-067 — Settings input remounting can disrupt focus/caret after asynchronous saves

- **Classification:** UX risk
- **Severity:** Low-Medium
- **Confidence:** Medium
- **Evidence:** Settings field components use value-derived React keys around `frontend/src/settings/SettingsPage.tsx:1671-1754`, so an authoritative value change can replace the input node rather than update it in place.
- **Impact / reproduction:** Save or receive a normalized value while continuing keyboard editing. The field can remount, losing caret position, text selection, undo context, or focus.
- **Recommendation:** Keep stable keys based on setting identity. Synchronize draft state deliberately when authoritative values change and avoid replacing the DOM node.
- **Regression test:** Focus/edit a field, resolve a delayed normalized save, and assert focus/caret behavior follows the documented policy.

## Validation results

All commands were executed from `frontend/` unless noted.

| Command                                   |                            Result | Review notes                                                                                                                                                                                                      |
| ----------------------------------------- | --------------------------------: | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `npm run typecheck`                       |                              Pass | Strict TypeScript completed without errors; no compile-time Wails binding mismatch was observed.                                                                                                                  |
| `npm run lint`                            |                              Pass | Babel reported that `src/App.tsx` exceeds 500 KB and was deoptimized during transform.                                                                                                                            |
| `npm test -- --reporter=verbose`          |                              Pass | 7 test files, 103 tests, approximately 16.8 seconds. Large stderr warning volume is captured in FE-052/FE-063.                                                                                                    |
| `npm run build`                           |                 Pass with warning | Main JS chunk 1,274.03 KB minified / 338.11 KB gzip; Vite warned about chunks over 500 KB. CSS was 43.65 KB / 9.72 KB gzip.                                                                                       |
| `npm run format:check`                    |                          **Fail** | `src/settings/SettingsPage.tsx` needs formatting; see FE-066.                                                                                                                                                     |
| `npm run test:release-ui`                 |                    Pass with skip | 15 passed, 1 skipped, approximately 74.2 seconds. The skipped case is strict committed-golden comparison.                                                                                                         |
| `npm audit --audit-level=moderate --json` | Pass after environment adjustment | Initial call failed because Node did not trust the local certificate chain. Re-running with `NODE_OPTIONS=--use-system-ca` reported 0 vulnerabilities: 0 info/low/moderate/high/critical across 729 dependencies. |
| `npm ls --all --json`                     |                              Pass | Dependency tree command completed successfully.                                                                                                                                                                   |

Passing checks should not be interpreted as disproving the findings above: most critical/high defects require delayed or reordered promises/events, long-lived buffers, real host APIs, or keyboard/manual accessibility interaction that the current suites do not model.

## Missing regression coverage, prioritized

### Priority 0 — Wrong-target/destructive action integrity

1. Project A→B detail response reordering, including action-target assertions.
2. Agent A→B project/path switch during analyze, draft, preview, and apply.
3. Restore A close→B open with A/B backup responses in both orders.
4. Provider setup close/reopen/backend switch during check, plan, apply, verify, and project detection.
5. Bulk-selection target reconciliation across filters and inventory removal.

Use deferred promises, never only immediately resolved mocks. Every destructive-action test should assert the exact immutable resource ID/name received by the backend mock.

### Priority 1 — Streams, terminals, and long-lived state

1. Log event delivered immediately after stream creation.
2. Stop failure, old-stream late events, and new-stream generation isolation.
3. 50,000+ log cap while paused/unpinned; monotonic unread/dropped counts.
4. Wrapped multiline logs and full-text reachability.
5. Exact export payload/file content for buffer, tail, filters, and all-history modes.
6. Terminal input/paste/schedule/resize rejection with no unhandled promise.
7. Concurrent terminal-close events and active-session validity.
8. Agent navigation away/back while generation is active.

### Priority 1 — Accessibility

1. Axe with color contrast enabled in light and dark themes.
2. Keyboard/focus traversal for shared Tabs and Terminal tabs.
3. Command palette focus trap, modal stacking prevention, Escape, and restoration.
4. DataTable Columns control through keyboard/touch and correct role model.
5. Live announcement behavior for error/status components.
6. Loader reduced-motion and keyboard Skip.
7. Accessible names for prompt and every icon button.
8. NVDA + packaged WebView2 manual pass for virtual tables and dialogs.

### Priority 2 — Responsive, theme, and platform coverage

1. Agent plus every current route at 320, 375, 768, 1024, 1260, and 1440 widths.
2. Light, dark, and system-theme changes at runtime.
3. Long localization-like strings, long image IDs/paths/addresses, empty/error/loading states.
4. Packaged Windows WebView2, macOS host, and supported Linux host smoke paths.
5. Port-forwarding and other multi-column settings tables at narrow widths.

### Priority 2 — Soak and performance

1. 24-hour-equivalent metric sample ingestion with update-time/memory budgets.
2. Log ingestion above cap under filters/search/pause.
3. 1/10/50 terminal tabs with listener, observer, memory, and CPU measurement.
4. Loader frame/render count under CPU throttling, hidden document, high DPI, and reduced motion.
5. React-profiler assertion that live metrics do not rerender unrelated active-page regions.
6. Entry and route chunk budgets in CI.

### Priority 2 — Runtime boundary and security hardening

1. Malformed Wails events and version-skewed/localStorage DTOs.
2. Clipboard denied/unavailable/rejected paths.
3. External URL scheme/host rejection.
4. Command redaction for secrets in arguments and URLs.
5. Effective packaged CSP/navigation-policy tests.

## Recommended remediation sequence

### Immediate safety work

1. Introduce operation generations/immutable target keys for FE-001 through FE-005.
2. Preserve last-known-good inventory data and separate slice status per FE-007/FE-008.
3. Correct log stream ID ownership, capped pause semantics, export contract, and wrap behavior per FE-009 through FE-012.
4. Centralize terminal RPC error handling and durable Agent request ownership per FE-006/FE-013.

### Next release hardening

1. Fix contrast and remove the Axe suppression.
2. Implement modal-stack/focus and tab keyboard patterns.
3. Make settings/port-forwarding loading and error states explicit.
4. Add the Priority 0/1 deferred-race tests and a packaged Wails integration smoke tier.

### Structural follow-up

1. Split `App.tsx` into route/domain modules and state machines.
2. Move high-frequency streams into selector-based stores and central dispatchers.
3. Adopt measured/ring-buffer data structures for charts/logs and define bundle/performance budgets.
4. Add boundary schemas, CSP/URL/clipboard hardening, accessibility linting, and ratcheted branch coverage.

## Positive controls observed

- Strict TypeScript and current generated service usage compile successfully.
- ESLint and all current unit tests pass.
- Production build and default release browser suite pass.
- `npm audit` reports no known dependency vulnerabilities once Node uses the system CA store.
- React's normal escaping is retained; no `dangerouslySetInnerHTML` or eval-style rendering was found in the reviewed frontend.
- Agent markdown link handling restricts rendered links to HTTP(S) and uses `rel="noreferrer"` for new tabs at `frontend/src/agent/AgentPage.tsx:1663-1708`.
- CSV export includes formula-injection hardening at `frontend/src/settings/SettingsPage.tsx:1999-2001`.
- Shared Modal has useful baseline focus capture/restoration and Tab wrapping at `frontend/src/components/ui/Modal.tsx:61-105`; FE-033 concerns bypassing/stacking that primitive, not an absence of all modal focus work.
- DataTable column-resize handles include keyboard support at `frontend/src/components/ui/DataTable.tsx:426-451`.

These controls are valuable, but they do not cover the runtime ordering, semantic accessibility, and long-session behaviors identified above.
