package projectconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	composecore "github.com/RCooLeR/Cairn/internal/compose"
	"github.com/RCooLeR/Cairn/internal/store"
)

const fingerprintVersion = 1

type fingerprintPayload struct {
	Version      int                  `json:"version"`
	Project      fingerprintProject   `json:"project"`
	Services     []fingerprintService `json:"services"`
	ConfigInputs string               `json:"configInputs,omitempty"`
}

type fingerprintProject struct {
	ID                 string   `json:"id"`
	ProviderID         string   `json:"providerID"`
	ContextName        string   `json:"contextName"`
	Name               string   `json:"name"`
	WorkingDir         string   `json:"workingDir"`
	ComposeFiles       []string `json:"composeFiles"`
	ComposeProjectName string   `json:"composeProjectName"`
}

type fingerprintService struct {
	ID             string         `json:"id"`
	ProjectID      string         `json:"projectID"`
	Name           string         `json:"name"`
	ImageRef       string         `json:"imageRef"`
	BuildContext   string         `json:"buildContext"`
	DockerfilePath string         `json:"dockerfilePath"`
	BuildTarget    string         `json:"buildTarget"`
	Metadata       map[string]any `json:"metadata"`
}

// Fingerprint returns a deterministic digest for the stable stored Compose
// target and, when supplied, its independently verified on-disk input digest.
// Runtime status, health, replica counters, pin state, and observation times
// are deliberately excluded.
func Fingerprint(project store.ProjectRecord, services []store.ServiceRecord, configInputs string) (string, error) {
	payload := fingerprintPayload{
		Version: fingerprintVersion,
		Project: fingerprintProject{
			ID:                 project.ID,
			ProviderID:         project.ProviderID,
			ContextName:        project.ContextName,
			Name:               project.Name,
			WorkingDir:         project.WorkingDir,
			ComposeFiles:       append([]string{}, project.ComposeFiles...),
			ComposeProjectName: composecore.ProjectNameFromID(project.ProviderID, project.ID),
		},
		Services:     make([]fingerprintService, 0, len(services)),
		ConfigInputs: configInputs,
	}
	for _, service := range services {
		metadata := service.Metadata
		if metadata == nil {
			metadata = map[string]any{}
		}
		payload.Services = append(payload.Services, fingerprintService{
			ID:             service.ID,
			ProjectID:      service.ProjectID,
			Name:           service.Name,
			ImageRef:       service.ImageRef,
			BuildContext:   service.BuildContext,
			DockerfilePath: service.DockerfilePath,
			BuildTarget:    service.BuildTarget,
			Metadata:       metadata,
		})
	}
	sort.Slice(payload.Services, func(i, j int) bool {
		left := payload.Services[i]
		right := payload.Services[j]
		if left.ID != right.ID {
			return left.ID < right.ID
		}
		return left.Name < right.Name
	})

	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}
