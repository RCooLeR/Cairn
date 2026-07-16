package providers

import (
	"context"
	"errors"
	"testing"

	"github.com/RCooLeR/Cairn/internal/apperror"
)

type runtimeScopeProviderStub struct {
	id              string
	contextName     string
	contextErr      error
	backendIdentity *string
	backendErr      error
}

func (p runtimeScopeProviderStub) ID() string { return p.id }
func (p runtimeScopeProviderStub) DockerContext(context.Context) (string, error) {
	return p.contextName, p.contextErr
}

type managedRuntimeScopeProviderStub struct{ runtimeScopeProviderStub }

type unsupportedSnapshotProvider struct{ PlatformProvider }

func (p managedRuntimeScopeProviderStub) BackendIdentity(context.Context) (string, error) {
	if p.backendIdentity == nil {
		return "", p.backendErr
	}
	return *p.backendIdentity, p.backendErr
}

func TestSnapshotRuntimeProviderRejectsUnsupportedMutableProvider(t *testing.T) {
	t.Parallel()
	if _, err := SnapshotRuntimeProvider(context.Background(), unsupportedSnapshotProvider{}); !apperror.IsCode(err, apperror.ProviderNotReady) {
		t.Fatalf("SnapshotRuntimeProvider() error = %v, want ProviderNotReady", err)
	}
}

func TestSnapshotRuntimeProviderFreezesWSLDistro(t *testing.T) {
	t.Parallel()
	original := NewWindowsWSL(WindowsWSLOptions{Distro: "cairn-a", Runner: newFakeRunner()})
	frozen, err := SnapshotRuntimeProvider(context.Background(), original)
	if err != nil {
		t.Fatalf("SnapshotRuntimeProvider() error = %v", err)
	}
	original.SetDistro("cairn-b")
	scope, err := ResolveRuntimeScope(context.Background(), frozen)
	if err != nil {
		t.Fatalf("ResolveRuntimeScope() error = %v", err)
	}
	if !scope.Matches(windowsWSLID, "wsl:cairn-a") {
		t.Fatalf("frozen scope = %q/%q", scope.ProviderID(), scope.ContextName())
	}
}

func TestWindowsWSLBackendIdentityNormalizesDistroCase(t *testing.T) {
	t.Parallel()
	upper := NewWindowsWSL(WindowsWSLOptions{Distro: "Ubuntu"})
	lower := NewWindowsWSL(WindowsWSLOptions{Distro: "ubuntu"})
	upperScope, err := ResolveRuntimeScope(context.Background(), upper)
	if err != nil {
		t.Fatalf("ResolveRuntimeScope(upper) error = %v", err)
	}
	lowerScope, err := ResolveRuntimeScope(context.Background(), lower)
	if err != nil {
		t.Fatalf("ResolveRuntimeScope(lower) error = %v", err)
	}
	if !upperScope.Equal(lowerScope) || upperScope.ContextName() != "wsl:ubuntu" {
		t.Fatalf("case-normalized scopes = %q and %q", upperScope.ContextName(), lowerScope.ContextName())
	}
}

func TestResolveRuntimeScopeUsesContextOnlyForUnmanagedProviders(t *testing.T) {
	scope, err := ResolveRuntimeScope(context.Background(), runtimeScopeProviderStub{id: "ctx:dev", contextName: "dev"})
	if err != nil || !scope.Matches("ctx:dev", "dev") {
		t.Fatalf("ResolveRuntimeScope() = %q/%q, %v", scope.ProviderID(), scope.ContextName(), err)
	}
}

func TestResolveRuntimeScopeFailsClosedForInvalidManagedIdentity(t *testing.T) {
	empty := ""
	for name, provider := range map[string]managedRuntimeScopeProviderStub{
		"error": {runtimeScopeProviderStub: runtimeScopeProviderStub{id: "managed", contextName: "fallback", backendErr: errors.New("identity failed")}},
		"empty": {runtimeScopeProviderStub: runtimeScopeProviderStub{id: "managed", contextName: "fallback", backendIdentity: &empty}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ResolveRuntimeScope(context.Background(), provider)
			if !apperror.IsCode(err, apperror.ProviderNotReady) {
				t.Fatalf("ResolveRuntimeScope() error = %v, want ProviderNotReady", err)
			}
		})
	}
}
