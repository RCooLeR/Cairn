package registry

import (
	"bytes"
	"context"
	_ "crypto/sha256"
	_ "crypto/sha512"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	digest "github.com/opencontainers/go-digest"
)

const (
	manifestAccept                    = "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json"
	maxRegistryDigestHeaderBytes      = 256
	maxRegistryContentTypeHeaderBytes = 256
)

func (m *Manager) ResolveDigest(ctx context.Context, image string, opts ResolveOptions) (*DigestResult, error) {
	ref, err := NormalizeImageRef(image)
	if err != nil {
		return nil, err
	}
	platform := normalizePlatform(opts.Platform)
	now := m.now()
	if ref.Pinned {
		return &DigestResult{
			Ref:            ref,
			ManifestDigest: ref.Digest,
			CheckedAt:      now,
		}, nil
	}
	key := cacheKey(ref, platform)
	if !opts.BypassCache {
		if cached, ok := m.cached(key, now); ok {
			cached.FromCache = true
			return &cached, nil
		}
	}
	if retryAfter, err := m.checkCircuit(ref.Registry, now); err != nil {
		return &DigestResult{Ref: ref, RateLimited: true, RetryAfter: retryAfter, CheckedAt: now}, err
	}

	ctx, cancel := context.WithTimeout(ctx, defaultTotalTimeout)
	defer cancel()

	var result *DigestResult
	err = m.withLimits(ctx, ref.Registry, func() error {
		provider, err := m.provider(ctx)
		if err != nil {
			return err
		}
		creds, err := m.credentialForRegistry(ctx, provider, ref.Registry)
		if err != nil {
			return err
		}
		resolved, err := m.resolveRemoteDigest(ctx, ref, platform, creds)
		if err != nil {
			return err
		}
		result = resolved
		return nil
	})
	if err != nil {
		m.recordRegistryFailure(ref.Registry, err, now, retryAfterFromError(err))
		return nil, err
	}
	m.recordRegistrySuccess(ref.Registry)
	m.storeCache(key, *result, now.Add(m.cacheTTL()))
	return result, nil
}

func (m *Manager) resolveRemoteDigest(ctx context.Context, ref ImageRef, platform Platform, creds credential) (*DigestResult, error) {
	tag := ref.Tag
	if tag == "" {
		tag = "latest"
	}
	scope := "repository:" + ref.Repository + ":pull"
	manifestURL := m.registryBaseURL(ref.Registry) + "/v2/" + strings.Trim(ref.Repository, "/") + "/manifests/" + url.PathEscape(tag)
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, manifestURL, nil)
	if err != nil {
		return nil, apperror.Wrap(apperror.RegistryUnreachable, "Build registry manifest request failed", err)
	}
	req.Header.Set("Accept", manifestAccept)
	resp, err := m.doAuthenticated(req, ref.Registry, scope, creds)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if err := statusError(resp); err != nil {
		return nil, err
	}
	digestValue, err := validatedRegistryDigest(resp.Header.Get("Docker-Content-Digest"))
	if err != nil {
		return nil, err
	}
	mediaType, err := validatedManifestMediaType(resp.Header.Get("Content-Type"))
	if err != nil {
		return nil, err
	}
	result := &DigestResult{
		Ref:            ref,
		ManifestDigest: digestValue,
		MediaType:      mediaType,
		CheckedAt:      m.now(),
	}
	if isIndexMediaType(mediaType) {
		result.IndexDigest = digestValue
		manifestDigest, err := m.resolvePlatformManifest(ctx, ref, tag, scope, platform, creds)
		if err != nil {
			return nil, err
		}
		result.ManifestDigest = manifestDigest
	}
	return result, nil
}

func (m *Manager) resolvePlatformManifest(ctx context.Context, ref ImageRef, tag string, scope string, platform Platform, creds credential) (string, error) {
	manifestURL := m.registryBaseURL(ref.Registry) + "/v2/" + strings.Trim(ref.Repository, "/") + "/manifests/" + url.PathEscape(tag)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return "", apperror.Wrap(apperror.RegistryUnreachable, "Build registry index request failed", err)
	}
	req.Header.Set("Accept", manifestAccept)
	resp, err := m.doAuthenticated(req, ref.Registry, scope, creds)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if err := statusError(resp); err != nil {
		return "", err
	}
	manifestDigest, err := decodePlatformManifest(resp.Body, resp.ContentLength, platform)
	if err != nil {
		return "", apperror.Wrap(apperror.RegistryUnreachable, "Parse registry index failed", err)
	}
	if manifestDigest != "" {
		return manifestDigest, nil
	}
	return "", apperror.New(apperror.RegistryUnreachable, "No manifest matched the requested platform", apperror.WithDetail(fmt.Sprintf("%s/%s", platform.OS, platform.Architecture)))
}

func decodePlatformManifest(body io.Reader, contentLength int64, want Platform) (string, error) {
	raw, err := readBoundedBody(body, contentLength, maxManifestIndexResponseBytes)
	if err != nil {
		return "", err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil {
		return "", err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return "", fmt.Errorf("registry index must be a JSON object")
	}
	matched := ""
	entryCount := 0
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return "", err
		}
		key, ok := keyToken.(string)
		if !ok {
			return "", fmt.Errorf("registry index contains a non-string object key")
		}
		if key != "manifests" {
			var ignored json.RawMessage
			if err := decoder.Decode(&ignored); err != nil {
				return "", err
			}
			continue
		}
		token, err := decoder.Token()
		if err != nil {
			return "", err
		}
		if delimiter, ok := token.(json.Delim); !ok || delimiter != '[' {
			return "", fmt.Errorf("registry manifests must be a JSON array")
		}
		for decoder.More() {
			entryCount++
			if entryCount > maxManifestIndexEntries {
				return "", fmt.Errorf("registry index contains more than %d manifests", maxManifestIndexEntries)
			}
			var manifest struct {
				Digest   string `json:"digest"`
				Platform struct {
					OS           string `json:"os"`
					Architecture string `json:"architecture"`
					Variant      string `json:"variant"`
				} `json:"platform"`
			}
			if err := decoder.Decode(&manifest); err != nil {
				return "", err
			}
			if len(manifest.Digest) > maxRegistryDigestHeaderBytes || len(manifest.Platform.OS) > 128 || len(manifest.Platform.Architecture) > 128 || len(manifest.Platform.Variant) > 128 {
				return "", fmt.Errorf("registry index contains an oversized field")
			}
			if manifest.Digest != "" {
				if _, err := digest.Parse(manifest.Digest); err != nil {
					return "", fmt.Errorf("registry index contains an invalid digest: %w", err)
				}
			}
			if matched == "" && manifest.Digest != "" && platformMatches(want, Platform{
				OS: manifest.Platform.OS, Architecture: manifest.Platform.Architecture, Variant: manifest.Platform.Variant,
			}) {
				matched = manifest.Digest
			}
		}
		if _, err := decoder.Token(); err != nil {
			return "", err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return "", err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return "", fmt.Errorf("registry index contains multiple JSON values")
		}
		return "", err
	}
	return matched, nil
}

func validatedRegistryDigest(header string) (string, error) {
	if len(header) > maxRegistryDigestHeaderBytes {
		return "", apperror.New(apperror.RegistryUnreachable, "Registry digest header exceeds safe size limit")
	}
	value := strings.TrimSpace(header)
	if value == "" {
		return "", apperror.New(apperror.RegistryUnreachable, "Registry response did not include Docker-Content-Digest")
	}
	if _, err := digest.Parse(value); err != nil {
		return "", apperror.Wrap(apperror.RegistryUnreachable, "Registry response included an invalid Docker-Content-Digest", err)
	}
	return value, nil
}

func validatedManifestMediaType(header string) (string, error) {
	if len(header) > maxRegistryContentTypeHeaderBytes {
		return "", apperror.New(apperror.RegistryUnreachable, "Registry Content-Type header exceeds safe size limit")
	}
	if strings.TrimSpace(header) == "" {
		return "", nil
	}
	mediaType, _, err := mime.ParseMediaType(header)
	if err != nil || len(mediaType) > 128 {
		return "", apperror.New(apperror.RegistryUnreachable, "Registry response included an invalid Content-Type header")
	}
	return mediaType, nil
}

func (m *Manager) registryBaseURL(registry string) string {
	host := registryAPIHost(registry)
	scheme := "https"
	if isPlainHTTPRegistry(registry) || (m != nil && m.PlainHTTPRegistries[normalizeRegistryHost(registry)]) {
		scheme = "http"
	}
	return scheme + "://" + host
}

func isPlainHTTPRegistry(registry string) bool {
	host := normalizeRegistryHost(registry)
	if splitHost, _, err := net.SplitHostPort(host); err == nil {
		host = splitHost
	}
	host = strings.Trim(host, "[]")
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func statusError(resp *http.Response) error {
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return apperror.New(apperror.RegistryAuth, "Registry authentication required", apperror.WithDetail(resp.Status))
	case resp.StatusCode == http.StatusTooManyRequests:
		detail := resp.Status
		if retry := retryAfter(resp.Header.Get("Retry-After")); retry > 0 {
			detail += "; retry-after=" + retry.String()
			return retryAfterError{
				error:      apperror.New(apperror.RegistryRateLimit, "Registry rate limit reached", apperror.WithDetail(detail)),
				retryAfter: retry,
			}
		}
		return apperror.New(apperror.RegistryRateLimit, "Registry rate limit reached", apperror.WithDetail(detail))
	case resp.StatusCode == http.StatusNotFound:
		return apperror.New(apperror.NotFound, "Registry tag no longer exists", apperror.WithDetail(resp.Status))
	case resp.StatusCode >= 500:
		return apperror.New(apperror.RegistryUnreachable, "Registry is unavailable", apperror.WithDetail(resp.Status))
	default:
		return apperror.New(apperror.RegistryUnreachable, "Registry request failed", apperror.WithDetail(resp.Status))
	}
}

func manifestMediaType(contentType string) string {
	if contentType == "" {
		return ""
	}
	mediaType, _, _ := strings.Cut(contentType, ";")
	return strings.TrimSpace(mediaType)
}

func isIndexMediaType(mediaType string) bool {
	return strings.Contains(mediaType, "manifest.list") || strings.Contains(mediaType, "image.index")
}

func normalizePlatform(platform Platform) Platform {
	if strings.TrimSpace(platform.OS) == "" {
		platform.OS = runtime.GOOS
	}
	if strings.TrimSpace(platform.Architecture) == "" {
		platform.Architecture = runtime.GOARCH
	}
	platform.OS = strings.ToLower(strings.TrimSpace(platform.OS))
	platform.Architecture = strings.ToLower(strings.TrimSpace(platform.Architecture))
	platform.Variant = strings.ToLower(strings.TrimSpace(platform.Variant))
	return platform
}

func platformMatches(want Platform, got Platform) bool {
	got = normalizePlatform(got)
	want = normalizePlatform(want)
	if want.OS != got.OS || want.Architecture != got.Architecture {
		return false
	}
	return want.Variant == "" || want.Variant == got.Variant
}

func cacheKey(ref ImageRef, platform Platform) string {
	platform = normalizePlatform(platform)
	return ref.Registry + "/" + ref.Repository + ":" + ref.Tag + "|" + platform.OS + "/" + platform.Architecture + "/" + platform.Variant
}

func (m *Manager) cached(key string, now time.Time) (DigestResult, bool) {
	if m == nil {
		return DigestResult{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.cache[key]
	if !ok || !now.Before(entry.ExpiresAt) {
		delete(m.cache, key)
		return DigestResult{}, false
	}
	return entry.Result, true
}

func (m *Manager) storeCache(key string, result DigestResult, expiresAt time.Time) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cache == nil {
		m.cache = map[string]cacheEntry{}
	}
	now := m.now()
	for cachedKey, entry := range m.cache {
		if !now.Before(entry.ExpiresAt) {
			delete(m.cache, cachedKey)
		}
	}
	limit := m.cacheEntryLimit
	if limit <= 0 {
		limit = defaultCacheEntryLimit
	}
	if _, exists := m.cache[key]; !exists && len(m.cache) >= limit {
		var oldestKey string
		var oldestExpiry time.Time
		for cachedKey, entry := range m.cache {
			if oldestKey == "" || entry.ExpiresAt.Before(oldestExpiry) {
				oldestKey = cachedKey
				oldestExpiry = entry.ExpiresAt
			}
		}
		delete(m.cache, oldestKey)
	}
	m.cache[key] = cacheEntry{Result: result, ExpiresAt: expiresAt}
}

func (m *Manager) withLimits(ctx context.Context, registry string, fn func() error) error {
	if m == nil {
		return fn()
	}
	select {
	case m.globalLimit <- struct{}{}:
		defer func() { <-m.globalLimit }()
	case <-ctx.Done():
		return apperror.Wrap(apperror.Timeout, "Registry check timed out", ctx.Err())
	}
	gate, releaseGate := m.registryLimiter(registry)
	defer releaseGate()
	select {
	case gate <- struct{}{}:
		defer func() { <-gate }()
	case <-ctx.Done():
		return apperror.Wrap(apperror.Timeout, "Registry check timed out", ctx.Err())
	}
	return fn()
}

func (m *Manager) registryLimiter(registry string) (chan struct{}, func()) {
	m.mu.Lock()
	key := normalizeRegistryHost(registry)
	if m.registryGate == nil {
		m.registryGate = map[string]*registryGateState{}
	}
	state, ok := m.registryGate[key]
	if !ok {
		limit := m.perRegistryLimit
		if limit <= 0 {
			limit = defaultPerRegistryLimit
		}
		state = &registryGateState{gate: make(chan struct{}, limit)}
		m.registryGate[key] = state
	}
	state.refs++
	m.mu.Unlock()
	return state.gate, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		state.refs--
		if state.refs == 0 && m.registryGate[key] == state {
			delete(m.registryGate, key)
		}
	}
}

func (m *Manager) checkCircuit(registry string, now time.Time) (time.Duration, error) {
	if m == nil {
		return 0, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := normalizeRegistryHost(registry)
	state, ok := m.circuit[key]
	if !ok {
		return 0, nil
	}
	if (!state.OpenUntil.IsZero() && !now.Before(state.OpenUntil)) || (!state.LastTouched.IsZero() && now.Sub(state.LastTouched) >= 10*time.Minute) {
		delete(m.circuit, key)
		return 0, nil
	}
	if now.Before(state.OpenUntil) {
		state.LastTouched = now
		m.circuit[key] = state
		err := apperror.New(apperror.RegistryRateLimit, "Registry backoff is active")
		return state.OpenUntil.Sub(now), err
	}
	return 0, nil
}

func (m *Manager) recordRegistrySuccess(registry string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.circuit, normalizeRegistryHost(registry))
}

func (m *Manager) recordRegistryFailure(registry string, err error, now time.Time, retryAfter time.Duration) {
	if m == nil {
		return
	}
	if !apperror.IsCode(err, apperror.RegistryRateLimit) && !apperror.IsCode(err, apperror.RegistryUnreachable) && !apperror.IsCode(err, apperror.Timeout) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.circuit == nil {
		m.circuit = map[string]circuitState{}
	}
	key := normalizeRegistryHost(registry)
	for circuitKey, existing := range m.circuit {
		if (!existing.OpenUntil.IsZero() && !now.Before(existing.OpenUntil)) || (!existing.LastTouched.IsZero() && now.Sub(existing.LastTouched) >= 10*time.Minute) {
			delete(m.circuit, circuitKey)
		}
	}
	limit := m.circuitEntryLimit
	if limit <= 0 {
		limit = defaultCircuitEntryLimit
	}
	if _, exists := m.circuit[key]; !exists && len(m.circuit) >= limit {
		var oldestKey string
		var oldestTouched time.Time
		for circuitKey, existing := range m.circuit {
			if oldestKey == "" || existing.LastTouched.Before(oldestTouched) {
				oldestKey = circuitKey
				oldestTouched = existing.LastTouched
			}
		}
		delete(m.circuit, oldestKey)
	}
	state := m.circuit[key]
	state.Failures++
	state.LastTouched = now
	if retryAfter > 0 {
		state.OpenUntil = now.Add(retryAfter)
	} else if apperror.IsCode(err, apperror.RegistryRateLimit) || state.Failures >= 5 {
		state.OpenUntil = now.Add(10 * time.Minute)
	}
	m.circuit[key] = state
}

func retryAfterFromError(err error) time.Duration {
	if err == nil || !apperror.IsCode(err, apperror.RegistryRateLimit) {
		return 0
	}
	var withRetryAfter interface {
		RetryAfter() time.Duration
	}
	if errors.As(err, &withRetryAfter) {
		return withRetryAfter.RetryAfter()
	}
	return 0
}

type retryAfterError struct {
	error
	retryAfter time.Duration
}

func (e retryAfterError) Unwrap() error {
	return e.error
}

func (e retryAfterError) RetryAfter() time.Duration {
	return e.retryAfter
}

func retryAfter(header string) time.Duration {
	value := strings.TrimSpace(header)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		return time.Duration(seconds) * time.Second
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return 0
	}
	return time.Until(when)
}
