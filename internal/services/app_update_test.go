package services

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/RCooLeR/Cairn/internal/models"
)

func TestCheckAppUpdateCanonicalizesTrustedReleaseURL(t *testing.T) {
	got, err := checkAppUpdateFixture(
		t,
		"v1.2.3",
		"HTTPS://GITHUB.COM:443/RCooLeR/Cairn/releases/tag/v1.2.3",
	)
	if err != nil {
		t.Fatalf("CheckAppUpdate() error = %v", err)
	}
	if got == nil {
		t.Fatal("CheckAppUpdate() = nil, want release notice")
	}
	if got.URL != "https://github.com/RCooLeR/Cairn/releases/tag/v1.2.3" {
		t.Fatalf("CheckAppUpdate().URL = %q", got.URL)
	}
}

func TestCheckAppUpdateRejectsUntrustedReleaseMetadata(t *testing.T) {
	tests := []struct {
		name    string
		tagName string
		url     string
	}{
		{
			name:    "HTTP scheme",
			tagName: "v1.2.3",
			url:     "http://github.com/RCooLeR/Cairn/releases/tag/v1.2.3",
		},
		{
			name:    "file scheme",
			tagName: "v1.2.3",
			url:     "file:///RCooLeR/Cairn/releases/tag/v1.2.3",
		},
		{
			name:    "credentials",
			tagName: "v1.2.3",
			url:     "https://user:secret@github.com/RCooLeR/Cairn/releases/tag/v1.2.3",
		},
		{
			name:    "nondefault port",
			tagName: "v1.2.3",
			url:     "https://github.com:8443/RCooLeR/Cairn/releases/tag/v1.2.3",
		},
		{
			name:    "lookalike host",
			tagName: "v1.2.3",
			url:     "https://github.com.example.test/RCooLeR/Cairn/releases/tag/v1.2.3",
		},
		{
			name:    "GitHub subdomain",
			tagName: "v1.2.3",
			url:     "https://www.github.com/RCooLeR/Cairn/releases/tag/v1.2.3",
		},
		{
			name:    "different repository",
			tagName: "v1.2.3",
			url:     "https://github.com/RCooLeR/Cairn-Desktop/releases/tag/v1.2.3",
		},
		{
			name:    "unexpected release path",
			tagName: "v1.2.3",
			url:     "https://github.com/RCooLeR/Cairn/releases/latest",
		},
		{
			name:    "tag mismatch",
			tagName: "v1.2.3",
			url:     "https://github.com/RCooLeR/Cairn/releases/tag/v1.2.4",
		},
		{
			name:    "encoded tag path",
			tagName: "v1.2.3",
			url:     "https://github.com/RCooLeR/Cairn/releases/tag/%761.2.3",
		},
		{
			name:    "query string",
			tagName: "v1.2.3",
			url:     "https://github.com/RCooLeR/Cairn/releases/tag/v1.2.3?source=update",
		},
		{
			name:    "fragment",
			tagName: "v1.2.3",
			url:     "https://github.com/RCooLeR/Cairn/releases/tag/v1.2.3#assets",
		},
		{
			name:    "missing v prefix",
			tagName: "1.2.3",
			url:     "https://github.com/RCooLeR/Cairn/releases/tag/1.2.3",
		},
		{
			name:    "prerelease tag",
			tagName: "v1.2.3-rc.1",
			url:     "https://github.com/RCooLeR/Cairn/releases/tag/v1.2.3-rc.1",
		},
		{
			name:    "noncanonical numeric tag",
			tagName: "v01.2.3",
			url:     "https://github.com/RCooLeR/Cairn/releases/tag/v01.2.3",
		},
		{
			name:    "malformed URL",
			tagName: "v1.2.3",
			url:     "://not-a-url",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := checkAppUpdateFixture(t, test.tagName, test.url)
			if err != nil {
				t.Fatalf("CheckAppUpdate() error = %v", err)
			}
			if got != nil {
				t.Fatalf("CheckAppUpdate() = %#v, want nil", got)
			}
		})
	}
}

func TestCheckAppUpdateRejectsOversizedAndTrailingDocuments(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "oversized",
			body: `{"tag_name":"v1.2.3","padding":"` + strings.Repeat("x", maxAppUpdateResponseBytes) + `"}`,
		},
		{
			name: "trailing JSON document",
			body: `{"tag_name":"v1.2.3"} {"tag_name":"v9.9.9"}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()

			oldURL := appUpdateURL
			oldClient := appUpdateHTTPClient
			appUpdateURL = server.URL
			appUpdateHTTPClient = server.Client()
			defer func() {
				appUpdateURL = oldURL
				appUpdateHTTPClient = oldClient
			}()

			got, err := (&SettingsService{}).CheckAppUpdate(context.Background(), "1.2.2")
			if err == nil {
				t.Fatalf("CheckAppUpdate() error = nil, got %#v", got)
			}
		})
	}
}

func checkAppUpdateFixture(t *testing.T, tagName string, htmlURL string) (*models.AppUpdateNotice, error) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"draft":        false,
			"prerelease":   false,
			"tag_name":     tagName,
			"name":         "Cairn " + tagName,
			"html_url":     htmlURL,
			"published_at": "2026-06-16T10:00:00Z",
		})
	}))
	defer server.Close()

	oldURL := appUpdateURL
	oldClient := appUpdateHTTPClient
	appUpdateURL = server.URL
	appUpdateHTTPClient = server.Client()
	defer func() {
		appUpdateURL = oldURL
		appUpdateHTTPClient = oldClient
	}()

	return (&SettingsService{}).CheckAppUpdate(context.Background(), "1.2.2")
}
