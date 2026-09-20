package services

import (
	"context"

	"github.com/RCooLeR/Cairn/internal/models"
)

func (s *SettingsService) GetWindowsDockerCLIShimStatus(ctx context.Context) (*models.WindowsDockerCLIShimStatus, error) {
	return windowsDockerCLIShimStatus(ctx, s.Settings)
}

func (s *SettingsService) InstallWindowsDockerCLIShim(ctx context.Context) (*models.WindowsDockerCLIShimStatus, error) {
	return installWindowsDockerCLIShim(ctx, s.Settings)
}
