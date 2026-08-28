package terminal

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"uuid"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/bus"
	dockercore "github.com/RCooLeR/Cairn/internal/docker"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
	"github.com/RCooLeR/Cairn/internal/runtimescope"
	"github.com/RCooLeR/Cairn/internal/store"
)

const (
	KindHost      = "host"
	KindBackend   = "backend"
	KindProject   = "project"
	KindContainer = "container"

	defaultCols        = 120
	defaultRows        = 30
	defaultMaxSessions = 16
	backendPathListSep = ":"
	maxTerminalWrite   = 1 << 20
	terminalWriteQueue = 32
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
	GetInScope(context.Context, runtimescope.Scope, string) (store.ProjectRecord, error)
}

type Options struct {
	MaxSessions int
	Now         func() time.Time
	PTYStarter  PTYStarter
	Scope       runtimescope.Scope
}

type Manager struct {
	provider Provider
	docker   DockerClient
	projects ProjectStore
	events   bus.Bus
	now      func() time.Time
	starter  PTYStarter
	max      int
	scope    runtimescope.Scope

	mu           sync.RWMutex
	sessions     map[string]*session
	reservations int
	stopped      bool
}

// sessionReservation accounts for terminal capacity before any child process,
// PTY, or container exec is created. Its active field is protected by
// Manager.mu.
type sessionReservation struct {
	manager *Manager
	active  bool
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
	writeQueue   chan terminalWriteRequest
	writerDone   chan struct{}
}

type terminalWriteRequest struct {
	ctx    context.Context
	data   []byte
	result chan error
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
		scope:    opts.Scope,
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
	reservation, err := m.reserveSession()
	if err != nil {
		return nil, err
	}
	defer m.releaseReservation(reservation)
	ptySession, err := m.starter.Start(ctx, PTYSpec{Argv: argv, Env: opts.Env, WorkingDir: cwd, Cols: opts.Cols, Rows: opts.Rows})
	if err != nil {
		closeAndWaitPTY(ptySession)
		return nil, mapTerminalStartError("open host terminal", err)
	}
	if ptySession == nil {
		return nil, apperror.New(apperror.Internal, "Open host terminal returned no PTY session")
	}
	info := models.TerminalSessionInfo{
		ID:         uuid.New().String(),
		Kind:       KindHost,
		Title:      "Host",
		Shell:      shellTitle(argv),
		User:       currentUsername(),
		WorkingDir: cwd,
		CreatedAt:  m.now(),
	}
	return m.addPTYSession(info, ptySession, reservation)
}

func (m *Manager) OpenBackendTerminal(ctx context.Context, opts models.TerminalOptions) (*models.TerminalSessionInfo, error) {
	if m.provider == nil {
		return nil, providerNotReady()
	}
	if err := m.validateRuntimeScope(ctx); err != nil {
		return nil, err
	}
	opts.Cols, opts.Rows = normalizeDimensions(opts.Cols, opts.Rows)
	argv, err := m.provider.BackendShellCommand(opts)
	if err != nil {
		return nil, err
	}
	cwd := opts.WorkingDir
	if cwd != "" {
		cwd, err = mapTerminalPathToBackend(m.provider, cwd, "terminal working directory")
		if err != nil {
			return nil, err
		}
	}
	return m.openProviderPTYTerminal(ctx, opts, argv, cwd, models.TerminalSessionInfo{
		Kind:  KindBackend,
		Title: m.provider.DisplayName(),
	})
}

func (m *Manager) OpenProjectTerminal(ctx context.Context, projectID string, opts models.TerminalOptions) (*models.TerminalSessionInfo, error) {
	if m.provider == nil || m.projects == nil || !m.scope.Valid() {
		return nil, providerNotReady()
	}
	if err := m.validateRuntimeScope(ctx); err != nil {
		return nil, err
	}
	project, err := m.projects.GetInScope(ctx, m.scope, projectID)
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
			path, err = mapTerminalPathToBackend(m.provider, path, "Compose file")
			if err != nil {
				return nil, err
			}
			mappedFiles = append(mappedFiles, path)
		}
		env["COMPOSE_FILE"] = strings.Join(mappedFiles, backendPathListSep)
	}
	maps.Copy(env, opts.Env)
	cwd := project.WorkingDir
	cwd, err = mapTerminalPathToBackend(m.provider, cwd, "project working directory")
	if err != nil {
		return nil, err
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

func (m *Manager) validateRuntimeScope(ctx context.Context) error {
	if !m.scope.Valid() {
		return providerNotReady()
	}
	provider, ok := m.provider.(providers.RuntimeScopeProvider)
	if !ok {
		return apperror.New(apperror.ProviderNotReady, "Terminal provider cannot verify runtime scope")
	}
	currentScope, err := providers.ResolveRuntimeScope(ctx, provider)
	if err != nil {
		return err
	}
	if !currentScope.Equal(m.scope) {
		return apperror.New(apperror.NotFound, "Terminal runtime context changed; reconnect the provider")
	}
	return nil
}

func (m *Manager) openProviderPTYTerminal(ctx context.Context, opts models.TerminalOptions, argv []string, cwd string, info models.TerminalSessionInfo) (*models.TerminalSessionInfo, error) {
	reservation, err := m.reserveSession()
	if err != nil {
		return nil, err
	}
	defer m.releaseReservation(reservation)
	ptySession, err := m.starter.Start(ctx, PTYSpec{Argv: argv, Env: opts.Env, WorkingDir: cwd, Cols: opts.Cols, Rows: opts.Rows})
	if err != nil {
		closeAndWaitPTY(ptySession)
		return nil, mapTerminalStartError("open backend terminal", err)
	}
	if ptySession == nil {
		return nil, apperror.New(apperror.Internal, "Open backend terminal returned no PTY session")
	}
	info.ID = uuid.New().String()
	info.Shell = shellTitle(argv)
	info.User = currentUsername()
	info.WorkingDir = cwd
	info.CreatedAt = m.now()
	return m.addPTYSession(info, ptySession, reservation)
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
	reservation, err := m.reserveSession()
	if err != nil {
		return nil, err
	}
	defer m.releaseReservation(reservation)
	shell := strings.TrimSpace(opts.Shell)
	if shell == "" {
		shells, err := m.docker.DetectContainerShells(ctx, containerID)
		if err != nil {
			return nil, err
		}
		if len(shells) == 0 {
			return nil, apperror.New(
				apperror.NotFound,
				"No interactive shell was found in this container",
				apperror.WithRepairHints("Install sh/bash in the image or specify a shell path manually."),
			)
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
		if execSession != nil {
			_ = execSession.Close()
		}
		return nil, err
	}
	if execSession == nil {
		return nil, apperror.New(apperror.Internal, "Open container terminal returned no exec session")
	}
	title := detail.Summary.Name
	if title == "" {
		title = shortID(containerID)
	}
	info := models.TerminalSessionInfo{
		ID:          uuid.New().String(),
		Kind:        KindContainer,
		Title:       title,
		Shell:       shell,
		User:        userLabel,
		WorkingDir:  opts.WorkingDir,
		ContainerID: detail.Summary.ID,
		IsRoot:      isRoot,
		CreatedAt:   m.now(),
	}
	closeCtx := terminalCloseContext(ctx)
	active := &session{
		info:      info,
		stream:    execSession,
		closeDone: make(chan struct{}),
		resize: func(cols int, rows int) error {
			return m.docker.ResizeContainerExec(closeCtx, execSession.ID, cols, rows)
		},
		inspectExit: func(ctx context.Context) int {
			inspect, err := m.docker.InspectContainerExec(ctx, execSession.ID)
			if err != nil {
				return -1
			}
			return inspect.ExitCode
		},
		closeContext: closeCtx,
	}
	if err := m.registerReserved(active, reservation); err != nil {
		_ = execSession.Close()
		return nil, err
	}
	m.startSessionWriter(active)
	go m.pump(active)
	return &info, nil
}

func (m *Manager) DetectContainerShells(ctx context.Context, containerID string) ([]string, error) {
	if m.docker == nil {
		return nil, providerNotReady()
	}
	return m.docker.DetectContainerShells(ctx, containerID)
}

func (m *Manager) WriteTerminal(ctx context.Context, sessionID string, data []byte) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return terminalWriteCanceled(err)
	}
	if len(data) > maxTerminalWrite {
		return apperror.New(
			apperror.Conflict,
			"Terminal input is too large",
			apperror.WithDetail(fmt.Sprintf("A single terminal write may contain at most %d bytes.", maxTerminalWrite)),
		)
	}
	active, err := m.lookup(sessionID)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}
	request := terminalWriteRequest{
		ctx:    ctx,
		data:   append([]byte(nil), data...),
		result: make(chan error, 1),
	}
	select {
	case active.writeQueue <- request:
	case <-ctx.Done():
		return terminalWriteCanceled(ctx.Err())
	case <-active.closeDone:
		return apperror.New(apperror.NotFound, "Terminal session not found")
	}
	select {
	case err := <-request.result:
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return terminalWriteCanceled(ctxErr)
			}
			return apperror.Wrap(apperror.Internal, "Write to terminal failed", err)
		}
		return nil
	case <-ctx.Done():
		return terminalWriteCanceled(ctx.Err())
	case <-active.closeDone:
		return apperror.New(apperror.NotFound, "Terminal session not found")
	}
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

func (m *Manager) Diagnostics() models.TerminalRuntimeDiagnostics {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return models.TerminalRuntimeDiagnostics{ActiveSessions: len(m.sessions)}
}

func (m *Manager) StopAll() {
	ids := make([]string, 0)
	m.mu.Lock()
	m.stopped = true
	for id := range m.sessions {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	var stopping sync.WaitGroup
	for _, id := range ids {
		stopping.Go(func() {
			m.finish(id, -1)
		})
	}
	stopping.Wait()
}

func (m *Manager) addPTYSession(info models.TerminalSessionInfo, ptySession PTYSession, reservation *sessionReservation) (*models.TerminalSessionInfo, error) {
	active := &session{
		info:      info,
		stream:    ptySession,
		closeDone: make(chan struct{}),
		resize:    ptySession.Resize,
		wait:      ptySession.Wait,
	}
	if err := m.registerReserved(active, reservation); err != nil {
		closeAndWaitPTY(ptySession)
		return nil, err
	}
	m.startSessionWriter(active)
	go m.pump(active)
	go func() {
		exitCode := ptySession.Wait()
		m.finish(info.ID, exitCode)
	}()
	return &info, nil
}

func (m *Manager) reserveSession() (*sessionReservation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped {
		return nil, providerNotReady()
	}
	if len(m.sessions)+m.reservations >= m.max {
		return nil, apperror.New(
			apperror.Conflict,
			"Terminal session limit reached",
			apperror.WithDetail(fmt.Sprintf("Cairn allows up to %d active terminal sessions.", m.max)),
			apperror.WithRepairHints("Close an existing terminal tab and try again."),
		)
	}
	m.reservations++
	return &sessionReservation{manager: m, active: true}, nil
}

func (m *Manager) releaseReservation(reservation *sessionReservation) {
	if reservation == nil || reservation.manager != m {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !reservation.active {
		return
	}
	reservation.active = false
	m.reservations--
}

func (m *Manager) registerReserved(active *session, reservation *sessionReservation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if reservation == nil || reservation.manager != m || !reservation.active {
		return apperror.New(apperror.Internal, "Terminal capacity reservation is invalid")
	}
	reservation.active = false
	m.reservations--
	if m.stopped {
		return providerNotReady()
	}
	if _, exists := m.sessions[active.info.ID]; exists {
		return apperror.New(apperror.Conflict, "Terminal session already exists")
	}
	m.sessions[active.info.ID] = active
	return nil
}

func closeAndWaitPTY(ptySession PTYSession) {
	if ptySession == nil {
		return
	}
	_ = ptySession.Close()
	ptySession.Wait()
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

func (m *Manager) startSessionWriter(active *session) {
	active.writeQueue = make(chan terminalWriteRequest, terminalWriteQueue)
	active.writerDone = make(chan struct{})
	go func() {
		defer close(active.writerDone)
		for {
			select {
			case <-active.closeDone:
				return
			case request := <-active.writeQueue:
				select {
				case <-active.closeDone:
					request.result <- io.ErrClosedPipe
					continue
				default:
				}
				if err := request.ctx.Err(); err != nil {
					request.result <- err
					continue
				}
				written, err := active.stream.Write(request.data)
				if err == nil && written != len(request.data) {
					err = io.ErrShortWrite
				}
				request.result <- err
			}
		}
	}()
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
		close(active.closeDone)
		_ = active.stream.Close()
		if active.writerDone != nil {
			<-active.writerDone
		}
		m.publishClosed(sessionID, exitCode)
	})
}

func terminalWriteCanceled(cause error) error {
	return apperror.Wrap(apperror.Cancelled, "Write to terminal canceled", cause)
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
	event := bus.Event{
		Topic: bus.TopicTerminalClosed,
		TS:    m.now(),
		Payload: ClosedPayload{
			SessionID: sessionID,
			ExitCode:  exitCode,
		},
	}
	if err := bus.PublishCriticalBounded(m.events, event); err != nil {
		slog.Warn("publish terminal closed event failed", "session", sessionID, "error", err)
	}
}

func (m *Manager) containerUser(ctx context.Context, containerID string, shell string, requested string) (bool, string) {
	user := strings.TrimSpace(requested)
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

func mapTerminalPathToBackend(provider Provider, path string, purpose string) (string, error) {
	mapped, err := provider.MapPathToBackend(path)
	if err != nil {
		return "", apperror.Wrap(
			apperror.WorkdirMissing,
			"Map "+purpose+" to Docker backend failed",
			err,
			apperror.WithDetail(path),
			apperror.WithRepairHints("Use a path available to the active Docker provider."),
		)
	}
	mapped = strings.TrimSpace(mapped)
	if mapped == "" {
		return "", apperror.New(
			apperror.WorkdirMissing,
			"Map "+purpose+" to Docker backend returned an empty path",
			apperror.WithDetail(path),
			apperror.WithRepairHints("Use a path available to the active Docker provider."),
		)
	}
	return mapped, nil
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
