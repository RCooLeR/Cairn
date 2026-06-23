package dockerbridge

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
)

const (
	defaultDockerSocket = "/var/run/docker.sock"
)

type Provider interface {
	ID() string
	DockerHost(context.Context) (string, error)
}

type DialerProvider interface {
	DockerDialContext(context.Context) (func(context.Context, string, string) (net.Conn, error), error)
}

type Options struct {
	Pipes []string
}

type Manager struct {
	provider Provider
	options  Options

	mu        sync.Mutex
	ctx       context.Context
	cancel    context.CancelFunc
	listeners []net.Listener
	conns     map[net.Conn]struct{}
	wg        sync.WaitGroup
}

func New(provider Provider, options Options) *Manager {
	return &Manager{
		provider: provider,
		options:  options,
		conns:    map[net.Conn]struct{}{},
	}
}

func (m *Manager) Start(ctx context.Context) error {
	if !supported {
		return nil
	}
	if m == nil || m.provider == nil {
		return nil
	}
	dialerProvider, ok := m.provider.(DialerProvider)
	if !ok {
		return nil
	}
	host, err := m.provider.DockerHost(ctx)
	if err != nil || !strings.HasPrefix(host, "unix://") {
		return err
	}
	dialContext, err := dialerProvider.DockerDialContext(ctx)
	if err != nil {
		return err
	}

	m.mu.Lock()
	if m.cancel != nil {
		m.mu.Unlock()
		return nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	m.ctx = runCtx
	m.cancel = cancel
	m.mu.Unlock()

	pipes := m.options.Pipes
	if len(pipes) == 0 {
		pipes = defaultPipes()
	}
	var listenErrs []error
	for _, pipe := range uniqueStrings(pipes) {
		listener, err := listenPipe(pipe)
		if err != nil {
			listenErrs = append(listenErrs, err)
			continue
		}
		m.mu.Lock()
		m.listeners = append(m.listeners, listener)
		m.mu.Unlock()
		m.wg.Add(1)
		go m.serve(runCtx, listener, dialContext)
	}
	if len(m.listeners) > 0 {
		return nil
	}
	cancel()
	m.mu.Lock()
	m.cancel = nil
	m.ctx = nil
	m.mu.Unlock()
	if len(listenErrs) > 0 {
		return errors.Join(listenErrs...)
	}
	return nil
}

func (m *Manager) Stop() {
	if m == nil {
		return
	}
	m.mu.Lock()
	cancel := m.cancel
	listeners := append([]net.Listener(nil), m.listeners...)
	conns := make([]net.Conn, 0, len(m.conns))
	for conn := range m.conns {
		conns = append(conns, conn)
	}
	m.cancel = nil
	m.ctx = nil
	m.listeners = nil
	m.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	for _, listener := range listeners {
		_ = listener.Close()
	}
	for _, conn := range conns {
		_ = conn.Close()
	}
	m.wg.Wait()
}

func (m *Manager) serve(ctx context.Context, listener net.Listener, dialContext func(context.Context, string, string) (net.Conn, error)) {
	defer m.wg.Done()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if temporary, ok := err.(interface{ Temporary() bool }); ok && temporary.Temporary() {
				continue
			}
			return
		}
		m.trackConn(conn)
		m.wg.Add(1)
		go m.handle(ctx, conn, dialContext)
	}
}

func (m *Manager) handle(ctx context.Context, client net.Conn, dialContext func(context.Context, string, string) (net.Conn, error)) {
	defer m.wg.Done()
	defer m.untrackConn(client)

	backend, err := dialContext(ctx, "unix", defaultDockerSocket)
	if err != nil {
		_ = client.Close()
		return
	}
	defer func() { _ = backend.Close() }()

	done := make(chan struct{})
	var once sync.Once
	closeBoth := func() {
		once.Do(func() {
			_ = client.Close()
			_ = backend.Close()
			close(done)
		})
	}

	go func() {
		select {
		case <-ctx.Done():
			closeBoth()
		case <-done:
		}
	}()

	var copies sync.WaitGroup
	copies.Add(2)
	go func() {
		defer copies.Done()
		_, _ = io.Copy(backend, client)
		closeBoth()
	}()
	go func() {
		defer copies.Done()
		_, _ = io.Copy(client, backend)
		closeBoth()
	}()
	copies.Wait()
}

func (m *Manager) trackConn(conn net.Conn) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.conns[conn] = struct{}{}
}

func (m *Manager) untrackConn(conn net.Conn) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.conns, conn)
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
