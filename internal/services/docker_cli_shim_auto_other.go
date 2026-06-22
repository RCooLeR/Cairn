//go:build !windows

package services

import (
	"context"

	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/store"
)

func ensureWindowsDockerCLIShim(context.Context, *store.SettingsRepository) (*models.WindowsDockerCLIShimStatus, error) {
	return &models.WindowsDockerCLIShimStatus{
		Supported: false,
		Message:   "Windows Docker CLI shim is only available on Windows.",
	}, nil
}
