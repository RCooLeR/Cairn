package terminal

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/bus"
	dockercore "github.com/RCooLeR/Cairn/internal/docker"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/runtimescope"
	"github.com/RCooLeR/Cairn/internal/store"
)

type terminalContextKey string

func TestManagerHostPTYLifecycle(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	eventBus := bus.New()
	defer eventBus.Close()
	dataEvents := eventBus.Subscribe(ctx, bus.TopicTerminalData, 4)
	closedEvents := eventBus.Subscribe(ctx, bus.TopicTerminalClosed, 4)
	starter := &fakePTYStarter{}
	manager := NewManager(fakeProvider{}, nil, nil, eventBus, Options{PTYStarter: starter})

	info, err := manager.OpenHostTerminal(ctx, models.TerminalOptions{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("OpenHostTerminal() error = %v", err)
	}
	if info.Kind != KindHost || info.Shell != "sh" || info.WorkingDir == "" {
		t.Fatalf("info = %#v", info)
	}
	pty := starter.last()
	pty.emit([]byte("hello"))

	payload := readTerminalData(t, dataEvents)
	if payload.SessionID != info.ID {
		t.Fatalf("payload session = %q, want %q", payload.SessionID, info.ID)
	}
	decoded, err := base64.StdEncoding.DecodeString(payload.DataBase64)
	if err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if string(decoded) != "hello" {
		t.Fatalf("data = %q", decoded)
	}

	if err := manager.WriteTerminal(ctx, info.ID, []byte("echo ok\n")); err != nil {
		t.Fatalf("WriteTerminal() error = %v", err)
	}
	if got := pty.written(); got != "echo ok\n" {
		t.Fatalf("written = %q", got)
	}
	if err := manager.ResizeTerminal(ctx, info.ID, 100, 40); err != nil {
		t.Fatalf("ResizeTerminal() error = %v", err)
	}
	if got := pty.lastResize(); got != [2]int{100, 40} {
		t.Fatalf("resize = %#v", got)
	}

	pty.exit(7)
	closed := readTerminalClosed(t, closedEvents)
	if closed.SessionID != info.ID || closed.ExitCode != 7 {
		t.Fatalf("closed = %#v", closed)
	}
	if sessions := manager.ListTerminalSessions(); len(sessions) != 0 {
		t.Fatalf("sessions after exit = %#v", sessions)
	}
}

func TestTerminalCloseContextOutlivesRequestCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), terminalContextKey("trace"), "kept"))
	closeCtx := terminalCloseContext(ctx)
	cancel()
	if err := closeCtx.Err(); err != nil {
		t.Fatalf("close context err = %v, want nil after request cancel", err)
	}
	if got := closeCtx.Value(terminalContextKey("trace")); got != "kept" {
		t.Fatalf("close context value = %#v, want kept", got)
	}
}

func TestCurrentUsernameFallsBackToOSUser(t *testing.T) {
	t.Setenv("USER", "")
	t.Setenv("USERNAME", "")
	previous := currentOSUser
	currentOSUser = func() (*user.User, error) {
		return &user.User{Username: "container-user"}, nil
	}
	t.Cleanup(func() { currentOSUser = previous })

	if got := currentUsername(); got != "container-user" {
		t.Fatalf("currentUsername() = %q, want OS user fallback", got)
	}
}

func TestManagerSessionLimitAndClose(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	starter := &fakePTYStarter{}
	manager := NewManager(fakeProvider{}, nil, nil, nil, Options{PTYStarter: starter, MaxSessions: 1, Scope: runtimescope.Must("linux_native", "default")})

	info, err := manager.OpenBackendTerminal(ctx, models.TerminalOptions{})
	if err != nil {
		t.Fatalf("OpenBackendTerminal() error = %v", err)
	}
	if _, err := manager.OpenHostTerminal(ctx, models.TerminalOptions{}); !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("second OpenHostTerminal() error = %v, want conflict", err)
	}
	if err := manager.CloseTerminal(info.ID); err != nil {
		t.Fatalf("CloseTerminal() error = %v", err)
	}
	if err := manager.WriteTerminal(ctx, info.ID, []byte("x")); !apperror.IsCode(err, apperror.NotFound) {
		t.Fatalf("WriteTerminal(closed) error = %v, want not found", err)
	}
}

func TestManagerReservesCapacityBeforeConcurrentPTYStarts(t *testing.T) {
	t.Parallel()
	const (
		maxSessions = 3
		attempts    = 15
	)
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	starter := newBlockingPTYStarter(attempts, release)
	manager := NewManager(fakeProvider{}, nil, nil, nil, Options{
		PTYStarter:  starter,
		MaxSessions: maxSessions,
	})
	results := make(chan terminalOpenResult, attempts)
	begin := make(chan struct{})
	for i := 0; i < attempts; i++ {
		go func() {
			<-begin
			info, err := manager.OpenHostTerminal(context.Background(), models.TerminalOptions{})
			results <- terminalOpenResult{info: info, err: err}
		}()
	}
	close(begin)

	for i := 0; i < maxSessions; i++ {
		select {
		case <-starter.started:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for admitted PTY start")
		}
	}
	for i := 0; i < attempts-maxSessions; i++ {
		result := awaitTerminalOpenResult(t, results)
		if result.info != nil || !apperror.IsCode(result.err, apperror.Conflict) {
			t.Fatalf("excess open result = %#v, want capacity conflict", result)
		}
	}
	if got := starter.callCount(); got != maxSessions {
		t.Fatalf("PTY starter calls while at capacity = %d, want %d", got, maxSessions)
	}

	releaseOnce.Do(func() { close(release) })
	for i := 0; i < maxSessions; i++ {
		result := awaitTerminalOpenResult(t, results)
		if result.err != nil || result.info == nil {
			t.Fatalf("admitted open result = %#v, want success", result)
		}
	}
	if got := len(manager.ListTerminalSessions()); got != maxSessions {
		t.Fatalf("active sessions = %d, want %d", got, maxSessions)
	}
	manager.StopAll()
}

func TestManagerReservesCapacityBeforeConcurrentContainerExecs(t *testing.T) {
	t.Parallel()
	const (
		maxSessions = 2
		attempts    = 10
	)
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	docker := newBlockingDockerClient(attempts, release)
	manager := NewManager(fakeProvider{}, docker, nil, nil, Options{MaxSessions: maxSessions})
	results := make(chan terminalOpenResult, attempts)
	begin := make(chan struct{})
	for i := 0; i < attempts; i++ {
		go func() {
			<-begin
			info, err := manager.OpenContainerTerminal(context.Background(), "container-1", models.ContainerTerminalOptions{Shell: "/bin/sh"})
			results <- terminalOpenResult{info: info, err: err}
		}()
	}
	close(begin)

	for i := 0; i < maxSessions; i++ {
		select {
		case <-docker.openStarted:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for admitted container exec")
		}
	}
	for i := 0; i < attempts-maxSessions; i++ {
		result := awaitTerminalOpenResult(t, results)
		if result.info != nil || !apperror.IsCode(result.err, apperror.Conflict) {
			t.Fatalf("excess container open result = %#v, want capacity conflict", result)
		}
	}
	if runCalls, openCalls := docker.callCounts(); runCalls != maxSessions || openCalls != maxSessions {
		t.Fatalf("container exec calls while at capacity = probe:%d open:%d, want %d each", runCalls, openCalls, maxSessions)
	}

	releaseOnce.Do(func() { close(release) })
	for i := 0; i < maxSessions; i++ {
		result := awaitTerminalOpenResult(t, results)
		if result.err != nil || result.info == nil {
			t.Fatalf("admitted container result = %#v, want success", result)
		}
	}
}

func TestManagerReleasesReservationWhenPTYStartFails(t *testing.T) {
	t.Parallel()
	starter := &failOncePTYStarter{}
	manager := NewManager(fakeProvider{}, nil, nil, nil, Options{PTYStarter: starter, MaxSessions: 1})

	if _, err := manager.OpenHostTerminal(context.Background(), models.TerminalOptions{}); !apperror.IsCode(err, apperror.Internal) {
		t.Fatalf("first OpenHostTerminal() error = %v, want start failure", err)
	}
	if got := manager.reservationCount(); got != 0 {
		t.Fatalf("reservations after start failure = %d, want 0", got)
	}
	info, err := manager.OpenHostTerminal(context.Background(), models.TerminalOptions{})
	if err != nil {
		t.Fatalf("second OpenHostTerminal() error = %v", err)
	}
	if info == nil || starter.callCount() != 2 {
		t.Fatalf("second open info = %#v, starter calls = %d", info, starter.callCount())
	}
	manager.StopAll()
}

func TestManagerRegistrationFailureClosesAndReapsPTY(t *testing.T) {
	t.Parallel()
	manager := NewManager(fakeProvider{}, nil, nil, nil, Options{MaxSessions: 2})
	firstReservation, err := manager.reserveSession()
	if err != nil {
		t.Fatalf("reserve first session: %v", err)
	}
	firstPTY := newFakePTYSession(PTYSpec{})
	info := models.TerminalSessionInfo{ID: "duplicate-session", Kind: KindHost}
	if _, err := manager.addPTYSession(info, firstPTY, firstReservation); err != nil {
		t.Fatalf("add first PTY session: %v", err)
	}

	secondReservation, err := manager.reserveSession()
	if err != nil {
		t.Fatalf("reserve duplicate session: %v", err)
	}
	rejectedPTY := newFakePTYSession(PTYSpec{})
	if _, err := manager.addPTYSession(info, rejectedPTY, secondReservation); !apperror.IsCode(err, apperror.Conflict) {
		t.Fatalf("add duplicate PTY session error = %v, want conflict", err)
	}
	select {
	case <-rejectedPTY.closed:
	default:
		t.Fatal("rejected PTY was not closed")
	}
	if got := rejectedPTY.waitCount(); got != 1 {
		t.Fatalf("rejected PTY Wait calls = %d, want 1", got)
	}
	if got := manager.reservationCount(); got != 0 {
		t.Fatalf("reservations after registration failure = %d, want 0", got)
	}
	if got := len(manager.ListTerminalSessions()); got != 1 {
		t.Fatalf("active sessions after registration failure = %d, want 1", got)
	}
	manager.StopAll()
}

func TestManagerContainerTerminalDetectsDefaultRootUser(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	docker := &fakeDockerClient{
		detail:  &models.ContainerDetail{Summary: models.ContainerSummary{ID: "abc123", Name: "api-1"}},
		shells:  []string{"/bin/sh"},
		runOut:  "0\n",
		runCode: 0,
	}
	manager := NewManager(fakeProvider{}, docker, nil, nil, Options{})

	info, err := manager.OpenContainerTerminal(ctx, "abc123", models.ContainerTerminalOptions{
		WorkingDir: "/app",
		Env:        map[string]string{"RAILS_ENV": "test"},
		Cols:       132,
		Rows:       43,
	})
	if err != nil {
		t.Fatalf("OpenContainerTerminal() error = %v", err)
	}
	if info.Kind != KindContainer || info.Title != "api-1" || info.Shell != "/bin/sh" || !info.IsRoot || info.User != "" {
		t.Fatalf("info = %#v", info)
	}
	if docker.detectedID != "abc123" {
		t.Fatalf("detectedID = %q", docker.detectedID)
	}
	if got := docker.runCmd; strings.Join(got.Cmd, " ") != "/bin/sh -c id -u" || got.User != "" {
		t.Fatalf("default user probe = %#v", got)
	}
	if docker.openContainerID != "abc123" || strings.Join(docker.openOpts.Cmd, " ") != "/bin/sh" {
		t.Fatalf("open = %q %#v", docker.openContainerID, docker.openOpts)
	}
	if docker.openOpts.WorkingDir != "/app" || docker.openOpts.Env["RAILS_ENV"] != "test" || docker.openOpts.Cols != 132 || docker.openOpts.Rows != 43 {
		t.Fatalf("open opts = %#v", docker.openOpts)
	}
}

func TestManagerContainerTerminalHandlesNoDetectedShells(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	docker := &fakeDockerClient{
		detail: &models.ContainerDetail{Summary: models.ContainerSummary{ID: "abc123", Name: "api-1"}},
		shells: []string{},
	}
	manager := NewManager(fakeProvider{}, docker, nil, nil, Options{})

	_, err := manager.OpenContainerTerminal(ctx, "abc123", models.ContainerTerminalOptions{})
	if !apperror.IsCode(err, apperror.NotFound) {
		t.Fatalf("OpenContainerTerminal(no shells) error = %v, want not found", err)
	}
}

func TestManagerContainerTerminalLabelsRequestedRootUser(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	docker := &fakeDockerClient{
		detail:  &models.ContainerDetail{Summary: models.ContainerSummary{ID: "abc123", Name: "api-1"}},
		runOut:  "0\n",
		runCode: 0,
	}
	manager := NewManager(fakeProvider{}, docker, nil, nil, Options{})

	info, err := manager.OpenContainerTerminal(ctx, "abc123", models.ContainerTerminalOptions{
		Shell: "/bin/sh",
		User:  "root",
	})
	if err != nil {
		t.Fatalf("OpenContainerTerminal(root) error = %v", err)
	}
	if !info.IsRoot || info.User != "root" {
		t.Fatalf("info = %#v", info)
	}
	if got := docker.runCmd; strings.Join(got.Cmd, " ") != "/bin/sh -c id -u" || got.User != "root" {
		t.Fatalf("root probe = %#v", got)
	}
}

func TestManagerProjectTerminalRegistersProjectInfo(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	starter := &fakePTYStarter{}
	projects := fakeProjectStore{
		record: store.ProjectRecord{
			ID:           "linux_native/demo",
			ProviderID:   "linux_native",
			ContextName:  "default",
			Name:         "demo",
			WorkingDir:   "/home/ada/demo",
			ComposeFiles: []string{"compose.yml", "/opt/extra.yml"},
		},
	}
	manager := NewManager(fakeProvider{}, nil, projects, nil, Options{PTYStarter: starter, Scope: runtimescope.Must("linux_native", "default")})

	info, err := manager.OpenProjectTerminal(ctx, "linux_native/demo", models.TerminalOptions{
		Env: map[string]string{"EXTRA": "1"},
	})
	if err != nil {
		t.Fatalf("OpenProjectTerminal() error = %v", err)
	}
	if info.Kind != KindProject || info.ProjectID != "linux_native/demo" || info.Title != "demo" {
		t.Fatalf("info = %#v", info)
	}
	active, err := manager.lookup(info.ID)
	if err != nil {
		t.Fatalf("lookup() error = %v", err)
	}
	if active.info.Kind != KindProject || active.info.ProjectID != "linux_native/demo" {
		t.Fatalf("registered info = %#v", active.info)
	}
	started := starter.last()
	if started == nil {
		t.Fatal("missing started PTY")
	}
	if started.spec.WorkingDir != "/home/ada/demo" {
		t.Fatalf("WorkingDir = %q", started.spec.WorkingDir)
	}
	expectedFiles := make([]string, 0, len(projects.record.ComposeFiles))
	for _, file := range projects.record.ComposeFiles {
		if !filepath.IsAbs(file) {
			file = filepath.Join(projects.record.WorkingDir, file)
		}
		expectedFiles = append(expectedFiles, file)
	}
	expectedComposeFile := strings.Join(expectedFiles, backendPathListSep)
	if started.spec.Env["COMPOSE_PROJECT_NAME"] != "demo" ||
		started.spec.Env["COMPOSE_FILE"] != expectedComposeFile ||
		started.spec.Env["EXTRA"] != "1" {
		t.Fatalf("env = %#v", started.spec.Env)
	}
}

func TestManagerBackendTerminalFailsClosedOnPathMappingError(t *testing.T) {
	t.Parallel()
	starter := &fakePTYStarter{}
	provider := fakeProvider{mapPath: func(string) (string, error) {
		return "", errors.New("unmappable path")
	}}
	manager := NewManager(provider, nil, nil, nil, Options{PTYStarter: starter, Scope: runtimescope.Must("linux_native", "default")})

	_, err := manager.OpenBackendTerminal(context.Background(), models.TerminalOptions{WorkingDir: `C:\Users\Ada\project`})
	if !apperror.IsCode(err, apperror.WorkdirMissing) {
		t.Fatalf("OpenBackendTerminal() error = %v, want workdir missing", err)
	}
	if starter.last() != nil {
		t.Fatal("terminal process started after path mapping failed")
	}
}

func TestManagerBackendTerminalRequiresRuntimeScope(t *testing.T) {
	t.Parallel()
	starter := &fakePTYStarter{}
	manager := NewManager(fakeProvider{}, nil, nil, nil, Options{PTYStarter: starter})

	_, err := manager.OpenBackendTerminal(context.Background(), models.TerminalOptions{})
	if !apperror.IsCode(err, apperror.ProviderNotReady) {
		t.Fatalf("OpenBackendTerminal() error = %v, want provider not ready", err)
	}
	if starter.last() != nil {
		t.Fatal("backend terminal process started without a runtime scope")
	}
}

func TestManagerProjectTerminalFailsClosedOnComposePathMappingError(t *testing.T) {
	t.Parallel()
	starter := &fakePTYStarter{}
	projects := fakeProjectStore{record: store.ProjectRecord{
		ID:           "windows_wsl/demo",
		ProviderID:   "windows_wsl",
		ContextName:  "wsl:demo",
		Name:         "demo",
		WorkingDir:   `C:\Users\Ada\demo`,
		ComposeFiles: []string{"compose.yml"},
	}}
	provider := fakeProvider{id: "windows_wsl", contextName: "wsl:demo", mapPath: func(path string) (string, error) {
		if strings.HasSuffix(path, "compose.yml") {
			return "", errors.New("Compose file is outside the backend mount")
		}
		return "/mnt/c/Users/Ada/demo", nil
	}}
	manager := NewManager(provider, nil, projects, nil, Options{PTYStarter: starter, Scope: runtimescope.Must("windows_wsl", "wsl:demo")})

	_, err := manager.OpenProjectTerminal(context.Background(), projects.record.ID, models.TerminalOptions{})
	if !apperror.IsCode(err, apperror.WorkdirMissing) {
		t.Fatalf("OpenProjectTerminal() error = %v, want workdir missing", err)
	}
	if starter.last() != nil {
		t.Fatal("terminal process started after Compose path mapping failed")
	}
}

func TestCheatsheetEntriesAreSafeOnlyWhenRunnable(t *testing.T) {
	t.Parallel()
	entries := CheatsheetEntries()
	if len(entries) < 60 {
		t.Fatalf("entries = %d, want at least 60", len(entries))
	}
	allowedCategories := map[string]bool{
		"cleanup":     true,
		"compose":     true,
		"containers":  true,
		"exec":        true,
		"images":      true,
		"logs":        true,
		"networks":    true,
		"stats/debug": true,
		"volumes":     true,
	}
	categories := map[string]bool{}
	seenCommands := map[string]bool{}
	for _, entry := range entries {
		if entry.Category == "" || entry.Command == "" || entry.Description == "" {
			t.Fatalf("incomplete cheatsheet entry = %#v", entry)
		}
		if !allowedCategories[entry.Category] {
			t.Fatalf("unexpected cheatsheet category %q in %#v", entry.Category, entry)
		}
		categories[entry.Category] = true
		if seenCommands[entry.Command] {
			t.Fatalf("duplicate cheatsheet command %q", entry.Command)
		}
		seenCommands[entry.Command] = true
		if entry.Runnable && entry.Risk != models.RiskSafe {
			t.Fatalf("non-safe runnable entry = %#v", entry)
		}
		for _, placeholder := range entry.Placeholders {
			if !strings.Contains(entry.Command, "<"+placeholder+">") {
				t.Fatalf("placeholder %q missing from command %q", placeholder, entry.Command)
			}
		}
		for _, placeholder := range commandPlaceholders(entry.Command) {
			found := false
			for _, declared := range entry.Placeholders {
				if declared == placeholder {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("command %q uses undeclared placeholder %q", entry.Command, placeholder)
			}
		}
	}
	for category := range allowedCategories {
		if !categories[category] {
			t.Fatalf("missing category %q", category)
		}
	}
}

func TestCheatsheetRisksMatchSecurityPolicy(t *testing.T) {
	t.Parallel()
	entries := map[string]models.CheatsheetEntry{}
	for _, entry := range CheatsheetEntries() {
		entries[entry.Command] = entry
	}
	want := map[string]models.Risk{
		"docker start <container>":                        models.RiskSafe,
		"docker stop <container>":                         models.RiskSafe,
		"docker restart <container>":                      models.RiskSafe,
		"docker pull <image>":                             models.RiskSafe,
		"docker run -d --name <name> <image>":             models.RiskSafe,
		"docker rename <container> <name>":                models.RiskSafe,
		"docker volume create <volume>":                   models.RiskSafe,
		"docker network create <network>":                 models.RiskSafe,
		"docker tag <image> <target>":                     models.RiskSafe,
		"docker save -o <path> <image>":                   models.RiskSafe,
		"docker load -i <path>":                           models.RiskSafe,
		"docker kill <container>":                         models.RiskNeedsConfirmation,
		"docker rm <container>":                           models.RiskNeedsConfirmation,
		"docker rmi <image>":                              models.RiskNeedsConfirmation,
		"docker network rm <network>":                     models.RiskNeedsConfirmation,
		"docker push <image>":                             models.RiskNeedsConfirmation,
		"docker compose up -d <service>":                  models.RiskNeedsConfirmation,
		"docker compose build --pull <service>":           models.RiskNeedsConfirmation,
		"docker rm -f <container>":                        models.RiskDestructive,
		"docker rmi -f <image>":                           models.RiskDestructive,
		"docker container prune":                          models.RiskDestructive,
		"docker image prune":                              models.RiskDestructive,
		"docker image prune -a":                           models.RiskDestructive,
		"docker builder prune":                            models.RiskDestructive,
		"docker compose up -d --force-recreate <service>": models.RiskDestructive,
		"docker compose down":                             models.RiskDestructive,
		"docker volume rm <volume>":                       models.RiskDangerous,
		"docker volume prune":                             models.RiskDangerous,
		"docker system prune":                             models.RiskDangerous,
		"docker system prune --volumes":                   models.RiskDangerous,
		"docker compose down --volumes":                   models.RiskDangerous,
	}
	for command, risk := range want {
		entry, ok := entries[command]
		if !ok {
			t.Fatalf("missing reviewed command %q", command)
		}
		if entry.Risk != risk {
			t.Fatalf("%q risk = %q, want %q", command, entry.Risk, risk)
		}
		if risk != models.RiskSafe && entry.Runnable {
			t.Fatalf("%q is runnable with non-safe risk %q", command, risk)
		}
	}
}

func commandPlaceholders(command string) []string {
	var placeholders []string
	for _, chunk := range strings.Split(command, "<")[1:] {
		name, _, ok := strings.Cut(chunk, ">")
		if ok && name != "" {
			placeholders = append(placeholders, name)
		}
	}
	return placeholders
}

type fakeProvider struct {
	id          string
	contextName string
	mapPath     func(string) (string, error)
}

func (p fakeProvider) ID() string {
	if p.id != "" {
		return p.id
	}
	return "linux_native"
}
func (fakeProvider) DisplayName() string { return "Linux native" }
func (p fakeProvider) DockerContext(context.Context) (string, error) {
	if p.contextName != "" {
		return p.contextName, nil
	}
	return "default", nil
}
func (fakeProvider) HostShellCommand(models.TerminalOptions) ([]string, error) {
	return []string{"/bin/sh"}, nil
}
func (fakeProvider) BackendShellCommand(models.TerminalOptions) ([]string, error) {
	return []string{"/bin/sh"}, nil
}

func (p fakeProvider) MapPathToBackend(path string) (string, error) {
	if p.mapPath != nil {
		return p.mapPath(path)
	}
	return path, nil
}

type fakeProjectStore struct {
	record store.ProjectRecord
	err    error
}

func (s fakeProjectStore) GetInScope(_ context.Context, scope runtimescope.Scope, id string) (store.ProjectRecord, error) {
	if s.err != nil {
		return store.ProjectRecord{}, s.err
	}
	if s.record.ID != id || !scope.Matches(s.record.ProviderID, s.record.ContextName) {
		return store.ProjectRecord{}, apperror.New(apperror.NotFound, "project not found")
	}
	return s.record, nil
}

type terminalOpenResult struct {
	info *models.TerminalSessionInfo
	err  error
}

func awaitTerminalOpenResult(t *testing.T, results <-chan terminalOpenResult) terminalOpenResult {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for terminal open result")
		return terminalOpenResult{}
	}
}

func (m *Manager) reservationCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.reservations
}

type blockingPTYStarter struct {
	started  chan struct{}
	release  <-chan struct{}
	mu       sync.Mutex
	calls    int
	sessions []*fakePTYSession
}

func newBlockingPTYStarter(buffer int, release <-chan struct{}) *blockingPTYStarter {
	return &blockingPTYStarter{
		started: make(chan struct{}, buffer),
		release: release,
	}
}

func (s *blockingPTYStarter) Start(ctx context.Context, spec PTYSpec) (PTYSession, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	s.started <- struct{}{}
	select {
	case <-s.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	pty := newFakePTYSession(spec)
	s.mu.Lock()
	s.sessions = append(s.sessions, pty)
	s.mu.Unlock()
	return pty, nil
}

func (s *blockingPTYStarter) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

type failOncePTYStarter struct {
	mu       sync.Mutex
	calls    int
	sessions []*fakePTYSession
}

func (s *failOncePTYStarter) Start(_ context.Context, spec PTYSpec) (PTYSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.calls == 1 {
		return nil, errors.New("injected PTY start failure")
	}
	pty := newFakePTYSession(spec)
	s.sessions = append(s.sessions, pty)
	return pty, nil
}

func (s *failOncePTYStarter) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

type fakePTYStarter struct {
	mu       sync.Mutex
	sessions []*fakePTYSession
	err      error
}

func (s *fakePTYStarter) Start(_ context.Context, spec PTYSpec) (PTYSession, error) {
	if s.err != nil {
		return nil, s.err
	}
	pty := newFakePTYSession(spec)
	s.mu.Lock()
	s.sessions = append(s.sessions, pty)
	s.mu.Unlock()
	return pty, nil
}

func (s *fakePTYStarter) last() *fakePTYSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.sessions) == 0 {
		return nil
	}
	return s.sessions[len(s.sessions)-1]
}

type fakePTYSession struct {
	spec    PTYSpec
	input   chan []byte
	exited  chan int
	closed  chan struct{}
	once    sync.Once
	mu      sync.Mutex
	buf     []byte
	writes  bytes.Buffer
	resizes [][2]int
	waits   int
}

func newFakePTYSession(spec PTYSpec) *fakePTYSession {
	return &fakePTYSession{
		spec:   spec,
		input:  make(chan []byte, 8),
		exited: make(chan int, 1),
		closed: make(chan struct{}),
	}
}

func (s *fakePTYSession) Read(p []byte) (int, error) {
	for {
		s.mu.Lock()
		if len(s.buf) > 0 {
			n := copy(p, s.buf)
			s.buf = s.buf[n:]
			s.mu.Unlock()
			return n, nil
		}
		s.mu.Unlock()
		select {
		case data := <-s.input:
			if data == nil {
				return 0, io.EOF
			}
			s.mu.Lock()
			s.buf = append(s.buf, data...)
			s.mu.Unlock()
		case <-s.closed:
			return 0, io.ErrClosedPipe
		}
	}
}

func (s *fakePTYSession) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writes.Write(p)
}

func (s *fakePTYSession) Close() error {
	s.once.Do(func() {
		close(s.closed)
	})
	return nil
}

func (s *fakePTYSession) Resize(cols int, rows int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resizes = append(s.resizes, [2]int{cols, rows})
	return nil
}

func (s *fakePTYSession) Wait() int {
	s.mu.Lock()
	s.waits++
	s.mu.Unlock()
	select {
	case code := <-s.exited:
		return code
	case <-s.closed:
		return -1
	}
}

func (s *fakePTYSession) waitCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.waits
}

func (s *fakePTYSession) emit(data []byte) {
	s.input <- data
}

func (s *fakePTYSession) exit(code int) {
	s.exited <- code
}

func (s *fakePTYSession) written() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writes.String()
}

func (s *fakePTYSession) lastResize() [2]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.resizes) == 0 {
		return [2]int{}
	}
	return s.resizes[len(s.resizes)-1]
}

type fakeDockerClient struct {
	detail          *models.ContainerDetail
	shells          []string
	runOut          string
	runCode         int
	runErr          error
	detectedID      string
	runCmd          dockercore.ExecOptions
	openContainerID string
	openOpts        dockercore.ExecOptions
}

func (f *fakeDockerClient) GetContainer(context.Context, string) (*models.ContainerDetail, error) {
	if f.detail == nil {
		return nil, apperror.New(apperror.NotFound, "container not found")
	}
	return f.detail, nil
}

func (f *fakeDockerClient) DetectContainerShells(_ context.Context, id string) ([]string, error) {
	f.detectedID = id
	if len(f.shells) == 0 {
		return nil, apperror.New(apperror.NotFound, "shell not found")
	}
	return append([]string(nil), f.shells...), nil
}

func (f *fakeDockerClient) OpenContainerExec(_ context.Context, id string, opts dockercore.ExecOptions) (*dockercore.ExecSession, error) {
	f.openContainerID = id
	f.openOpts = opts
	return &dockercore.ExecSession{ID: "exec-1"}, nil
}

func (f *fakeDockerClient) ResizeContainerExec(context.Context, string, int, int) error {
	return nil
}

func (f *fakeDockerClient) InspectContainerExec(context.Context, string) (*dockercore.ExecInspect, error) {
	return &dockercore.ExecInspect{ExitCode: 0}, nil
}

func (f *fakeDockerClient) RunContainerExec(_ context.Context, _ string, opts dockercore.ExecOptions) (string, int, error) {
	f.runCmd = opts
	if f.runErr != nil {
		return "", -1, f.runErr
	}
	return f.runOut, f.runCode, nil
}

type blockingDockerClient struct {
	openStarted chan struct{}
	release     <-chan struct{}
	mu          sync.Mutex
	runCalls    int
	openCalls   int
}

func newBlockingDockerClient(buffer int, release <-chan struct{}) *blockingDockerClient {
	return &blockingDockerClient{
		openStarted: make(chan struct{}, buffer),
		release:     release,
	}
}

func (f *blockingDockerClient) GetContainer(context.Context, string) (*models.ContainerDetail, error) {
	return &models.ContainerDetail{Summary: models.ContainerSummary{ID: "container-1", Name: "test"}}, nil
}

func (f *blockingDockerClient) DetectContainerShells(context.Context, string) ([]string, error) {
	return nil, errors.New("unexpected shell detection")
}

func (f *blockingDockerClient) OpenContainerExec(ctx context.Context, _ string, _ dockercore.ExecOptions) (*dockercore.ExecSession, error) {
	f.mu.Lock()
	f.openCalls++
	id := f.openCalls
	f.mu.Unlock()
	f.openStarted <- struct{}{}
	select {
	case <-f.release:
		return &dockercore.ExecSession{ID: fmt.Sprintf("exec-%d", id)}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (*blockingDockerClient) ResizeContainerExec(context.Context, string, int, int) error {
	return nil
}

func (*blockingDockerClient) InspectContainerExec(context.Context, string) (*dockercore.ExecInspect, error) {
	return &dockercore.ExecInspect{ExitCode: 0}, nil
}

func (f *blockingDockerClient) RunContainerExec(context.Context, string, dockercore.ExecOptions) (string, int, error) {
	f.mu.Lock()
	f.runCalls++
	f.mu.Unlock()
	return "1000\n", 0, nil
}

func (f *blockingDockerClient) callCounts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runCalls, f.openCalls
}

func readTerminalData(t *testing.T, ch <-chan bus.Event) DataPayload {
	t.Helper()
	select {
	case event := <-ch:
		payload, ok := event.Payload.(DataPayload)
		if !ok {
			t.Fatalf("payload = %#v", event.Payload)
		}
		return payload
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for terminal:data")
		return DataPayload{}
	}
}

func readTerminalClosed(t *testing.T, ch <-chan bus.Event) ClosedPayload {
	t.Helper()
	select {
	case event := <-ch:
		payload, ok := event.Payload.(ClosedPayload)
		if !ok {
			t.Fatalf("payload = %#v", event.Payload)
		}
		return payload
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for terminal:closed")
		return ClosedPayload{}
	}
}
