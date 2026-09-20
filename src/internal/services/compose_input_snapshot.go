package services

import (
	"context"

	composecore "github.com/RCooLeR/Cairn/internal/compose"
	"github.com/RCooLeR/Cairn/internal/models"
)

type verifiedComposeInput = composecore.VerifiedConfigInput

// runVerifiedComposeConfig routes every service-level Compose config read
// through the shared compose-layer top-level input verifier. Keeping the
// invariant in the Compose package also lets background detection use it.
func runVerifiedComposeConfig(ctx context.Context, client *composecore.Client, opts composecore.ProjectOptions) (*composecore.ConfigResult, []verifiedComposeInput, error) {
	return client.ConfigVerified(ctx, opts)
}

func composeRawPreviewsFromInputs(ctx context.Context, inputs []verifiedComposeInput, budget *projectPreviewBudget) []models.ComposeRawFile {
	previews := make([]models.ComposeRawFile, 0, min(len(inputs), maxProjectPreviewFiles))
	for _, input := range inputs {
		if contextReadError(ctx) != nil || len(previews) >= maxProjectPreviewFiles || !budget.allowCandidate() {
			break
		}
		content := composeStructurePreview(string(input.Content))
		if !budget.reserveFile(input.Path, content) {
			continue
		}
		previews = append(previews, models.ComposeRawFile{Path: input.Path, Content: content})
	}
	return previews
}

func contextReadError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
