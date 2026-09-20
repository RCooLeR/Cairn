//go:build !windows

package services

import (
	"context"

	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/store"
)

func windowsDockerCLIShimStatus(context.Context, *store.SettingsRepository) (*models.WindowsDockerCLIShimStatus, error) {
	return &models.WindowsDockerCLIShimStatus{
		Supported: false,
		Message:   "Windows Docker CLI shim is only available on Windows.",
	}, nil
}

func installWindowsDockerCLIShim(context.Context, *store.SettingsRepository) (*models.WindowsDockerCLIShimStatus, error) {
	return windowsDockerCLIShimStatus(context.Background(), nil)
}
