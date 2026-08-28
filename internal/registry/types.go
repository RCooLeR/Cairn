package registry

import (
	"context"
	"maps"
	"net/http"
	"sync"
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
	"github.com/RCooLeR/Cairn/internal/store"
)

const (
	DefaultRegistry      = "docker.io"
	dockerHubAPIRegistry = "registry-1.docker.io"

	defaultCacheTTL          = time.Hour
	defaultRequestTimeout    = 10 * time.Second
	defaultTotalTimeout      = 30 * time.Second
	defaultGlobalLimit       = 8
	defaultPerRegistryLimit  = 3
	defaultCacheEntryLimit   = 1024
	defaultCircuitEntryLimit = 256

	maxTokenResponseBytes         = 64 << 10
	maxManifestIndexResponseBytes = 4 << 20
	maxRegistryTokenBytes         = 32 << 10
	maxManifestIndexEntries       = 4096
)

type ProviderResolver interface {
	ActiveProvider(context.Context) (providers.PlatformProvider, error)
}

type DockerInputRunner interface {
	RunDockerWithInput(context.Context, string, ...string) (*providers.CommandResult, error)
}

type BackendCommandRunner interface {
	RunBackendCommand(context.Context, string, ...string) (*providers.CommandResult, error)
}

type Manager struct {
	Providers ProviderResolver
	Audit     *store.AuditRepository
	Settings  *store.SettingsRepository

	HTTPClient          *http.Client
	Now                 func() time.Time
	CacheTTL            time.Duration
	PlainHTTPRegistries map[string]bool
	// TrustedAuthRealms maps a registry host to additional HTTPS origins that
	// may receive credentials for that registry. Same-origin realms are always
	// accepted; cross-origin realms must be listed explicitly.
	TrustedAuthRealms map[string][]string

	globalLimit       chan struct{}
	perRegistryLimit  int
	cacheEntryLimit   int
	circuitEntryLimit int

	mu           sync.Mutex
	loginMu      sync.Mutex
	configMu     sync.Mutex
	cache        map[string]cacheEntry
	registryGate map[string]*registryGateState
	circuit      map[string]circuitState
}

type cacheEntry struct {
	Result    DigestResult
	ExpiresAt time.Time
}

type circuitState struct {
	Failures    int
	OpenUntil   time.Time
	LastTouched time.Time
}

type registryGateState struct {
	gate chan struct{}
	refs int
}

type ImageRef struct {
	Registry   string `json:"registry"`
	Repository string `json:"repository"`
	Tag        string `json:"tag,omitempty"`
	Digest     string `json:"digest,omitempty"`
	Pinned     bool   `json:"pinned"`
	Normalized string `json:"normalized"`
}

type Platform struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	Variant      string `json:"variant,omitempty"`
}

type ResolveOptions struct {
	BypassCache bool
	Platform    Platform
}

type DigestResult struct {
	Ref            ImageRef
	IndexDigest    string
	ManifestDigest string
	MediaType      string
	FromCache      bool
	RateLimited    bool
	RetryAfter     time.Duration
	CheckedAt      time.Time
}

type dockerConfig struct {
	Auths       map[string]dockerAuth `json:"auths"`
	CredHelpers map[string]string     `json:"credHelpers"`
	CredsStore  string                `json:"credsStore"`
}

type dockerAuth struct {
	Auth          string `json:"auth"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	IdentityToken string `json:"identitytoken"`
}

type credential struct {
	Username      string
	Password      string
	IdentityToken string
	Source        string
}

func NewManager(providers ProviderResolver, audit *store.AuditRepository) *Manager {
	return &Manager{
		Providers:  providers,
		Audit:      audit,
		HTTPClient: &http.Client{Timeout: defaultRequestTimeout},
		Now:        func() time.Time { return time.Now().UTC() },
		CacheTTL:   defaultCacheTTL,
		TrustedAuthRealms: map[string][]string{
			DefaultRegistry:       {"https://auth.docker.io"},
			dockerHubAPIRegistry:  {"https://auth.docker.io"},
			"registry.gitlab.com": {"https://gitlab.com"},
		},
		globalLimit:       make(chan struct{}, defaultGlobalLimit),
		perRegistryLimit:  defaultPerRegistryLimit,
		cacheEntryLimit:   defaultCacheEntryLimit,
		circuitEntryLimit: defaultCircuitEntryLimit,
		cache:             map[string]cacheEntry{},
		registryGate:      map[string]*registryGateState{},
		circuit:           map[string]circuitState{},
	}
}

// CloneBoundTo returns an independent registry manager that resolves
// credentials through one runtime-bound provider resolver. Static policy is
// copied from the receiver, while all provider-sensitive runtime state (digest
// cache, circuit breakers, limiters, and operation locks) starts empty.
//
// The returned manager may safely outlive or run concurrently with the source
// manager. In particular, it does not reuse a digest that was authorized under
// another provider's Docker configuration.
func (m *Manager) CloneBoundTo(providers ProviderResolver) *Manager {
	bound := NewManager(providers, nil)
	if m == nil {
		return bound
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	bound.Audit = m.Audit
	bound.Settings = m.Settings
	bound.HTTPClient = m.HTTPClient
	bound.Now = m.Now
	bound.CacheTTL = m.CacheTTL
	bound.PlainHTTPRegistries = cloneRegistryBoolMap(m.PlainHTTPRegistries)
	bound.TrustedAuthRealms = cloneRegistryStringSliceMap(m.TrustedAuthRealms)
	bound.perRegistryLimit = m.perRegistryLimit
	bound.cacheEntryLimit = m.cacheEntryLimit
	bound.circuitEntryLimit = m.circuitEntryLimit
	if globalLimit := cap(m.globalLimit); globalLimit > 0 {
		bound.globalLimit = make(chan struct{}, globalLimit)
	}
	return bound
}

func cloneRegistryBoolMap(source map[string]bool) map[string]bool {
	if source == nil {
		return nil
	}
	cloned := make(map[string]bool, len(source))
	maps.Copy(cloned, source)
	return cloned
}

func cloneRegistryStringSliceMap(source map[string][]string) map[string][]string {
	if source == nil {
		return nil
	}
	cloned := make(map[string][]string, len(source))
	for key, values := range source {
		cloned[key] = append([]string(nil), values...)
	}
	return cloned
}

func (m *Manager) now() time.Time {
	if m != nil && m.Now != nil {
		return m.Now().UTC()
	}
	return time.Now().UTC()
}

func (m *Manager) httpClient() *http.Client {
	if m != nil && m.HTTPClient != nil {
		return m.HTTPClient
	}
	return &http.Client{Timeout: defaultRequestTimeout}
}

func (m *Manager) cacheTTL() time.Duration {
	if m != nil && m.CacheTTL > 0 {
		return m.CacheTTL
	}
	return defaultCacheTTL
}

func (m *Manager) provider(ctx context.Context) (providers.PlatformProvider, error) {
	if m == nil || m.Providers == nil {
		return nil, notReady()
	}
	return m.Providers.ActiveProvider(ctx)
}

func account(registry string, username string, source string, verified time.Time) models.RegistryAccount {
	return models.RegistryAccount{
		Registry:       registry,
		Username:       username,
		Source:         source,
		LoggedIn:       true,
		LastVerifiedAt: verified,
	}
}
