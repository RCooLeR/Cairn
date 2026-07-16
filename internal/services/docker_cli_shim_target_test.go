package services

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RCooLeR/Cairn/internal/store"
)

func TestReadWindowsDockerShimTarget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content []byte
		missing bool
		want    string
	}{
		{name: "missing uses script fallback", missing: true, want: defaultWindowsDockerShimDistro},
		{name: "blank uses script fallback", content: []byte(" \r\n\t"), want: defaultWindowsDockerShimDistro},
		{name: "target is trimmed", content: []byte("  cairn-dev\r\n"), want: "cairn-dev"},
		{name: "unicode target", content: []byte("Ubuntu-開発\n"), want: "Ubuntu-開発"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "distro.txt")
			if !test.missing {
				if err := os.WriteFile(path, test.content, 0o600); err != nil {
					t.Fatalf("write target: %v", err)
				}
			}

			got, err := readWindowsDockerShimTarget(path)
			if err != nil {
				t.Fatalf("read target: %v", err)
			}
			if got != test.want {
				t.Fatalf("target = %q, want %q", got, test.want)
			}
		})
	}
}

func TestReadWindowsDockerShimTargetRejectsMalformedFiles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content []byte
		wantErr string
	}{
		{
			name:    "oversized",
			content: []byte(strings.Repeat("x", maxWindowsDockerShimTargetSize+1)),
			wantErr: "exceeds",
		},
		{name: "invalid UTF-8", content: []byte{0xff, 0xfe}, wantErr: "UTF-8"},
		{name: "multiple lines", content: []byte("Ubuntu\ncairn-next"), wantErr: "single line"},
		{name: "control character", content: []byte("Ubuntu\x00next"), wantErr: "control"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "distro.txt")
			if err := os.WriteFile(path, test.content, 0o600); err != nil {
				t.Fatalf("write target: %v", err)
			}

			if _, err := readWindowsDockerShimTarget(path); err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("error = %v, want error containing %q", err, test.wantErr)
			}
		})
	}
}

func TestSelectedWindowsShimDistroPropagatesSettingsErrors(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "cairn.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := database.Migrate(ctx); err != nil {
		_ = database.Close()
		t.Fatalf("migrate store: %v", err)
	}
	settings := database.Settings()
	if err := database.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	if distro, err := selectedWindowsShimDistro(ctx, settings); err == nil {
		t.Fatalf("selected distro after store close = %q, nil; want read error", distro)
	}
}

func TestSelectedWindowsShimDistroValidatesPersistedValue(t *testing.T) {
	ctx := context.Background()
	database, err := store.Open(ctx, filepath.Join(t.TempDir(), "cairn.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("migrate store: %v", err)
	}
	settings := database.Settings()
	if err := settings.SetString(ctx, "windows.wsl_distro", "Ubuntu\ncairn-next"); err != nil {
		t.Fatalf("set distro: %v", err)
	}

	if distro, err := selectedWindowsShimDistro(ctx, settings); err == nil || !strings.Contains(err.Error(), "single line") {
		t.Fatalf("selected distro = %q, error = %v; want single-line validation error", distro, err)
	}
}

func TestWindowsDockerShimTargetStatus(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "distro.txt")
	if err := os.WriteFile(path, []byte("Ubuntu\n"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}

	target, warning := windowsDockerShimTargetStatus(path, "cairn-next", true)
	if target != "Ubuntu" {
		t.Fatalf("target = %q, want Ubuntu", target)
	}
	if !strings.Contains(warning, `"Ubuntu"`) || !strings.Contains(warning, `"cairn-next"`) || !strings.Contains(warning, "Reinstall") {
		t.Fatalf("mismatch warning = %q", warning)
	}

	_, warning = windowsDockerShimTargetStatus(path, " ubuntu ", true)
	if warning != "" {
		t.Fatalf("case-insensitive matching target warning = %q, want empty", warning)
	}

	oversized := filepath.Join(t.TempDir(), "oversized.txt")
	if err := os.WriteFile(oversized, []byte(strings.Repeat("x", maxWindowsDockerShimTargetSize+1)), 0o600); err != nil {
		t.Fatalf("write oversized target: %v", err)
	}
	target, warning = windowsDockerShimTargetStatus(oversized, "Ubuntu", true)
	if target != "" || !strings.Contains(warning, "could not verify") {
		t.Fatalf("unreadable installed target = %q, warning = %q", target, warning)
	}

	target, warning = windowsDockerShimTargetStatus(oversized, "Ubuntu", false)
	if target != "" || warning != "" {
		t.Fatalf("uninstalled malformed target = %q, warning = %q; want both empty", target, warning)
	}
}
