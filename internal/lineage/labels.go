package lineage

import "time"

type CairnBuildLabelInput struct {
	ProjectID      string
	Service        string
	ComposeFile    string
	DockerfilePath string
	BaseName       string
	BaseDigest     string
	BuildTime      time.Time
	Platform       string
}

func CairnBuildLabels(input CairnBuildLabelInput) map[string]string {
	labels := map[string]string{}
	addLabel(labels, cairnProjectLabel, input.ProjectID)
	addLabel(labels, cairnServiceLabel, input.Service)
	addLabel(labels, cairnComposeFileLabel, input.ComposeFile)
	addLabel(labels, cairnDockerfileLabel, input.DockerfilePath)
	addLabel(labels, cairnBaseNameLabel, input.BaseName)
	addLabel(labels, cairnBaseDigestLabel, input.BaseDigest)
	addLabel(labels, cairnPlatformLabel, input.Platform)
	if !input.BuildTime.IsZero() {
		labels[cairnBuildTimeLabel] = input.BuildTime.UTC().Format(time.RFC3339Nano)
	}
	return labels
}

func addLabel(labels map[string]string, key string, value string) {
	if value != "" {
		labels[key] = value
	}
}
