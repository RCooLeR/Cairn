package security

import (
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
)

func TestQuotePlanArgUsesShellQuoting(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "simple", value: "nginx:latest", want: "nginx:latest"},
		{name: "trim simple", value: "  alpine:3.20  ", want: "alpine:3.20"},
		{name: "empty", value: "  ", want: "''"},
		{name: "space", value: "my volume", want: "'my volume'"},
		{name: "single quote", value: "team's volume", want: `'team'"'"'s volume'`},
		{name: "shell metachar", value: "repo/$tag", want: "'repo/$tag'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := quotePlanArg(tt.value); got != tt.want {
				t.Fatalf("quotePlanArg(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

func TestRemoveVolumePlanPreviewShellQuotesTarget(t *testing.T) {
	plan, err := NewRemoveVolumePlan(models.VolumeSummary{Name: "team's volume"}, false, time.Date(2026, 6, 16, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("NewRemoveVolumePlan() error = %v", err)
	}
	if got, want := plan.Plan.Commands[0].Command, `docker volume rm 'team'"'"'s volume'`; got != want {
		t.Fatalf("preview command = %q, want %q", got, want)
	}
}
