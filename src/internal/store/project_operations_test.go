package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/runtimescope"
)

func TestProjectOperationGateCanceledDeletionStaysFencedUntilOperationsExit(t *testing.T) {
	t.Parallel()
	gate := newProjectOperationGate()
	scope := runtimescope.Must("linux_native", "default")
	key, ok := projectOperationKeyFromScope(scope, "linux_native/gated")
	if !ok {
		t.Fatal("projectOperationKeyFromScope() rejected valid scope")
	}

	operationCtx, releaseOperation, err := gate.beginOperation(context.Background(), key, 0)
	if err != nil {
		t.Fatalf("beginOperation() error = %v", err)
	}
	defer releaseOperation()

	deleteCtx, cancelDeletion := context.WithCancel(context.Background())
	deletionDone := make(chan error, 1)
	go func() {
		_, err := gate.beginDeletion(deleteCtx, key)
		deletionDone <- err
	}()

	select {
	case <-operationCtx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("deletion did not cancel the active operation")
	}
	cancelDeletion()
	select {
	case err := <-deletionDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("beginDeletion() error = %v, want context canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled deletion did not return")
	}

	if _, err := gate.generation(key); !errors.Is(err, ErrProjectOperationSuperseded) {
		t.Fatalf("generation() while old operation unwinds error = %v, want superseded", err)
	}
	if _, _, err := gate.beginOperation(context.Background(), key, 2); !errors.Is(err, ErrProjectOperationSuperseded) {
		t.Fatalf("beginOperation(new generation) while old operation unwinds error = %v, want superseded", err)
	}

	releaseOperation()
	deadline := time.Now().Add(2 * time.Second)
	for {
		generation, err := gate.generation(key)
		if err == nil {
			if generation != 2 {
				t.Fatalf("generation after drain = %d, want 2", generation)
			}
			break
		}
		if !errors.Is(err, ErrProjectOperationSuperseded) {
			t.Fatalf("generation after drain error = %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("deletion fence was not released after the operation exited")
		}
		time.Sleep(time.Millisecond)
	}

	nextCtx, releaseNext, err := gate.beginOperation(context.Background(), key, 2)
	if err != nil {
		t.Fatalf("beginOperation(next generation) error = %v", err)
	}
	if nextCtx.Err() != nil {
		t.Fatalf("next operation context error = %v", nextCtx.Err())
	}
	releaseNext()
}

func TestProjectOperationGateMutationIsExclusiveAndAdvancesRevision(t *testing.T) {
	t.Parallel()
	gate := newProjectOperationGate()
	key := projectOperationKey{
		providerID:  "linux_native",
		contextName: "default",
		projectID:   "linux_native/exclusive",
	}

	firstCtx, releaseFirst, err := gate.beginOperation(context.Background(), key, 0)
	if err != nil {
		t.Fatalf("beginOperation(first) error = %v", err)
	}
	if firstCtx.Err() != nil {
		t.Fatalf("first operation context error = %v", firstCtx.Err())
	}
	if _, err := gate.generation(key); !errors.Is(err, ErrProjectOperationInProgress) {
		t.Fatalf("generation() during mutation error = %v, want in progress", err)
	}
	if _, _, err := gate.beginOperation(context.Background(), key, 0); !errors.Is(err, ErrProjectOperationInProgress) {
		t.Fatalf("beginOperation(concurrent) error = %v, want in progress", err)
	}

	releaseFirst()
	generation, err := gate.generation(key)
	if err != nil {
		t.Fatalf("generation() after first mutation error = %v", err)
	}
	if generation != 1 {
		t.Fatalf("generation() after first mutation = %d, want 1", generation)
	}
	if _, _, err := gate.beginOperation(context.Background(), key, 0); !errors.Is(err, ErrProjectOperationSuperseded) {
		t.Fatalf("beginOperation(stale revision) error = %v, want superseded", err)
	}
	_, releaseSecond, err := gate.beginOperation(context.Background(), key, generation)
	if err != nil {
		t.Fatalf("beginOperation(current revision) error = %v", err)
	}
	releaseSecond()
}

func TestProjectOperationGateGenerationProbeDoesNotRetainUnknownProject(t *testing.T) {
	t.Parallel()
	gate := newProjectOperationGate()
	key := projectOperationKey{
		providerID:  "linux_native",
		contextName: "default",
		projectID:   "linux_native/missing",
	}
	generation, err := gate.generation(key)
	if err != nil {
		t.Fatalf("generation() error = %v", err)
	}
	if generation != 0 {
		t.Fatalf("generation() = %d, want implicit generation zero", generation)
	}
	if len(gate.entries) != 0 {
		t.Fatalf("generation probe retained %d gate entries, want 0", len(gate.entries))
	}
}

func TestDeleteStaleDetectedProjectsOnlyDeletesLeasedCandidates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openStoreForProjectTest(t)
	projects := db.Projects()
	scope := runtimescope.Must("linux_native", "default")
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	staleAt := now.Add(-48 * time.Hour)
	cutoff := now.Add(-24 * time.Hour)
	leasedID := "linux_native/leased-stale"
	unleasedID := "linux_native/late-stale"
	if err := projects.SaveSnapshot(ctx, scope, []ProjectRecord{
		{
			ID:          leasedID,
			ProviderID:  scope.ProviderID(),
			ContextName: scope.ContextName(),
			Name:        "leased-stale",
			Source:      ProjectSourceLabels,
			LastSeenAt:  staleAt,
		},
		{
			ID:          unleasedID,
			ProviderID:  scope.ProviderID(),
			ContextName: scope.ContextName(),
			Name:        "late-stale",
			Source:      ProjectSourceLabels,
			LastSeenAt:  staleAt,
		},
	}, nil, staleAt, time.Time{}); err != nil {
		t.Fatalf("SaveSnapshot() error = %v", err)
	}

	tx, err := db.writer.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := deleteStaleDetectedProjects(
		ctx,
		tx,
		scope.ProviderID(),
		scope.ContextName(),
		cutoff,
		[]string{leasedID},
	); err != nil {
		t.Fatalf("deleteStaleDetectedProjects() error = %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}

	if _, err := projects.GetInScope(ctx, scope, leasedID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("leased stale project lookup error = %v, want sql.ErrNoRows", err)
	}
	if _, err := projects.GetInScope(ctx, scope, unleasedID); err != nil {
		t.Fatalf("unleased stale project was deleted: %v", err)
	}
}
