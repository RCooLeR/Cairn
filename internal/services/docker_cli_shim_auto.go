package services

import (
	"context"

	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/store"
)

func EnsureWindowsDockerCLIShim(ctx context.Context, settings *store.SettingsRepository) (*models.WindowsDockerCLIShimStatus, error) {
	return ensureWindowsDockerCLIShim(ctx, settings)
}
