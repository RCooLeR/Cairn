package services

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/RCooLeR/Cairn/internal/models"
)

func dockerRunCommand(req models.RunImageRequest) string {
	args := []string{"docker", "run", "-d"}
	if req.Name != "" {
		args = append(args, "--name", req.Name)
	}
	for _, port := range req.Ports {
		publish := dockerPortPublish(port)
		if publish != "" {
			args = append(args, "-p", publish)
		}
	}
	for _, env := range req.Env {
		name := strings.TrimSpace(env.Name)
		if name == "" {
			continue
		}
		value := env.Value
		if secretLike(name) {
			value = "********"
		}
		args = append(args, "-e", name+"="+value)
	}
	for _, mount := range req.Volumes {
		target := strings.TrimSpace(mount.Target)
		if target == "" {
			continue
		}
		source := strings.TrimSpace(mount.Source)
		mountType := strings.TrimSpace(mount.Type)
		if mountType == "" {
			mountType = "volume"
		}
		if mountType == "volume" && mount.VolumeName != "" {
			source = strings.TrimSpace(mount.VolumeName)
		}
		if source == "" {
			continue
		}
		mode := "rw"
		if mount.ReadOnly {
			mode = "ro"
		}
		args = append(args, "--mount", fmt.Sprintf("type=%s,source=%s,target=%s,%s", mountType, source, target, mode))
	}
	if req.NetworkID != "" {
		args = append(args, "--network", req.NetworkID)
	}
	if req.RestartPolicy != "" && req.RestartPolicy != "no" {
		args = append(args, "--restart", req.RestartPolicy)
	}
	if req.User != "" {
		args = append(args, "--user", req.User)
	}
	args = append(args, req.ImageRef)
	args = append(args, req.Command...)
	return joinCommand(args)
}

func runImageRisk(req models.RunImageRequest) models.Risk {
	risk := models.RiskSafe
	for _, mount := range req.Volumes {
		mountType := strings.TrimSpace(mount.Type)
		if mountType == "" {
			mountType = "volume"
		}
		if mountType != "bind" {
			continue
		}
		if !mount.ReadOnly || isSensitiveBindSource(mount.Source) {
			return models.RiskDangerous
		}
		risk = models.RiskNeedsConfirmation
	}
	return risk
}

func isSensitiveBindSource(source string) bool {
	value := strings.TrimSpace(strings.ReplaceAll(source, "\\", "/"))
	if value == "" {
		return false
	}
	lower := strings.ToLower(strings.TrimRight(value, "/"))
	switch lower {
	case "/", ".", "/var/run/docker.sock", "/run/docker.sock":
		return true
	}
	if len(lower) == 2 && lower[1] == ':' {
		return true
	}
	if len(lower) == 3 && lower[1] == ':' && lower[2] == '/' {
		return true
	}
	clean := strings.ToLower(strings.ReplaceAll(filepath.Clean(source), "\\", "/"))
	return clean == "/" || clean == "/var/run/docker.sock" || clean == "/run/docker.sock"
}

func dockerRenameCommand(oldName string, newName string) string {
	return joinCommand([]string{"docker", "rename", oldName, newName})
}

func dockerSaveCommand(imageRefs []string, destPath string) string {
	args := []string{"docker", "save", "-o", destPath}
	args = append(args, imageRefs...)
	return joinCommand(args)
}

func dockerVolumeCreateCommand(req models.CreateVolumeRequest) string {
	args := []string{"docker", "volume", "create"}
	if req.Driver != "" {
		args = append(args, "--driver", req.Driver)
	}
	args = appendSortedMapArgs(args, "--opt", req.DriverOpts)
	args = appendSortedMapArgs(args, "--label", req.Labels)
	args = append(args, req.Name)
	return joinCommand(args)
}

func dockerNetworkCreateCommand(req models.CreateNetworkRequest) string {
	args := []string{"docker", "network", "create"}
	if req.Driver != "" {
		args = append(args, "--driver", req.Driver)
	}
	if req.Subnet != "" {
		args = append(args, "--subnet", req.Subnet)
	}
	if req.Gateway != "" {
		args = append(args, "--gateway", req.Gateway)
	}
	if req.Internal {
		args = append(args, "--internal")
	}
	if req.Attachable {
		args = append(args, "--attachable")
	}
	args = appendSortedMapArgs(args, "--label", req.Labels)
	args = append(args, req.Name)
	return joinCommand(args)
}

func appendSortedMapArgs(args []string, flag string, values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		args = append(args, flag, key+"="+values[key])
	}
	return args
}

func dockerPortPublish(port models.PortMapping) string {
	containerPort := strings.TrimSpace(port.ContainerPort)
	if containerPort == "" {
		return ""
	}
	protocol := strings.TrimSpace(port.Protocol)
	if protocol == "" {
		protocol = "tcp"
	}
	target := containerPort + "/" + protocol
	hostIP := formatPublishHostIP(strings.TrimSpace(port.HostIP))
	hostPort := strings.TrimSpace(port.HostPort)
	switch {
	case hostIP == "" && hostPort == "":
		return target
	case hostIP == "":
		return hostPort + ":" + target
	case hostPort == "":
		return hostIP + "::" + target
	default:
		return hostIP + ":" + hostPort + ":" + target
	}
}

func formatPublishHostIP(hostIP string) string {
	if hostIP == "" || strings.HasPrefix(hostIP, "[") || !strings.Contains(hostIP, ":") {
		return hostIP
	}
	return "[" + hostIP + "]"
}

func joinCommand(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		if strings.TrimSpace(arg) == "" {
			continue
		}
		quoted = append(quoted, quoteArg(arg))
	}
	return strings.Join(quoted, " ")
}

func quoteArg(arg string) string {
	if arg == "" {
		return `""`
	}
	if strings.ContainsAny(arg, " \t\r\n\"'") {
		return `"` + strings.ReplaceAll(arg, `"`, `\"`) + `"`
	}
	return arg
}

func secretLike(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	parts := strings.FieldsFunc(lower, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
	for _, part := range parts {
		switch part {
		case "password", "passwd", "token", "secret", "key", "auth", "credential", "credentials":
			return true
		}
	}
	return false
}
