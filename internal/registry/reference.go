package registry

import (
	"strings"

	"github.com/RCooLeR/Cairn/internal/apperror"
	distref "github.com/distribution/reference"
)

func NormalizeImageRef(raw string) (ImageRef, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ImageRef{}, apperror.New(apperror.Conflict, "Image reference is required")
	}
	named, err := distref.ParseNormalizedNamed(value)
	if err != nil {
		return ImageRef{}, apperror.Wrap(apperror.Conflict, "Invalid image reference", err)
	}
	tagged := distref.TagNameOnly(named)
	registry := normalizeRegistryHost(distref.Domain(tagged))
	repository := distref.Path(tagged)

	tag := ""
	if withTag, ok := tagged.(distref.Tagged); ok {
		tag = withTag.Tag()
	}

	digest := ""
	pinned := false
	if withDigest, ok := named.(distref.Digested); ok {
		digest = withDigest.Digest().String()
		pinned = true
		if rawContainsDigest(value) {
			tag = ""
		}
	}

	normalized := registry + "/" + repository
	if tag != "" {
		normalized += ":" + tag
	}
	if digest != "" {
		normalized += "@" + digest
	}
	return ImageRef{
		Registry:   registry,
		Repository: repository,
		Tag:        tag,
		Digest:     digest,
		Pinned:     pinned,
		Normalized: normalized,
	}, nil
}

func rawContainsDigest(value string) bool {
	return strings.Contains(value, "@sha256:")
}

func normalizeRegistryHost(raw string) string {
	value := strings.TrimSpace(strings.ToLower(raw))
	value = strings.TrimPrefix(value, "https://")
	value = strings.TrimPrefix(value, "http://")
	value = strings.TrimSuffix(value, "/")
	value = strings.TrimSuffix(value, "/v1")
	value = strings.TrimSuffix(value, "/v2")
	switch value {
	case "", "index.docker.io", "registry-1.docker.io", "docker.io/v1", "index.docker.io/v1":
		return DefaultRegistry
	default:
		return value
	}
}

func registryAPIHost(registry string) string {
	host := normalizeRegistryHost(registry)
	if host == DefaultRegistry {
		return dockerHubAPIRegistry
	}
	return host
}

func registryDisplayArg(registry string) string {
	return normalizeRegistryHost(registry)
}

func registryCLIArg(registry string) (string, error) {
	value := registryDisplayArg(registry)
	if value == "" {
		return "", apperror.New(apperror.Conflict, "Registry is required")
	}
	if strings.HasPrefix(value, "-") || strings.ContainsAny(value, " \t\r\n") {
		return "", apperror.New(
			apperror.Conflict,
			"Invalid registry host",
			apperror.WithDetail("Registry hosts cannot start with '-' or contain whitespace."),
		)
	}
	return value, nil
}
