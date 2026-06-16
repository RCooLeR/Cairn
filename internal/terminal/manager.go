package terminal

import (
	"context"
	"encoding/base64"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/bus"
	dockercore "github.com/RCooLeR/Cairn/internal/docker"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/store"
	"github.com/google/uuid"
)

const (
	KindHost      = "host"
	KindBackend   = "backend"
	KindProject   = "project"
	KindContainer = "container"

	defaultCols        = 120
	defaultRows        = 30
	defaultMaxSessions = 16
)

type DataPayload struct {
	SessionID  string `json:"sessionID"`
	DataBase64 string `json:"dataBase64"`
}

type ClosedPayload struct {
	SessionID string `json:"sessionID"`
	ExitCode  int    `json:"exitCode"`
}

type Provider interface {
	ID() string
	DisplayName() string
	HostShellCommand(models.TerminalOptions) ([]string, error)
	BackendShellCommand(models.TerminalOptions) ([]string, error)
	MapPathToBackend(string) (string, error)
}

type DockerClient interface {
	GetContainer(context.Context, string) (*models.ContainerDetail, error)
	DetectContainerShells(context.Context, string) ([]string, error)
	OpenContainerExec(context.Context, string, dockercore.ExecOptions) (*dockercore.ExecSession, error)
	ResizeContainerExec(context.Context, string, int, int) error
	InspectContainerExec(context.Context, string) (*dockercore.ExecInspect, error)
	RunContainerExec(context.Context, string, dockercore.ExecOptions) (string, int, error)
}

type ProjectStore interface {
	Get(context.Context, string) (store.ProjectRecord, error)
}

type Options struct {
	MaxSessions int
	Now         func() time.Time
	PTYStarter  PTYStarter
}

type Manager struct {
	provider Provider
	docker   DockerClient
	projects ProjectStore
	events   bus.Bus
	now      func() time.Time
	starter  PTYStarter
	max      int

	mu       sync.RWMutex
	sessions map[string]*session
}

type session struct {
	info         models.TerminalSessionInfo
	stream       io.ReadWriteCloser
	resize       func(int, int) error
	wait         func() int
	inspectExit  func(context.Context) int
	finishOnce   sync.Once
	closeDone    chan struct{}
	closeContext context.Context
}

func NewManager(provider Provider, docker DockerClient, projects ProjectStore, events bus.Bus, opts Options) *Manager {
	maxSessions := opts.MaxSessions
	if maxSessions <= 0 {
		maxSessions = defaultMaxSessions
	}
	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	starter := opts.PTYStarter
	if starter == nil {
		starter = newDefaultPTYStarter()
	}
	return &Manager{
		provider: provider,
		docker:   docker,
		projects: projects,
		events:   events,
		now:      now,
		starter:  starter,
		max:      maxSessions,
		sessions: map[string]*session{},
	}
}

func (m *Manager) OpenHostTerminal(ctx context.Context, opts models.TerminalOptions) (*models.TerminalSessionInfo, error) {
	if m.provider == nil {
		return nil, providerNotReady()
	}
	opts.Cols, opts.Rows = normalizeDimensions(opts.Cols, opts.Rows)
	argv, err := m.provider.HostShellCommand(opts)
	if err != nil {
		return nil, err
	}
	cwd := opts.WorkingDir
	if cwd == "" {
		cwd, _ = os.UserHomeDir()
	}
	ptySession, err := m.starter.Start(ctx, PTYSpec{Argv: argv, Env: opts.Env, WorkingDir: cwd, Cols: opts.Cols, Rows: opts.Rows})
	if err != nil {
		return nil, mapTerminalStartError("open host terminal", err)
	}
	info := models.TerminalSessionInfo{
		ID:         uuid.NewString(),
		Kind:       KindHost,
		Title:      "Host",
		Shell:      shellTitle(argv),
		User:       currentUsername(),
		WorkingDir: cwd,
		CreatedAt:  m.now(),
	}
	return m.addPTYSession(info, ptySession)
}

func (m *Manager) OpenBackendTerminal(ctx context.Context, opts models.TerminalOptions) (*models.TerminalSessionInfo, error) {
	if m.provider == nil {
		return nil, providerNotReady()
	}
	opts.Cols, opts.Rows = normalizeDimensions(opts.Cols, opts.Rows)
	argv, err := m.provider.BackendShellCommand(opts)
	if err != nil {
		return nil, err
	}
	cwd := opts.WorkingDir
	if cwd != "" {
		if mapped, err := m.provider.MapPathToBackend(cwd); err == nil && mapped != "" {
			cwd = mapped
		}
	}
	return m.openProviderPTYTerminal(ctx, opts, argv, cwd, models.TerminalSessionInfo{
		Kind:  KindBackend,
		Title: m.provider.DisplayName(),
	})
}

func (m *Manager) OpenProjectTerminal(ctx context.Context, projectID string, opts models.TerminalOptions) (*models.TerminalSessionInfo, error) {
	if m.provider == nil || m.projects == nil {
		return nil, providerNotReady()
	}
	project, err := m.projects.Get(ctx, projectID)
	if err != nil {
		return nil, apperror.Wrap(apperror.NotFound, "Project not found", err)
	}
	if strings.TrimSpace(project.WorkingDir) == "" {
		return nil, apperror.New(
			apperror.WorkdirMissing,
			"Project working directory is missing",
			apperror.WithRepairHints("Re-scan or re-import the Compose project."),
		)
	}

	env := map[string]string{
		"COMPOSE_PROJECT_NAME": project.Name,
	}
	if len(project.ComposeFiles) > 0 {
		mappedFiles := make([]string, 0, len(project.ComposeFiles))
		for _, file := range project.ComposeFiles {
			if strings.TrimSpace(file) == "" {
				continue
			}
			path := file
			if !filepath.IsAbs(path) {
				path = filepath.Join(project.WorkingDir, path)
			}
			if mapped, err := m.provider.MapPathToBackend(path); err == nil && mapped != "" {
				path = mapped
			}
			mappedFiles = append(mappedFiles, path)
		}
		env["COMPOSE_FILE"] = strings.Join(mappedFiles, string(os.PathListSeparator))
	}
	for key, value := range opts.Env {
		env[key] = value
	}
	cwd := project.WorkingDir
	if mapped, err := m.provider.MapPathToBackend(cwd); err == nil && mapped != "" {
		cwd = mapped
	}
	opts.WorkingDir = cwd
	opts.Env = env
	opts.Cols, opts.Rows = normalizeDimensions(opts.Cols, opts.Rows)

	argv, err := m.provider.BackendShellCommand(opts)
	if err != nil {
		return nil, err
	}
	return m.openProviderPTYTerminal(ctx, opts, argv, cwd, models.TerminalSessionInfo{
		Kind:       KindProject,
		Title:      project.Name,
		ProjectID:  projectID,
		WorkingDir: cwd,
	})
}

func (m *Manager) openProviderPTYTerminal(ctx context.Context, opts models.TerminalOptions, argv []string, cwd string, info models.TerminalSessionInfo) (*models.TerminalSessionInfo, error) {
	ptySession, err := m.starter.Start(ctx, PTYSpec{Argv: argv, Env: opts.Env, WorkingDir: cwd, Cols: opts.Cols, Rows: opts.Rows})
	if err != nil {
		return nil, mapTerminalStartError("open backend terminal", err)
	}
	info.ID = uuid.NewString()
	info.Shell = shellTitle(argv)
	info.User = currentUsername()
	info.WorkingDir = cwd
	info.CreatedAt = m.now()
	return m.addPTYSession(info, ptySession)
}

func (m *Manager) OpenContainerTerminal(ctx context.Context, containerID string, opts models.ContainerTerminalOptions) (*models.TerminalSessionInfo, error) {
	if m.docker == nil {
		return nil, providerNotReady()
	}
	opts.Cols, opts.Rows = normalizeDimensions(opts.Cols, opts.Rows)
	detail, err := m.docker.GetContainer(ctx, containerID)
	if err != nil {
		return nil, err
	}
	shell := strings.TrimSpace(opts.Shell)
	if shell == "" {
		shells, err := m.docker.DetectContainerShells(ctx, containerID)
		if err != nil {
			return nil, err
		}
		shell = shells[0]
	}

	isRoot, userLabel := m.containerUser(ctx, containerID, shell, opts.User)
	execSession, err := m.docker.OpenContainerExec(ctx, containerID, dockercore.ExecOptions{
		Cmd:        []string{shell},
		User:       opts.User,
		WorkingDir: opts.WorkingDir,
		Env:        opts.Env,
		TTY:        true,
		Cols:       opts.Cols,
		Rows:       opts.Rows,
	})
	if err != nil {
		return nil, err
	}
	title := detail.Summary.Name
	if title == "" {
		title = shortID(containerID)
	}
	info := models.TerminalSessionInfo{
		ID:          uuid.NewString(),
		Kind:        KindContainer,
		Title:       title,
		Shell:       shell,
		User:        userLabel,
		WorkingDir:  opts.WorkingDir,
		ContainerID: detail.Summary.ID,
		IsRoot:      isRoot,
		CreatedAt:   m.now(),
	}
	active := &session{
		info:      info,
		stream:    execSession,
		closeDone: make(chan struct{}),
		resize: func(cols int, rows int) error {
			return m.docker.ResizeContainerExec(ctx, execSession.ID, cols, rows)
		},
		inspectExit: func(ctx context.Context) int {
			inspect, err := m.docker.InspectContainerExec(ctx, execSession.ID)
			if err != nil {
				return -1
			}
			return inspect.ExitCode
		},
		closeContext: terminalCloseContext(ctx),
	}
	if err := m.register(active); err != nil {
		_ = execSession.Close()
		return nil, err
	}
	go m.pump(active)
	return &info, nil
}

func (m *Manager) DetectContainerShells(ctx context.Context, containerID string) ([]string, error) {
	if m.docker == nil {
		return nil, providerNotReady()
	}
	return m.docker.DetectContainerShells(ctx, containerID)
}

func (m *Manager) WriteTerminal(_ context.Context, sessionID string, data []byte) error {
	active, err := m.lookup(sessionID)
	if err != nil {
		return err
	}
	_, err = active.stream.Write(data)
	if err != nil {
		return apperror.Wrap(apperror.Internal, "Write to terminal failed", err)
	}
	return nil
}

func (m *Manager) ResizeTerminal(_ context.Context, sessionID string, cols int, rows int) error {
	active, err := m.lookup(sessionID)
	if err != nil {
		return err
	}
	cols, rows = normalizeDimensions(cols, rows)
	if active.resize == nil {
		return nil
	}
	return active.resize(cols, rows)
}

func (m *Manager) CloseTerminal(sessionID string) error {
	if _, err := m.lookup(sessionID); err != nil {
		return err
	}
	m.finish(sessionID, -1)
	return nil
}

func (m *Manager) ListTerminalSessions() []models.TerminalSessionInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	infos := make([]models.TerminalSessionInfo, 0, len(m.sessions))
	for _, active := range m.sessions {
		infos = append(infos, active.info)
	}
	sort.Slice(infos, func(i, j int) bool {
		return infos[i].CreatedAt.Before(infos[j].CreatedAt)
	})
	return infos
}

func (m *Manager) StopAll() {
	ids := make([]string, 0)
	m.mu.RLock()
	for id := range m.sessions {
		ids = append(ids, id)
	}
	m.mu.RUnlock()
	for _, id := range ids {
		m.finish(id, -1)
	}
}

func (m *Manager) addPTYSession(info models.TerminalSessionInfo, ptySession PTYSession) (*models.TerminalSessionInfo, error) {
	active := &session{
		info:      info,
		stream:    ptySession,
		closeDone: make(chan struct{}),
		resize:    ptySession.Resize,
		wait:      ptySession.Wait,
	}
	if err := m.register(active); err != nil {
		_ = ptySession.Close()
		return nil, err
	}
	go m.pump(active)
	go func() {
		exitCode := ptySession.Wait()
		m.finish(info.ID, exitCode)
	}()
	return &info, nil
}

func (m *Manager) register(active *session) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sessions) >= m.max {
		return apperror.New(
			apperror.Conflict,
			"Terminal session limit reached",
			apperror.WithDetail("Cairn allows up to 16 active terminal sessions."),
			apperror.WithRepairHints("Close an existing terminal tab and try again."),
		)
	}
	m.sessions[active.info.ID] = active
	return nil
}

func (m *Manager) lookup(sessionID string) (*session, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	active := m.sessions[sessionID]
	if active == nil {
		return nil, apperror.New(apperror.NotFound, "Terminal session not found")
	}
	return active, nil
}

func (m *Manager) pump(active *session) {
	buf := make([]byte, 32*1024)
	for {
		n, err := active.stream.Read(buf)
		if n > 0 {
			m.publishData(active.info.ID, buf[:n])
		}
		if err != nil {
			exitCode := -1
			if active.inspectExit != nil {
				exitCode = active.inspectExit(active.closeContext)
			}
			m.finish(active.info.ID, exitCode)
			return
		}
	}
}

func terminalCloseContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return context.WithoutCancel(ctx)
}

func (m *Manager) finish(sessionID string, exitCode int) {
	m.mu.RLock()
	active := m.sessions[sessionID]
	m.mu.RUnlock()
	if active == nil {
		return
	}
	active.finishOnce.Do(func() {
		m.mu.Lock()
		delete(m.sessions, sessionID)
		m.mu.Unlock()
		_ = active.stream.Close()
		m.publishClosed(sessionID, exitCode)
		close(active.closeDone)
	})
}

func (m *Manager) publishData(sessionID string, data []byte) {
	if m.events == nil || len(data) == 0 {
		return
	}
	m.events.Publish(bus.Event{
		Topic: bus.TopicTerminalData,
		TS:    m.now(),
		Payload: DataPayload{
			SessionID:  sessionID,
			DataBase64: base64.StdEncoding.EncodeToString(data),
		},
	})
}

func (m *Manager) publishClosed(sessionID string, exitCode int) {
	if m.events == nil {
		return
	}
	m.events.Publish(bus.Event{
		Topic: bus.TopicTerminalClosed,
		TS:    m.now(),
		Payload: ClosedPayload{
			SessionID: sessionID,
			ExitCode:  exitCode,
		},
	})
}

func (m *Manager) containerUser(ctx context.Context, containerID string, shell string, requested string) (bool, string) {
	user := strings.TrimSpace(requested)
	if user == "" {
		return false, ""
	}
	out, code, err := m.docker.RunContainerExec(ctx, containerID, dockercore.ExecOptions{
		Cmd:  shellCommand(shell, "id -u"),
		User: requested,
	})
	if err != nil || code != 0 {
		return user == "0" || user == "root", user
	}
	uid := strings.TrimSpace(out)
	return uid == "0", user
}

func shellCommand(shell string, command string) []string {
	if strings.Contains(filepath.Base(shell), "bash") {
		return []string{shell, "-lc", command}
	}
	return []string{shell, "-c", command}
}

func normalizeDimensions(cols int, rows int) (int, int) {
	if cols <= 0 {
		cols = defaultCols
	}
	if rows <= 0 {
		rows = defaultRows
	}
	return cols, rows
}

func shortID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}

func shellTitle(argv []string) string {
	if len(argv) == 0 {
		return "shell"
	}
	return filepath.Base(argv[0])
}

func currentUsername() string {
	if value := strings.TrimSpace(os.Getenv("USER")); value != "" {
		return value
	}
	if value := strings.TrimSpace(os.Getenv("USERNAME")); value != "" {
		return value
	}
	if current, err := currentOSUser(); err == nil && current != nil {
		if value := strings.TrimSpace(current.Username); value != "" {
			return value
		}
	}
	return ""
}

var currentOSUser = user.Current

func mapTerminalStartError(action string, err error) error {
	if err == nil {
		return nil
	}
	return apperror.Wrap(apperror.Internal, action+" failed", err, apperror.WithDetail(err.Error()))
}

func providerNotReady() error {
	return apperror.New(
		apperror.ProviderNotReady,
		"Provider is not ready",
		apperror.WithRepairHints("Connect a Docker provider from onboarding."),
	)
}
