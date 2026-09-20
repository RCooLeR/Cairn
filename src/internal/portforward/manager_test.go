package portforward

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/bus"
	"github.com/RCooLeR/Cairn/internal/models"
)

func TestBindAddrForMirrorsPublishInterface(t *testing.T) {
	t.Parallel()
	cases := []struct {
		hostIP string
		want   string
	}{
		{"", "0.0.0.0"},
		{"0.0.0.0", "0.0.0.0"},
		{"::", "::"},
		{"127.0.0.1", "127.0.0.1"},
		{"::1", "::1"},
		{"192.168.1.50", "192.168.1.50"},
		{"not-an-ip", "not-an-ip"},
	}
	for _, tc := range cases {
		if got := bindAddrFor(tc.hostIP); got != tc.want {
			t.Errorf("bindAddrFor(%q) = %q, want %q", tc.hostIP, got, tc.want)
		}
	}
}

func TestDesiredForwardsKeepsPublishedBindsAndSkipsUnpublished(t *testing.T) {
	t.Parallel()
	containers := []models.ContainerSummary{
		{
			ID:   "a",
			Name: "web",
			Ports: []models.PortBinding{
				{HostIP: "127.0.0.1", HostPort: "8080", ContainerPort: "80", Protocol: "tcp"},
				{HostIP: "0.0.0.0", HostPort: "8080", ContainerPort: "80", Protocol: "tcp"},
				{HostIP: "0.0.0.0", HostPort: "53", ContainerPort: "53", Protocol: "udp"},
				{ContainerPort: "9000", Protocol: "tcp"}, // unpublished (no host port)
			},
		},
	}
	got := desiredForwards(containers)
	if len(got) != 3 {
		t.Fatalf("desiredForwards size = %d, want two supported and one visible unsupported row (%+v)", len(got), got)
	}
	tcp, ok := got[forwardKey("tcp", "0.0.0.0", 8080)]
	if !ok || tcp.bindAddr != "0.0.0.0" {
		t.Fatalf("tcp 8080 = %+v, want bind 0.0.0.0 (broadest wins)", tcp)
	}
	udp, ok := got[forwardKey("udp", "0.0.0.0", 53)]
	if !ok || udp.bindAddr != "0.0.0.0" || udp.protocol != "udp" {
		t.Fatalf("udp 53 = %+v, want udp/0.0.0.0", udp)
	}
	loopback := got[forwardKey("tcp", "127.0.0.1", 8080)]
	if loopback.blockedReason == "" || loopback.containerID != "a" {
		t.Fatalf("loopback diagnostic = %+v, want visible target-bound error", loopback)
	}
}

func TestDesiredForwardsSurfacesUnsupportedLoopbackAndIPv6Binds(t *testing.T) {
	t.Parallel()
	containers := []models.ContainerSummary{{
		ID:   "a",
		Name: "web",
		Ports: []models.PortBinding{
			{HostIP: "127.0.0.1", HostPort: "8080", ContainerPort: "80", Protocol: "tcp"},
			{HostIP: "::1", HostPort: "8081", ContainerPort: "80", Protocol: "tcp"},
			{HostIP: "fe80::1", HostPort: "8082", ContainerPort: "80", Protocol: "tcp"},
			{HostIP: "192.168.1.50", HostPort: "8083", ContainerPort: "80", Protocol: "tcp"},
			{HostIP: "::", HostPort: "8084", ContainerPort: "80", Protocol: "tcp"},
			{HostIP: "not-an-ip", HostPort: "8085", ContainerPort: "80", Protocol: "tcp"},
		},
	}}

	got := desiredForwards(containers)
	if len(got) != 6 {
		t.Fatalf("desiredForwards size = %d, want one supported and five visible unsupported rows (%+v)", len(got), got)
	}
	if fwd, ok := got[forwardKey("tcp", "192.168.1.50", 8083)]; !ok || fwd.bindAddr != "192.168.1.50" {
		t.Fatalf("tcp 8083 = %+v, want mirrored IPv4 bind", fwd)
	}
	unsupported := []string{
		forwardKey("tcp", "127.0.0.1", 8080),
		forwardKey("tcp", "::1", 8081),
		forwardKey("tcp", "fe80::1", 8082),
		forwardKey("tcp", "::", 8084),
		forwardKey("tcp", "not-an-ip", 8085),
	}
	for _, key := range unsupported {
		forward, ok := got[key]
		if !ok || forward.blockedReason == "" || forward.containerID != "a" {
			t.Errorf("unsupported forward %q = %+v/%t, want visible target-bound error", key, forward, ok)
		}
	}
}

func TestDesiredForwardsKeepsDistinctConcreteBindsAndRejectsExactConflict(t *testing.T) {
	t.Parallel()
	containers := []models.ContainerSummary{
		{ID: "a", Name: "first", Ports: []models.PortBinding{{HostIP: "192.168.1.50", HostPort: "8080", Protocol: "tcp"}}},
		{ID: "b", Name: "second", Ports: []models.PortBinding{{HostIP: "192.168.1.51", HostPort: "8080", Protocol: "tcp"}}},
		{ID: "c", Name: "conflict", Ports: []models.PortBinding{{HostIP: "192.168.1.50", HostPort: "8080", Protocol: "tcp"}}},
	}

	got := desiredForwards(containers)
	if len(got) != 2 {
		t.Fatalf("desiredForwards size = %d, want two address-specific listeners (%+v)", len(got), got)
	}
	conflict := got[forwardKey("tcp", "192.168.1.50", 8080)]
	if conflict.blockedReason == "" || conflict.containerID != "" {
		t.Fatalf("exact listener collision = %+v, want targetless visible conflict", conflict)
	}
	independent := got[forwardKey("tcp", "192.168.1.51", 8080)]
	if independent.containerID != "b" || independent.blockedReason != "" {
		t.Fatalf("independent listener = %+v, want container b", independent)
	}
}

func TestDesiredForwardsRejectsWildcardConcreteListenerOverlap(t *testing.T) {
	t.Parallel()
	containers := []models.ContainerSummary{
		{ID: "a", Name: "wildcard", Ports: []models.PortBinding{{HostIP: "0.0.0.0", HostPort: "8080", Protocol: "tcp"}}},
		{ID: "b", Name: "concrete", Ports: []models.PortBinding{{HostIP: "192.168.1.51", HostPort: "8080", Protocol: "tcp"}}},
	}

	got := desiredForwards(containers)
	for key, forward := range got {
		if forward.blockedReason == "" || forward.containerID != "" {
			t.Fatalf("overlapping listener %q = %+v, want targetless visible conflict", key, forward)
		}
	}
}

func TestIsConnResetRecognizesWrappedErrno(t *testing.T) {
	t.Parallel()
	if !isConnReset(syscall.ECONNRESET) {
		t.Fatal("ECONNRESET was not recognized")
	}
	if !isConnReset(&net.OpError{Op: "read", Err: syscall.Errno(10054)}) {
		t.Fatal("wrapped WSAECONNRESET was not recognized")
	}
	if isConnReset(errors.New("permission denied")) {
		t.Fatal("non-reset error was misclassified")
	}
}

func TestManagerForwardsTCPEndToEnd(t *testing.T) {
	t.Parallel()
	listenerCh := make(chan net.Listener, 1)
	manager := newTestManager(t, fakeListerWithPort("18080", "tcp"), &echoDialer{}, Options{
		Enabled:           true,
		ReconcileInterval: 20 * time.Millisecond,
		Listen:            capturingListen(listenerCh),
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager.Start(ctx)
	t.Cleanup(manager.StopAll)

	listener := awaitListener(t, listenerCh)
	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("dial host forward: %v", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := readN(t, conn, 4)
	if string(got) != "ping" {
		t.Fatalf("echo = %q, want %q", got, "ping")
	}
}

func TestManagerForwardsUDPEndToEnd(t *testing.T) {
	t.Parallel()
	echo := startUDPEcho(t)
	packetCh := make(chan net.PacketConn, 1)
	manager := newTestManager(t, fakeListerWithPort("15353", "udp"), &udpDialer{target: echo.LocalAddr().String()}, Options{
		Enabled:           true,
		ReconcileInterval: 20 * time.Millisecond,
		ListenPacket:      capturingListenPacket(packetCh),
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager.Start(ctx)
	t.Cleanup(manager.StopAll)

	host := awaitPacketConn(t, packetCh)
	client, err := net.Dial("udp", host.LocalAddr().String())
	if err != nil {
		t.Fatalf("dial udp host forward: %v", err)
	}
	defer func() { _ = client.Close() }()

	if _, err := client.Write([]byte("pong")); err != nil {
		t.Fatalf("udp write: %v", err)
	}
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 16)
	n, err := client.Read(buf)
	if err != nil {
		t.Fatalf("udp read: %v", err)
	}
	if string(buf[:n]) != "pong" {
		t.Fatalf("udp echo = %q, want %q", buf[:n], "pong")
	}
}

func TestManagerReplacesFailedUDPBackendSessionImmediately(t *testing.T) {
	t.Parallel()
	packetCh := make(chan net.PacketConn, 1)
	dialer := &controlledPacketDialer{created: make(chan *controlledPacketConn, 2)}
	manager := newTestManager(t, fakeListerWithPort("15354", "udp"), dialer, Options{
		Enabled:           true,
		ReconcileInterval: time.Hour,
		ListenPacket:      capturingListenPacket(packetCh),
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager.Start(ctx)
	t.Cleanup(manager.StopAll)

	host := awaitPacketConn(t, packetCh)
	client, err := net.Dial("udp", host.LocalAddr().String())
	if err != nil {
		t.Fatalf("dial udp host forward: %v", err)
	}
	defer func() { _ = client.Close() }()

	if _, err := client.Write([]byte("first")); err != nil {
		t.Fatalf("write first datagram: %v", err)
	}
	first := awaitControlledPacketConn(t, dialer.created)
	if got := awaitControlledPacketWrite(t, first.writes); string(got) != "first" {
		t.Fatalf("first backend payload = %q, want first", got)
	}
	first.fail(errors.New("backend read failed"))
	select {
	case <-first.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("failed backend session was not evicted and closed")
	}

	if _, err := client.Write([]byte("second")); err != nil {
		t.Fatalf("write second datagram: %v", err)
	}
	second := awaitControlledPacketConn(t, dialer.created)
	if second == first {
		t.Fatal("same failed backend session was reused")
	}
	if got := awaitControlledPacketWrite(t, second.writes); string(got) != "second" {
		t.Fatalf("replacement backend payload = %q, want second", got)
	}
}

func TestManagerReportsBindConflict(t *testing.T) {
	t.Parallel()
	failingListen := func(context.Context, string, string) (net.Listener, error) {
		return nil, errors.New("address already in use")
	}
	manager := newTestManager(t, fakeListerWithPort("18081", "tcp"), &echoDialer{}, Options{
		Enabled:           true,
		ReconcileInterval: 20 * time.Millisecond,
		Listen:            failingListen,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager.Start(ctx)
	t.Cleanup(manager.StopAll)

	forward := awaitForward(t, manager, func(forwards []models.PortForward) bool {
		return len(forwards) == 1 && forwards[0].Status == statusError
	})
	if forward.Reason == "" || forward.HostPort != 18081 {
		t.Fatalf("conflict forward = %+v", forward)
	}
}

func TestManagerSurfacesAmbiguousTargetWithoutBinding(t *testing.T) {
	t.Parallel()
	lister := &fakeLister{containers: []models.ContainerSummary{
		{ID: "a", Name: "first", Ports: []models.PortBinding{{HostIP: "192.168.1.50", HostPort: "18082", Protocol: "tcp"}}},
		{ID: "b", Name: "second", Ports: []models.PortBinding{{HostIP: "192.168.1.50", HostPort: "18082", Protocol: "tcp"}}},
	}}
	var mu sync.Mutex
	listenCalls := 0
	manager := newTestManager(t, lister, &echoDialer{}, Options{
		Enabled:           true,
		ReconcileInterval: time.Hour,
		Listen: func(context.Context, string, string) (net.Listener, error) {
			mu.Lock()
			listenCalls++
			mu.Unlock()
			return nil, errors.New("listener must not be attempted for an ambiguous target")
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager.Start(ctx)
	t.Cleanup(manager.StopAll)

	reported := awaitForward(t, manager, func(forwards []models.PortForward) bool {
		return len(forwards) == 1 && forwards[0].Status == statusError
	})
	if !strings.Contains(reported.Reason, "will not choose") || reported.ContainerID != "" {
		t.Fatalf("ambiguous forward = %+v, want targetless conflict", reported)
	}
	manager.mu.Lock()
	var before *forward
	for _, active := range manager.forwards {
		before = active
	}
	manager.mu.Unlock()
	manager.reconcileOnce(ctx)
	manager.mu.Lock()
	var after *forward
	for _, active := range manager.forwards {
		after = active
	}
	manager.mu.Unlock()
	if before == nil || after != before {
		t.Fatal("deterministic policy conflict was needlessly recreated during reconciliation")
	}
	mu.Lock()
	gotListenCalls := listenCalls
	mu.Unlock()
	if gotListenCalls != 0 {
		t.Fatalf("listen calls = %d, want zero for ambiguous target", gotListenCalls)
	}
}

func TestManagerRetriesErrorForwardOnNextReconcile(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	calls := 0
	listenerCh := make(chan net.Listener, 1)
	flakyListen := func(ctx context.Context, network, address string) (net.Listener, error) {
		mu.Lock()
		calls++
		call := calls
		mu.Unlock()
		if call == 1 {
			return nil, errors.New("address already in use")
		}
		return capturingListen(listenerCh)(ctx, network, address)
	}
	manager := newTestManager(t, fakeListerWithPort("18084", "tcp"), &echoDialer{}, Options{
		Enabled:           true,
		ReconcileInterval: 20 * time.Millisecond,
		Listen:            flakyListen,
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager.Start(ctx)
	t.Cleanup(manager.StopAll)

	awaitListener(t, listenerCh)
	awaitForward(t, manager, func(forwards []models.PortForward) bool {
		mu.Lock()
		gotCalls := calls
		mu.Unlock()
		return gotCalls >= 2 && len(forwards) == 1 && forwards[0].Status == statusActive
	})
}

func TestManagerMarksAcceptLoopFailure(t *testing.T) {
	t.Parallel()
	manager := newTestManager(t, fakeListerWithPort("18085", "tcp"), &echoDialer{}, Options{
		Enabled:           true,
		ReconcileInterval: time.Hour,
		Listen: func(context.Context, string, string) (net.Listener, error) {
			return errorAcceptListener{}, nil
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager.Start(ctx)
	t.Cleanup(manager.StopAll)

	forward := awaitForward(t, manager, func(forwards []models.PortForward) bool {
		return len(forwards) == 1 && forwards[0].Status == statusError
	})
	if !strings.Contains(forward.Reason, "accept failed") {
		t.Fatalf("forward reason = %q, want accept failure", forward.Reason)
	}
}

func TestManagerStopsForwardWhenContainerRemoved(t *testing.T) {
	t.Parallel()
	lister := fakeListerWithPort("18082", "tcp")
	listenerCh := make(chan net.Listener, 1)
	manager := newTestManager(t, lister, &echoDialer{}, Options{
		Enabled:           true,
		ReconcileInterval: 20 * time.Millisecond,
		Listen:            capturingListen(listenerCh),
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager.Start(ctx)
	t.Cleanup(manager.StopAll)

	listener := awaitListener(t, listenerCh)
	awaitForward(t, manager, func(forwards []models.PortForward) bool { return len(forwards) == 1 })

	// Container goes away -> forward must be torn down and its listener closed.
	lister.set(nil)
	awaitForward(t, manager, func(forwards []models.PortForward) bool { return len(forwards) == 0 })

	if _, err := listener.Accept(); err == nil {
		t.Fatal("listener should be closed after the forward is removed")
	}
}

func TestManagerSetEnabledTogglesForwards(t *testing.T) {
	t.Parallel()
	listenerCh := make(chan net.Listener, 8)
	manager := newTestManager(t, fakeListerWithPort("18083", "tcp"), &echoDialer{}, Options{
		Enabled:           false,
		ReconcileInterval: time.Hour, // rely on SetEnabled, not the ticker
		Listen:            capturingListen(listenerCh),
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager.Start(ctx)
	t.Cleanup(manager.StopAll)

	awaitForward(t, manager, func(forwards []models.PortForward) bool { return len(forwards) == 0 })

	manager.SetEnabled(true)
	awaitForward(t, manager, func(forwards []models.PortForward) bool { return len(forwards) == 1 })

	manager.SetEnabled(false)
	awaitForward(t, manager, func(forwards []models.PortForward) bool { return len(forwards) == 0 })
}

// --- test helpers ---

func newTestManager(t *testing.T, lister DockerLister, dialer Dialer, opts Options) *Manager {
	t.Helper()
	return NewManager(lister, dialer, bus.New(), opts)
}

type fakeLister struct {
	mu         sync.Mutex
	containers []models.ContainerSummary
}

func fakeListerWithPort(hostPort, protocol string) *fakeLister {
	return &fakeLister{containers: []models.ContainerSummary{{
		ID:    "c1",
		Name:  "svc",
		Ports: []models.PortBinding{{HostIP: "0.0.0.0", HostPort: hostPort, ContainerPort: "80", Protocol: protocol}},
	}}}
}

func (f *fakeLister) set(containers []models.ContainerSummary) {
	f.mu.Lock()
	f.containers = containers
	f.mu.Unlock()
}

func (f *fakeLister) ListContainers(context.Context, models.ContainerListOptions) ([]models.ContainerSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]models.ContainerSummary(nil), f.containers...), nil
}

// echoDialer relays TCP into an in-memory echo via net.Pipe.
type echoDialer struct{}

func (*echoDialer) DialStream(context.Context, int) (net.Conn, error) {
	client, server := net.Pipe()
	go func() { _, _ = io.Copy(server, server) }()
	return client, nil
}

func (*echoDialer) DialPacket(context.Context, int) (net.Conn, error) {
	return nil, errors.New("not supported")
}

// udpDialer dials a real UDP echo server for the UDP relay path.
type udpDialer struct{ target string }

func (*udpDialer) DialStream(context.Context, int) (net.Conn, error) {
	return nil, errors.New("not supported")
}

func (d *udpDialer) DialPacket(ctx context.Context, _ int) (net.Conn, error) {
	var dialer net.Dialer
	return dialer.DialContext(ctx, "udp", d.target)
}

type controlledPacketDialer struct {
	created chan *controlledPacketConn
}

func (*controlledPacketDialer) DialStream(context.Context, int) (net.Conn, error) {
	return nil, errors.New("not supported")
}

func (d *controlledPacketDialer) DialPacket(context.Context, int) (net.Conn, error) {
	conn := &controlledPacketConn{
		failRead: make(chan error, 1),
		closed:   make(chan struct{}),
		writes:   make(chan []byte, 2),
	}
	d.created <- conn
	return conn, nil
}

type controlledPacketConn struct {
	failRead chan error
	closed   chan struct{}
	writes   chan []byte
	close    sync.Once
}

func (c *controlledPacketConn) Read([]byte) (int, error) {
	select {
	case err := <-c.failRead:
		return 0, err
	case <-c.closed:
		return 0, net.ErrClosed
	}
}

func (c *controlledPacketConn) Write(payload []byte) (int, error) {
	select {
	case <-c.closed:
		return 0, net.ErrClosed
	default:
	}
	copyOfPayload := append([]byte(nil), payload...)
	select {
	case c.writes <- copyOfPayload:
		return len(payload), nil
	case <-c.closed:
		return 0, net.ErrClosed
	}
}

func (c *controlledPacketConn) Close() error {
	c.close.Do(func() { close(c.closed) })
	return nil
}

func (*controlledPacketConn) LocalAddr() net.Addr              { return fakeAddr("backend-local") }
func (*controlledPacketConn) RemoteAddr() net.Addr             { return fakeAddr("backend-remote") }
func (*controlledPacketConn) SetDeadline(time.Time) error      { return nil }
func (*controlledPacketConn) SetReadDeadline(time.Time) error  { return nil }
func (*controlledPacketConn) SetWriteDeadline(time.Time) error { return nil }
func (c *controlledPacketConn) fail(err error)                 { c.failRead <- err }

func awaitControlledPacketConn(t *testing.T, created <-chan *controlledPacketConn) *controlledPacketConn {
	t.Helper()
	select {
	case conn := <-created:
		return conn
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for UDP backend session")
		return nil
	}
}

func awaitControlledPacketWrite(t *testing.T, writes <-chan []byte) []byte {
	t.Helper()
	select {
	case payload := <-writes:
		return payload
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for relayed UDP datagram")
		return nil
	}
}

type errorAcceptListener struct{}

func (errorAcceptListener) Accept() (net.Conn, error) { return nil, errors.New("accept failed") }
func (errorAcceptListener) Close() error              { return nil }
func (errorAcceptListener) Addr() net.Addr            { return fakeAddr("127.0.0.1:0") }

type fakeAddr string

func (a fakeAddr) Network() string { return "tcp" }
func (a fakeAddr) String() string  { return string(a) }

func capturingListen(ch chan<- net.Listener) listenFunc {
	return func(context.Context, string, string) (net.Listener, error) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		ch <- listener
		return listener, nil
	}
}

func capturingListenPacket(ch chan<- net.PacketConn) listenPacketFunc {
	return func(context.Context, string, string) (net.PacketConn, error) {
		conn, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		ch <- conn
		return conn, nil
	}
}

func startUDPEcho(t *testing.T) *net.UDPConn {
	t.Helper()
	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("resolve udp echo: %v", err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		t.Fatalf("listen udp echo: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	go func() {
		buf := make([]byte, 2048)
		for {
			n, src, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			_, _ = conn.WriteToUDP(buf[:n], src)
		}
	}()
	return conn
}

func awaitListener(t *testing.T, ch <-chan net.Listener) net.Listener {
	t.Helper()
	select {
	case listener := <-ch:
		return listener
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for host listener")
		return nil
	}
}

func awaitPacketConn(t *testing.T, ch <-chan net.PacketConn) net.PacketConn {
	t.Helper()
	select {
	case conn := <-ch:
		return conn
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for host packet conn")
		return nil
	}
}

func awaitForward(t *testing.T, manager *Manager, predicate func([]models.PortForward) bool) models.PortForward {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		forwards := manager.ListForwards()
		if predicate(forwards) {
			if len(forwards) > 0 {
				return forwards[0]
			}
			return models.PortForward{}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for forward predicate; current = %+v", manager.ListForwards())
	return models.PortForward{}
}

func readN(t *testing.T, conn net.Conn, n int) []byte {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, n)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read %d bytes: %v", n, err)
	}
	return buf
}
