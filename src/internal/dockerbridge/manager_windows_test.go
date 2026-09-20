//go:build windows

package dockerbridge

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	winio "github.com/Microsoft/go-winio"
)

type startingBridgeProvider struct {
	fakeBridgeProvider
	entered chan struct{}
	release chan struct{}
}

func (p startingBridgeProvider) DockerHost(context.Context) (string, error) {
	close(p.entered)
	<-p.release
	return "", errors.New("provider unavailable")
}

func TestStopJoinsConcurrentStart(t *testing.T) {
	provider := startingBridgeProvider{entered: make(chan struct{}), release: make(chan struct{})}
	manager := New(provider, Options{})
	started := make(chan error, 1)
	go func() { started <- manager.Start(context.Background()) }()
	<-provider.entered
	// Start must hold lifecycle ownership until provider setup completes.
	// Do not use synctest.Wait here: a sync.Mutex wait is not durably blocked.
	if manager.lifecycleMu.TryLock() {
		manager.lifecycleMu.Unlock()
		close(provider.release)
		<-started
		t.Fatal("Start did not own the lifecycle during provider setup")
	}
	stopped := make(chan struct{})
	go func() { manager.Stop(); close(stopped) }()
	close(provider.release)
	if err := <-started; err == nil {
		t.Fatal("expected provider startup failure")
	}
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop did not join completed startup")
	}
}

type fakeBridgeProvider struct{}

func (fakeBridgeProvider) ID() string {
	return "windows_wsl_ubuntu"
}

func (fakeBridgeProvider) DockerHost(context.Context) (string, error) {
	return "unix:///var/run/docker.sock", nil
}

func (fakeBridgeProvider) DockerDialContext(context.Context) (func(context.Context, string, string) (net.Conn, error), error) {
	return func(context.Context, string, string) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer func() { _ = server.Close() }()
			_, _ = io.Copy(server, server)
		}()
		return client, nil
	}, nil
}

func TestManagerForwardsNamedPipeToProviderDialer(t *testing.T) {
	ctx := context.Background()
	pipe := `\\.\pipe\cairn_test_bridge_` + strings.ReplaceAll(t.Name(), "/", "_")
	manager := New(fakeBridgeProvider{}, Options{Pipes: []string{pipe}})
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer manager.Stop()

	timeout := 2 * time.Second
	conn, err := winio.DialPipe(pipe, &timeout)
	if err != nil {
		t.Fatalf("DialPipe() error = %v", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	buffer := make([]byte, len("ping"))
	if _, err := io.ReadFull(conn, buffer); err != nil {
		t.Fatalf("ReadFull() error = %v", err)
	}
	if string(buffer) != "ping" {
		t.Fatalf("echo = %q, want ping", string(buffer))
	}
}
