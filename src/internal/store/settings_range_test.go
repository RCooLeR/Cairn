package store

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
)

func TestSettingDefinitionsAreValid(t *testing.T) {
	if err := validateSettingDefinitions(); err != nil {
		t.Fatalf("validateSettingDefinitions() error = %v", err)
	}
}

func TestIntegerSettingRanges(t *testing.T) {
	ctx := context.Background()
	s := openMigratedStore(t, ctx)
	defer closeStore(t, s)

	tests := []struct {
		key     string
		minimum int
		maximum int
	}{
		{key: "updates.check_interval_hours", minimum: 0, maximum: 24 * 365},
		{key: "metrics.retention_raw_minutes", minimum: 1, maximum: 24 * 60},
		{key: "metrics.sample_interval_seconds", minimum: 1, maximum: 10},
		{key: "agent.max_context_lines", minimum: 100, maximum: 2_000},
		{key: "macos.colima_cpu", minimum: 1, maximum: 128},
		{key: "macos.colima_memory_gb", minimum: 1, maximum: 512},
		{key: "macos.colima_disk_gb", minimum: 1, maximum: 2_048},
	}

	settings := s.Settings()
	for _, test := range tests {
		t.Run(test.key, func(t *testing.T) {
			for _, boundary := range []int{test.minimum, test.maximum} {
				if err := settings.SetInt(ctx, test.key, boundary); err != nil {
					t.Fatalf("SetInt(%d) error = %v", boundary, err)
				}
				if got, err := settings.GetInt(ctx, test.key); err != nil || got != boundary {
					t.Fatalf("GetInt() = %d, %v; want %d, nil", got, err, boundary)
				}
			}

			for _, outside := range []int{test.minimum - 1, test.maximum + 1} {
				if err := settings.SetInt(ctx, test.key, outside); !errors.Is(err, ErrInvalidValue) {
					t.Fatalf("SetInt(%d) error = %v, want ErrInvalidValue", outside, err)
				}
			}
			if got, err := settings.GetInt(ctx, test.key); err != nil || got != test.maximum {
				t.Fatalf("value after rejected writes = %d, %v; want prior maximum %d, nil", got, err, test.maximum)
			}
		})
	}
}

func TestIntegerSettingParsingRejectsOverflowBeforeConversion(t *testing.T) {
	ctx := context.Background()
	s := openMigratedStore(t, ctx)
	defer closeStore(t, s)
	settings := s.Settings()

	for _, raw := range []string{
		"9223372036854775808",
		"-9223372036854775809",
		"2000.0000000000000001",
		strings.Repeat("9", maxSettingNumberLength+1),
	} {
		if err := settings.SetRaw(ctx, "agent.max_context_lines", raw); !errors.Is(err, ErrTypeMismatch) {
			t.Fatalf("SetRaw(%q) error = %v, want ErrTypeMismatch", raw, err)
		}
	}
	if err := settings.SetValue(ctx, "agent.max_context_lines", math.MaxFloat64); !errors.Is(err, ErrTypeMismatch) {
		t.Fatalf("SetValue(MaxFloat64) error = %v, want ErrTypeMismatch", err)
	}
	if _, ok := asSettingInt(json.Number("9223372036854775808")); ok {
		t.Fatal("asSettingInt accepted a JSON integer larger than int64")
	}

	// Older writers could serialize an integral value with a decimal point or
	// exponent. Keep accepting those exact integers when they are in range.
	for _, raw := range []string{"400.0", "4e2"} {
		if err := settings.SetRaw(ctx, "agent.max_context_lines", raw); err != nil {
			t.Fatalf("SetRaw(%q) error = %v", raw, err)
		}
		if got, err := settings.GetInt(ctx, "agent.max_context_lines"); err != nil || got != 400 {
			t.Fatalf("GetInt() after %q = %d, %v; want 400, nil", raw, got, err)
		}
	}
}

func TestMigrateRepairsInvalidExistingIntegerSettings(t *testing.T) {
	ctx := context.Background()
	s := openMigratedStore(t, ctx)
	defer closeStore(t, s)

	tests := []struct {
		key          string
		invalidRaw   string
		defaultValue int
		readError    error
	}{
		{key: "updates.check_interval_hours", invalidRaw: "-1", defaultValue: 24, readError: ErrInvalidValue},
		{key: "metrics.retention_raw_minutes", invalidRaw: "0", defaultValue: 60, readError: ErrInvalidValue},
		{key: "metrics.sample_interval_seconds", invalidRaw: "11", defaultValue: 2, readError: ErrInvalidValue},
		{key: "agent.max_context_lines", invalidRaw: "99", defaultValue: 400, readError: ErrInvalidValue},
		{key: "macos.colima_cpu", invalidRaw: "129", defaultValue: 2, readError: ErrInvalidValue},
		{key: "macos.colima_memory_gb", invalidRaw: "513", defaultValue: 4, readError: ErrInvalidValue},
		{key: "macos.colima_disk_gb", invalidRaw: "9223372036854775808", defaultValue: 60, readError: ErrTypeMismatch},
	}

	for _, test := range tests {
		if _, err := s.writer.ExecContext(ctx, "UPDATE settings SET value = ? WHERE key = ?", test.invalidRaw, test.key); err != nil {
			t.Fatalf("seed invalid %s: %v", test.key, err)
		}
		if _, err := s.Settings().GetInt(ctx, test.key); !errors.Is(err, test.readError) {
			t.Fatalf("GetInt(%s) before repair error = %v, want %v", test.key, err, test.readError)
		}
	}

	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() repair error = %v", err)
	}
	for _, test := range tests {
		if got, err := s.Settings().GetInt(ctx, test.key); err != nil || got != test.defaultValue {
			t.Fatalf("GetInt(%s) after repair = %d, %v; want %d, nil", test.key, got, err, test.defaultValue)
		}
	}
}
