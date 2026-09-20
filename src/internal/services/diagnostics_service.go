package services

import (
	"context"
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
)

func (s *DiagnosticsService) GetRuntimeDiagnostics(context.Context) (*models.RuntimeDiagnostics, error) {
	diagnostics := &models.RuntimeDiagnostics{
		Stdio:     providers.StdioTransportDiagnostics(),
		CheckedAt: time.Now().UTC(),
	}

	unlock := s.lockRuntime()
	defer unlock()
	if s.Logs != nil && s.Logs.Manager != nil {
		diagnostics.Logs = s.Logs.Manager.Diagnostics()
	}
	if s.Metrics != nil && s.Metrics.Manager != nil {
		diagnostics.Metrics = s.Metrics.Manager.Diagnostics()
	}
	if s.Terminal != nil && s.Terminal.Manager != nil {
		diagnostics.Terminals = s.Terminal.Manager.Diagnostics()
	}
	if s.PortForward != nil && s.PortForward.Manager != nil {
		diagnostics.PortForwards = models.PortForwardRuntimeDiagnostics{
			Supported:      true,
			ActiveForwards: len(s.PortForward.Manager.ListForwards()),
		}
	}
	return diagnostics, nil
}
