package services

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	maxAgentProjectFileBytes      int64 = 64 * 1024
	maxAgentProjectAggregateBytes int64 = 512 * 1024
	maxAgentProjectCandidates           = 128
	maxAgentProjectVisitedEntries       = 4096
	maxProjectPreviewFileBytes    int64 = 512 * 1024
	maxProjectPreviewTotalBytes   int64 = 2 * 1024 * 1024
	maxProjectPreviewCandidates         = 128
	maxProjectPreviewAttempts           = 64
	maxProjectPreviewFiles              = 32
	maxProjectPreviewNameBytes          = 256
	maxImportComposeFiles               = 32
	maxComposeErrorCandidates           = 8
	maxComposeErrorScanBytes            = 4 * 1024
	maxComposePreviewYAMLDepth          = 128
	maxComposePreviewYAMLNodes          = 32 * 1024
	maxComposePreviewYAMLKeys           = 8 * 1024
)

var (
	errBoundedFileNotRegular = errors.New("file is not a regular file")
	errBoundedFileTooLarge   = errors.New("file exceeds the read limit")
	errBoundedFileChanged    = errors.New("file changed while it was being opened")
	errBoundedFileDuplicate  = errors.New("file was already inspected")
	envAssignmentKeyPattern  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	composeLinePattern       = regexp.MustCompile(`(?i)\bline\s+[0-9]+(?::[0-9]+)?\b`)
	composeSchemaKeys        = stringSet(
		"action", "additional_contexts", "aliases", "annotations", "app_protocol", "args", "attach", "attachable", "aux_addresses",
		"bind", "blkio_config", "build", "cache_from", "cache_to", "capabilities", "cgroup", "cgroup_parent", "command", "condition",
		"config", "configs", "consistency", "container_name", "content", "context", "count", "cpus", "create_host_path", "credential_spec",
		"depends_on", "deploy", "develop", "device_cgroup_rules", "device_ids", "devices", "disable", "dns", "dns_opt", "dns_search",
		"dockerfile", "dockerfile_inline", "domainname", "driver", "driver_opts", "enable_ipv4", "enable_ipv6", "endpoint_mode", "entrypoint",
		"entitlements", "env_file", "environment", "exec", "expose", "extends", "external", "external_links", "extra_hosts", "file", "format",
		"gateway", "gid", "gpus", "group_add", "healthcheck", "host_ip", "hostname", "ignore", "image", "include", "init", "initial_sync",
		"internal", "interval", "ip_range", "ipam", "ipc", "isolation", "label_file", "labels", "limits", "link_local_ips", "links", "logging",
		"mac_address", "mem_limit", "mem_reservation", "mem_swappiness", "memory", "memswap_limit", "mode", "name", "network", "network_mode",
		"networks", "no_cache", "nocopy", "oom_kill_disable", "oom_score_adj", "options", "path", "pid", "pids", "pids_limit", "placement",
		"platform", "platforms", "ports", "post_start", "pre_stop", "privileged", "profiles", "project_directory", "propagation", "protocol",
		"provenance", "published", "pull", "pull_policy", "read_only", "replicas", "required", "reservations", "resources", "restart",
		"restart_policy", "retries", "rollback_config", "runtime", "sbom", "scale", "secrets", "security_opt", "service", "services", "shm_size",
		"size", "soft", "source", "ssh", "start_interval", "start_period", "stdin_open", "stop_grace_period", "stop_signal", "storage_opt",
		"subnet", "sysctls", "tags", "target", "test", "timeout", "tmpfs", "tty", "type", "uid", "ulimits", "update_config", "use_api_socket",
		"user", "userns_mode", "uts", "version", "volume", "volumes", "volumes_from", "watch", "working_dir",
	)
	composeEntityMapKeys = stringSet("configs", "depends_on", "networks", "secrets", "services", "ulimits", "volumes")
	composeOpaqueMapKeys = stringSet(
		"additional_contexts", "annotations", "args", "aux_addresses", "driver_opts", "environment", "extra_hosts", "labels", "options", "storage_opt", "sysctls",
	)
)

type yamlKeyMode uint8

const (
	yamlSchemaKeys yamlKeyMode = iota
	yamlEntityKeys
	yamlOpaqueKeys
)

type yamlRedactionState struct {
	maskedKeys int
	nodes      int
	keys       int
}

type boundedFileRead struct {
	Content   []byte
	ReadBytes int64
	Truncated bool
}

// verifiedProjectRoot pins the directory identity used for a multi-file read.
// Rechecking it after each file is opened prevents a renamed/replaced project
// root from silently changing the authority boundary between candidates.
type verifiedProjectRoot struct {
	absPath  string
	realPath string
	info     fs.FileInfo
}

type projectPreviewBudget struct {
	candidates int
	attempts   int
	files      int
	readBytes  int64
	dtoBytes   int64
}

func newProjectPreviewBudget() *projectPreviewBudget {
	return &projectPreviewBudget{}
}

func (b *projectPreviewBudget) allowCandidate() bool {
	if b == nil || b.candidates >= maxProjectPreviewCandidates {
		return false
	}
	b.candidates++
	return true
}

func (b *projectPreviewBudget) allowAttempt() bool {
	if b == nil || b.attempts >= maxProjectPreviewAttempts || b.readBytes >= maxProjectPreviewTotalBytes || b.dtoBytes >= maxProjectPreviewTotalBytes {
		return false
	}
	b.attempts++
	return true
}

func (b *projectPreviewBudget) readLimit() int64 {
	if b == nil {
		return 0
	}
	return min(maxProjectPreviewFileBytes, maxProjectPreviewTotalBytes-b.readBytes)
}

func (b *projectPreviewBudget) recordReadBytes(size int64) {
	if b != nil {
		b.readBytes += size
	}
}

func (b *projectPreviewBudget) reserveFile(path string, content string) bool {
	if b == nil || b.files >= maxProjectPreviewFiles {
		return false
	}
	size := int64(len(path) + len(content))
	if size < 0 || b.dtoBytes+size > maxProjectPreviewTotalBytes {
		return false
	}
	b.files++
	b.dtoBytes += size
	return true
}

func (b *projectPreviewBudget) reserveString(value string) bool {
	if b == nil {
		return false
	}
	size := int64(len(value))
	if size < 0 || b.dtoBytes+size > maxProjectPreviewTotalBytes {
		return false
	}
	b.dtoBytes += size
	return true
}

// readBoundedRegularFile rejects links and special files before opening them,
// verifies that the opened handle is still the file that was inspected, and
// never allocates more than maxBytes plus the one-byte overflow sentinel.
func readBoundedRegularFile(path string, maxBytes int64, allowTruncate bool) (boundedFileRead, error) {
	return readBoundedRegularFileVerified(path, maxBytes, allowTruncate, nil)
}

// readBoundedRegularProjectFile additionally proves, after opening the file,
// that the opened handle is reachable through a regular file below root. This
// keeps external, traversal, final-symlink, and swapped-parent targets out of
// project previews while still allowing a project root itself to be a symlink.
func readBoundedRegularProjectFile(root string, candidate string, maxBytes int64, allowTruncate bool) (boundedFileRead, string, error) {
	return readBoundedRegularProjectFileVerified(root, candidate, maxBytes, allowTruncate, nil)
}

func readBoundedRegularProjectFileOnce(root string, candidate string, maxBytes int64, allowTruncate bool, opened *[]fs.FileInfo) (boundedFileRead, string, error) {
	return readBoundedRegularProjectFileVerified(root, candidate, maxBytes, allowTruncate, func(info fs.FileInfo) error {
		for _, existing := range *opened {
			if os.SameFile(existing, info) {
				return errBoundedFileDuplicate
			}
		}
		*opened = append(*opened, info)
		return nil
	})
}

func readBoundedRegularProjectFileVerified(root string, candidate string, maxBytes int64, allowTruncate bool, openedVerify func(fs.FileInfo) error) (boundedFileRead, string, error) {
	verifiedRoot, err := verifyProjectReadRoot(root)
	if err != nil {
		return boundedFileRead{}, "", err
	}
	return readBoundedRegularProjectFileFromRoot(verifiedRoot, candidate, maxBytes, allowTruncate, openedVerify)
}

func readBoundedRegularProjectFileFromRootOnce(root verifiedProjectRoot, candidate string, maxBytes int64, allowTruncate bool, opened *[]fs.FileInfo) (boundedFileRead, string, error) {
	return readBoundedRegularProjectFileFromRoot(root, candidate, maxBytes, allowTruncate, func(info fs.FileInfo) error {
		for _, existing := range *opened {
			if os.SameFile(existing, info) {
				return errBoundedFileDuplicate
			}
		}
		*opened = append(*opened, info)
		return nil
	})
}

func readBoundedRegularProjectFileFromRoot(root verifiedProjectRoot, candidate string, maxBytes int64, allowTruncate bool, openedVerify func(fs.FileInfo) error) (boundedFileRead, string, error) {
	absPath, relPath, err := resolveProjectReadPathFromRoot(root, candidate)
	if err != nil {
		return boundedFileRead{}, "", err
	}
	var openedFile fs.FileInfo
	result, err := readBoundedRegularFileVerified(absPath, maxBytes, allowTruncate, func(opened fs.FileInfo) error {
		if err := verifyOpenedProjectFile(root, absPath, opened); err != nil {
			return err
		}
		if openedVerify != nil {
			if err := openedVerify(opened); err != nil {
				return err
			}
		}
		openedFile = opened
		return nil
	})
	if err != nil {
		return result, filepath.ToSlash(relPath), err
	}
	if err := verifyOpenedProjectFile(root, absPath, openedFile); err != nil {
		return result, filepath.ToSlash(relPath), err
	}
	return result, filepath.ToSlash(relPath), nil
}

func verifyOpenedProjectFile(root verifiedProjectRoot, absPath string, opened fs.FileInfo) error {
	if opened == nil {
		return errBoundedFileChanged
	}
	if err := root.verifyCurrent(); err != nil {
		return err
	}
	realTarget, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return err
	}
	if !pathWithinRoot(root.realPath, realTarget) {
		return fs.ErrPermission
	}
	current, err := os.Stat(realTarget)
	if err != nil {
		return err
	}
	if !current.Mode().IsRegular() || !os.SameFile(opened, current) {
		return errBoundedFileChanged
	}
	return nil
}

func readBoundedRegularFileVerified(path string, maxBytes int64, allowTruncate bool, verify func(fs.FileInfo) error) (boundedFileRead, error) {
	if maxBytes < 0 {
		return boundedFileRead{}, fmt.Errorf("invalid file read limit")
	}
	before, err := os.Lstat(path)
	if err != nil {
		return boundedFileRead{}, err
	}
	if !before.Mode().IsRegular() {
		return boundedFileRead{}, errBoundedFileNotRegular
	}
	if err := pinFileIdentity(before); err != nil {
		return boundedFileRead{}, err
	}
	if !allowTruncate && before.Size() > maxBytes {
		return boundedFileRead{}, errBoundedFileTooLarge
	}

	file, err := os.Open(path)
	if err != nil {
		return boundedFileRead{}, err
	}
	defer file.Close()

	opened, err := file.Stat()
	if err != nil {
		return boundedFileRead{}, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return boundedFileRead{}, errBoundedFileChanged
	}
	if !allowTruncate && opened.Size() > maxBytes {
		return boundedFileRead{}, errBoundedFileTooLarge
	}
	if verify != nil {
		if err := verify(opened); err != nil {
			return boundedFileRead{}, err
		}
	}

	readLimit := maxBytes + 1
	content, err := io.ReadAll(io.LimitReader(file, readLimit))
	readBytes := int64(len(content))
	if err != nil {
		return boundedFileRead{Content: content, ReadBytes: readBytes}, err
	}
	if int64(len(content)) > maxBytes {
		if !allowTruncate {
			return boundedFileRead{Content: content, ReadBytes: readBytes}, errBoundedFileTooLarge
		}
	}
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(opened, after) || after.Size() != opened.Size() || !after.ModTime().Equal(opened.ModTime()) {
		return boundedFileRead{Content: content, ReadBytes: readBytes}, errBoundedFileChanged
	}
	if int64(len(content)) > maxBytes {
		return boundedFileRead{Content: content[:maxBytes], ReadBytes: readBytes, Truncated: true}, nil
	}
	return boundedFileRead{
		Content:   content,
		ReadBytes: readBytes,
		Truncated: allowTruncate && opened.Size() > int64(len(content)),
	}, nil
}

func resolveProjectReadPath(root string, candidate string) (string, string, string, error) {
	verifiedRoot, err := verifyProjectReadRoot(root)
	if err != nil {
		return "", "", "", err
	}
	absPath, relPath, err := resolveProjectReadPathFromRoot(verifiedRoot, candidate)
	return verifiedRoot.realPath, absPath, relPath, err
}

func verifyProjectReadRoot(root string) (verifiedProjectRoot, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return verifiedProjectRoot{}, fs.ErrInvalid
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return verifiedProjectRoot{}, err
	}
	rootInfo, err := os.Stat(absRoot)
	if err != nil {
		return verifiedProjectRoot{}, err
	}
	if !rootInfo.IsDir() {
		return verifiedProjectRoot{}, fs.ErrInvalid
	}
	if err := pinFileIdentity(rootInfo); err != nil {
		return verifiedProjectRoot{}, err
	}
	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return verifiedProjectRoot{}, err
	}
	realInfo, err := os.Stat(realRoot)
	if err != nil || !realInfo.IsDir() || !os.SameFile(rootInfo, realInfo) {
		return verifiedProjectRoot{}, errBoundedFileChanged
	}
	return verifiedProjectRoot{absPath: absRoot, realPath: realRoot, info: rootInfo}, nil
}

// Windows FileInfo values obtained from a path resolve their file ID lazily
// inside os.SameFile. Force that lookup while the inspected path is still
// known to name the same object; otherwise a later path replacement could make
// an old FileInfo silently acquire the replacement's identity.
func pinFileIdentity(info fs.FileInfo) error {
	if info == nil || !os.SameFile(info, info) {
		return errBoundedFileChanged
	}
	return nil
}

func (root verifiedProjectRoot) verifyCurrent() error {
	current, err := os.Stat(root.absPath)
	if err != nil {
		return err
	}
	if !current.IsDir() || root.info == nil || !os.SameFile(root.info, current) {
		return errBoundedFileChanged
	}
	realPath, err := filepath.EvalSymlinks(root.absPath)
	if err != nil {
		return err
	}
	realInfo, err := os.Stat(realPath)
	if err != nil {
		return err
	}
	if !realInfo.IsDir() || !os.SameFile(root.info, realInfo) {
		return errBoundedFileChanged
	}
	return nil
}

func resolveProjectReadPathFromRoot(root verifiedProjectRoot, candidate string) (string, string, error) {
	if err := root.verifyCurrent(); err != nil {
		return "", "", err
	}

	absPath := strings.TrimSpace(candidate)
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(root.absPath, absPath)
	}
	resolvedPath, err := filepath.Abs(absPath)
	if err != nil {
		return "", "", err
	}
	absPath = resolvedPath
	relPath, err := filepath.Rel(root.absPath, absPath)
	if err != nil || pathEscapesRoot(relPath) {
		realTarget, evalErr := filepath.EvalSymlinks(absPath)
		if evalErr != nil || !pathWithinRoot(root.realPath, realTarget) {
			return "", "", fs.ErrPermission
		}
		relPath, err = filepath.Rel(root.realPath, realTarget)
		if err != nil || pathEscapesRoot(relPath) {
			return "", "", fs.ErrPermission
		}
	}
	return absPath, relPath, nil
}

func pathWithinRoot(root string, target string) bool {
	rel, err := filepath.Rel(root, target)
	return err == nil && !pathEscapesRoot(rel)
}

func pathEscapesRoot(rel string) bool {
	return rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func projectPathIdentity(path string) string {
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}

func lexicalProjectCandidateIdentity(root string, candidate string) (string, error) {
	absRoot, err := filepath.Abs(strings.TrimSpace(root))
	if err != nil {
		return "", err
	}
	absPath := strings.TrimSpace(candidate)
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(absRoot, absPath)
	}
	absPath, err = filepath.Abs(absPath)
	if err != nil {
		return "", err
	}
	return projectPathIdentity(absPath), nil
}

// composeStructurePreview retains the YAML key/collection shape required for
// review while replacing every scalar value. Hiding all values (rather than
// guessing which keys are secret) also covers opaque tokens and build args.
func composeStructurePreview(content string) string {
	const unavailable = "# Compose values hidden; a structured preview was unavailable.\n"
	if strings.TrimSpace(content) == "" {
		return ""
	}
	if int64(len(content)) > maxProjectPreviewFileBytes {
		return unavailable
	}
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(content), &document); err != nil {
		return unavailable
	}
	if !redactYAMLValues(&document, yamlSchemaKeys) {
		return unavailable
	}
	preview, err := yaml.Marshal(&document)
	if err != nil || int64(len(preview)) > maxProjectPreviewFileBytes {
		return unavailable
	}
	return string(preview)
}

func redactYAMLValues(node *yaml.Node, mode yamlKeyMode) bool {
	return redactYAMLValuesWithState(node, mode, &yamlRedactionState{}, 0)
}

func redactYAMLValuesWithState(node *yaml.Node, mode yamlKeyMode, state *yamlRedactionState, depth int) bool {
	if node == nil {
		return true
	}
	if depth > maxComposePreviewYAMLDepth || state.nodes >= maxComposePreviewYAMLNodes {
		return false
	}
	state.nodes++
	node.HeadComment = ""
	node.LineComment = ""
	node.FootComment = ""
	node.Anchor = ""
	switch node.Kind {
	case yaml.DocumentNode:
		node.Tag = ""
		node.Style = 0
		for _, child := range node.Content {
			if !redactYAMLValuesWithState(child, mode, state, depth+1) {
				return false
			}
		}
	case yaml.SequenceNode:
		node.Tag = "!!seq"
		node.Style = 0
		for _, child := range node.Content {
			if !redactYAMLValuesWithState(child, mode, state, depth+1) {
				return false
			}
		}
	case yaml.MappingNode:
		if len(node.Content)%2 != 0 {
			return false
		}
		keyCount := len(node.Content) / 2
		if keyCount > maxComposePreviewYAMLKeys-state.keys || keyCount > maxComposePreviewYAMLNodes-state.nodes {
			return false
		}
		state.keys += keyCount
		// Mapping keys are rewritten in place instead of traversed, but still
		// count every key node toward the global traversal budget.
		state.nodes += keyCount
		node.Tag = "!!map"
		node.Style = 0
		for i := 0; i+1 < len(node.Content); i += 2 {
			key := node.Content[i]
			value := node.Content[i+1]
			allowed := mode == yamlSchemaKeys && key.Kind == yaml.ScalarNode && key.Value == strings.ToLower(key.Value)
			if _, ok := composeSchemaKeys[key.Value]; !ok {
				allowed = false
			}
			childMode := yamlOpaqueKeys
			switch mode {
			case yamlSchemaKeys:
				if allowed {
					childMode = yamlSchemaKeys
					if _, ok := composeEntityMapKeys[key.Value]; ok {
						childMode = yamlEntityKeys
					}
					if _, ok := composeOpaqueMapKeys[key.Value]; ok {
						childMode = yamlOpaqueKeys
					}
				}
			case yamlEntityKeys:
				childMode = yamlSchemaKeys
			case yamlOpaqueKeys:
				childMode = yamlOpaqueKeys
			}
			keyValue := key.Value
			if !allowed {
				state.maskedKeys++
				keyValue = fmt.Sprintf("redacted-key-%d", state.maskedKeys)
			}
			*key = yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: keyValue}
			if !redactYAMLValuesWithState(value, childMode, state, depth+1) {
				return false
			}
		}
	case yaml.AliasNode:
		*node = yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "[REDACTED]"}
	case yaml.ScalarNode:
		node.Tag = "!!str"
		node.Value = "[REDACTED]"
		node.Style = 0
	}
	return true
}

func stringSet(values ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

// envStructurePreview exposes only syntactically valid variable names. Values,
// comments, malformed lines, and multiline continuations are never echoed.
func envStructurePreview(content string) string {
	const (
		header      = "# Environment values hidden by Cairn import review.\n"
		unavailable = "# Environment values hidden; the structured preview exceeded its display limit.\n"
	)
	var preview strings.Builder
	preview.Grow(min(len(content), int(maxProjectPreviewFileBytes)))
	preview.WriteString(header)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "\ufeff"))
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		separator := strings.IndexByte(line, '=')
		if separator <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:separator])
		if envAssignmentKeyPattern.MatchString(key) {
			entry := key + "=[REDACTED]\n"
			if int64(preview.Len()+len(entry)) > maxProjectPreviewFileBytes {
				return unavailable
			}
			preview.WriteString(entry)
		}
	}
	return preview.String()
}

func safeComposeDisplayErrors(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	message := "Compose validation failed. Review the selected project files."
	for i, value := range values {
		if i >= maxComposeErrorCandidates {
			break
		}
		if len(value) > maxComposeErrorScanBytes {
			value = value[:maxComposeErrorScanBytes]
		}
		if line := composeLinePattern.FindString(value); line != "" {
			message += " Reported at " + strings.ToLower(line) + "."
			break
		}
	}
	return []string{message}
}
