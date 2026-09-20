package providers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
)

const (
	existingContextIDPrefix        = "ctx:"
	maxExistingContextTLSFiles     = 32
	maxExistingContextTLSFileBytes = 4 << 20
)

type ExistingContextOptions struct {
	ContextName string
	Runner      CommandRunner
	StdioDialer WSLStdioDialer
}

type ExistingContextProvider struct {
	contextName string
	runner      CommandRunner
	stdioDialer WSLStdioDialer

	targetMu         sync.Mutex
	targetCond       *sync.Cond
	freezeTarget     bool
	expected         *existingContextTarget
	frozenTLSDir     string
	activeTargetOps  int
	runtimeClosed    bool
	runtimeCloseErr  error
	runtimeCloseDone chan struct{}
}

type existingContextTarget struct {
	name          string
	host          string
	fingerprint   string
	skipTLSVerify bool
	dockerArgs    []string
	tlsFiles      []existingContextTLSFileFingerprint
}

type existingContextTLSFileFingerprint struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	SHA256   string `json:"sha256"`
	content  []byte
	captured bool
}

type existingContextInspect struct {
	Name        string                     `json:"Name"`
	Endpoints   map[string]json.RawMessage `json:"Endpoints"`
	TLSMaterial map[string][]string        `json:"TLSMaterial"`
	Storage     struct {
		TLSPath string `json:"TLSPath"`
	} `json:"Storage"`
}

func NewExistingContext(opts ExistingContextOptions) *ExistingContextProvider {
	runner := opts.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	stdioDialer := opts.StdioDialer
	if stdioDialer == nil {
		stdioDialer = dialCommandStdio
	}
	provider := &ExistingContextProvider{
		contextName:      strings.TrimSpace(opts.ContextName),
		runner:           runner,
		stdioDialer:      stdioDialer,
		runtimeCloseDone: make(chan struct{}),
	}
	provider.targetCond = sync.NewCond(&provider.targetMu)
	return provider
}

func ExistingContextProviderID(contextName string) string {
	return existingContextIDPrefix + strings.TrimSpace(contextName)
}

func (p *ExistingContextProvider) ID() string {
	return ExistingContextProviderID(p.configuredContext())
}

func (p *ExistingContextProvider) DisplayName() string {
	return "Docker context: " + p.configuredContext()
}

func (p *ExistingContextProvider) Type() string {
	return TypeExistingContext
}

func (p *ExistingContextProvider) Platform() string {
	return PlatformAny
}

func (p *ExistingContextProvider) Detect(ctx context.Context) (*models.ProviderStatus, error) {
	status := &models.ProviderStatus{}
	if _, err := p.runner.LookPath("docker"); err != nil {
		status.Problems = append(status.Problems, providerProblem(
			ProblemDockerMissing,
			"Docker CLI is not installed or not on PATH.",
			"Install Docker CLI or choose a platform-managed provider.",
			true,
		))
		return status, nil
	}
	status.DockerInstalled = true

	contextInfo, ok := p.findContext(ctx)
	if !ok {
		status.Problems = append(status.Problems, providerProblem(
			ProblemContextMissing,
			fmt.Sprintf("Docker context %q was not found.", p.configuredContext()),
			"Create the Docker context outside Cairn or choose a different context.",
			true,
		))
		return status, nil
	}
	status.CurrentContext = contextInfo.Name
	status.DockerHost = contextInfo.DockerHost
	if isUnencryptedTCPHost(contextInfo.DockerHost) {
		status.Warnings = append(status.Warnings, models.ProviderWarning{
			Code:    WarningUnencryptedTCP,
			Message: "This Docker context uses unencrypted tcp:// transport.",
		})
	}

	if composeVersion, ok := p.runDockerContextText(ctx, "compose", "version", "--short"); ok {
		status.ComposeInstalled = true
		status.ComposeVersion = normalizeDockerVersion(composeVersion)
	} else {
		status.Problems = append(status.Problems, providerProblem(
			ProblemComposeMissing,
			"Docker Compose is missing for the selected context.",
			"Install Docker Compose v2 for this Docker CLI.",
			true,
		))
	}
	if buildxVersion, ok := p.runDockerContextText(ctx, "buildx", "version"); ok {
		status.BuildxInstalled = true
		status.BackendVersion = normalizeDockerVersion(buildxVersion)
	} else {
		status.Problems = append(status.Problems, providerProblem(
			ProblemBuildxMissing,
			"Docker Buildx is missing for the selected context.",
			"Install or update Docker Buildx for this Docker CLI.",
			true,
		))
	}
	if dockerVersion, ok := p.runDockerContextText(ctx, "info", "--format", "{{.ServerVersion}}"); ok {
		status.DockerRunning = true
		status.DockerVersion = normalizeDockerVersion(dockerVersion)
	} else {
		status.Problems = append(status.Problems, providerProblem(
			ProblemDockerDown,
			"Docker daemon ping through the selected context failed.",
			"Start the backend for this context or choose a reachable context.",
			true,
		))
	}

	status.Installed = status.DockerInstalled && status.ComposeInstalled && status.BuildxInstalled
	status.Running = status.DockerRunning
	status.Healthy = status.Installed && status.Running && !hasBlockingProblem(status.Problems)
	return status, nil
}

func (p *ExistingContextProvider) PlanInstall(context.Context, models.InstallOptions) (*models.CommandPlan, error) {
	return nil, apperror.New(apperror.ProviderNotReady, "Existing Docker contexts are managed outside Cairn")
}

func (p *ExistingContextProvider) ExecuteInstallStep(context.Context, string, int, chan<- InstallProgress) error {
	return apperror.New(apperror.ProviderNotReady, "Existing Docker contexts are managed outside Cairn")
}

func (p *ExistingContextProvider) Start(context.Context) error {
	return apperror.New(apperror.ProviderNotReady, "Cairn cannot start an existing Docker context")
}

func (p *ExistingContextProvider) Stop(context.Context) error {
	return apperror.New(apperror.ProviderNotReady, "Cairn cannot stop an existing Docker context")
}

func (p *ExistingContextProvider) Restart(context.Context) error {
	return apperror.New(apperror.ProviderNotReady, "Cairn cannot restart an existing Docker context")
}

func (p *ExistingContextProvider) DockerHost(ctx context.Context) (string, error) {
	target, err := p.runtimeTarget(ctx)
	if err != nil {
		return "", err
	}
	return target.host, nil
}

func (p *ExistingContextProvider) DockerContext(ctx context.Context) (string, error) {
	return p.BackendIdentity(ctx)
}

func (p *ExistingContextProvider) BackendIdentity(ctx context.Context) (string, error) {
	target, err := p.runtimeTarget(ctx)
	if err != nil {
		return "", err
	}
	return "docker-context:" + target.name + "@sha256:" + target.fingerprint, nil
}

func (p *ExistingContextProvider) DockerDialContext(context.Context) (func(context.Context, string, string) (net.Conn, error), error) {
	return func(ctx context.Context, _, _ string) (net.Conn, error) {
		release, err := p.beginRuntimeTargetOperation()
		if err != nil {
			return nil, err
		}
		defer release()
		prefix, err := p.dockerCommandPrefix(ctx)
		if err != nil {
			return nil, err
		}
		command := append([]string{"docker"}, prefix...)
		command = append(command, "system", "dial-stdio")
		return p.stdioDialer(ctx, command)
	}, nil
}

func (p *ExistingContextProvider) RunDocker(ctx context.Context, args ...string) (*CommandResult, error) {
	release, err := p.beginRuntimeTargetOperation()
	if err != nil {
		return nil, err
	}
	defer release()
	prefix, err := p.dockerCommandPrefix(ctx)
	if err != nil {
		return nil, err
	}
	dockerArgs := append(prefix, args...)
	return p.runner.Run(ctx, dockerOperationTimeout, "docker", dockerArgs...)
}

func (p *ExistingContextProvider) RunDockerWithInput(ctx context.Context, input string, args ...string) (*CommandResult, error) {
	if runner, ok := p.runner.(OptionsCommandRunner); ok {
		release, err := p.beginRuntimeTargetOperation()
		if err != nil {
			return nil, err
		}
		defer release()
		prefix, err := p.dockerCommandPrefix(ctx)
		if err != nil {
			return nil, err
		}
		dockerArgs := append(prefix, args...)
		return runner.RunWithOptions(ctx, CommandRunOptions{
			Timeout: dockerOperationTimeout,
			Stdin:   input,
		}, "docker", dockerArgs...)
	}
	return p.RunDocker(ctx, args...)
}

func (p *ExistingContextProvider) RunBackendCommand(ctx context.Context, input string, args ...string) (*CommandResult, error) {
	if len(args) == 0 {
		return nil, apperror.New(apperror.Conflict, "Backend command is required")
	}
	if runner, ok := p.runner.(OptionsCommandRunner); ok {
		return runner.RunWithOptions(ctx, CommandRunOptions{
			Timeout: commandTimeout,
			Stdin:   input,
		}, args[0], args[1:]...)
	}
	return p.runner.Run(ctx, commandTimeout, args[0], args[1:]...)
}

func (p *ExistingContextProvider) RunCompose(ctx context.Context, workdir string, args ...string) (*CommandResult, error) {
	return p.RunComposeEnv(ctx, workdir, nil, args...)
}

func (p *ExistingContextProvider) RunComposeEnv(ctx context.Context, workdir string, env []string, args ...string) (*CommandResult, error) {
	release, err := p.beginRuntimeTargetOperation()
	if err != nil {
		return nil, err
	}
	defer release()
	prefix, err := p.dockerCommandPrefix(ctx)
	if err != nil {
		return nil, err
	}
	composeArgs := append(prefix, "compose")
	composeArgs = append(composeArgs, args...)
	timeout := composeTimeoutForArgs(args)
	if runner, ok := p.runner.(OptionsCommandRunner); ok {
		return runner.RunWithOptions(ctx, CommandRunOptions{
			Timeout: timeout,
			Workdir: workdir,
			Env:     env,
		}, "docker", composeArgs...)
	}
	result, err := p.runner.Run(ctx, timeout, "docker", composeArgs...)
	if result != nil && workdir != "" {
		result.Workdir = workdir
	}
	return result, err
}

func (p *ExistingContextProvider) HostShellCommand(opts models.TerminalOptions) ([]string, error) {
	shell := strings.TrimSpace(opts.Shell)
	if shell != "" {
		return []string{shell}, nil
	}
	switch runtime.GOOS {
	case "windows":
		if _, err := p.runner.LookPath("pwsh"); err == nil {
			return []string{"pwsh"}, nil
		}
		return []string{"powershell.exe"}, nil
	case "darwin":
		return []string{"/bin/zsh"}, nil
	default:
		if envShell := os.Getenv("SHELL"); envShell != "" {
			return []string{envShell}, nil
		}
		return []string{"/bin/sh"}, nil
	}
}

func (p *ExistingContextProvider) BackendShellCommand(models.TerminalOptions) ([]string, error) {
	return nil, apperror.New(apperror.ProviderNotReady, "Backend shell is not available for generic existing Docker contexts")
}

func (p *ExistingContextProvider) MapPathToBackend(hostPath string) (string, error) {
	value := strings.TrimSpace(hostPath)
	if value == "" {
		return "", errors.New("path is empty")
	}
	return value, nil
}

func (p *ExistingContextProvider) MapPathToHost(backendPath string) (string, error) {
	value := strings.TrimSpace(backendPath)
	if value == "" {
		return "", errors.New("path is empty")
	}
	return value, nil
}

func (p *ExistingContextProvider) configuredContext() string {
	if strings.TrimSpace(p.contextName) != "" {
		return strings.TrimSpace(p.contextName)
	}
	return "default"
}

func (p *ExistingContextProvider) runtimeTarget(ctx context.Context) (existingContextTarget, error) {
	if !p.freezeTarget {
		return p.inspectContextTarget(ctx, false)
	}

	p.targetMu.Lock()
	if p.runtimeClosed {
		err := existingContextRuntimeClosedError()
		p.targetMu.Unlock()
		return existingContextTarget{}, err
	}
	if p.expected == nil {
		p.targetMu.Unlock()
		return existingContextTarget{}, apperror.New(apperror.ProviderNotReady, "Docker context runtime target is not frozen")
	}
	p.targetMu.Unlock()

	current, err := p.inspectContextTarget(ctx, false)
	if err != nil {
		return existingContextTarget{}, err
	}

	p.targetMu.Lock()
	defer p.targetMu.Unlock()
	if p.runtimeClosed {
		return existingContextTarget{}, existingContextRuntimeClosedError()
	}
	if p.expected == nil || current.fingerprint != p.expected.fingerprint {
		return existingContextTarget{}, apperror.New(
			apperror.ProviderNotReady,
			"Docker context target changed; reconnect the provider",
			apperror.WithDetail(p.configuredContext()),
		)
	}
	// Keep executing with the exact command arguments captured for this runtime
	// generation. The current inspect is used only to revalidate the target.
	target := *p.expected
	target.dockerArgs = append([]string(nil), p.expected.dockerArgs...)
	return target, nil
}

func (p *ExistingContextProvider) freezeRuntimeTarget(ctx context.Context) error {
	target, err := p.inspectContextTarget(ctx, true)
	if err != nil {
		return err
	}
	frozenFiles, frozenDir, err := freezeExistingContextTLSFiles(target.tlsFiles)
	if err != nil {
		return apperror.Wrap(
			apperror.ProviderNotReady,
			"Freeze Docker context TLS material failed",
			err,
			apperror.WithDetail(p.configuredContext()),
		)
	}
	target.dockerArgs = existingContextDockerArgs(target.host, target.skipTLSVerify, frozenFiles)
	target.tlsFiles = nil

	p.targetMu.Lock()
	defer p.targetMu.Unlock()
	if p.runtimeClosed {
		_ = os.RemoveAll(frozenDir)
		return existingContextRuntimeClosedError()
	}
	p.expected = &target
	p.frozenTLSDir = frozenDir
	return nil
}

func (p *ExistingContextProvider) beginRuntimeTargetOperation() (func(), error) {
	if !p.freezeTarget {
		return func() {}, nil
	}
	p.targetMu.Lock()
	if p.runtimeClosed {
		p.targetMu.Unlock()
		return nil, existingContextRuntimeClosedError()
	}
	p.activeTargetOps++
	p.targetMu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			p.targetMu.Lock()
			p.activeTargetOps--
			if p.activeTargetOps == 0 {
				p.targetCond.Broadcast()
			}
			p.targetMu.Unlock()
		})
	}, nil
}

// CloseRuntime releases private TLS material owned by a frozen runtime
// snapshot. It waits for in-flight Docker CLI process launches so their pinned
// files cannot disappear between validation and process startup.
func (p *ExistingContextProvider) CloseRuntime() error {
	if p == nil || !p.freezeTarget {
		return nil
	}
	p.targetMu.Lock()
	if p.runtimeClosed {
		done := p.runtimeCloseDone
		p.targetMu.Unlock()
		<-done
		p.targetMu.Lock()
		err := p.runtimeCloseErr
		p.targetMu.Unlock()
		return err
	}
	p.runtimeClosed = true
	for p.activeTargetOps > 0 {
		p.targetCond.Wait()
	}
	frozenTLSDir := p.frozenTLSDir
	p.frozenTLSDir = ""
	p.targetMu.Unlock()

	var closeErr error
	if frozenTLSDir != "" {
		closeErr = os.RemoveAll(frozenTLSDir)
	}
	p.targetMu.Lock()
	p.runtimeCloseErr = closeErr
	p.expected = nil
	close(p.runtimeCloseDone)
	p.targetMu.Unlock()
	return closeErr
}

func existingContextRuntimeClosedError() error {
	return apperror.New(apperror.ProviderNotReady, "Docker context runtime is closed; reconnect the provider")
}

func (p *ExistingContextProvider) dockerCommandPrefix(ctx context.Context) ([]string, error) {
	if !p.freezeTarget {
		return []string{"--context", p.configuredContext()}, nil
	}
	target, err := p.runtimeTarget(ctx)
	if err != nil {
		return nil, err
	}
	return append([]string(nil), target.dockerArgs...), nil
}

func (p *ExistingContextProvider) inspectContextTarget(ctx context.Context, captureTLS bool) (existingContextTarget, error) {
	name := p.configuredContext()
	result, err := p.runner.Run(ctx, commandTimeout, "docker", "context", "inspect", name)
	if err != nil || result == nil || result.ExitCode != 0 {
		return existingContextTarget{}, apperror.Wrap(
			apperror.ProviderNotReady,
			"Docker context configuration is not available",
			err,
			apperror.WithDetail(commandFailureDetail(result, err)),
		)
	}
	if result.StdoutTruncated {
		return existingContextTarget{}, apperror.New(apperror.ProviderNotReady, "Docker context configuration exceeded the safe output limit")
	}
	var entries []json.RawMessage
	if err := json.Unmarshal([]byte(strings.TrimSpace(result.Stdout)), &entries); err != nil || len(entries) != 1 {
		return existingContextTarget{}, apperror.Wrap(apperror.ProviderNotReady, "Docker context configuration is invalid", err)
	}
	var inspected existingContextInspect
	if err := json.Unmarshal(entries[0], &inspected); err != nil {
		return existingContextTarget{}, apperror.Wrap(apperror.ProviderNotReady, "Docker context configuration is invalid", err)
	}
	endpointRaw, ok := inspected.Endpoints["docker"]
	var endpoint struct {
		Host          string `json:"Host"`
		SkipTLSVerify bool   `json:"SkipTLSVerify"`
	}
	if !ok || json.Unmarshal(endpointRaw, &endpoint) != nil || strings.TrimSpace(endpoint.Host) == "" || (strings.TrimSpace(inspected.Name) != "" && inspected.Name != name) {
		return existingContextTarget{}, apperror.New(apperror.ProviderNotReady, "Docker context endpoint is not available")
	}
	var canonicalEndpoint any
	if err := json.Unmarshal(endpointRaw, &canonicalEndpoint); err != nil {
		return existingContextTarget{}, apperror.Wrap(apperror.ProviderNotReady, "Docker context configuration is invalid", err)
	}
	tlsPath, tlsFiles, err := existingContextTLSFingerprint(inspected, captureTLS)
	if err != nil {
		return existingContextTarget{}, apperror.Wrap(
			apperror.ProviderNotReady,
			"Docker context TLS material is not available",
			err,
			apperror.WithDetail(name),
		)
	}
	fingerprintInput := struct {
		Name     string                              `json:"name"`
		Endpoint any                                 `json:"endpoint"`
		TLSPath  string                              `json:"tlsPath"`
		TLSFiles []existingContextTLSFileFingerprint `json:"tlsFiles"`
	}{
		Name:     name,
		Endpoint: canonicalEndpoint,
		TLSPath:  tlsPath,
		TLSFiles: tlsFiles,
	}
	encoded, err := json.Marshal(fingerprintInput)
	if err != nil {
		return existingContextTarget{}, apperror.Wrap(apperror.ProviderNotReady, "Docker context configuration is invalid", err)
	}
	digest := sha256.Sum256(encoded)
	host := strings.TrimSpace(endpoint.Host)
	return existingContextTarget{
		name:          name,
		host:          host,
		fingerprint:   hex.EncodeToString(digest[:]),
		skipTLSVerify: endpoint.SkipTLSVerify,
		tlsFiles:      tlsFiles,
	}, nil
}

func existingContextTLSFingerprint(inspected existingContextInspect, captureContent bool) (string, []existingContextTLSFileFingerprint, error) {
	tlsPath := filepath.Clean(strings.TrimSpace(inspected.Storage.TLSPath))
	if tlsPath == "." && strings.TrimSpace(inspected.Storage.TLSPath) == "" {
		tlsPath = ""
	}
	filenames := append([]string(nil), inspected.TLSMaterial["docker"]...)
	if len(filenames) == 0 {
		return tlsPath, nil, nil
	}
	if tlsPath == "" {
		return "", nil, errors.New("docker context TLS storage path is empty")
	}
	if len(filenames) > maxExistingContextTLSFiles {
		return "", nil, fmt.Errorf("docker context declares %d TLS files; maximum is %d", len(filenames), maxExistingContextTLSFiles)
	}
	seen := make(map[string]struct{}, len(filenames))
	for i, rawFilename := range filenames {
		filename := strings.TrimSpace(rawFilename)
		if filename == "" {
			return "", nil, errors.New("docker context declares an empty TLS filename")
		}
		if filename != rawFilename || filepath.IsAbs(filename) || filepath.VolumeName(filename) != "" || filepath.Base(filename) != filename || strings.ContainsAny(filename, `/\<>:"|?*`) || filename == "." || filename == ".." {
			return "", nil, fmt.Errorf("docker context TLS filename %q is not a portable basename", rawFilename)
		}
		key := strings.ToLower(filename)
		if _, duplicate := seen[key]; duplicate {
			return "", nil, fmt.Errorf("docker context declares duplicate TLS filename %q", filename)
		}
		seen[key] = struct{}{}
		filenames[i] = filename
	}
	sort.Strings(filenames)
	baseDir := filepath.Clean(filepath.Join(tlsPath, "docker"))
	files := make([]existingContextTLSFileFingerprint, 0, len(filenames))
	for _, filename := range filenames {
		path := filepath.Clean(filepath.Join(baseDir, filename))
		relative, err := filepath.Rel(baseDir, path)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
			return "", nil, fmt.Errorf("docker context TLS filename %q escapes its storage directory", filename)
		}
		digest, content, err := readExistingContextTLSFile(path, captureContent)
		if err != nil {
			return "", nil, fmt.Errorf("hash Docker context TLS file %q: %w", path, err)
		}
		files = append(files, existingContextTLSFileFingerprint{Name: filename, Path: path, SHA256: digest, content: content, captured: captureContent})
	}
	return tlsPath, files, nil
}

func readExistingContextTLSFile(path string, captureContent bool) (string, []byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(io.LimitReader(file, maxExistingContextTLSFileBytes+1))
	if err != nil {
		return "", nil, err
	}
	if len(content) > maxExistingContextTLSFileBytes {
		return "", nil, fmt.Errorf("file exceeds %d bytes", maxExistingContextTLSFileBytes)
	}
	digest := sha256.Sum256(content)
	if !captureContent {
		content = nil
	}
	return hex.EncodeToString(digest[:]), content, nil
}

func freezeExistingContextTLSFiles(files []existingContextTLSFileFingerprint) ([]existingContextTLSFileFingerprint, string, error) {
	if len(files) == 0 {
		return nil, "", nil
	}
	root, err := os.MkdirTemp("", "cairn-existing-context-tls-")
	if err != nil {
		return nil, "", err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(root)
		}
	}()
	if err := os.Chmod(root, 0o700); err != nil {
		return nil, "", err
	}
	dockerDir := filepath.Join(root, "docker")
	if err := os.Mkdir(dockerDir, 0o700); err != nil {
		return nil, "", err
	}
	if err := os.Chmod(dockerDir, 0o700); err != nil {
		return nil, "", err
	}

	frozen := make([]existingContextTLSFileFingerprint, 0, len(files))
	for _, source := range files {
		if !source.captured {
			return nil, "", fmt.Errorf("docker context TLS file %q was not captured", source.Name)
		}
		destination := filepath.Join(dockerDir, source.Name)
		if err := writeExistingContextTLSFile(destination, source.content); err != nil {
			return nil, "", fmt.Errorf("write frozen Docker context TLS file %q: %w", source.Name, err)
		}
		frozen = append(frozen, existingContextTLSFileFingerprint{
			Name:   source.Name,
			Path:   destination,
			SHA256: source.SHA256,
		})
	}
	cleanup = false
	return frozen, root, nil
}

func writeExistingContextTLSFile(path string, content []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func existingContextDockerArgs(host string, skipTLSVerify bool, files []existingContextTLSFileFingerprint) []string {
	dockerArgs := []string{"--host", host}
	if len(files) == 0 {
		// Override DOCKER_TLS/DOCKER_TLS_VERIFY so a frozen plain-text context
		// cannot be retargeted by ambient process configuration.
		return append(dockerArgs, "--tls=false", "--tlsverify=false")
	}
	paths := map[string]string{"ca.pem": "", "cert.pem": "", "key.pem": ""}
	for _, file := range files {
		name := strings.ToLower(file.Name)
		if _, supported := paths[name]; supported {
			paths[name] = file.Path
		}
	}
	// Explicit empty values are intentional. Docker CLI otherwise falls back
	// to ~/.docker/{ca,cert,key}.pem for omitted TLS flags, which would make the
	// runtime depend on mutable credentials outside the captured context.
	return append(dockerArgs,
		"--tls=true",
		fmt.Sprintf("--tlsverify=%t", !skipTLSVerify),
		"--tlscacert", paths["ca.pem"],
		"--tlscert", paths["cert.pem"],
		"--tlskey", paths["key.pem"],
	)
}

func (p *ExistingContextProvider) findContext(ctx context.Context) (models.DockerContextInfo, bool) {
	contexts, ok := listDockerContexts(ctx, p.runner)
	if !ok {
		return models.DockerContextInfo{}, false
	}
	for _, contextInfo := range contexts {
		if contextInfo.Name == p.configuredContext() {
			return contextInfo, true
		}
	}
	return models.DockerContextInfo{}, false
}

func (p *ExistingContextProvider) runDockerContextText(ctx context.Context, args ...string) (string, bool) {
	dockerArgs := append([]string{"--context", p.configuredContext()}, args...)
	result, err := p.runner.Run(ctx, commandTimeout, "docker", dockerArgs...)
	if err != nil || result == nil || result.ExitCode != 0 || result.StdoutTruncated {
		return "", false
	}
	return strings.TrimSpace(result.Stdout), true
}

func listDockerContexts(ctx context.Context, runner CommandRunner) ([]models.DockerContextInfo, bool) {
	if runner == nil {
		runner = ExecRunner{}
	}
	result, err := runner.Run(ctx, commandTimeout, "docker", "context", "ls", "--format", "json")
	if err != nil || result == nil || result.ExitCode != 0 || result.StdoutTruncated {
		return nil, false
	}
	contexts, err := parseDockerContextList(result.Stdout)
	return contexts, err == nil
}

func isUnencryptedTCPHost(host string) bool {
	normalized := strings.ToLower(strings.TrimSpace(host))
	return strings.HasPrefix(normalized, "tcp://")
}
