package runtimescope

import "testing"

func TestScopeRequiresBothComponentsAndIsExact(t *testing.T) {
	if _, ok := New("provider", ""); ok {
		t.Fatal("New() accepted an empty context")
	}
	if _, ok := New("", "context"); ok {
		t.Fatal("New() accepted an empty provider")
	}
	scope, ok := New(" provider ", " context ")
	if !ok {
		t.Fatal("New() rejected a complete scope")
	}
	if scope.ProviderID() != "provider" || scope.ContextName() != "context" {
		t.Fatalf("scope = %q/%q", scope.ProviderID(), scope.ContextName())
	}
	if !scope.Matches("provider", "context") || scope.Matches("provider", "other") {
		t.Fatal("Matches() did not enforce exact scope")
	}
}
