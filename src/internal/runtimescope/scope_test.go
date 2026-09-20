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

func TestWindowsWSLContextV1MatchesPersistedCanonicalization(t *testing.T) {
	t.Parallel()

	fromDistro, ok := WindowsWSLContextV1("  ÜBUNTU  ")
	if !ok || fromDistro != "wsl:übuntu" {
		t.Fatalf("WindowsWSLContextV1() = %q, %v; want wsl:übuntu, true", fromDistro, ok)
	}
	fromContext, ok := CanonicalizeWindowsWSLContextV1("  WSL:ÜBUNTU  ")
	if !ok || fromContext != fromDistro {
		t.Fatalf("CanonicalizeWindowsWSLContextV1() = %q, %v; want %q, true", fromContext, ok, fromDistro)
	}

	for _, invalid := range []string{"", "wsl:", "docker:ubuntu"} {
		if got, valid := CanonicalizeWindowsWSLContextV1(invalid); valid || got != "" {
			t.Fatalf("CanonicalizeWindowsWSLContextV1(%q) = %q, %v; want empty, false", invalid, got, valid)
		}
	}
}
