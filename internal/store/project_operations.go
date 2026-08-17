package store

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/RCooLeR/Cairn/internal/runtimescope"
)

var (
	// ErrProjectOperationSuperseded means a project mutation was planned for a
	// lifecycle revision that is no longer current.
	ErrProjectOperationSuperseded = errors.New("project operation was superseded by a lifecycle revision change")

	// ErrProjectOperationInProgress means another exclusive mutation currently
	// owns the scoped project.
	ErrProjectOperationInProgress = errors.New("another project operation is already in progress")
)

type projectOperationKey struct {
	providerID  string
	contextName string
	projectID   string
}

type projectOperationState struct {
	generation uint64
	deleting   bool
	deleteDone chan struct{}
	active     map[uint64]context.CancelFunc
	idle       chan struct{}
	nextID     uint64
}

type projectOperationGate struct {
	mu      sync.Mutex
	entries map[projectOperationKey]*projectOperationState
}

func newProjectOperationGate() *projectOperationGate {
	return &projectOperationGate{entries: map[projectOperationKey]*projectOperationState{}}
}

func (g *projectOperationGate) generation(key projectOperationKey) (uint64, error) {
	if g == nil {
		return 0, nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	state := g.entries[key]
	if state == nil {
		// Generation zero is the implicit first incarnation. Read-only plan
		// probes must not retain an entry for every unknown project ID.
		return 0, nil
	}
	if state.deleting {
		return 0, ErrProjectOperationSuperseded
	}
	if len(state.active) > 0 {
		return 0, ErrProjectOperationInProgress
	}
	return state.generation, nil
}

func (g *projectOperationGate) beginOperation(
	parent context.Context,
	key projectOperationKey,
	expectedGeneration uint64,
) (context.Context, func(), error) {
	if parent == nil {
		parent = context.Background()
	}
	if err := parent.Err(); err != nil {
		return nil, nil, err
	}
	if g == nil {
		return parent, func() {}, nil
	}

	g.mu.Lock()
	state := g.stateLocked(key)
	if state.deleting {
		g.mu.Unlock()
		return nil, nil, ErrProjectOperationSuperseded
	}
	if len(state.active) > 0 {
		g.mu.Unlock()
		return nil, nil, ErrProjectOperationInProgress
	}
	if state.generation != expectedGeneration {
		g.mu.Unlock()
		return nil, nil, ErrProjectOperationSuperseded
	}
	advanceProjectOperationGeneration(state)
	state.idle = make(chan struct{})
	state.nextID++
	operationID := state.nextID
	operationCtx, cancel := context.WithCancel(parent)
	state.active[operationID] = cancel
	g.mu.Unlock()

	var once sync.Once
	release := func() {
		once.Do(func() {
			cancel()
			g.mu.Lock()
			delete(state.active, operationID)
			if len(state.active) == 0 {
				close(state.idle)
			}
			g.mu.Unlock()
		})
	}
	return operationCtx, release, nil
}

func (g *projectOperationGate) beginDeletion(ctx context.Context, key projectOperationKey) (func(), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if g == nil {
		return func() {}, nil
	}

	for {
		g.mu.Lock()
		state := g.stateLocked(key)
		if state.deleting {
			deleteDone := state.deleteDone
			g.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-deleteDone:
				continue
			}
		}

		state.deleting = true
		advanceProjectOperationGeneration(state)
		state.deleteDone = make(chan struct{})
		deleteDone := state.deleteDone
		idle := state.idle
		cancels := make([]context.CancelFunc, 0, len(state.active))
		for _, cancel := range state.active {
			cancels = append(cancels, cancel)
		}
		g.mu.Unlock()

		for _, cancel := range cancels {
			cancel()
		}
		select {
		case <-ctx.Done():
			// The caller is no longer waiting, but the canceled operations may
			// still be unwinding external work and writing their final history.
			// Keep the deletion fence owned until every operation has released;
			// otherwise a new-generation operation could overlap an old one.
			go func() {
				<-idle
				g.finishDeletion(state, deleteDone)
			}()
			return nil, ctx.Err()
		case <-idle:
		}

		var once sync.Once
		return func() {
			once.Do(func() {
				g.finishDeletion(state, deleteDone)
			})
		}, nil
	}
}

func advanceProjectOperationGeneration(state *projectOperationState) {
	state.generation++
	if state.generation == 0 {
		state.generation = 1
	}
}

func (g *projectOperationGate) finishDeletion(state *projectOperationState, deleteDone chan struct{}) {
	g.mu.Lock()
	if state.deleting && state.deleteDone == deleteDone {
		state.deleting = false
		state.deleteDone = nil
		close(deleteDone)
	}
	g.mu.Unlock()
}

func (g *projectOperationGate) stateLocked(key projectOperationKey) *projectOperationState {
	state := g.entries[key]
	if state != nil {
		return state
	}
	idle := make(chan struct{})
	close(idle)
	state = &projectOperationState{
		active: map[uint64]context.CancelFunc{},
		idle:   idle,
	}
	g.entries[key] = state
	return state
}

func projectOperationKeyFromScope(scope runtimescope.Scope, projectID string) (projectOperationKey, bool) {
	projectID = strings.TrimSpace(projectID)
	if !scope.Valid() || projectID == "" {
		return projectOperationKey{}, false
	}
	return projectOperationKey{
		providerID:  scope.ProviderID(),
		contextName: scope.ContextName(),
		projectID:   projectID,
	}, true
}
