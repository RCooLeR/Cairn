//go:build windows

package dockerbridge

import (
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	winio "github.com/Microsoft/go-winio"
)

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
