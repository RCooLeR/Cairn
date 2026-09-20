package registry

import "github.com/RCooLeR/Cairn/internal/apperror"

func notReady() error {
	return apperror.New(
		apperror.ProviderNotReady,
		"Registry manager is not ready",
		apperror.WithRepairHints("Connect a Docker provider from onboarding."),
	)
}
