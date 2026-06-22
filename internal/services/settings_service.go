package services

import (
	"context"
	"encoding/json"
	"net/http"
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
)

const generalAutostartAppSetting = "general.autostart_app"

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
		return settings, nil
	}
	return map[string]any{}, nil
}

func (s *SettingsService) SetSetting(ctx context.Context, key string, value any) error {
	if s.Settings != nil {
		if strings.TrimSpace(strings.ToLower(key)) == generalAutostartAppSetting && s.Autostart != nil {
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
	var release struct {
		Draft       bool   `json:"draft"`
		Prerelease  bool   `json:"prerelease"`
		TagName     string `json:"tag_name"`
		Name        string `json:"name"`
		HTMLURL     string `json:"html_url"`
		PublishedAt string `json:"published_at"`
	}
	if err := json.NewDecoder(response.Body).Decode(&release); err != nil {
		return nil, apperror.Wrap(apperror.Internal, "Decode app update response failed", err)
	}
	if release.Draft || release.Prerelease || release.TagName == "" || release.HTMLURL == "" || !isNewerAppVersion(release.TagName, currentVersion) {
		return nil, nil
	}
	return &models.AppUpdateNotice{
		Version:     normalizeAppVersionLabel(release.TagName),
		URL:         release.HTMLURL,
		Name:        release.Name,
		PublishedAt: release.PublishedAt,
	}, nil
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
