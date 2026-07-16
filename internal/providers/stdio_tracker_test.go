package providers

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStdioTransportDiagnosticsStoresMinimizedCommandSummary(t *testing.T) {
	tests := []struct {
		name    string
		command []string
		want    string
		secrets []string
	}{
		{
			name: "linux docker and common secret forms",
			command: []string{
				"/usr/local/bin/docker",
				"--token", "separate-flag-secret",
				"--password=inline-flag-secret",
				"GITHUB_TOKEN=environment-secret",
				"https://url-user-secret:url-password-secret@example.test/v2/",
				`--client-secret="quoted value secret"`,
				"--host", "unix:///home/private-user/.docker/run/docker.sock",
				"system", "dial-stdio",
			},
			want: "docker system dial-stdio",
			secrets: []string{
				"separate-flag-secret",
				"inline-flag-secret",
				"environment-secret",
				"url-user-secret",
				"url-password-secret",
				"quoted value secret",
				"private-user",
			},
		},
		{
			name: "windows docker path and switch forms",
			command: []string{
				`C:\Program Files\Docker\docker.exe`,
				`/password:windows-switch-secret`,
				`DOCKER_AUTH_CONFIG={"auth":"windows-env-secret"}`,
				`--registry=https://windows-user:windows-url-secret@registry.example.test`,
				"system", "dial-stdio",
			},
			want: "docker.exe system dial-stdio",
			secrets: []string{
				"Program Files",
				"windows-switch-secret",
				"windows-env-secret",
				"windows-user",
				"windows-url-secret",
			},
		},
		{
			name: "wsl wrapper retains only recognized nested operation",
			command: []string{
				`C:\Windows\System32\wsl.exe`,
				"-d", "private-distro-name", "--",
				"docker", "--host", "unix:///home/private-user/docker.sock",
				"system", "dial-stdio",
				"--api-key", "nested-secret",
			},
			want: "wsl.exe (docker system dial-stdio)",
			secrets: []string{
				"System32",
				"private-distro-name",
				"private-user",
				"nested-secret",
			},
		},
		{
			name: "unknown helper never retains arbitrary arguments",
			command: []string{
				`C:\Users\private-user\bin\custom-bridge.exe`,
				"--credential", "custom-secret",
				`API_TOKEN='quoted-custom-secret'`,
				`https://custom-user:custom-password@example.test`,
			},
			want: "custom-bridge.exe",
			secrets: []string{
				"private-user",
				"custom-secret",
				"quoted-custom-secret",
				"custom-user",
				"custom-password",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tracker := newStdioTransportTracker()
			tracker.open(test.command)
			diagnostics := tracker.diagnostics()
			if len(diagnostics.ActiveConnections) != 1 {
				t.Fatalf("ActiveConnections = %#v, want one item", diagnostics.ActiveConnections)
			}
			if got := diagnostics.ActiveConnections[0].Command; got != test.want {
				t.Fatalf("Command = %q, want %q", got, test.want)
			}

			payload, err := json.Marshal(diagnostics)
			if err != nil {
				t.Fatalf("json.Marshal() error = %v", err)
			}
			for _, secret := range test.secrets {
				if strings.Contains(string(payload), secret) {
					t.Fatalf("diagnostics DTO leaked %q: %s", secret, payload)
				}
			}
		})
	}
}

func TestStdioCommandSummaryIsBoundedAndUTF8Safe(t *testing.T) {
	command := []string{"/tmp/" + strings.Repeat("\u5ca9", 200) + ".exe", "--token", "secret"}
	summary := stdioCommandSummary(command)
	if len(summary) > stdioCommandSummaryLimitBytes {
		t.Fatalf("summary length = %d, limit %d", len(summary), stdioCommandSummaryLimitBytes)
	}
	if !strings.HasSuffix(summary, "...") {
		t.Fatalf("summary = %q, want explicit truncation marker", summary)
	}
	if strings.Contains(summary, "secret") {
		t.Fatalf("summary leaked argv secret: %q", summary)
	}
}
