package compose

import (
	"strings"
)

const (
	LabelProject     = "com.docker.compose.project"
	LabelService     = "com.docker.compose.service"
	LabelWorkingDir  = "com.docker.compose.project.working_dir"
	LabelConfigFiles = "com.docker.compose.project.config_files"
)

func NormalizeProjectName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func ProjectID(providerID string, projectName string) string {
	projectName = NormalizeProjectName(projectName)
	if strings.TrimSpace(providerID) == "" || projectName == "" {
		return projectName
	}
	return strings.TrimSpace(providerID) + "/" + projectName
}

func ServiceID(projectID string, serviceName string) string {
	serviceName = strings.TrimSpace(serviceName)
	if strings.TrimSpace(projectID) == "" || serviceName == "" {
		return serviceName
	}
	return strings.TrimSpace(projectID) + "/" + serviceName
}

func ProjectNameFromID(providerID string, projectID string) string {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return ""
	}
	prefix := strings.TrimSpace(providerID) + "/"
	if providerID != "" && strings.HasPrefix(projectID, prefix) {
		return strings.TrimPrefix(projectID, prefix)
	}
	if before, after, ok := strings.Cut(projectID, "/"); ok && before != "" && after != "" {
		return after
	}
	return projectID
}
