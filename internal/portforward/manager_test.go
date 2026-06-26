package portforward

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
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
		{"::", "0.0.0.0"},
		{"127.0.0.1", "127.0.0.1"},
		{"::1", "127.0.0.1"},
		{"192.168.1.50", "192.168.1.50"},
		{"not-an-ip", "0.0.0.0"},
	}
	for _, tc := range cases {
		if got := bindAddrFor(tc.hostIP); got != tc.want {
			t.Errorf("bindAddrFor(%q) = %q, want %q", tc.hostIP, got, tc.want)
		}
	}
}

func TestDesiredForwardsKeepsBroadestBindAndSkipsUnpublished(t *testing.T) {
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
	if len(got) != 2 {
		t.Fatalf("desiredForwards size = %d, want 2 (%+v)", len(got), got)
	}
	tcp, ok := got[forwardKey("tcp", 8080)]
	if !ok || tcp.bindAddr != "0.0.0.0" {
		t.Fatalf("tcp 8080 = %+v, want bind 0.0.0.0 (broadest wins)", tcp)
	}
	udp, ok := got[forwardKey("udp", 53)]
	if !ok || udp.bindAddr != "0.0.0.0" || udp.protocol != "udp" {
		t.Fatalf("udp 53 = %+v, want udp/0.0.0.0", udp)
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
	defer conn.Close()

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
	defer client.Close()

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
