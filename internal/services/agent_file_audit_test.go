package services

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/security"
	"github.com/RCooLeR/Cairn/internal/store"
)

type fakeAgentAuditStore struct {
	mu sync.Mutex

	nextID      int64
	insertCalls int
	updateCalls int
	insertErrAt int
	updateErrAt int
	insertErr   error
	updateErr   error
	onInsert    func()
	records     map[int64]store.AuditRecord
	inserted    []store.AuditRecord
	updated     []store.AuditOutcome
	recordOrder []int64
}

func newFakeAgentAuditStore() *fakeAgentAuditStore {
	return &fakeAgentAuditStore{records: map[int64]store.AuditRecord{}}
}

func (f *fakeAgentAuditStore) Insert(_ context.Context, record store.AuditRecord) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.insertCalls++
	if f.insertErrAt == f.insertCalls {
		return 0, f.insertErr
	}
	f.nextID++
	f.records[f.nextID] = record
	f.inserted = append(f.inserted, record)
	f.recordOrder = append(f.recordOrder, f.nextID)
	if f.onInsert != nil {
		f.onInsert()
	}
	return f.nextID, nil
}

func (f *fakeAgentAuditStore) UpdateOutcome(_ context.Context, id int64, outcome store.AuditOutcome) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updateCalls++
	if f.updateErrAt == f.updateCalls {
		return f.updateErr
	}
	if outcome.Status != "success" && outcome.Status != "failed" {
		return fmt.Errorf("invalid terminal audit status %q", outcome.Status)
	}
	record, ok := f.records[id]
	if !ok {
		return fmt.Errorf("audit record %d not found", id)
	}
	if record.Status != "started" {
		return fmt.Errorf("audit record %d already finalized", id)
	}
	record.Status = outcome.Status
	record.ExitCode = outcome.ExitCode
	record.Duration = outcome.Duration
	record.Error = outcome.Error
	f.records[id] = record
	f.updated = append(f.updated, outcome)
	return nil
}

func (f *fakeAgentAuditStore) snapshot() ([]store.AuditRecord, []store.AuditRecord, []store.AuditOutcome, int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	records := make([]store.AuditRecord, 0, len(f.recordOrder))
	for _, id := range f.recordOrder {
		records = append(records, f.records[id])
	}
	return records, append([]store.AuditRecord(nil), f.inserted...), append([]store.AuditOutcome(nil), f.updated...), f.insertCalls, f.updateCalls
}

func TestAgentServiceApplyFileEditFinalizesOneSafeAuditRecord(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	target := filepath.Join(root, ".env")
	if err := os.WriteFile(target, []byte("APP_TOKEN=old\n"), 0o600); err != nil {
		t.Fatalf("seed target: %v", err)
	}
	beforeInfo, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat target before edit: %v", err)
	}
	wantPerm := beforeInfo.Mode().Perm()
	content := "APP_TOKEN=super-secret-agent-content\n"
	plans, planID := saveAgentFileEditTestPlan(t, root, ".env", content, false, hashAgentFile([]byte("APP_TOKEN=old\n")))
	audit := newFakeAgentAuditStore()
	service := &AgentService{Plans: plans, Audit: audit}

	result, err := service.ApplyFileEdit(ctx, planID, "")
	if err != nil {
		t.Fatalf("ApplyFileEdit() error = %v", err)
	}
	if result == nil || result.Path != ".env" || result.BytesWritten != len([]byte(content)) {
		t.Fatalf("ApplyFileEdit() result = %#v", result)
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(raw) != content {
		t.Fatalf("target = %q, want replacement", raw)
	}
	if info, err := os.Stat(target); err != nil {
		t.Fatalf("stat target: %v", err)
	} else if got := info.Mode().Perm(); got != wantPerm {
		t.Fatalf("target permissions = %04o, want preserved %04o", got, wantPerm)
	}

	records, inserted, updated, insertCalls, updateCalls := audit.snapshot()
	if insertCalls != 1 || updateCalls != 1 || len(records) != 1 || len(inserted) != 1 || len(updated) != 1 {
		t.Fatalf("audit calls/records = insert:%d update:%d records:%#v inserted:%#v updated:%#v", insertCalls, updateCalls, records, inserted, updated)
	}
	if inserted[0].Status != "started" || records[0].Status != "success" || records[0].ExitCode == nil || *records[0].ExitCode != 0 {
		t.Fatalf("audit lifecycle = inserted:%#v final:%#v", inserted[0], records[0])
	}
	assertAgentAuditDoesNotContain(t, append(records, inserted...), "super-secret-agent-content")
}

func TestAgentServiceApplyFileEditAuditsEveryFailureOutcome(t *testing.T) {
	tests := []struct {
		name             string
		prepare          func(*testing.T, string) (relativePath string, create bool, originalHash string)
		injectWriteError bool
		wantCode         apperror.Code
		wantAuditCode    string
		wantWriterCalls  int
	}{
		{
			name: "path validation",
			prepare: func(_ *testing.T, _ string) (string, bool, string) {
				return "../outside.env", true, ""
			},
			wantCode:        apperror.Conflict,
			wantAuditCode:   string(apperror.Conflict),
			wantWriterCalls: 0,
		},
		{
			name: "stale hash",
			prepare: func(t *testing.T, root string) (string, bool, string) {
				t.Helper()
				path := filepath.Join(root, ".env")
				if err := os.WriteFile(path, []byte("VALUE=changed\n"), 0o600); err != nil {
					t.Fatalf("seed stale target: %v", err)
				}
				return ".env", false, hashAgentFile([]byte("VALUE=previewed\n"))
			},
			wantCode:        apperror.Conflict,
			wantAuditCode:   string(apperror.Conflict),
			wantWriterCalls: 0,
		},
		{
			name: "directory target",
			prepare: func(t *testing.T, root string) (string, bool, string) {
				t.Helper()
				if err := os.Mkdir(filepath.Join(root, "config.yml"), 0o755); err != nil {
					t.Fatalf("seed directory target: %v", err)
				}
				return "config.yml", false, ""
			},
			wantCode:        apperror.Conflict,
			wantAuditCode:   string(apperror.Conflict),
			wantWriterCalls: 0,
		},
		{
			name: "write failure",
			prepare: func(t *testing.T, root string) (string, bool, string) {
				t.Helper()
				path := filepath.Join(root, ".env")
				if err := os.WriteFile(path, []byte("VALUE=old\n"), 0o600); err != nil {
					t.Fatalf("seed write target: %v", err)
				}
				return ".env", false, hashAgentFile([]byte("VALUE=old\n"))
			},
			injectWriteError: true,
			wantCode:         "",
			wantAuditCode:    string(apperror.Internal),
			wantWriterCalls:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			relativePath, create, originalHash := tt.prepare(t, root)
			content := "PASSWORD=super-secret-failure-content\n"
			plans, planID := saveAgentFileEditTestPlan(t, root, relativePath, content, create, originalHash)
			audit := newFakeAgentAuditStore()
			writerCalls := 0
			service := &AgentService{Plans: plans, Audit: audit}
			service.writeFile = func(_ security.AgentFileEditPlan, _ fs.FileMode) error {
				writerCalls++
				if tt.injectWriteError {
					return errors.New("injected writer failure super-secret-failure-content")
				}
				return nil
			}

			_, err := service.ApplyFileEdit(context.Background(), planID, "")
			if err == nil {
				t.Fatal("ApplyFileEdit() succeeded, want failure")
			}
			if tt.wantCode != "" && !apperror.IsCode(err, tt.wantCode) {
				t.Fatalf("ApplyFileEdit() error = %v, want %s", err, tt.wantCode)
			}
			if writerCalls != tt.wantWriterCalls {
				t.Fatalf("writer calls = %d, want %d", writerCalls, tt.wantWriterCalls)
			}
			records, inserted, updated, insertCalls, updateCalls := audit.snapshot()
			if insertCalls != 1 || updateCalls != 1 || len(records) != 1 || len(inserted) != 1 || len(updated) != 1 {
				t.Fatalf("audit calls/records = insert:%d update:%d records:%#v inserted:%#v updated:%#v", insertCalls, updateCalls, records, inserted, updated)
			}
			if inserted[0].Status != "started" || records[0].Status != "failed" || records[0].Error != tt.wantAuditCode {
				t.Fatalf("audit lifecycle = inserted:%#v final:%#v", inserted[0], records[0])
			}
			assertAgentAuditDoesNotContain(t, append(records, inserted...), "super-secret-failure-content")
		})
	}
}

func TestAgentServiceApplyFileEditFailsClosedWhenIntentAuditFails(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, ".env")
	original := []byte("VALUE=old\n")
	if err := os.WriteFile(target, original, 0o600); err != nil {
		t.Fatalf("seed target: %v", err)
	}
	plans, planID := saveAgentFileEditTestPlan(t, root, ".env", "VALUE=super-secret-new\n", false, hashAgentFile(original))
	audit := newFakeAgentAuditStore()
	audit.insertErrAt = 1
	audit.insertErr = errors.New("audit unavailable super-secret-audit-cause")
	writerCalls := 0
	service := &AgentService{Plans: plans, Audit: audit}
	service.writeFile = func(_ security.AgentFileEditPlan, _ fs.FileMode) error {
		writerCalls++
		return nil
	}

	result, err := service.ApplyFileEdit(context.Background(), planID, "")
	if result != nil || !apperror.IsCode(err, apperror.Internal) {
		t.Fatalf("ApplyFileEdit() = (%#v, %v), want fail-closed internal error", result, err)
	}
	if writerCalls != 0 {
		t.Fatalf("writer calls = %d, want zero", writerCalls)
	}
	assertFileContent(t, target, string(original))
	assertMarshaledErrorDoesNotContain(t, err, "super-secret")
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) || appErr.Partial != nil {
		t.Fatalf("ApplyFileEdit() error = %#v, want non-partial failure before mutation", err)
	}
}

func TestAgentServiceApplyFileEditReturnsPartialSuccessWhenOutcomeAuditFails(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, ".env")
	original := []byte("VALUE=old\n")
	content := "VALUE=super-secret-new\n"
	if err := os.WriteFile(target, original, 0o600); err != nil {
		t.Fatalf("seed target: %v", err)
	}
	plans, planID := saveAgentFileEditTestPlan(t, root, ".env", content, false, hashAgentFile(original))
	audit := newFakeAgentAuditStore()
	audit.updateErrAt = 1
	audit.updateErr = errors.New("audit finalize unavailable super-secret-audit-cause")
	service := &AgentService{Plans: plans, Audit: audit}

	result, err := service.ApplyFileEdit(context.Background(), planID, "")
	if result == nil || !apperror.IsCode(err, apperror.Internal) {
		t.Fatalf("ApplyFileEdit() = (%#v, %v), want result plus typed partial-success error", result, err)
	}
	assertFileContent(t, target, content)
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) || appErr.Partial == nil {
		t.Fatalf("ApplyFileEdit() error = %#v, want partial resource", err)
	}
	if appErr.Partial.Type != "file" || appErr.Partial.ID != ".env" || appErr.Partial.State != "updated_audit_incomplete" || appErr.Partial.CleanupRequired {
		t.Fatalf("partial resource = %#v", appErr.Partial)
	}
	assertMarshaledErrorDoesNotContain(t, err, "super-secret")
	records, inserted, updated, insertCalls, updateCalls := audit.snapshot()
	if insertCalls != 1 || updateCalls != 1 || len(records) != 1 || len(inserted) != 1 || len(updated) != 0 || records[0].Status != "started" {
		t.Fatalf("audit state = insert:%d update:%d records:%#v inserted:%#v updated:%#v", insertCalls, updateCalls, records, inserted, updated)
	}
	assertAgentAuditDoesNotContain(t, append(records, inserted...), "super-secret")
}

func TestAgentServiceApplyFileEditSurfacesFailedOutcomeAuditFailure(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, ".env")
	original := []byte("VALUE=old\n")
	if err := os.WriteFile(target, original, 0o600); err != nil {
		t.Fatalf("seed target: %v", err)
	}
	plans, planID := saveAgentFileEditTestPlan(t, root, ".env", "VALUE=super-secret-new\n", false, hashAgentFile(original))
	audit := newFakeAgentAuditStore()
	audit.updateErrAt = 1
	audit.updateErr = errors.New("audit finalize unavailable super-secret-audit-cause")
	service := &AgentService{Plans: plans, Audit: audit}
	service.writeFile = func(_ security.AgentFileEditPlan, _ fs.FileMode) error {
		return errors.New("writer unavailable super-secret-writer-cause")
	}

	result, err := service.ApplyFileEdit(context.Background(), planID, "")
	if result != nil || !apperror.IsCode(err, apperror.Internal) {
		t.Fatalf("ApplyFileEdit() = (%#v, %v), want visible audit-finalization error", result, err)
	}
	assertFileContent(t, target, string(original))
	assertMarshaledErrorDoesNotContain(t, err, "super-secret")
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) || appErr.Partial != nil {
		t.Fatalf("ApplyFileEdit() error = %#v, want non-partial failed mutation", err)
	}
	records, _, _, _, _ := audit.snapshot()
	if len(records) != 1 || records[0].Status != "started" {
		t.Fatalf("audit records = %#v, want visible unfinished started attempt", records)
	}
}

func TestAgentServiceApplyFileEditAuditsRejectedPlanWithoutPlanIDLeak(t *testing.T) {
	plans := security.NewAgentFileEditPlanStore(nil)
	t.Cleanup(plans.Close)
	audit := newFakeAgentAuditStore()
	service := &AgentService{Plans: plans, Audit: audit}
	secretPlanID := "super-secret-untrusted-plan-id"

	result, err := service.ApplyFileEdit(context.Background(), secretPlanID, "")
	if result != nil || !apperror.IsCode(err, apperror.PlanExpired) {
		t.Fatalf("ApplyFileEdit() = (%#v, %v), want plan-expired error", result, err)
	}
	records, _, _, insertCalls, updateCalls := audit.snapshot()
	if insertCalls != 1 || updateCalls != 0 || len(records) != 1 {
		t.Fatalf("audit state = insert:%d update:%d records:%#v", insertCalls, updateCalls, records)
	}
	if records[0].Status != "failed" || records[0].TargetID != "unresolved" || records[0].Error != string(apperror.PlanExpired) {
		t.Fatalf("rejected audit record = %#v", records[0])
	}
	if fingerprint := agentFileEditAttemptFingerprint(secretPlanID); !strings.Contains(records[0].Command, fingerprint) {
		t.Fatalf("rejected audit command = %q, want plan fingerprint %q", records[0].Command, fingerprint)
	}
	assertAgentAuditDoesNotContain(t, records, secretPlanID)
}

func TestAgentServiceApplyFileEditFinalizesCancellationAfterAuditIntent(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, ".env")
	original := []byte("VALUE=old\n")
	if err := os.WriteFile(target, original, 0o600); err != nil {
		t.Fatalf("seed target: %v", err)
	}
	plans, planID := saveAgentFileEditTestPlan(t, root, ".env", "VALUE=new\n", false, hashAgentFile(original))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	audit := newFakeAgentAuditStore()
	audit.onInsert = cancel
	writerCalls := 0
	service := &AgentService{Plans: plans, Audit: audit}
	service.writeFile = func(_ security.AgentFileEditPlan, _ fs.FileMode) error {
		writerCalls++
		return nil
	}

	result, err := service.ApplyFileEdit(ctx, planID, "")
	if result != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("ApplyFileEdit() = (%#v, %v), want cancellation after intent", result, err)
	}
	if writerCalls != 0 {
		t.Fatalf("writer calls = %d, want zero after cancellation", writerCalls)
	}
	assertFileContent(t, target, string(original))
	records, inserted, updated, insertCalls, updateCalls := audit.snapshot()
	if insertCalls != 1 || updateCalls != 1 || len(records) != 1 || len(inserted) != 1 || len(updated) != 1 {
		t.Fatalf("audit state = insert:%d update:%d records:%#v inserted:%#v updated:%#v", insertCalls, updateCalls, records, inserted, updated)
	}
	if inserted[0].Status != "started" || records[0].Status != "failed" || records[0].Error != string(apperror.Cancelled) {
		t.Fatalf("canceled audit lifecycle = inserted:%#v final:%#v", inserted[0], records[0])
	}
}

func TestAgentServiceApplyFileEditRequiresAuditBeforeTakingPlan(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, ".env")
	original := []byte("VALUE=old\n")
	content := "VALUE=new\n"
	if err := os.WriteFile(target, original, 0o600); err != nil {
		t.Fatalf("seed target: %v", err)
	}
	plans, planID := saveAgentFileEditTestPlan(t, root, ".env", content, false, hashAgentFile(original))
	service := &AgentService{Plans: plans}

	if result, err := service.ApplyFileEdit(context.Background(), planID, ""); result != nil || !apperror.IsCode(err, apperror.Internal) {
		t.Fatalf("ApplyFileEdit() without audit = (%#v, %v), want fail-closed internal error", result, err)
	}
	assertFileContent(t, target, string(original))

	service.Audit = newFakeAgentAuditStore()
	if result, err := service.ApplyFileEdit(context.Background(), planID, ""); err != nil || result == nil {
		t.Fatalf("ApplyFileEdit() after configuring audit = (%#v, %v), want retained plan to apply", result, err)
	}
	assertFileContent(t, target, content)
}

func saveAgentFileEditTestPlan(t *testing.T, root string, relativePath string, content string, create bool, originalHash string) (*security.AgentFileEditPlanStore, string) {
	t.Helper()
	plans := security.NewAgentFileEditPlanStore(nil)
	t.Cleanup(plans.Close)
	planID := "plan-agent-file-audit-test"
	plan := security.AgentFileEditPlan{
		Plan: models.CommandPlan{
			PlanID:    planID,
			Risk:      models.RiskNeedsConfirmation,
			ExpiresAt: time.Now().UTC().Add(time.Minute),
		},
		ProjectID:    "linux_native/agent-audit-test",
		ProjectName:  "agent-audit-test",
		WorkingDir:   root,
		RelativePath: relativePath,
		AbsolutePath: filepath.Join(root, filepath.FromSlash(relativePath)),
		Content:      content,
		OriginalHash: originalHash,
		CreateFile:   create,
	}
	if err := plans.Save(plan); err != nil {
		t.Fatalf("save agent file edit plan: %v", err)
	}
	return plans, planID
}

func assertAgentAuditDoesNotContain(t *testing.T, records []store.AuditRecord, secret string) {
	t.Helper()
	if strings.Contains(fmt.Sprintf("%#v", records), secret) {
		t.Fatalf("audit records leaked %q: %#v", secret, records)
	}
}

func assertMarshaledErrorDoesNotContain(t *testing.T, err error, secret string) {
	t.Helper()
	if payload := string(apperror.Marshal(err)); strings.Contains(payload, secret) {
		t.Fatalf("marshaled error leaked %q: %s", secret, payload)
	}
}

func assertFileContent(t *testing.T, path string, want string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(raw) != want {
		t.Fatalf("%s = %q, want %q", path, raw, want)
	}
}
