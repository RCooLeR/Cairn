package dockerbridge

import (
	"context"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Simulate Accept completing after Stop has taken its connection snapshot.
type lateAcceptListener struct {
	conn    net.Conn
	entered chan struct{}
	closed  chan struct{}
	once    sync.Once
}

func (l *lateAcceptListener) Accept() (net.Conn, error) {
	l.once.Do(func() { close(l.entered) })
	<-l.closed
	return l.conn, nil
}

func (l *lateAcceptListener) Close() error {
	close(l.closed)
	return nil
}

func (l *lateAcceptListener) Addr() net.Addr { return l.conn.LocalAddr() }

func TestStopClosesLateAcceptWithoutOpeningBackend(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client, peer := net.Pipe()
	defer func() { _ = peer.Close() }()
	listener := &lateAcceptListener{conn: client, entered: make(chan struct{}), closed: make(chan struct{})}
	m := New(nil, Options{})
	m.ctx, m.cancel = ctx, cancel
	m.listeners = []net.Listener{listener}
	var dials atomic.Int32
	m.wg.Add(1)
	go m.serve(ctx, listener, func(context.Context, string, string) (net.Conn, error) {
		dials.Add(1)
		return nil, context.Canceled
	})
	<-listener.entered
	stopped := make(chan struct{})
	go func() { m.Stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop did not join the late accept")
	}
	if dials.Load() != 0 {
		t.Fatal("shutdown opened a new backend connection")
	}
	if len(m.conns) != 0 {
		t.Fatalf("retained %d connections after Stop", len(m.conns))
	}
	_ = peer.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := peer.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("late connection was not closed: %v", err)
	}
}

func TestHandleClosesBackendReturnedAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client, peer := net.Pipe()
	defer func() { _ = peer.Close() }()
	backend, backendPeer := net.Pipe()
	defer func() { _ = backendPeer.Close() }()
	m := New(nil, Options{})
	m.wg.Add(1)
	m.handle(ctx, client, func(context.Context, string, string) (net.Conn, error) {
		cancel()
		return backend, nil
	})
	for _, conn := range []net.Conn{peer, backendPeer} {
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		if _, err := conn.Read(make([]byte, 1)); err != io.EOF {
			t.Fatalf("connection returned after cancellation was not closed: %v", err)
		}
	}
}
