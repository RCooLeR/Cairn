package security

import (
	"strings"
	"unicode"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
)

// NormalizeCreateVolumeRequest applies the service-wide policy for Docker
// volume creation. The local driver's bind and recursive-bind options are
// intentionally rejected: they are host bind mounts disguised as named
// volumes and must not bypass explicit bind-mount planning.
func NormalizeCreateVolumeRequest(req models.CreateVolumeRequest) (models.CreateVolumeRequest, error) {
	req.Name = strings.TrimSpace(req.Name)
	req.Driver = strings.TrimSpace(req.Driver)
	if req.Driver == "" || strings.EqualFold(req.Driver, "local") {
		req.Driver = "local"
		opts, err := normalizeLocalVolumeDriverOpts(req.DriverOpts)
		if err != nil {
			return models.CreateVolumeRequest{}, err
		}
		req.DriverOpts = opts
	}
	return req, nil
}

func normalizeLocalVolumeDriverOpts(opts map[string]string) (map[string]string, error) {
	if len(opts) == 0 {
		return nil, nil
	}
	normalized := make(map[string]string, len(opts))
	for rawKey, rawValue := range opts {
		if containsVolumeOptionControl(rawKey) || containsVolumeOptionControl(rawValue) {
			return nil, invalidLocalVolumeOptions("Local volume option keys and values must be non-empty single-line text.")
		}
		key := strings.ToLower(strings.TrimSpace(rawKey))
		if key == "" || strings.TrimSpace(rawValue) == "" {
			return nil, invalidLocalVolumeOptions("Local volume option keys and values must be non-empty single-line text.")
		}
		if _, exists := normalized[key]; exists {
			return nil, invalidLocalVolumeOptions("Local volume options contain duplicate keys after case and whitespace normalization.")
		}
		// Driver option values are opaque to Cairn. Preserve their bytes exactly;
		// use trimmed copies only when inspecting mount semantics below.
		normalized[key] = rawValue
	}

	mountOptions, exists := normalized["o"]
	if !exists {
		return normalized, nil
	}
	for _, rawToken := range strings.Split(mountOptions, ",") {
		token := strings.TrimSpace(rawToken)
		if token == "" {
			return nil, invalidLocalVolumeOptions("The local volume mount-options value contains an empty token.")
		}
		name := token
		if before, _, found := strings.Cut(token, "="); found {
			name = before
		}
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "bind", "rbind":
			return nil, apperror.New(
				apperror.Conflict,
				"Bind-backed local volumes are not supported",
				apperror.WithDetail("Use an explicit bind mount through Run Image planning instead of local volume bind/rbind driver options."),
			)
		}
	}
	return normalized, nil
}

func containsVolumeOptionControl(value string) bool {
	return strings.IndexFunc(value, func(r rune) bool {
		return r == 0 || r == '\r' || r == '\n' || unicode.IsControl(r)
	}) >= 0
}

func invalidLocalVolumeOptions(detail string) error {
	return apperror.New(
		apperror.Conflict,
		"Invalid local volume options",
		apperror.WithDetail(detail),
	)
}
