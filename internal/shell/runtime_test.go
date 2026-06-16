package shell

import (
	"context"
	"sync"
	"testing"

	"github.com/RCooLeR/Cairn/internal/services"
)

func TestNewAppRuntimeUsesNamedConfigAndStoppedState(t *testing.T) {
	runtimeMu := &sync.RWMutex{}
	runtimeController := newAppRuntime(appRuntimeConfig{
		RootCtx:         context.Background(),
		ServiceMu:       runtimeMu,
		DockerService:   &services.DockerService{RuntimeMu: runtimeMu},
		ProjectService:  &services.ProjectService{RuntimeMu: runtimeMu},
		ComposeService:  &services.ComposeService{RuntimeMu: runtimeMu},
		MetricsService:  &services.MetricsService{RuntimeMu: runtimeMu},
		LogsService:     &services.LogsService{RuntimeMu: runtimeMu},
		TerminalService: &services.TerminalService{RuntimeMu: runtimeMu},
		UpdateService:   &services.UpdateService{RuntimeMu: runtimeMu},
		LineageService:  &services.ImageLineageService{RuntimeMu: runtimeMu},
		BackupService:   &services.BackupService{RuntimeMu: runtimeMu},
	})

	if runtimeController.state != runtimeStateStopped {
		t.Fatalf("initial runtime state = %q, want %q", runtimeController.state, runtimeStateStopped)
	}
	if runtimeController.projectService.RuntimeMu != runtimeMu {
		t.Fatal("named runtime config did not wire project service")
	}
}

func TestAppRuntimeNilProviderClearsServicesAndReturnsStopped(t *testing.T) {
	runtimeMu := &sync.RWMutex{}
	projectService := &services.ProjectService{
		RuntimeMu:   runtimeMu,
		ProviderID:  "old-provider",
		ContextName: "old-context",
	}
	runtimeController := newAppRuntime(appRuntimeConfig{
		RootCtx:         context.Background(),
		ServiceMu:       runtimeMu,
		DockerService:   &services.DockerService{RuntimeMu: runtimeMu},
		ProjectService:  projectService,
		ComposeService:  &services.ComposeService{RuntimeMu: runtimeMu},
		MetricsService:  &services.MetricsService{RuntimeMu: runtimeMu},
		LogsService:     &services.LogsService{RuntimeMu: runtimeMu},
		TerminalService: &services.TerminalService{RuntimeMu: runtimeMu},
		UpdateService:   &services.UpdateService{RuntimeMu: runtimeMu},
		LineageService:  &services.ImageLineageService{RuntimeMu: runtimeMu},
		BackupService:   &services.BackupService{RuntimeMu: runtimeMu},
	})

	summary, err := runtimeController.RebindProvider(context.Background(), nil)
	if err != nil {
		t.Fatalf("RebindProvider(nil) error = %v", err)
	}
	if summary != nil {
		t.Fatalf("RebindProvider(nil) summary = %#v, want nil", summary)
	}
	if runtimeController.state != runtimeStateStopped {
		t.Fatalf("runtime state = %q, want %q", runtimeController.state, runtimeStateStopped)
	}
	if projectService.ProviderID != "" || projectService.ContextName != "" {
		t.Fatalf("project service not cleared: provider=%q context=%q", projectService.ProviderID, projectService.ContextName)
	}
}
