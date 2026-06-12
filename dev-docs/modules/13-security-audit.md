# Module 13 — Security & Audit

`internal/security` — command-plan pipeline, risk evaluation, confirmations, audit log, secret redaction. Policy source: [05-security.md].

## 1. Command-plan pipeline

```go
type PlannedCommand struct{ Order int; Argv []string; Display string; WorkingDir string; Risk Risk; Explanation string }
type CommandPlan struct{ PlanID string; Title string; Risk Risk; Commands []PlannedCommand;
                         Effects []string; RequiresTypedName string; CreatedAt, ExpiresAt time.Time }
```

- `Plan*` methods build plans; plans stored in-memory registry (TTL 10 min) keyed by PlanID.
- `Apply*(planID, confirmation)`: validates plan exists/not expired (`E_PLAN_EXPIRED`), risk satisfied (`E_CONFIRMATION_REQUIRED` if typed name missing/mismatched), then executes steps sequentially through the owning module; each step audited; first failure stops the plan (already-executed steps reported).
- Safe operations get auto-approved single-step plans internally — uniform audit/preview path with zero extra UI friction.

## 2. Risk evaluator

Single table mapping operation → risk ([05-security.md §2] is normative). Evaluated server-side only. Context can raise risk (e.g. remove-volume → dangerous; remove-volume-in-use → blocked unless explicit override flag, which itself requires typed name).

## 3. Audit writer

Two-row pattern: `started` row at execution begin, terminal row (`success|failed|cancelled`) with exit code/duration/error. Fields per [03-data-model.md §6]. All writes pass the redactor (§4). Viewer API: `GetAuditLog(filter{time range, action prefix, status, project})`.

## 4. Redactor

Masks: values following `-p/--password/--token/--secret`, `user:pass@` in URLs, env assignments matching `(?i)(pass|token|secret|key)=`, base64 auth blobs. Applied to audit, command_history, app logs, and error details. Test corpus must show zero leaks.

## 5. Typed-name confirmation

Backend stores expected name in plan; frontend modal sends user input with Apply; comparison exact (case-sensitive). Never compared client-side only.

## 6. Tests

Unit: risk table (every operation enumerated — test fails when a new service method lacks a mapping), plan expiry, typed-name mismatch, redactor corpus. Integration: audit two-row integrity for all confirmed actions; [06-testing.md §6] suite.
