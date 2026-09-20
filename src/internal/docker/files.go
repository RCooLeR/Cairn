package docker

import (
	"context"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
)

const containerFileListScript = `
set -eu
p="${CAIRN_PATH:-/}"
if [ -z "$p" ]; then
  p="/"
fi
if [ ! -e "$p" ] && [ ! -L "$p" ]; then
  exit 44
fi
emit_entry() {
  item="$1"
  name="${item##*/}"
  kind="other"
  link=""
  if [ -L "$item" ]; then
    kind="symlink"
    link="$(readlink "$item" 2>/dev/null || true)"
  elif [ -d "$item" ]; then
    kind="directory"
  elif [ -f "$item" ]; then
    kind="file"
  fi
  size="$(stat -c '%s' "$item" 2>/dev/null || echo 0)"
  mode="$(stat -c '%A' "$item" 2>/dev/null || echo '')"
  mtime="$(stat -c '%Y' "$item" 2>/dev/null || echo 0)"
  printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$kind" "$name" "$item" "$size" "$mode" "$mtime" "$link"
}
if [ -d "$p" ]; then
  for child in "$p"/* "$p"/.[!.]* "$p"/..?*; do
    [ -e "$child" ] || [ -L "$child" ] || continue
    emit_entry "$child"
  done
else
  emit_entry "$p"
fi
`

func (c *Client) ListContainerFiles(ctx context.Context, containerID string, requestedPath string) (*models.ContainerFileListing, error) {
	containerPath, err := normalizeContainerPath(requestedPath)
	if err != nil {
		return nil, err
	}
	shells, err := c.DetectContainerShells(ctx, containerID)
	if err != nil {
		return nil, err
	}
	var output string
	var exitCode int
	for _, shell := range shells {
		output, exitCode, err = c.RunContainerExec(ctx, containerID, ExecOptions{
			Cmd: []string{shell, "-c", containerFileListScript},
			Env: map[string]string{"CAIRN_PATH": containerPath},
		})
		if err != nil {
			return nil, err
		}
		if exitCode != 126 && exitCode != 127 {
			break
		}
	}
	if exitCode == 44 {
		return nil, apperror.New(
			apperror.NotFound,
			"Container path not found",
			apperror.WithDetail(containerPath),
		)
	}
	if exitCode == 126 || exitCode == 127 {
		return nil, apperror.New(
			apperror.NotFound,
			"Container shell is unavailable",
			apperror.WithDetail("Cairn needs a POSIX shell inside the container to browse files."),
			apperror.WithRepairHints("Use the Inspect tab or container logs for shell-less images."),
		)
	}
	if exitCode != 0 {
		return nil, apperror.New(
			apperror.DockerUnreachable,
			"Container file listing failed",
			apperror.WithDetail(strings.TrimSpace(output)),
		)
	}

	entries := parseContainerFileEntries(output)
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Type == "directory" && entries[j].Type != "directory" {
			return true
		}
		if entries[i].Type != "directory" && entries[j].Type == "directory" {
			return false
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})

	return &models.ContainerFileListing{
		ContainerID: containerID,
		Path:        containerPath,
		ParentPath:  parentContainerPath(containerPath),
		Entries:     entries,
	}, nil
}

func normalizeContainerPath(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "/", nil
	}
	if strings.ContainsRune(trimmed, 0) {
		return "", apperror.New(apperror.NotFound, "Container path is invalid")
	}
	if !strings.HasPrefix(trimmed, "/") {
		trimmed = "/" + trimmed
	}
	cleaned := path.Clean(trimmed)
	if cleaned == "." {
		return "/", nil
	}
	return cleaned, nil
}

func parentContainerPath(value string) string {
	cleaned, err := normalizeContainerPath(value)
	if err != nil || cleaned == "/" {
		return ""
	}
	parent := path.Dir(cleaned)
	if parent == "." {
		return "/"
	}
	return parent
}

func parseContainerFileEntries(output string) []models.ContainerFileEntry {
	lines := strings.Split(output, "\n")
	entries := make([]models.ContainerFileEntry, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 7)
		if len(fields) < 6 {
			continue
		}
		size, _ := strconv.ParseInt(fields[3], 10, 64)
		unixSeconds, _ := strconv.ParseInt(fields[5], 10, 64)
		entry := models.ContainerFileEntry{
			Type:      fields[0],
			Name:      fields[1],
			Path:      fields[2],
			SizeBytes: size,
			Mode:      fields[4],
		}
		if unixSeconds > 0 {
			entry.ModifiedAt = time.Unix(unixSeconds, 0).UTC()
		}
		if len(fields) > 6 {
			entry.LinkTarget = fields[6]
		}
		entries = append(entries, entry)
	}
	return entries
}
