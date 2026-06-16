package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
)

const manifestAccept = "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json"

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
		creds, _ := m.credentialForRegistry(ctx, provider, ref.Registry)
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
	digest := strings.TrimSpace(resp.Header.Get("Docker-Content-Digest"))
	mediaType := manifestMediaType(resp.Header.Get("Content-Type"))
	if digest == "" {
		return nil, apperror.New(apperror.RegistryUnreachable, "Registry response did not include Docker-Content-Digest")
	}
	result := &DigestResult{
		Ref:            ref,
		ManifestDigest: digest,
		MediaType:      mediaType,
		CheckedAt:      m.now(),
	}
	if isIndexMediaType(mediaType) {
		result.IndexDigest = digest
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
	var payload struct {
		Manifests []struct {
			Digest   string `json:"digest"`
			Platform struct {
				OS           string `json:"os"`
				Architecture string `json:"architecture"`
				Variant      string `json:"variant"`
			} `json:"platform"`
		} `json:"manifests"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", apperror.Wrap(apperror.RegistryUnreachable, "Parse registry index failed", err)
	}
	for _, manifest := range payload.Manifests {
		if platformMatches(platform, Platform{
			OS:           manifest.Platform.OS,
			Architecture: manifest.Platform.Architecture,
			Variant:      manifest.Platform.Variant,
		}) {
			if strings.TrimSpace(manifest.Digest) == "" {
				break
			}
			return manifest.Digest, nil
		}
	}
	return "", apperror.New(apperror.RegistryUnreachable, "No manifest matched the requested platform", apperror.WithDetail(fmt.Sprintf("%s/%s", platform.OS, platform.Architecture)))
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
	gate := m.registryLimiter(registry)
	select {
	case gate <- struct{}{}:
		defer func() { <-gate }()
	case <-ctx.Done():
		return apperror.Wrap(apperror.Timeout, "Registry check timed out", ctx.Err())
	}
	return fn()
}

func (m *Manager) registryLimiter(registry string) chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := normalizeRegistryHost(registry)
	gate, ok := m.registryGate[key]
	if !ok {
		limit := m.perRegistryLimit
		if limit <= 0 {
			limit = defaultPerRegistryLimit
		}
		gate = make(chan struct{}, limit)
		m.registryGate[key] = gate
	}
	return gate
}

func (m *Manager) checkCircuit(registry string, now time.Time) (time.Duration, error) {
	if m == nil {
		return 0, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	state := m.circuit[normalizeRegistryHost(registry)]
	if now.Before(state.OpenUntil) {
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
	key := normalizeRegistryHost(registry)
	state := m.circuit[key]
	state.Failures++
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
