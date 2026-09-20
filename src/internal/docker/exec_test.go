package docker

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/moby/moby/api/types/container"
	dockerclient "github.com/moby/moby/client"
)

type execAttachAPI struct {
	*fakeAPI
	attach func(context.Context, string, dockerclient.ExecAttachOptions) (dockerclient.ExecAttachResult, error)
}

func (a execAttachAPI) ExecAttach(ctx context.Context, id string, opts dockerclient.ExecAttachOptions) (dockerclient.ExecAttachResult, error) {
	return a.attach(ctx, id, opts)
}

func TestShellDetectionPreservesExecTransportFailure(t *testing.T) {
	t.Parallel()
	api := newFakeAPI()
	api.containerInspects["test"] = container.InspectResponse{ID: "test", Image: "image"}
	transportError := errors.New("dial wsl+stdio: unknown network wsl+stdio")
	client := New(fakeDockerProvider{}, nil)
	client.api = execAttachAPI{fakeAPI: api, attach: func(context.Context, string, dockerclient.ExecAttachOptions) (dockerclient.ExecAttachResult, error) {
		return dockerclient.ExecAttachResult{}, transportError
	}}
	_, err := client.DetectContainerShells(context.Background(), "test")
	if !errors.Is(err, transportError) || !apperror.IsCode(err, apperror.DockerUnreachable) {
		t.Fatalf("DetectContainerShells error = %v, want original transport failure", err)
	}
	if len(api.execCreates) != 1 {
		t.Fatalf("shell probes = %d, want fail fast after first transport error", len(api.execCreates))
	}
}

func TestMissingContainerShellOnlyRecognizesMissingExecutable(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		message string
		missing bool
	}{
		{`OCI runtime exec failed: exec: "/bin/bash": stat /bin/bash: no such file or directory`, true},
		{`exec: "/bin/bash": executable file not found in $PATH`, true},
		{`dial unix /var/run/docker.sock: no such file or directory`, false},
		{`No such container: /bin/bash`, false},
		{`exec: "/bin/bash": permission denied`, false},
	} {
		err := mapDockerError("attach container exec", errors.New(tc.message))
		if got := missingContainerShell(err, "/bin/bash"); got != tc.missing {
			t.Errorf("missingContainerShell(%q) = %v", tc.message, got)
		}
	}
}

func TestRunContainerExecCancelsSilentHijackedStream(t *testing.T) {
	t.Parallel()
	for _, tty := range []bool{false, true} {
		t.Run(map[bool]string{true: "tty", false: "multiplexed"}[tty], func(t *testing.T) {
			clientConn, serverConn := net.Pipe()
			defer func() { _ = clientConn.Close() }()
			defer func() { _ = serverConn.Close() }()
			client := New(fakeDockerProvider{}, nil)
			client.unaryTimeout = 30 * time.Millisecond
			client.api = execAttachAPI{fakeAPI: newFakeAPI(), attach: func(context.Context, string, dockerclient.ExecAttachOptions) (dockerclient.ExecAttachResult, error) {
				return dockerclient.ExecAttachResult{HijackedResponse: dockerclient.NewHijackedResponse(clientConn, "")}, nil
			}}
			done := make(chan error, 1)
			go func() {
				_, _, err := client.RunContainerExec(context.Background(), "test", ExecOptions{Cmd: []string{"sh"}, TTY: tty})
				done <- err
			}()
			select {
			case err := <-done:
				if !apperror.IsCode(err, apperror.Timeout) {
					t.Fatalf("RunContainerExec error = %v, want timeout", err)
				}
			case <-time.After(time.Second):
				t.Fatal("RunContainerExec hung after its timeout")
			}
		})
	}
}

func TestRunContainerExecBoundsCapturedOutput(t *testing.T) {
	t.Parallel()
	for _, tty := range []bool{false, true} {
		t.Run(map[bool]string{true: "tty", false: "multiplexed"}[tty], func(t *testing.T) {
			api := newFakeAPI()
			api.execOutputs["noisy"] = strings.Repeat("x", maxContainerExecOutput+1)
			client := New(fakeDockerProvider{}, nil)
			client.api = api
			output, code, err := client.RunContainerExec(context.Background(), "test", ExecOptions{Cmd: []string{"noisy"}, TTY: tty})
			if !apperror.IsCode(err, apperror.Conflict) || code != -1 {
				t.Fatalf("RunContainerExec code=%d error=%v, want output limit", code, err)
			}
			if len(output) != maxContainerExecOutput {
				t.Fatalf("captured %d bytes, want %d", len(output), maxContainerExecOutput)
			}
		})
	}
}

func TestOpenContainerExecCancelsProcessBackedUpgrade(t *testing.T) {
	t.Parallel()
	testOpenContainerExecCancelsUpgrade(t, true)
}

func TestOpenContainerExecCancelsNativeUpgrade(t *testing.T) {
	t.Parallel()
	testOpenContainerExecCancelsUpgrade(t, false)
}

func testOpenContainerExecCancelsUpgrade(t *testing.T, processBacked bool) {
	t.Helper()
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/_ping":
			w.Header().Set("API-Version", "1.55")
		case "/v1.55/containers/test/exec":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"Id":"terminal"}`)
		case "/v1.55/exec/terminal/start":
			<-release // Simulate a daemon that never upgrades the exec socket.
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	defer close(release)
	var api APIClient
	var err error
	if processBacked {
		api, err = newSDKClientWithDialer("wsl+stdio://cairn-test", func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
		})
	} else {
		api, err = newSDKClient("tcp://" + server.Listener.Addr().String())
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = api.Close() })
	client := New(fakeDockerProvider{}, nil)
	client.api = api
	client.unaryTimeout = 100 * time.Millisecond
	done := make(chan error, 1)
	go func() {
		_, err := client.OpenContainerExec(context.Background(), "test", ExecOptions{Cmd: []string{"sh"}, TTY: true})
		done <- err
	}()
	select {
	case err := <-done:
		if !apperror.IsCode(err, apperror.Timeout) {
			t.Fatalf("OpenContainerExec error = %v, want timeout", err)
		}
	case <-time.After(time.Second):
		t.Fatal("OpenContainerExec did not cancel a stalled HTTP upgrade")
	}
}

func TestOpenContainerExecStreamOutlivesOpeningRequest(t *testing.T) {
	t.Parallel()
	clientConn, serverConn := net.Pipe()
	defer func() { _ = clientConn.Close() }()
	defer func() { _ = serverConn.Close() }()
	var attachCtx context.Context
	client := New(fakeDockerProvider{}, nil)
	client.api = execAttachAPI{fakeAPI: newFakeAPI(), attach: func(ctx context.Context, _ string, _ dockerclient.ExecAttachOptions) (dockerclient.ExecAttachResult, error) {
		attachCtx = ctx
		return dockerclient.ExecAttachResult{HijackedResponse: dockerclient.NewHijackedResponse(clientConn, "")}, nil
	}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session, err := client.OpenContainerExec(ctx, "test", ExecOptions{Cmd: []string{"sh"}, TTY: true})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	cancel()
	if err := attachCtx.Err(); err != nil {
		t.Fatalf("attached stream inherited RPC cancellation: %v", err)
	}
	writeDone := make(chan error, 1)
	go func() {
		_, err := serverConn.Write([]byte("still connected"))
		writeDone <- err
	}()
	_ = clientConn.SetReadDeadline(time.Now().Add(time.Second))
	output := make([]byte, len("still connected"))
	if _, err := io.ReadFull(session, output); err != nil {
		t.Fatalf("read after request cancellation: %v", err)
	}
	if err := <-writeDone; err != nil {
		t.Fatalf("write after request cancellation: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(attachCtx.Err(), context.Canceled) {
		t.Fatal("closing the terminal did not cancel its attach context")
	}
}

func TestSDKContainerExecUsesProcessBackedDialer(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/_ping":
			w.Header().Set("API-Version", "1.55")
			w.WriteHeader(http.StatusOK)
		case "/v1.55/exec/terminal/start":
			// Drain the request before closing the upgraded socket; leaving
			// unread request bytes can make Windows send an abortive TCP reset.
			_, _ = io.Copy(io.Discard, r.Body)
			conn, rw, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			defer func() { _ = conn.Close() }()
			_, _ = rw.WriteString("HTTP/1.1 101 UPGRADED\r\nContent-Type: application/vnd.docker.raw-stream\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\ncairn-terminal\n")
			_ = rw.Flush()
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	dialer := func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", server.Listener.Addr().String())
	}
	api, err := newSDKClientWithDialer("wsl+stdio://cairn-test", dialer)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = api.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	response, err := api.ExecAttach(ctx, "terminal", dockerclient.ExecAttachOptions{TTY: true})
	if err != nil {
		t.Fatalf("ExecAttach() over process-backed dialer: %v", err)
	}
	defer response.Close()
	output, err := io.ReadAll(response.Reader)
	if err != nil || string(output) != "cairn-terminal\n" {
		t.Fatalf("terminal output = %q, err = %v", output, err)
	}
}
