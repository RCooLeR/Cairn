package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/terminal"
)

var (
	appUpdateHTTPClient = &http.Client{Timeout: 10 * time.Second}
	appUpdateURL        = "https://api.github.com/repos/RCooLeR/Cairn/releases/latest"
	stableAppReleaseTag = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
)

const (
	generalAutostartAppSetting = "general.autostart_app"
	cairnReleaseOrigin         = "https://github.com"
	cairnReleasePathPrefix     = "/RCooLeR/Cairn/releases/tag/"
	maxAppUpdateResponseBytes  = 256 * 1024
)

func (s *SettingsService) GetSettings(ctx context.Context) (map[string]any, error) {
	if s.Settings != nil {
		settings, err := s.Settings.All(ctx)
		if err != nil {
			return nil, err
		}
		if s.Autostart != nil {
			enabled, err := s.Autostart.Enabled(ctx)
			if err == nil {
				settings[generalAutostartAppSetting] = enabled
			}
		}
		sanitizeAgentEndpointForDisplay(settings)
		return settings, nil
	}
	return map[string]any{}, nil
}

func sanitizeAgentEndpointForDisplay(settings map[string]any) {
	if settings == nil {
		return
	}
	endpoint, ok := settings["agent.endpoint"].(string)
	if !ok {
		// Treat malformed legacy values as sensitive and do not echo their
		// structure across the native-to-renderer boundary.
		settings["agent.endpoint"] = ""
		return
	}
	canonicalEndpoint, err := canonicalAgentEndpoint(endpoint)
	if err != nil {
		// Legacy or externally modified settings may predate endpoint
		// containment. Never echo a credential-bearing or otherwise invalid
		// URL across the native-to-renderer boundary.
		settings["agent.endpoint"] = ""
		return
	}
	settings["agent.endpoint"] = canonicalEndpoint
}

func (s *SettingsService) SetSetting(ctx context.Context, key string, value any) error {
	if s.Settings != nil {
		normalizedKey := strings.TrimSpace(strings.ToLower(key))
		if normalizedKey == "agent.endpoint" {
			endpoint, ok := value.(string)
			if !ok {
				return invalidAgentEndpointError("The configured local Agent endpoint must be a string URL.")
			}
			canonicalEndpoint, err := canonicalAgentEndpoint(endpoint)
			if err != nil {
				return err
			}
			key = "agent.endpoint"
			value = canonicalEndpoint
		}
		if normalizedKey == generalAutostartAppSetting && s.Autostart != nil {
			enabled, ok := value.(bool)
			if ok {
				if err := s.Autostart.SetEnabled(ctx, enabled); err != nil {
					return apperror.Wrap(
						apperror.Internal,
						"Update login autostart failed",
						err,
						apperror.WithDetail(err.Error()),
						apperror.WithRepairHints("Run Cairn from the installed application path and try again."),
					)
				}
			}
		}
		return s.Settings.SetValue(ctx, key, value)
	}
	return notReady()
}

func (s *SettingsService) GetAuditLog(ctx context.Context, filter models.AuditFilter) ([]models.AuditEntry, error) {
	if s.Audit != nil {
		return s.Audit.List(ctx, filter)
	}
	return []models.AuditEntry{}, nil
}

func (s *SettingsService) GetNotifications(ctx context.Context, unreadOnly bool) ([]models.Notification, error) {
	if s.Notifications != nil {
		return s.Notifications.List(ctx, unreadOnly, 100)
	}
	return []models.Notification{}, nil
}

func (s *SettingsService) MarkNotificationsRead(ctx context.Context, ids []int64) error {
	if s.Notifications != nil {
		return s.Notifications.MarkRead(ctx, ids)
	}
	return nil
}

func (s *SettingsService) GetCheatsheet(_ context.Context) ([]models.CheatsheetEntry, error) {
	return terminal.CheatsheetEntries(), nil
}

func (s *SettingsService) OpenPath(_ context.Context, path string) error {
	return notReady()
}

func (s *SettingsService) AppVersion(_ context.Context) (*models.VersionInfo, error) {
	return versionInfo(), nil
}

func (s *SettingsService) CheckAppUpdate(ctx context.Context, currentVersion string) (*models.AppUpdateNotice, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, appUpdateURL, nil)
	if err != nil {
		return nil, apperror.Wrap(apperror.Internal, "Create app update request failed", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Cairn")
	response, err := appUpdateHTTPClient.Do(req)
	if err != nil {
		return nil, apperror.Wrap(apperror.Internal, "Check app update failed", err)
	}
	defer func() {
		_ = response.Body.Close()
	}()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, nil
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxAppUpdateResponseBytes+1))
	if err != nil {
		return nil, apperror.Wrap(apperror.Internal, "Read app update response failed", err)
	}
	if len(payload) > maxAppUpdateResponseBytes {
		return nil, apperror.New(apperror.Internal, "App update response exceeded the safe size limit")
	}
	var release struct {
		Draft       bool   `json:"draft"`
		Prerelease  bool   `json:"prerelease"`
		TagName     string `json:"tag_name"`
		Name        string `json:"name"`
		HTMLURL     string `json:"html_url"`
		PublishedAt string `json:"published_at"`
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if err := decoder.Decode(&release); err != nil {
		return nil, apperror.Wrap(apperror.Internal, "Decode app update response failed", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return nil, apperror.Wrap(apperror.Internal, "Decode app update response failed", err)
	}
	canonicalURL, validReleaseURL := canonicalAppReleaseURL(release.HTMLURL, release.TagName)
	if release.Draft || release.Prerelease || !validReleaseURL || !isNewerAppVersion(release.TagName, currentVersion) {
		return nil, nil
	}
	return &models.AppUpdateNotice{
		Version:     normalizeAppVersionLabel(release.TagName),
		URL:         canonicalURL,
		Name:        release.Name,
		PublishedAt: release.PublishedAt,
	}, nil
}

// canonicalAppReleaseURL validates update metadata before it crosses the
// native-to-renderer boundary. GitHub's release API is the only legitimate
// source, and Cairn publishes stable releases as vMAJOR.MINOR.PATCH tags.
func canonicalAppReleaseURL(rawURL string, tagName string) (string, bool) {
	if !stableAppReleaseTag.MatchString(tagName) {
		return "", false
	}

	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Opaque != "" || parsed.User != nil {
		return "", false
	}
	if !strings.EqualFold(parsed.Scheme, "https") || !strings.EqualFold(parsed.Hostname(), "github.com") {
		return "", false
	}
	if port := parsed.Port(); port != "" && port != "443" {
		return "", false
	}
	if parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || strings.Contains(rawURL, "#") {
		return "", false
	}

	expectedPath := cairnReleasePathPrefix + tagName
	if parsed.Path != expectedPath || parsed.EscapedPath() != expectedPath {
		return "", false
	}

	return cairnReleaseOrigin + expectedPath, true
}

func versionInfo() *models.VersionInfo {
	info := &models.VersionInfo{
		Version:   Version,
		Commit:    Commit,
		BuildDate: BuildDate,
		GoVersion: runtime.Version(),
	}
	if info.Commit == "" {
		if buildInfo, ok := debug.ReadBuildInfo(); ok {
			for _, setting := range buildInfo.Settings {
				if setting.Key == "vcs.revision" {
					info.Commit = setting.Value
					break
				}
			}
		}
	}
	return info
}

func isNewerAppVersion(candidate string, current string) bool {
	candidateParts := appVersionParts(candidate)
	currentParts := appVersionParts(current)
	for index := 0; index < 3; index++ {
		if candidateParts[index] > currentParts[index] {
			return true
		}
		if candidateParts[index] < currentParts[index] {
			return false
		}
	}
	return false
}

func appVersionParts(value string) [3]int {
	normalized := normalizeAppVersionLabel(value)
	if index := strings.IndexAny(normalized, "+-"); index >= 0 {
		normalized = normalized[:index]
	}
	raw := strings.Split(normalized, ".")
	var parts [3]int
	for index := 0; index < len(raw) && index < len(parts); index++ {
		part, _ := strconv.Atoi(raw[index])
		parts[index] = part
	}
	return parts
}

func normalizeAppVersionLabel(value string) string {
	return strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(value), "v"), "V")
}
