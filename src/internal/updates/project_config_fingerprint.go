package updates

import (
	"context"

	composecore "github.com/RCooLeR/Cairn/internal/compose"
	"github.com/RCooLeR/Cairn/internal/projectconfig"
	"github.com/RCooLeR/Cairn/internal/store"
)

// projectConfigurationFingerprint covers the stored Compose target and stable
// service configuration used to construct and execute an update. Runtime
// status, health, replica counters, pin state, and last-seen timestamps are
// deliberately excluded because they can change without changing the
// confirmation target.
func projectConfigurationFingerprint(project store.ProjectRecord, services []store.ServiceRecord) (string, error) {
	return projectconfig.Fingerprint(project, services, "")
}

// projectConfigurationFingerprintWithInputs also binds the confirmation to
// the verified on-disk Compose dependency closure. Update and rollback plans
// execute Compose after confirmation, so database fields alone are not a
// sufficient target identity.
func projectConfigurationFingerprintWithInputs(ctx context.Context, project store.ProjectRecord, services []store.ServiceRecord) (string, error) {
	configInputs, err := composecore.FingerprintConfigInputs(ctx, composeOptionsFromProject(project))
	if err != nil {
		return "", err
	}
	return projectconfig.Fingerprint(project, services, configInputs)
}
