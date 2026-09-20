package portforward

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
)

func TestForwardClosesConnectionsTrackedAfterStop(t *testing.T) {
	t.Parallel()
	fwd := &forward{closers: map[interface{ Close() error }]struct{}{}}
	fwd.stop()
	conn, peer := net.Pipe()
	defer func() { _ = peer.Close() }()
	defer func() { _ = conn.Close() }()
	fwd.track(conn)
	_ = peer.SetWriteDeadline(time.Now().Add(time.Second))
	if _, err := peer.Write([]byte("late")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("late connection write = %v, want closed pipe", err)
	}
}

func TestManagerStopsWhenUDPDialCompletesDuringShutdown(t *testing.T) {
	t.Parallel()
	backend, peer := net.Pipe()
	defer func() { _ = peer.Close() }()
	defer func() { _ = backend.Close() }()
	dialer := &shutdownPacketDialer{backend: backend, started: make(chan struct{})}
	packetCh := make(chan net.PacketConn, 1)
	manager := newTestManager(t, fakeListerWithPort("15355", "udp"), dialer, Options{
		Enabled:           true,
		ReconcileInterval: time.Hour,
		ListenPacket:      capturingListenPacket(packetCh),
	})
	manager.Start(context.Background())
	t.Cleanup(manager.StopAll)
	host := awaitPacketConn(t, packetCh)
	client, err := net.Dial("udp", host.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	if _, err := client.Write([]byte("packet")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-dialer.started:
	case <-time.After(2 * time.Second):
		t.Fatal("backend dial did not start")
	}
	stopped := make(chan struct{})
	go func() {
		manager.StopAll()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		// Unblock a broken implementation before the test's cleanup waits.
		_ = backend.Close()
		t.Fatal("shutdown hung on a backend returned after cancellation")
	}
}

func TestManagerConcurrentStartStop(t *testing.T) {
	t.Parallel()
	manager := newTestManager(t, &fakeLister{}, &echoDialer{}, Options{})
	var workers sync.WaitGroup
	for range 20 {
		workers.Go(func() {
			manager.Start(context.Background())
			manager.StopAll()
		})
	}
	workers.Wait()
	manager.StopAll()
}

func TestCancelledReconcilePreservesCurrentForwards(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lister := &cancelledReconcileLister{cancel: cancel}
	manager := NewManager(lister, &echoDialer{}, nil, Options{Enabled: true})
	current := &forward{closers: map[interface{ Close() error }]struct{}{}}
	manager.forwards["current"] = current
	manager.reconcileOnce(ctx)
	if manager.forwards["current"] != current || current.stopped {
		t.Fatal("cancelled inventory response removed a current forward")
	}
}

type cancelledReconcileLister struct {
	cancel context.CancelFunc
}

func (l *cancelledReconcileLister) ListContainers(context.Context, models.ContainerListOptions) ([]models.ContainerSummary, error) {
	l.cancel()
	return nil, nil
}

type shutdownPacketDialer struct {
	backend net.Conn
	started chan struct{}
}

func (d *shutdownPacketDialer) DialStream(context.Context, int) (net.Conn, error) {
	panic("TCP dial was not expected")
}

func (d *shutdownPacketDialer) DialPacket(ctx context.Context, _ int) (net.Conn, error) {
	close(d.started)
	<-ctx.Done()
	return d.backend, nil
}
