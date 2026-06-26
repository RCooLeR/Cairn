package portforward

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/RCooLeR/Cairn/internal/bus"
	"github.com/RCooLeR/Cairn/internal/models"
)

// NewManager builds a port-forward manager. docker and dialer are required;
// when either is nil the manager is inert (Start is a no-op), which lets the
// app construct it uniformly for providers that do not support forwarding.
func NewManager(docker DockerLister, dialer Dialer, events bus.Bus, opts Options) *Manager {
	m := &Manager{
		docker:            docker,
		dialer:            dialer,
		events:            events,
		reconcileInterval: opts.ReconcileInterval,
		now:               opts.Now,
		listen:            opts.Listen,
		listenPacket:      opts.ListenPacket,
		enabled:           opts.Enabled,
		forwards:          map[string]*forward{},
	}
	if m.reconcileInterval <= 0 {
		m.reconcileInterval = defaultReconcileInterval
	}
	if m.now == nil {
		m.now = func() time.Time { return time.Now().UTC() }
	}
	if m.listen == nil {
		m.listen = func(ctx context.Context, network, address string) (net.Listener, error) {
			var lc net.ListenConfig
			return lc.Listen(ctx, network, address)
		}
	}
	if m.listenPacket == nil {
		m.listenPacket = func(ctx context.Context, network, address string) (net.PacketConn, error) {
			var lc net.ListenConfig
			return lc.ListenPacket(ctx, network, address)
		}
	}
	return m
}

// Start begins watching containers and binding host forwards. It is safe to
// call more than once; subsequent calls are no-ops until StopAll.
func (m *Manager) Start(ctx context.Context) {
	if m == nil || m.docker == nil || m.dialer == nil {
		return
	}
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return
	}
	m.ctx, m.cancel = context.WithCancel(ctx)
	m.started = true
	m.mu.Unlock()

	m.wg.Add(2)
	go m.reconcileLoop()
	go m.watchObjects()
}

// StopAll cancels the manager, closes every host listener and live relay
// connection, and blocks until all goroutines have returned.
func (m *Manager) StopAll() {
	if m == nil {
		return
	}
	m.mu.Lock()
	if !m.started {
		m.mu.Unlock()
		return
	}
	if m.cancel != nil {
		m.cancel()
	}
	forwards := make([]*forward, 0, len(m.forwards))
	for key, fwd := range m.forwards {
		delete(m.forwards, key)
		forwards = append(forwards, fwd)
	}
	m.started = false
	m.mu.Unlock()

	for _, fwd := range forwards {
		fwd.stop()
	}
	m.wg.Wait()
}

// Enabled reports whether auto-forwarding is currently on.
func (m *Manager) Enabled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.enabled
}

// SetEnabled toggles auto-forwarding. Turning it off tears down every forward;
// turning it on reconciles immediately against the running containers.
func (m *Manager) SetEnabled(enabled bool) {
	m.mu.Lock()
	if m.enabled == enabled {
		m.mu.Unlock()
		return
	}
	m.enabled = enabled
	ctx := m.ctx
	started := m.started
	m.mu.Unlock()
	if started && ctx != nil {
		m.reconcileOnce(ctx)
	}
}

// ListForwards returns the current forwards sorted by port then protocol.
func (m *Manager) ListForwards() []models.PortForward {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]models.PortForward, 0, len(m.forwards))
	for _, fwd := range m.forwards {
		out = append(out, models.PortForward{
			Protocol:      fwd.spec.protocol,
			HostPort:      fwd.spec.hostPort,
			BindAddr:      fwd.spec.bindAddr,
			ContainerID:   fwd.spec.containerID,
			ContainerName: fwd.spec.containerName,
			Status:        fwd.status,
			Reason:        fwd.reason,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].HostPort != out[j].HostPort {
			return out[i].HostPort < out[j].HostPort
		}
		return out[i].Protocol < out[j].Protocol
	})
	return out
}

func (m *Manager) reconcileLoop() {
	defer m.wg.Done()
	ticker := time.NewTicker(m.reconcileInterval)
	defer ticker.Stop()
	m.reconcileOnce(m.ctx)
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.reconcileOnce(m.ctx)
		}
	}
}

// watchObjects reconciles promptly when the container inventory changes instead
// of waiting for the next tick.
func (m *Manager) watchObjects() {
	defer m.wg.Done()
	if m.events == nil {
		return
	}
	ch := m.events.Subscribe(m.ctx, bus.TopicObjectsChanged, 16)
	for {
		select {
		case <-m.ctx.Done():
			return
		case _, ok := <-ch:
			if !ok {
				return
			}
			m.reconcileOnce(m.ctx)
		}
	}
}

// reconcileOnce diffs the desired forwards against the live ones, starting and
// stopping host listeners as containers come and go. It is serialized by
// reconcileMu so the ticker and the objects:changed watcher never race.
func (m *Manager) reconcileOnce(ctx context.Context) {
	if ctx == nil || ctx.Err() != nil {
		return
	}
	m.reconcileMu.Lock()
	defer m.reconcileMu.Unlock()

	m.mu.Lock()
	enabled := m.enabled
	current := make(map[string]*forward, len(m.forwards))
	for key, fwd := range m.forwards {
		current[key] = fwd
	}
	m.mu.Unlock()

	desired := map[string]spec{}
	if enabled {
		containers, err := m.docker.ListContainers(ctx, models.ContainerListOptions{All: false})
		if err != nil {
			// Leave existing forwards in place on a transient list failure so a
			// daemon blip does not tear down working forwards.
			return
		}
		desired = desiredForwards(containers)
	}

	changed := false
	for key, fwd := range current {
		if want, ok := desired[key]; ok && want == fwd.spec {
			continue
		}
		m.mu.Lock()
		delete(m.forwards, key)
		m.mu.Unlock()
		fwd.stop()
		changed = true
	}

	for key, want := range desired {
		if existing, ok := current[key]; ok && existing.spec == want {
			continue
		}
		fwd := m.startForward(ctx, want)
		m.mu.Lock()
		m.forwards[key] = fwd
		m.mu.Unlock()
		changed = true
	}

	if changed {
		m.publishChanged()
	}
}

func (m *Manager) startForward(ctx context.Context, s spec) *forward {
	fctx, cancel := context.WithCancel(ctx)
	fwd := &forward{spec: s, cancel: cancel, status: statusActive, closers: map[interface{ Close() error }]struct{}{}}
	address := net.JoinHostPort(s.bindAddr, strconv.Itoa(s.hostPort))

	if s.protocol == protoUDP {
		conn, err := m.listenPacket(fctx, "udp", address)
		if err != nil {
			fwd.fail(err)
			cancel()
			return fwd
		}
		fwd.track(conn)
		fwd.wg.Add(1)
		go m.serveUDP(fctx, fwd, conn)
		slog.Info("port forward bound", "protocol", protoUDP, "address", address, "container", s.containerName)
		return fwd
	}

	listener, err := m.listen(fctx, "tcp", address)
	if err != nil {
		fwd.fail(err)
		cancel()
		return fwd
	}
	fwd.track(listener)
	fwd.wg.Add(1)
	go m.serveTCP(fctx, fwd, listener)
	slog.Info("port forward bound", "protocol", protoTCP, "address", address, "container", s.containerName)
	return fwd
}

func (m *Manager) serveTCP(ctx context.Context, fwd *forward, listener net.Listener) {
	defer fwd.wg.Done()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				continue
			}
			return
		}
		fwd.track(conn)
		fwd.wg.Add(1)
		go func() {
			defer fwd.wg.Done()
			defer fwd.untrack(conn)
			m.relayTCP(ctx, fwd, conn)
		}()
	}
}

func (m *Manager) relayTCP(ctx context.Context, fwd *forward, client net.Conn) {
	backend, err := m.dialer.DialStream(ctx, fwd.spec.hostPort)
	if err != nil {
		_ = client.Close()
		return
	}
	fwd.track(backend)
	defer fwd.untrack(backend)
	relay(ctx, client, backend)
}

// relay copies bytes in both directions until either side closes or the context
// is cancelled, then closes both ends exactly once.
func relay(ctx context.Context, a, b net.Conn) {
	done := make(chan struct{})
	var once sync.Once
	closeBoth := func() {
		once.Do(func() {
			_ = a.Close()
			_ = b.Close()
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
		_, _ = io.Copy(b, a)
		closeBoth()
	}()
	go func() {
		defer copies.Done()
		_, _ = io.Copy(a, b)
		closeBoth()
	}()
	copies.Wait()
}

func (m *Manager) publishChanged() {
	if m.events == nil {
		return
	}
	m.events.Publish(bus.Event{Topic: bus.TopicPortForwardChanged, TS: m.now(), Payload: m.ListForwards()})
}

func (f *forward) fail(err error) {
	f.status = statusError
	f.reason = err.Error()
	slog.Warn("port forward could not bind host port",
		"protocol", f.spec.protocol, "port", f.spec.hostPort, "bind", f.spec.bindAddr, "error", err)
}

func (f *forward) track(closer interface{ Close() error }) {
	f.mu.Lock()
	f.closers[closer] = struct{}{}
	f.mu.Unlock()
}

func (f *forward) untrack(closer interface{ Close() error }) {
	f.mu.Lock()
	delete(f.closers, closer)
	f.mu.Unlock()
}

func (f *forward) stop() {
	if f.cancel != nil {
		f.cancel()
	}
	f.mu.Lock()
	closers := make([]interface{ Close() error }, 0, len(f.closers))
	for closer := range f.closers {
		closers = append(closers, closer)
	}
	f.closers = map[interface{ Close() error }]struct{}{}
	f.mu.Unlock()
	for _, closer := range closers {
		_ = closer.Close()
	}
	f.wg.Wait()
}
