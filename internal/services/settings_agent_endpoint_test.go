package services

import (
	"context"
	"testing"

	"github.com/RCooLeR/Cairn/internal/apperror"
)

func TestSettingsServiceCanonicalizesAgentEndpointBeforePersisting(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "IPv4 scheme and root slash",
			raw:  "  HTTP://127.0.0.1:11434/  ",
			want: "http://127.0.0.1:11434",
		},
		{
			name: "IPv6 compression",
			raw:  "HTTPS://[0:0:0:0:0:0:0:1]:8443/",
			want: "https://[::1]:8443",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			db := openServiceTestStore(t)
			service := &SettingsService{Settings: db.Settings()}

			if err := service.SetSetting(ctx, " Agent.Endpoint ", tt.raw); err != nil {
				t.Fatalf("SetSetting(agent.endpoint) error = %v", err)
			}
			got, err := db.Settings().GetString(ctx, "agent.endpoint")
			if err != nil {
				t.Fatalf("GetString(agent.endpoint) error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("persisted agent.endpoint = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSettingsServiceRejectsNonStringAgentEndpointWithoutOverwriting(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openServiceTestStore(t)
	service := &SettingsService{Settings: db.Settings()}
	const prior = "http://127.0.0.1:11434"
	if err := service.SetSetting(ctx, "agent.endpoint", prior); err != nil {
		t.Fatalf("SetSetting(valid agent.endpoint) error = %v", err)
	}

	for _, invalid := range []any{true, 11434, map[string]any{"url": prior}, []any{prior}} {
		err := service.SetSetting(ctx, " AGENT.ENDPOINT ", invalid)
		if !apperror.IsCode(err, apperror.ProviderNotReady) {
			t.Fatalf("SetSetting(agent.endpoint, %T) error = %v, want ProviderNotReady", invalid, err)
		}
		got, err := db.Settings().GetString(ctx, "agent.endpoint")
		if err != nil {
			t.Fatalf("GetString(agent.endpoint) error = %v", err)
		}
		if got != prior {
			t.Fatalf("agent.endpoint after rejected %T = %q, want prior %q", invalid, got, prior)
		}
	}
}

func TestSettingsServiceBlanksNonStringAgentEndpointBeforeDisplay(t *testing.T) {
	sanitizeAgentEndpointForDisplay(nil)
	settings := map[string]any{"agent.endpoint": map[string]any{"url": "http://user:secret@127.0.0.1:11434"}}
	sanitizeAgentEndpointForDisplay(settings)
	if got := settings["agent.endpoint"]; got != "" {
		t.Fatalf("displayed malformed agent.endpoint = %#v, want empty string", got)
	}
}

func TestSettingsServiceRejectsInvalidAgentEndpointWithoutOverwriting(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openServiceTestStore(t)
	service := &SettingsService{Settings: db.Settings()}
	const prior = "http://127.0.0.1:11434"
	if err := service.SetSetting(ctx, "agent.endpoint", prior); err != nil {
		t.Fatalf("SetSetting(valid agent.endpoint) error = %v", err)
	}

	for _, invalid := range []string{
		"http://169.254.169.254:80",
		"http://user:secret@127.0.0.1:11434",
		"http://127.0.0.1:11434/v1",
	} {
		if err := service.SetSetting(ctx, "agent.endpoint", invalid); err == nil {
			t.Fatalf("SetSetting(%q) succeeded, want rejection", invalid)
		}
		got, err := db.Settings().GetString(ctx, "agent.endpoint")
		if err != nil {
			t.Fatalf("GetString(agent.endpoint) error = %v", err)
		}
		if got != prior {
			t.Fatalf("agent.endpoint after rejected %q = %q, want prior %q", invalid, got, prior)
		}
	}
}

func TestSettingsServiceDoesNotExposeInvalidLegacyAgentEndpoint(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openServiceTestStore(t)
	const legacy = "http://user:legacy-secret@127.0.0.1:11434"
	if err := db.Settings().SetString(ctx, "agent.endpoint", legacy); err != nil {
		t.Fatalf("seed legacy agent.endpoint: %v", err)
	}

	settings, err := (&SettingsService{Settings: db.Settings()}).GetSettings(ctx)
	if err != nil {
		t.Fatalf("GetSettings() error = %v", err)
	}
	if got, ok := settings["agent.endpoint"].(string); !ok || got != "" {
		t.Fatalf("GetSettings()[agent.endpoint] = %#v, want empty safe value", settings["agent.endpoint"])
	}
	if got, err := db.Settings().GetString(ctx, "agent.endpoint"); err != nil || got != legacy {
		t.Fatalf("stored legacy agent.endpoint = %q, %v; GetSettings must not silently rewrite it", got, err)
	}
}
