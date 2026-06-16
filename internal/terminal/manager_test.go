package terminal

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"os"
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
	manager := NewManager(fakeProvider{}, nil, nil, nil, Options{PTYStarter: starter, MaxSessions: 1})

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

func TestManagerContainerTerminalDetectsShellWithoutDefaultUserProbe(t *testing.T) {
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
	if info.Kind != KindContainer || info.Title != "api-1" || info.Shell != "/bin/sh" || info.IsRoot || info.User != "" {
		t.Fatalf("info = %#v", info)
	}
	if docker.detectedID != "abc123" {
		t.Fatalf("detectedID = %q", docker.detectedID)
	}
	if docker.runCmd.Cmd != nil {
		t.Fatalf("unexpected default user probe = %#v", docker.runCmd.Cmd)
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
			Name:         "demo",
			WorkingDir:   "/home/ada/demo",
			ComposeFiles: []string{"compose.yml", "/opt/extra.yml"},
		},
	}
	manager := NewManager(fakeProvider{}, nil, projects, nil, Options{PTYStarter: starter})

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
	expectedComposeFile := strings.Join([]string{
		filepath.Join(projects.record.WorkingDir, "compose.yml"),
		filepath.Join(projects.record.WorkingDir, "/opt/extra.yml"),
	}, string(os.PathListSeparator))
	if started.spec.Env["COMPOSE_PROJECT_NAME"] != "demo" ||
		started.spec.Env["COMPOSE_FILE"] != expectedComposeFile ||
		started.spec.Env["EXTRA"] != "1" {
		t.Fatalf("env = %#v", started.spec.Env)
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

type fakeProvider struct{}

func (fakeProvider) ID() string          { return "linux_native" }
func (fakeProvider) DisplayName() string { return "Linux native" }
func (fakeProvider) HostShellCommand(models.TerminalOptions) ([]string, error) {
	return []string{"/bin/sh"}, nil
}
func (fakeProvider) BackendShellCommand(models.TerminalOptions) ([]string, error) {
	return []string{"/bin/sh"}, nil
}
func (fakeProvider) MapPathToBackend(path string) (string, error) {
	return path, nil
}

type fakeProjectStore struct {
	record store.ProjectRecord
	err    error
}

func (s fakeProjectStore) Get(_ context.Context, id string) (store.ProjectRecord, error) {
	if s.err != nil {
		return store.ProjectRecord{}, s.err
	}
	if s.record.ID != id {
		return store.ProjectRecord{}, apperror.New(apperror.NotFound, "project not found")
	}
	return s.record, nil
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
	select {
	case code := <-s.exited:
		return code
	case <-s.closed:
		return -1
	}
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
