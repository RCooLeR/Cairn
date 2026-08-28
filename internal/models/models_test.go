package models

import (
	"encoding/json"
	"testing"
	"time"
)

func TestOptionalTimesOmitZeroValues(t *testing.T) {
	timestamp := time.Date(2026, time.August, 28, 12, 34, 56, 0, time.UTC)
	tests := []struct {
		name      string
		field     string
		zero      any
		populated any
	}{
		{name: "container start", field: "startedAt", zero: ContainerSummary{}, populated: ContainerSummary{StartedAt: timestamp}},
		{name: "container file modification", field: "modifiedAt", zero: ContainerFileEntry{}, populated: ContainerFileEntry{ModifiedAt: timestamp}},
		{name: "stdio open", field: "lastOpenedAt", zero: StdioTransportDiagnostics{}, populated: StdioTransportDiagnostics{LastOpenedAt: timestamp}},
		{name: "stdio close", field: "lastClosedAt", zero: StdioTransportDiagnostics{}, populated: StdioTransportDiagnostics{LastClosedAt: timestamp}},
		{name: "network creation", field: "createdAt", zero: NetworkDetail{}, populated: NetworkDetail{CreatedAt: timestamp}},
		{name: "update finish", field: "finishedAt", zero: UpdateHistoryItem{}, populated: UpdateHistoryItem{FinishedAt: timestamp}},
		{name: "registry account verification", field: "lastVerifiedAt", zero: RegistryAccount{}, populated: RegistryAccount{LastVerifiedAt: timestamp}},
		{name: "registry auth verification", field: "verifiedAt", zero: RegistryAuthStatus{}, populated: RegistryAuthStatus{VerifiedAt: timestamp}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if fields := marshalObject(t, test.zero); fields[test.field] != nil {
				t.Fatalf("zero time field %q was encoded as %s", test.field, fields[test.field])
			}
			if fields := marshalObject(t, test.populated); fields[test.field] == nil {
				t.Fatalf("non-zero time field %q was omitted", test.field)
			}
		})
	}
}

func marshalObject(t *testing.T, value any) map[string]json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal model: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatalf("decode marshaled model: %v", err)
	}
	return fields
}
