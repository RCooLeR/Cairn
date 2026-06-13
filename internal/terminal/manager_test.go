package terminal

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/bus"
	dockercore "github.com/RCooLeR/Cairn/internal/docker"
	"github.com/RCooLeR/Cairn/internal/models"
)

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

func TestManagerContainerTerminalDetectsShellAndRoot(t *testing.T) {
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
	if info.Kind != KindContainer || info.Title != "api-1" || info.Shell != "/bin/sh" || !info.IsRoot || info.User != "root" {
		t.Fatalf("info = %#v", info)
	}
	if docker.detectedID != "abc123" {
		t.Fatalf("detectedID = %q", docker.detectedID)
	}
	if got := docker.runCmd; strings.Join(got.Cmd, " ") != "/bin/sh -c id -u" {
		t.Fatalf("root probe = %#v", got.Cmd)
	}
	if docker.openContainerID != "abc123" || strings.Join(docker.openOpts.Cmd, " ") != "/bin/sh" {
		t.Fatalf("open = %q %#v", docker.openContainerID, docker.openOpts)
	}
	if docker.openOpts.WorkingDir != "/app" || docker.openOpts.Env["RAILS_ENV"] != "test" || docker.openOpts.Cols != 132 || docker.openOpts.Rows != 43 {
		t.Fatalf("open opts = %#v", docker.openOpts)
	}
}

func TestCheatsheetEntriesAreSafeOnlyWhenRunnable(t *testing.T) {
	t.Parallel()
	entries := CheatsheetEntries()
	if len(entries) < 60 {
		t.Fatalf("entries = %d, want at least 60", len(entries))
	}
	categories := map[string]bool{}
	for _, entry := range entries {
		categories[entry.Category] = true
		if entry.Runnable && entry.Risk != models.RiskSafe {
			t.Fatalf("non-safe runnable entry = %#v", entry)
		}
	}
	for _, category := range []string{"containers", "images", "compose", "volumes", "networks", "logs", "exec", "stats/debug", "cleanup"} {
		if !categories[category] {
			t.Fatalf("missing category %q", category)
		}
	}
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
