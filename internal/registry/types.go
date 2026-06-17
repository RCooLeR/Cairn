package registry

import (
	"context"
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

	defaultCacheTTL         = time.Hour
	defaultRequestTimeout   = 10 * time.Second
	defaultTotalTimeout     = 30 * time.Second
	defaultGlobalLimit      = 8
	defaultPerRegistryLimit = 3
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

	globalLimit      chan struct{}
	perRegistryLimit int

	mu           sync.Mutex
	cache        map[string]cacheEntry
	registryGate map[string]chan struct{}
	circuit      map[string]circuitState
}

type cacheEntry struct {
	Result    DigestResult
	ExpiresAt time.Time
}

type circuitState struct {
	Failures  int
	OpenUntil time.Time
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
		Providers:        providers,
		Audit:            audit,
		HTTPClient:       &http.Client{Timeout: defaultRequestTimeout},
		Now:              func() time.Time { return time.Now().UTC() },
		CacheTTL:         defaultCacheTTL,
		globalLimit:      make(chan struct{}, defaultGlobalLimit),
		perRegistryLimit: defaultPerRegistryLimit,
		cache:            map[string]cacheEntry{},
		registryGate:     map[string]chan struct{}{},
		circuit:          map[string]circuitState{},
	}
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
