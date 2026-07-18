package security

const BuildArgsPresentMetadataKey = "hasBuildArgs"

// BuildArgsPresent deliberately retains only a fixed-size presence bit.
// Compose argument names and values are project-controlled and may themselves
// contain secrets or pathological cardinality, so neither belongs in ordinary
// service metadata or durable history.
func BuildArgsPresent(buildArgs map[string]string) bool {
	return len(buildArgs) > 0
}
