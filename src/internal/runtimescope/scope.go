package runtimescope

import "strings"

const windowsWSLContextPrefix = "wsl:"

// Scope identifies one immutable provider/backend runtime binding.
// Its fields are private so callers cannot construct or mutate a partial scope.
type Scope struct {
	providerID  string
	contextName string
}

func New(providerID string, contextName string) (Scope, bool) {
	providerID = strings.TrimSpace(providerID)
	contextName = strings.TrimSpace(contextName)
	if providerID == "" || contextName == "" {
		return Scope{}, false
	}
	return Scope{providerID: providerID, contextName: contextName}, true
}

// Must is intended for static configuration and test fixtures.
func Must(providerID string, contextName string) Scope {
	scope, ok := New(providerID, contextName)
	if !ok {
		panic("runtime scope requires provider and context")
	}
	return scope
}

func (s Scope) ProviderID() string { return s.providerID }

func (s Scope) ContextName() string { return s.contextName }

func (s Scope) Valid() bool { return s.providerID != "" && s.contextName != "" }

func (s Scope) Matches(providerID string, contextName string) bool {
	return s.Valid() && s.providerID == strings.TrimSpace(providerID) && s.contextName == strings.TrimSpace(contextName)
}

func (s Scope) Equal(other Scope) bool {
	return s.Valid() && other.Valid() && s.providerID == other.providerID && s.contextName == other.contextName
}

// WindowsWSLContextV1 returns the canonical runtime identity for one WSL
// distribution. The V1 suffix freezes the persisted identity contract used by
// schema migration 0012; changing it requires a new migration and helper.
func WindowsWSLContextV1(distro string) (string, bool) {
	distro = strings.TrimSpace(distro)
	if distro == "" {
		return "", false
	}
	return windowsWSLContextPrefix + strings.ToLower(distro), true
}

// CanonicalizeWindowsWSLContextV1 canonicalizes a persisted wsl:<distro>
// identity with the same Unicode-aware rule used by the runtime provider.
func CanonicalizeWindowsWSLContextV1(contextName string) (string, bool) {
	contextName = strings.TrimSpace(contextName)
	if len(contextName) < len(windowsWSLContextPrefix) ||
		!strings.EqualFold(contextName[:len(windowsWSLContextPrefix)], windowsWSLContextPrefix) {
		return "", false
	}
	return WindowsWSLContextV1(contextName[len(windowsWSLContextPrefix):])
}
