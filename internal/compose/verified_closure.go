package compose

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"gopkg.in/yaml.v3"
)

type verifiedConfigPreparation struct {
	root                     verifiedConfigRoot
	inputs                   []VerifiedConfigInput
	fingerprint              string
	snapshotDir              string
	snapshotFiles            []string
	snapshotProjectDirectory string
	snapshotEnvFiles         []string
	cleanup                  func() error
}

type verifiedConfigClosure struct {
	ctx         context.Context
	root        verifiedConfigRoot
	entries     map[string][]byte
	directories map[string]struct{}
	parsed      map[verifiedConfigParseKey]struct{}
	visiting    map[verifiedConfigParseKey]struct{}
	candidates  int
	totalBytes  int64
}

type verifiedConfigParseKey struct {
	path             string
	projectDirectory string
}

const verifiedConfigFingerprintVersion = 1

type verifiedConfigFingerprintPayload struct {
	Version               int                             `json:"version"`
	TopFiles              []string                        `json:"topFiles"`
	ProjectDirectory      string                          `json:"projectDirectory"`
	InterpolationEnvFiles []string                        `json:"interpolationEnvFiles"`
	Directories           []string                        `json:"directories"`
	Files                 []verifiedConfigFingerprintFile `json:"files"`
}

type verifiedConfigFingerprintFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

func prepareVerifiedConfigInputs(ctx context.Context, opts ProjectOptions) (*verifiedConfigPreparation, error) {
	if err := verifiedConfigContextError(ctx); err != nil {
		return nil, err
	}
	workdir := strings.TrimSpace(opts.Workdir)
	if workdir == "" {
		for _, candidate := range opts.Files {
			candidate = strings.TrimSpace(candidate)
			if filepath.IsAbs(candidate) {
				workdir = filepath.Dir(candidate)
				break
			}
		}
	}
	root, err := verifyConfigRoot(workdir)
	if err != nil {
		return nil, verifiedConfigInputError(err)
	}
	candidates := append([]string(nil), opts.Files...)
	if len(candidates) == 0 {
		candidates = discoverVerifiedComposeFiles(root.absPath)
	}
	if len(candidates) == 0 {
		return nil, apperror.New(apperror.ComposeInvalid, "No Compose files were found")
	}

	closure := &verifiedConfigClosure{
		ctx:         ctx,
		root:        root,
		entries:     make(map[string][]byte),
		directories: make(map[string]struct{}),
		parsed:      make(map[verifiedConfigParseKey]struct{}),
		visiting:    make(map[verifiedConfigParseKey]struct{}),
	}
	inputs := make([]VerifiedConfigInput, 0, len(candidates))
	topPaths := make([]string, 0, len(candidates))
	seenTop := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		absPath, relPath, err := resolveVerifiedConfigPath(root, candidate)
		if err != nil {
			return nil, verifiedConfigInputError(err)
		}
		identity := verifiedConfigPathIdentity(absPath)
		if _, duplicate := seenTop[identity]; duplicate {
			continue
		}
		seenTop[identity] = struct{}{}
		content, exists, err := closure.addFile(absPath, true, false)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, verifiedConfigInputError(fs.ErrNotExist)
		}
		portableRel := filepath.ToSlash(relPath)
		inputs = append(inputs, VerifiedConfigInput{Path: portableRel, Content: append([]byte(nil), content...)})
		topPaths = append(topPaths, absPath)
	}
	if len(topPaths) == 0 {
		return nil, apperror.New(apperror.ComposeInvalid, "Choose at least one Compose file")
	}

	projectDirectory := filepath.Dir(topPaths[0])
	if explicit := strings.TrimSpace(opts.ProjectDirectory); explicit != "" {
		projectDirectory, err = closure.resolveTopProjectDirectory(explicit)
		if err != nil {
			return nil, err
		}
	}
	if err := closure.rememberDirectory(projectDirectory); err != nil {
		return nil, err
	}
	for index, path := range topPaths {
		if err := closure.scanCompose(path, inputs[index].Content, projectDirectory, 0); err != nil {
			return nil, err
		}
	}

	envPaths, err := closure.collectTopInterpolationEnvFiles(opts.InterpolationEnvFiles)
	if err != nil {
		return nil, err
	}
	if err := root.verifyCurrent(); err != nil {
		return nil, verifiedConfigInputError(err)
	}
	fingerprint, err := fingerprintVerifiedConfigClosure(closure, topPaths, projectDirectory, envPaths)
	if err != nil {
		return nil, internalVerifiedConfigError(err)
	}

	snapshotDir, err := os.MkdirTemp("", "cairn-compose-input-")
	if err != nil {
		return nil, internalVerifiedConfigError()
	}
	cleanup := func() error { return cleanupVerifiedConfigSnapshot(snapshotDir) }
	failSnapshot := func(cause error) (*verifiedConfigPreparation, error) {
		if cleanupErr := cleanup(); cleanupErr != nil {
			return nil, internalVerifiedConfigError(cause, cleanupErr)
		}
		return nil, internalVerifiedConfigError(cause)
	}
	// os.MkdirTemp creates the directory with mode 0700 on Unix. Reapplying
	// chmod is redundant there and can make a newly created directory
	// temporarily unresolvable under Windows sandbox/ACL implementations.
	snapshotRoot, err := verifyConfigRoot(snapshotDir)
	if err != nil {
		return failSnapshot(fmt.Errorf("verify snapshot directory: %w", err))
	}
	rels := make([]string, 0, len(closure.entries))
	for rel := range closure.entries {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	directoryRels := make([]string, 0, len(closure.directories))
	for rel := range closure.directories {
		directoryRels = append(directoryRels, rel)
	}
	sort.Slice(directoryRels, func(i, j int) bool {
		leftDepth := strings.Count(filepath.Clean(directoryRels[i]), string(os.PathSeparator))
		rightDepth := strings.Count(filepath.Clean(directoryRels[j]), string(os.PathSeparator))
		if leftDepth == rightDepth {
			return directoryRels[i] < directoryRels[j]
		}
		return leftDepth < rightDepth
	})
	for _, rel := range directoryRels {
		path := filepath.Join(snapshotDir, rel)
		if !verifiedConfigPathWithin(snapshotDir, path) {
			return failSnapshot(fmt.Errorf("validate snapshot directory path: %w", fs.ErrPermission))
		}
		if err := os.MkdirAll(path, 0o700); err != nil {
			return failSnapshot(fmt.Errorf("create snapshot directory: %w", err))
		}
	}
	for _, rel := range rels {
		if err := verifiedConfigContextError(ctx); err != nil {
			if cleanupErr := cleanup(); cleanupErr != nil {
				return nil, internalVerifiedConfigError()
			}
			return nil, err
		}
		if err := snapshotRoot.verifyCurrent(); err != nil {
			return failSnapshot(fmt.Errorf("revalidate snapshot directory: %w", err))
		}
		destination := filepath.Join(snapshotDir, rel)
		if !verifiedConfigPathWithin(snapshotDir, destination) {
			return failSnapshot(fmt.Errorf("validate snapshot file path: %w", fs.ErrPermission))
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return failSnapshot(fmt.Errorf("create snapshot file directory: %w", err))
		}
		if err := writeVerifiedConfigSnapshot(destination, closure.entries[rel]); err != nil {
			return failSnapshot(fmt.Errorf("write snapshot file: %w", err))
		}
	}
	if err := root.verifyCurrent(); err != nil {
		if cleanupErr := cleanup(); cleanupErr != nil {
			return nil, internalVerifiedConfigError()
		}
		return nil, verifiedConfigInputError(err)
	}

	snapshotFiles := make([]string, 0, len(topPaths))
	for _, path := range topPaths {
		rel, relErr := filepath.Rel(root.absPath, path)
		if relErr != nil || verifiedConfigPathEscapes(rel) {
			return failSnapshot(relErr)
		}
		snapshotFiles = append(snapshotFiles, filepath.Join(snapshotDir, rel))
	}
	projectRel, err := filepath.Rel(root.absPath, projectDirectory)
	if err != nil || verifiedConfigPathEscapes(projectRel) {
		return failSnapshot(err)
	}
	snapshotEnvFiles := make([]string, 0, len(envPaths))
	for _, path := range envPaths {
		rel, relErr := filepath.Rel(root.absPath, path)
		if relErr != nil || verifiedConfigPathEscapes(rel) {
			return failSnapshot(relErr)
		}
		snapshotEnvFiles = append(snapshotEnvFiles, filepath.Join(snapshotDir, rel))
	}
	return &verifiedConfigPreparation{
		root:                     root,
		inputs:                   inputs,
		fingerprint:              fingerprint,
		snapshotDir:              snapshotDir,
		snapshotFiles:            snapshotFiles,
		snapshotProjectDirectory: filepath.Join(snapshotDir, projectRel),
		snapshotEnvFiles:         snapshotEnvFiles,
		cleanup:                  cleanup,
	}, nil
}

func fingerprintVerifiedConfigClosure(
	closure *verifiedConfigClosure,
	topPaths []string,
	projectDirectory string,
	envPaths []string,
) (string, error) {
	relativePaths := func(paths []string) ([]string, error) {
		result := make([]string, 0, len(paths))
		for _, path := range paths {
			rel, err := closure.relativePath(path)
			if err != nil {
				return nil, err
			}
			result = append(result, filepath.ToSlash(rel))
		}
		return result, nil
	}
	topFiles, err := relativePaths(topPaths)
	if err != nil {
		return "", err
	}
	interpolationEnvFiles, err := relativePaths(envPaths)
	if err != nil {
		return "", err
	}
	projectDirectoryAbs, err := filepath.Abs(projectDirectory)
	if err != nil || !verifiedConfigPathWithin(closure.root.absPath, projectDirectoryAbs) {
		return "", unsafeComposeDependencyError()
	}
	projectDirectoryRel, err := filepath.Rel(closure.root.absPath, projectDirectoryAbs)
	if err != nil || verifiedConfigPathEscapes(projectDirectoryRel) {
		return "", unsafeComposeDependencyError()
	}

	directories := make([]string, 0, len(closure.directories))
	for rel := range closure.directories {
		directories = append(directories, filepath.ToSlash(rel))
	}
	sort.Strings(directories)
	filePaths := make([]string, 0, len(closure.entries))
	for rel := range closure.entries {
		filePaths = append(filePaths, rel)
	}
	sort.Strings(filePaths)
	files := make([]verifiedConfigFingerprintFile, 0, len(filePaths))
	for _, rel := range filePaths {
		contentSum := sha256.Sum256(closure.entries[rel])
		files = append(files, verifiedConfigFingerprintFile{
			Path:   filepath.ToSlash(rel),
			SHA256: hex.EncodeToString(contentSum[:]),
		})
	}
	payload := verifiedConfigFingerprintPayload{
		Version:               verifiedConfigFingerprintVersion,
		TopFiles:              topFiles,
		ProjectDirectory:      filepath.ToSlash(projectDirectoryRel),
		InterpolationEnvFiles: interpolationEnvFiles,
		Directories:           directories,
		Files:                 files,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func (closure *verifiedConfigClosure) collectTopInterpolationEnvFiles(selected []string) ([]string, error) {
	if len(selected) > maxVerifiedConfigCandidates {
		return nil, verifiedConfigLimitError()
	}
	if len(selected) == 0 {
		path := filepath.Join(closure.root.absPath, ".env")
		_, exists, err := closure.addFile(path, false, false)
		if err != nil {
			return nil, err
		}
		if !exists {
			if err := closure.addSyntheticFile(path, nil); err != nil {
				return nil, err
			}
		}
		return []string{path}, nil
	}
	paths := make([]string, 0, len(selected))
	seen := make(map[string]struct{}, len(selected))
	for _, candidate := range selected {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		path, err := closure.resolveFile(closure.root.absPath, candidate)
		if err != nil {
			return nil, err
		}
		identity := verifiedConfigPathIdentity(path)
		if _, duplicate := seen[identity]; duplicate {
			continue
		}
		seen[identity] = struct{}{}
		if _, exists, err := closure.addFile(path, true, true); err != nil {
			return nil, err
		} else if !exists {
			return nil, verifiedConfigInputError(fs.ErrNotExist)
		}
		paths = append(paths, path)
	}
	if len(paths) == 0 {
		return nil, apperror.New(apperror.ComposeInvalid, "Choose at least one Compose environment file")
	}
	return paths, nil
}

func (closure *verifiedConfigClosure) addSyntheticFile(path string, content []byte) error {
	if err := closure.consumeCandidate(); err != nil {
		return err
	}
	rel, err := closure.relativePath(path)
	if err != nil {
		return err
	}
	if _, exists := closure.entries[rel]; exists {
		return nil
	}
	if len(closure.entries) >= maxVerifiedConfigFiles || closure.totalBytes+int64(len(content)) > maxVerifiedConfigTotalBytes {
		return verifiedConfigLimitError()
	}
	closure.entries[rel] = append([]byte(nil), content...)
	closure.totalBytes += int64(len(content))
	return nil
}

func (closure *verifiedConfigClosure) addFile(path string, required bool, dependency bool) ([]byte, bool, error) {
	if err := closure.consumeCandidate(); err != nil {
		return nil, false, err
	}
	if err := closure.root.verifyCurrent(); err != nil {
		return nil, false, verifiedConfigInputError(err)
	}
	rel, err := closure.relativePath(path)
	if err != nil {
		return nil, false, err
	}
	if content, exists := closure.entries[rel]; exists {
		return content, true, nil
	}
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		if boundaryErr := closure.verifyNearestExistingParent(path); boundaryErr != nil {
			return nil, false, boundaryErr
		}
		if required {
			return nil, false, verifiedConfigInputError(fs.ErrNotExist)
		}
		return nil, false, nil
	}
	if err != nil || !info.Mode().IsRegular() {
		if dependency {
			return nil, false, boundedComposeDependencyError()
		}
		return nil, false, verifiedConfigInputError(errVerifiedConfigNotRegular)
	}
	if len(closure.entries) >= maxVerifiedConfigFiles || closure.totalBytes >= maxVerifiedConfigTotalBytes {
		return nil, false, verifiedConfigLimitError()
	}
	limit := min(maxVerifiedConfigFileBytes, maxVerifiedConfigTotalBytes-closure.totalBytes)
	content, _, _, err := readVerifiedConfigFile(closure.root, path, limit)
	if err != nil {
		if dependency {
			return nil, false, boundedComposeDependencyError()
		}
		return nil, false, verifiedConfigInputError(err)
	}
	closure.entries[rel] = append([]byte(nil), content...)
	closure.totalBytes += int64(len(content))
	return closure.entries[rel], true, nil
}

func (closure *verifiedConfigClosure) consumeCandidate() error {
	if err := verifiedConfigContextError(closure.ctx); err != nil {
		return err
	}
	closure.candidates++
	if closure.candidates > maxVerifiedConfigCandidates {
		return verifiedConfigTraversalLimitError()
	}
	return nil
}

func (closure *verifiedConfigClosure) relativePath(path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil || !verifiedConfigPathWithin(closure.root.absPath, absPath) {
		return "", unsafeComposeDependencyError()
	}
	rel, err := filepath.Rel(closure.root.absPath, absPath)
	if err != nil || verifiedConfigPathEscapes(rel) || rel == "." {
		return "", unsafeComposeDependencyError()
	}
	return filepath.Clean(rel), nil
}

func (closure *verifiedConfigClosure) resolveFile(baseDir string, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.Contains(value, "$") || strings.Contains(value, "://") || strings.HasPrefix(value, "~") {
		return "", unsafeComposeDependencyError()
	}
	path := filepath.FromSlash(value)
	if filepath.IsAbs(path) || filepath.VolumeName(path) != "" {
		return "", unsafeComposeDependencyError()
	}
	absPath, err := filepath.Abs(filepath.Join(baseDir, path))
	if err != nil || !verifiedConfigPathWithin(closure.root.absPath, absPath) {
		return "", unsafeComposeDependencyError()
	}
	if err := closure.verifyNearestExistingParent(absPath); err != nil {
		return "", err
	}
	return absPath, nil
}

func (closure *verifiedConfigClosure) resolveTopProjectDirectory(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", unsafeComposeDependencyError()
	}
	if !filepath.IsAbs(value) {
		return closure.resolveDirectory(closure.root.absPath, value)
	}
	absPath, err := filepath.Abs(value)
	if err != nil || !verifiedConfigPathWithin(closure.root.absPath, absPath) {
		return "", unsafeComposeDependencyError()
	}
	info, statErr := os.Lstat(absPath)
	if statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", apperror.New(apperror.ComposeInvalid, "Compose project directories must be stable directories inside the project")
	}
	realPath, evalErr := filepath.EvalSymlinks(absPath)
	if evalErr != nil || !verifiedConfigPathWithin(closure.root.realPath, realPath) {
		return "", unsafeComposeDependencyError()
	}
	return absPath, nil
}

func (closure *verifiedConfigClosure) resolveDirectory(baseDir string, value string) (string, error) {
	path, err := closure.resolveFile(baseDir, value)
	if err != nil {
		return "", err
	}
	info, statErr := os.Lstat(path)
	if errors.Is(statErr, fs.ErrNotExist) {
		return path, nil
	}
	if statErr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", apperror.New(apperror.ComposeInvalid, "Compose project directories must be stable directories inside the project")
	}
	realPath, evalErr := filepath.EvalSymlinks(path)
	if evalErr != nil || !verifiedConfigPathWithin(closure.root.realPath, realPath) {
		return "", unsafeComposeDependencyError()
	}
	return path, nil
}

func (closure *verifiedConfigClosure) verifyNearestExistingParent(path string) error {
	if !verifiedConfigPathWithin(closure.root.absPath, path) {
		return unsafeComposeDependencyError()
	}
	current := path
	for {
		info, err := os.Lstat(current)
		if err == nil {
			if current != path && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
				return unsafeComposeDependencyError()
			}
			realPath, evalErr := filepath.EvalSymlinks(current)
			if evalErr != nil || !verifiedConfigPathWithin(closure.root.realPath, realPath) {
				return unsafeComposeDependencyError()
			}
			return nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return boundedComposeDependencyError()
		}
		parent := filepath.Dir(current)
		if parent == current || !verifiedConfigPathWithin(closure.root.absPath, parent) {
			return unsafeComposeDependencyError()
		}
		current = parent
	}
}

func (closure *verifiedConfigClosure) scanCompose(path string, content []byte, projectDirectory string, depth int) error {
	if depth > maxVerifiedConfigDepth {
		return apperror.New(apperror.ComposeInvalid, "Compose dependency nesting exceeds the review limits")
	}
	if err := verifiedConfigContextError(closure.ctx); err != nil {
		return err
	}
	if err := closure.rememberDirectory(projectDirectory); err != nil {
		return err
	}
	pathRel, err := closure.relativePath(path)
	if err != nil {
		return err
	}
	projectRel, err := closure.relativePath(filepath.Join(projectDirectory, ".cairn-project-marker"))
	if err != nil {
		return err
	}
	projectRel = filepath.Dir(projectRel)
	// Keep the components separate. Unix paths may legally contain delimiter
	// characters, so concatenating them can alias two distinct parse scopes and
	// skip dependency scanning for one of them.
	key := verifiedConfigParseKey{
		path:             verifiedConfigPathIdentity(pathRel),
		projectDirectory: verifiedConfigPathIdentity(projectRel),
	}
	if _, cycle := closure.visiting[key]; cycle {
		return apperror.New(apperror.ComposeInvalid, "Compose include or extends dependencies contain a cycle")
	}
	if _, done := closure.parsed[key]; done {
		return nil
	}
	closure.visiting[key] = struct{}{}
	defer delete(closure.visiting, key)

	var document yaml.Node
	if err := yaml.Unmarshal(content, &document); err != nil {
		// Compose owns syntax diagnostics. Invalid YAML cannot provide a
		// trustworthy dependency path, and the private mirror prevents fallback
		// reads from the host project.
		closure.parsed[key] = struct{}{}
		return nil
	}
	mapping := yamlDocumentMapping(&document)
	if mapping == nil {
		closure.parsed[key] = struct{}{}
		return nil
	}
	if err := closure.scanIncludes(yamlMappingValue(mapping, "include"), projectDirectory, depth+1); err != nil {
		return err
	}
	services := yamlMappingValue(mapping, "services")
	if services != nil && services.Kind == yaml.MappingNode {
		if err := visitYAMLMappingEntries(services, func(_ string, service *yaml.Node) error {
			if service.Kind != yaml.MappingNode {
				return nil
			}
			for _, key := range []string{"env_file", "label_file"} {
				if err := closure.scanFileValue(yamlMappingValue(service, key), projectDirectory, false, depth+1); err != nil {
					return err
				}
			}
			if build := yamlMappingValue(service, "build"); build != nil && build.Kind == yaml.MappingNode {
				if err := closure.scanFileValue(yamlMappingValue(build, "env_file"), projectDirectory, false, depth+1); err != nil {
					return err
				}
			}
			extends := yamlMappingValue(service, "extends")
			if extends != nil && extends.Kind == yaml.MappingNode {
				if err := closure.scanFileValue(yamlMappingValue(extends, "file"), projectDirectory, true, depth+1); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}
	for _, sectionName := range []string{"configs", "secrets"} {
		section := yamlMappingValue(mapping, sectionName)
		if section == nil || section.Kind != yaml.MappingNode {
			continue
		}
		if err := visitYAMLMappingEntries(section, func(_ string, entry *yaml.Node) error {
			if entry.Kind == yaml.MappingNode {
				return closure.scanFileValue(yamlMappingValue(entry, "file"), projectDirectory, false, depth+1)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	closure.parsed[key] = struct{}{}
	return nil
}

func (closure *verifiedConfigClosure) scanIncludes(node *yaml.Node, parentProjectDirectory string, depth int) error {
	node = yamlNodeTarget(node)
	if node == nil {
		return nil
	}
	if node.Kind == yaml.SequenceNode {
		for _, entry := range node.Content {
			if err := closure.scanIncludeEntry(entry, parentProjectDirectory, depth); err != nil {
				return err
			}
		}
		return nil
	}
	return closure.scanIncludeEntry(node, parentProjectDirectory, depth)
}

func (closure *verifiedConfigClosure) scanIncludeEntry(node *yaml.Node, parentProjectDirectory string, depth int) error {
	node = yamlNodeTarget(node)
	if node == nil {
		return nil
	}
	var pathNode, envNode, projectNode *yaml.Node
	switch node.Kind {
	case yaml.ScalarNode:
		pathNode = node
	case yaml.MappingNode:
		pathNode = yamlMappingValue(node, "path")
		envNode = yamlMappingValue(node, "env_file")
		projectNode = yamlMappingValue(node, "project_directory")
	default:
		return nil
	}
	paths, err := staticPathValues(pathNode)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return nil
	}
	resolved := make([]string, 0, len(paths))
	for _, value := range paths {
		path, err := closure.resolveFile(parentProjectDirectory, value)
		if err != nil {
			return err
		}
		resolved = append(resolved, path)
	}
	projectDirectory := filepath.Dir(resolved[0])
	if projectNode != nil {
		values, err := staticPathValues(projectNode)
		if err != nil {
			return err
		}
		if len(values) > 1 {
			return unsafeComposeDependencyError()
		}
		if len(values) == 1 {
			projectDirectory, err = closure.resolveDirectory(parentProjectDirectory, values[0])
			if err != nil {
				return err
			}
		}
	}
	if err := closure.rememberDirectory(projectDirectory); err != nil {
		return err
	}
	if envNode == nil {
		defaultEnv := filepath.Join(projectDirectory, ".env")
		if _, _, err := closure.addFile(defaultEnv, false, true); err != nil {
			return err
		}
	} else if err := closure.scanFileValue(envNode, parentProjectDirectory, false, depth); err != nil {
		return err
	}
	for _, includePath := range resolved {
		content, exists, err := closure.addFile(includePath, false, true)
		if err != nil {
			return err
		}
		if exists {
			if err := closure.scanCompose(includePath, content, projectDirectory, depth); err != nil {
				return err
			}
		}
	}
	return nil
}

func (closure *verifiedConfigClosure) scanFileValue(node *yaml.Node, baseDir string, parseCompose bool, depth int) error {
	node = yamlNodeTarget(node)
	if node == nil {
		return nil
	}
	if node.Kind == yaml.MappingNode {
		node = yamlMappingValue(node, "path")
	}
	values, err := staticPathValues(node)
	if err != nil {
		return err
	}
	for _, value := range values {
		path, err := closure.resolveFile(baseDir, value)
		if err != nil {
			return err
		}
		content, exists, err := closure.addFile(path, false, true)
		if err != nil {
			return err
		}
		if parseCompose && exists {
			if err := closure.scanCompose(path, content, baseDir, depth); err != nil {
				return err
			}
		}
	}
	return nil
}

type staticPathTraversal struct {
	visiting map[*yaml.Node]struct{}
	nodes    int
	values   int
}

func staticPathValues(node *yaml.Node) ([]string, error) {
	state := &staticPathTraversal{visiting: make(map[*yaml.Node]struct{})}
	return state.read(node, 0)
}

func (state *staticPathTraversal) read(node *yaml.Node, depth int) ([]string, error) {
	if state == nil || depth > maxVerifiedConfigDepth {
		return nil, apperror.New(apperror.ComposeInvalid, "Compose dependency nesting exceeds the review limits")
	}
	node = yamlNodeTarget(node)
	if node == nil {
		return nil, nil
	}
	state.nodes++
	if state.nodes > maxVerifiedConfigPathNodes {
		return nil, verifiedConfigTraversalLimitError()
	}
	switch node.Kind {
	case yaml.ScalarNode:
		value := strings.TrimSpace(node.Value)
		if value == "" {
			return nil, nil
		}
		if strings.Contains(value, "$") || strings.Contains(value, "://") || strings.HasPrefix(value, "~") {
			return nil, unsafeComposeDependencyError()
		}
		state.values++
		if state.values > maxVerifiedConfigCandidates {
			return nil, verifiedConfigTraversalLimitError()
		}
		return []string{value}, nil
	case yaml.SequenceNode:
		if _, cycle := state.visiting[node]; cycle {
			return nil, unsafeComposeDependencyError()
		}
		state.visiting[node] = struct{}{}
		defer delete(state.visiting, node)
		values := make([]string, 0, len(node.Content))
		for _, entry := range node.Content {
			entry = yamlNodeTarget(entry)
			if entry != nil && entry.Kind == yaml.MappingNode {
				entry = yamlMappingValue(entry, "path")
			}
			items, err := state.read(entry, depth+1)
			if err != nil {
				return nil, err
			}
			values = append(values, items...)
		}
		return values, nil
	default:
		return nil, nil
	}
}

func (closure *verifiedConfigClosure) rememberDirectory(path string) error {
	absPath, err := filepath.Abs(path)
	if err != nil || !verifiedConfigPathWithin(closure.root.absPath, absPath) {
		return unsafeComposeDependencyError()
	}
	rel, err := filepath.Rel(closure.root.absPath, absPath)
	if err != nil || verifiedConfigPathEscapes(rel) {
		return unsafeComposeDependencyError()
	}
	closure.directories[filepath.Clean(rel)] = struct{}{}
	return nil
}

func cleanupVerifiedConfigSnapshot(snapshotDir string) error {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		_ = filepath.WalkDir(snapshotDir, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if entry.IsDir() {
				_ = os.Chmod(path, 0o700)
			} else {
				_ = os.Chmod(path, 0o600)
			}
			return nil
		})
		lastErr = os.RemoveAll(snapshotDir)
		_, statErr := os.Stat(snapshotDir)
		if errors.Is(statErr, fs.ErrNotExist) {
			return nil
		}
		if statErr != nil {
			lastErr = errors.Join(lastErr, statErr)
		}
	}
	if lastErr == nil {
		lastErr = errors.New("private Compose snapshot still exists after cleanup")
	}
	return lastErr
}

func restoreVerifiedConfigPaths(config *ConfigResult, originalRoot string, snapshotRoots ...string) {
	if config == nil {
		return
	}
	restore := func(value string) string {
		for _, snapshotRoot := range snapshotRoots {
			if suffix, ok := verifiedSnapshotSuffix(value, snapshotRoot); ok {
				if suffix == "" {
					return originalRoot
				}
				return filepath.Join(originalRoot, filepath.FromSlash(suffix))
			}
		}
		return value
	}
	for index := range config.Services {
		service := &config.Services[index]
		service.BuildContext = restore(service.BuildContext)
		service.DockerfilePath = restore(service.DockerfilePath)
		for envIndex := range service.EnvFiles {
			service.EnvFiles[envIndex] = restore(service.EnvFiles[envIndex])
		}
	}
	for index := range config.EnvFiles {
		config.EnvFiles[index] = restore(config.EnvFiles[index])
	}
	for index := range config.API.EnvFiles {
		config.API.EnvFiles[index] = restore(config.API.EnvFiles[index])
	}
	for _, snapshotRoot := range snapshotRoots {
		if strings.TrimSpace(snapshotRoot) == "" {
			continue
		}
		config.Raw = strings.ReplaceAll(config.Raw, filepath.ToSlash(snapshotRoot), filepath.ToSlash(originalRoot))
		config.Raw = strings.ReplaceAll(config.Raw, snapshotRoot, originalRoot)
		config.API.ResolvedYAML = strings.ReplaceAll(config.API.ResolvedYAML, filepath.ToSlash(snapshotRoot), filepath.ToSlash(originalRoot))
		config.API.ResolvedYAML = strings.ReplaceAll(config.API.ResolvedYAML, snapshotRoot, originalRoot)
	}
}

func verifiedSnapshotSuffix(value string, snapshotRoot string) (string, bool) {
	value = strings.TrimSpace(value)
	snapshotRoot = strings.TrimSpace(snapshotRoot)
	if value == "" || snapshotRoot == "" {
		return "", false
	}
	normalizedValue := strings.TrimRight(strings.ReplaceAll(value, "\\", "/"), "/")
	normalizedRoot := strings.TrimRight(strings.ReplaceAll(snapshotRoot, "\\", "/"), "/")
	compareValue, compareRoot := normalizedValue, normalizedRoot
	if strings.Contains(normalizedRoot, ":") {
		compareValue, compareRoot = strings.ToLower(compareValue), strings.ToLower(compareRoot)
	}
	if compareValue == compareRoot {
		return "", true
	}
	if !strings.HasPrefix(compareValue, compareRoot+"/") {
		return "", false
	}
	return strings.TrimPrefix(normalizedValue[len(normalizedRoot):], "/"), true
}

func verifiedConfigLimitError() error {
	return apperror.New(apperror.ComposeInvalid, "Compose input closure exceeds the review limits")
}

func verifiedConfigTraversalLimitError() error {
	return apperror.New(apperror.ComposeInvalid, "Compose dependency traversal exceeds the review limits")
}

func boundedComposeDependencyError() error {
	return apperror.New(apperror.ComposeInvalid, "Compose dependencies must be bounded regular files that remain stable inside the project")
}
