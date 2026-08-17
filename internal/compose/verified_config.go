package compose

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"gopkg.in/yaml.v3"
)

const (
	maxVerifiedConfigFileBytes  int64 = 512 * 1024
	maxVerifiedConfigTotalBytes int64 = 2 * 1024 * 1024
	maxVerifiedConfigCandidates       = 128
	maxVerifiedConfigFiles            = 32
	maxVerifiedConfigDepth            = 8
	maxVerifiedConfigPathNodes        = maxVerifiedConfigCandidates * 4
)

var (
	errVerifiedConfigNotRegular = errors.New("compose input is not a regular file")
	errVerifiedConfigTooLarge   = errors.New("compose input exceeds the read limit")
	errVerifiedConfigChanged    = errors.New("compose input changed while it was opened")
)

// VerifiedConfigInput is an immutable in-memory copy of one selected Compose
// file. Path is project-relative and never exposes a host-absolute path.
type VerifiedConfigInput struct {
	Path    string
	Content []byte
}

// FingerprintConfigInputs returns a bounded digest of the complete
// statically-addressable Compose input closure, including selected files,
// includes, extends, interpolation env files, env_file entries, configs,
// secrets, and label files. It invokes no provider command.
func FingerprintConfigInputs(ctx context.Context, opts ProjectOptions) (string, error) {
	prepared, err := prepareVerifiedConfigInputs(ctx, opts)
	if err != nil {
		return "", err
	}
	fingerprint := prepared.fingerprint
	if cleanupErr := prepared.cleanup(); cleanupErr != nil {
		return "", internalVerifiedConfigError(cleanupErr)
	}
	if err := verifiedConfigContextError(ctx); err != nil {
		return "", err
	}
	return fingerprint, nil
}

type verifiedConfigRoot struct {
	absPath  string
	realPath string
	info     fs.FileInfo
}

// ConfigVerified bounds and verifies the complete statically-addressable
// Compose input closure before the Compose process starts, then invokes
// Compose against a private immutable project mirror.
func (c *Client) ConfigVerified(ctx context.Context, opts ProjectOptions) (result *ConfigResult, inputs []VerifiedConfigInput, resultErr error) {
	if len(opts.Files) > maxVerifiedConfigCandidates ||
		len(opts.InterpolationEnvFiles) > maxVerifiedConfigCandidates ||
		len(opts.Profiles) > maxVerifiedConfigCandidates ||
		len(opts.Env) > maxVerifiedConfigCandidates {
		return nil, nil, verifiedConfigLimitError()
	}
	prepared, err := prepareVerifiedConfigInputs(ctx, opts)
	if err != nil {
		return nil, nil, err
	}
	cleaned := false
	defer func() {
		if !cleaned {
			if cleanupErr := prepared.cleanup(); cleanupErr != nil {
				result = nil
				inputs = prepared.inputs
				resultErr = internalVerifiedConfigError()
			}
		}
	}()

	verifiedOpts := opts
	verifiedOpts.Workdir = prepared.snapshotDir
	verifiedOpts.ProjectDirectory = prepared.snapshotProjectDirectory
	verifiedOpts.Files = prepared.snapshotFiles
	verifiedOpts.InterpolationEnvFiles = prepared.snapshotEnvFiles
	backendOpts, err := c.strictBackendProjectOptions(ctx, verifiedOpts)
	if err != nil {
		return nil, prepared.inputs, err
	}
	// Review/detection must never materialize arbitrary host-process
	// environment values into the returned model. Dependency paths containing
	// interpolation were already rejected while building the private closure.
	config, configErr := c.configWithBackendProjectOptionsFlags(ctx, backendOpts, "--no-interpolate")
	if config != nil {
		restoreVerifiedConfigPaths(config, prepared.root.absPath, prepared.snapshotDir, backendOpts.Workdir)
	}
	if cleanupErr := prepared.cleanup(); cleanupErr != nil {
		return nil, prepared.inputs, internalVerifiedConfigError()
	}
	cleaned = true
	if err := verifiedConfigContextError(ctx); err != nil {
		return nil, prepared.inputs, err
	}
	return config, prepared.inputs, configErr
}

func (c *Client) strictBackendProjectOptions(ctx context.Context, opts ProjectOptions) (ProjectOptions, error) {
	if c == nil {
		return ProjectOptions{}, apperror.New(apperror.ProviderNotReady, "Compose client is not ready")
	}
	mapper, ok := c.runner.(PathMapper)
	if !ok || mapper == nil {
		return opts, nil
	}
	mapRequired := func(path string) (string, error) {
		if strings.TrimSpace(path) == "" {
			return path, nil
		}
		mapped, err := mapper.MapPathToBackend(path)
		if contextErr := composeContextError(ctx, err); contextErr != nil {
			return "", contextErr
		}
		if err != nil || strings.TrimSpace(mapped) == "" {
			return "", apperror.New(apperror.ProviderNotReady, "Compose input paths could not be mapped to the active backend")
		}
		return mapped, nil
	}
	var err error
	if opts.Workdir, err = mapRequired(opts.Workdir); err != nil {
		return ProjectOptions{}, err
	}
	if opts.ProjectDirectory, err = mapRequired(opts.ProjectDirectory); err != nil {
		return ProjectOptions{}, err
	}
	if len(opts.Files) > 0 {
		files := make([]string, len(opts.Files))
		for index, file := range opts.Files {
			files[index], err = mapRequired(file)
			if err != nil {
				return ProjectOptions{}, err
			}
		}
		opts.Files = files
	}
	if len(opts.InterpolationEnvFiles) > 0 {
		files := make([]string, len(opts.InterpolationEnvFiles))
		for index, file := range opts.InterpolationEnvFiles {
			files[index], err = mapRequired(file)
			if err != nil {
				return ProjectOptions{}, err
			}
		}
		opts.InterpolationEnvFiles = files
	}
	return opts, nil
}

func visitYAMLMappingEntries(mapping *yaml.Node, visit func(string, *yaml.Node) error) error {
	return visitYAMLMappingEntriesSeen(mapping, visit, map[*yaml.Node]struct{}{})
}

func visitYAMLMappingEntriesSeen(mapping *yaml.Node, visit func(string, *yaml.Node) error, seen map[*yaml.Node]struct{}) error {
	mapping = yamlNodeTarget(mapping)
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	if _, duplicate := seen[mapping]; duplicate {
		return nil
	}
	seen[mapping] = struct{}{}
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		key := mapping.Content[index]
		value := yamlNodeTarget(mapping.Content[index+1])
		if key.Kind == yaml.ScalarNode && key.Value == "<<" {
			if value.Kind == yaml.SequenceNode {
				for _, entry := range value.Content {
					if err := visitYAMLMappingEntriesSeen(entry, visit, seen); err != nil {
						return err
					}
				}
			} else if err := visitYAMLMappingEntriesSeen(value, visit, seen); err != nil {
				return err
			}
			continue
		}
		if key.Kind == yaml.ScalarNode {
			if err := visit(key.Value, value); err != nil {
				return err
			}
		}
	}
	return nil
}

func yamlDocumentMapping(document *yaml.Node) *yaml.Node {
	if document == nil {
		return nil
	}
	node := yamlNodeTarget(document)
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		node = yamlNodeTarget(node.Content[0])
	}
	if node.Kind != yaml.MappingNode {
		return nil
	}
	return node
}

func yamlMappingValue(mapping *yaml.Node, key string) *yaml.Node {
	return yamlMappingValueSeen(mapping, key, map[*yaml.Node]struct{}{})
}

func yamlMappingValueSeen(mapping *yaml.Node, key string, seen map[*yaml.Node]struct{}) *yaml.Node {
	mapping = yamlNodeTarget(mapping)
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	if _, duplicate := seen[mapping]; duplicate {
		return nil
	}
	seen[mapping] = struct{}{}
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Kind == yaml.ScalarNode && mapping.Content[index].Value == key {
			return yamlNodeTarget(mapping.Content[index+1])
		}
	}
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Kind != yaml.ScalarNode || mapping.Content[index].Value != "<<" {
			continue
		}
		merge := yamlNodeTarget(mapping.Content[index+1])
		if merge == nil {
			continue
		}
		if merge.Kind == yaml.MappingNode {
			if value := yamlMappingValueSeen(merge, key, seen); value != nil {
				return value
			}
			continue
		}
		if merge.Kind == yaml.SequenceNode {
			for _, entry := range merge.Content {
				if value := yamlMappingValueSeen(entry, key, seen); value != nil {
					return value
				}
			}
		}
	}
	return nil
}

func yamlNodeTarget(node *yaml.Node) *yaml.Node {
	for node != nil && node.Kind == yaml.AliasNode && node.Alias != nil && node.Alias != node {
		node = node.Alias
	}
	return node
}

func unsafeComposeDependencyError() error {
	return apperror.New(apperror.ComposeInvalid, "Compose dependencies must use static paths inside the project")
}

func verifyConfigRoot(path string) (verifiedConfigRoot, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return verifiedConfigRoot{}, fs.ErrInvalid
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return verifiedConfigRoot{}, fmt.Errorf("resolve root path: %w", err)
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return verifiedConfigRoot{}, fmt.Errorf("stat root directory: %w", err)
	}
	if !info.IsDir() {
		return verifiedConfigRoot{}, fs.ErrInvalid
	}
	if !os.SameFile(info, info) {
		return verifiedConfigRoot{}, fmt.Errorf("pin root directory identity: %w", errVerifiedConfigChanged)
	}
	realPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return verifiedConfigRoot{}, fmt.Errorf("resolve root directory links: %w", err)
	}
	realInfo, err := os.Stat(realPath)
	if err != nil {
		return verifiedConfigRoot{}, fmt.Errorf("stat resolved root directory: %w", err)
	}
	if !realInfo.IsDir() || !os.SameFile(info, realInfo) {
		return verifiedConfigRoot{}, errVerifiedConfigChanged
	}
	return verifiedConfigRoot{absPath: absPath, realPath: realPath, info: info}, nil
}

func (root verifiedConfigRoot) verifyCurrent() error {
	current, err := os.Stat(root.absPath)
	if err != nil || !current.IsDir() || root.info == nil || !os.SameFile(root.info, current) {
		return errVerifiedConfigChanged
	}
	realPath, err := filepath.EvalSymlinks(root.absPath)
	if err != nil {
		return errVerifiedConfigChanged
	}
	realInfo, err := os.Stat(realPath)
	if err != nil || !realInfo.IsDir() || !os.SameFile(root.info, realInfo) {
		return errVerifiedConfigChanged
	}
	return nil
}

func resolveVerifiedConfigPath(root verifiedConfigRoot, candidate string) (string, string, error) {
	if err := root.verifyCurrent(); err != nil {
		return "", "", err
	}
	absPath := candidate
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(root.absPath, absPath)
	}
	absPath, err := filepath.Abs(absPath)
	if err != nil {
		return "", "", err
	}
	relPath, err := filepath.Rel(root.absPath, absPath)
	if err != nil || verifiedConfigPathEscapes(relPath) {
		realTarget, evalErr := filepath.EvalSymlinks(absPath)
		if evalErr != nil || !verifiedConfigPathWithin(root.realPath, realTarget) {
			return "", "", fs.ErrPermission
		}
		relPath, err = filepath.Rel(root.realPath, realTarget)
		if err != nil || verifiedConfigPathEscapes(relPath) {
			return "", "", fs.ErrPermission
		}
	}
	return absPath, relPath, nil
}

func readVerifiedConfigFile(root verifiedConfigRoot, path string, limit int64) ([]byte, int64, fs.FileInfo, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, 0, nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, 0, nil, errVerifiedConfigNotRegular
	}
	if !os.SameFile(before, before) {
		return nil, 0, nil, errVerifiedConfigChanged
	}
	if before.Size() > limit {
		return nil, 0, nil, errVerifiedConfigTooLarge
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, nil, err
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return nil, 0, nil, errVerifiedConfigChanged
	}
	if opened.Size() > limit {
		return nil, 0, nil, errVerifiedConfigTooLarge
	}
	if root.verifyCurrent() != nil {
		return nil, 0, nil, errVerifiedConfigChanged
	}
	realTarget, err := filepath.EvalSymlinks(path)
	if err != nil || !verifiedConfigPathWithin(root.realPath, realTarget) {
		return nil, 0, nil, fs.ErrPermission
	}
	current, err := os.Stat(realTarget)
	if err != nil || !current.Mode().IsRegular() || !os.SameFile(opened, current) {
		return nil, 0, nil, errVerifiedConfigChanged
	}
	content, err := io.ReadAll(io.LimitReader(file, limit+1))
	readBytes := int64(len(content))
	if err != nil {
		return content, readBytes, opened, err
	}
	if readBytes > limit {
		return content, readBytes, opened, errVerifiedConfigTooLarge
	}
	after, err := file.Stat()
	if err != nil || !os.SameFile(opened, after) || after.Size() != opened.Size() || !after.ModTime().Equal(opened.ModTime()) {
		return content, readBytes, opened, errVerifiedConfigChanged
	}
	if err := root.verifyCurrent(); err != nil {
		return content, readBytes, opened, errVerifiedConfigChanged
	}
	currentPath, err := os.Lstat(path)
	if err != nil || !currentPath.Mode().IsRegular() || !os.SameFile(opened, currentPath) {
		return content, readBytes, opened, errVerifiedConfigChanged
	}
	currentRealPath, err := filepath.EvalSymlinks(path)
	if err != nil || !verifiedConfigPathWithin(root.realPath, currentRealPath) {
		return content, readBytes, opened, errVerifiedConfigChanged
	}
	currentTarget, err := os.Stat(currentRealPath)
	if err != nil || !currentTarget.Mode().IsRegular() || !os.SameFile(opened, currentTarget) {
		return content, readBytes, opened, errVerifiedConfigChanged
	}
	return append([]byte(nil), content...), readBytes, opened, nil
}

func writeVerifiedConfigSnapshot(path string, content []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := io.Copy(file, bytes.NewReader(content))
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Chmod(path, 0o400)
}

func discoverVerifiedComposeFiles(root string) []string {
	var files []string
	for _, name := range []string{"compose.yaml", "compose.yml", "docker-compose.yml", "docker-compose.yaml"} {
		path := filepath.Join(root, name)
		if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() {
			files = append(files, path)
			break
		}
	}
	if len(files) == 0 {
		return nil
	}
	for _, name := range []string{"compose.override.yaml", "compose.override.yml"} {
		path := filepath.Join(root, name)
		if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() {
			files = append(files, path)
		}
	}
	return files
}

func verifiedConfigPathWithin(root string, target string) bool {
	rel, err := filepath.Rel(root, target)
	return err == nil && !verifiedConfigPathEscapes(rel)
}

func verifiedConfigPathEscapes(rel string) bool {
	return rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func verifiedConfigPathIdentity(path string) string {
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}

func verifiedConfigInputError(err error) error {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.Is(err, errVerifiedConfigTooLarge):
		return apperror.New(apperror.ComposeInvalid, "Compose input exceeds the review limits")
	case errors.Is(err, errVerifiedConfigNotRegular), errors.Is(err, errVerifiedConfigChanged), errors.Is(err, fs.ErrPermission):
		return apperror.New(apperror.ComposeInvalid, "Compose inputs must be stable regular files inside the project")
	default:
		return apperror.New(apperror.ComposeInvalid, "Compose input could not be read safely")
	}
}

func internalVerifiedConfigError(causes ...error) error {
	cause := errors.Join(causes...)
	if cause != nil {
		return apperror.Wrap(apperror.Internal, "Prepare Compose review input failed", cause)
	}
	return apperror.New(apperror.Internal, "Prepare Compose review input failed")
}

func verifiedConfigContextError(ctx context.Context) error {
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
