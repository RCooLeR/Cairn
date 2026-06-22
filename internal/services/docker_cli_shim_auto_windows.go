//go:build windows

package services

import (
	"context"
	"strings"

	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/store"
)

func ensureWindowsDockerCLIShim(ctx context.Context, settings *store.SettingsRepository) (*models.WindowsDockerCLIShimStatus, error) {
	status, err := windowsDockerCLIShimStatus(ctx, settings)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(status.DockerOnPath) != "" {
		return status, nil
	}
	return installWindowsDockerCLIShim(ctx, settings)
}
