//go:build windows

package docker

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/RCooLeR/Cairn/internal/apperror"
)

func TestOpenContainerExecCancelsNamedPipeUpgrade(t *testing.T) {
	t.Parallel()
	pipePath := fmt.Sprintf(`\\.\pipe\cairn-exec-test-%d-%d`, os.Getpid(), time.Now().UnixNano())
	listener, err := winio.ListenPipe(pipePath, nil)
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/_ping":
			w.Header().Set("API-Version", "1.55")
		case "/v1.55/containers/test/exec":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"Id":"terminal"}`)
		case "/v1.55/exec/terminal/start":
			<-release
		default:
			http.NotFound(w, r)
		}
	})}
	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		close(release)
		_ = server.Close()
		<-serveDone
	})
	api, err := newSDKClient("npipe://" + strings.ReplaceAll(pipePath, `\`, "/"))
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
		t.Fatal("OpenContainerExec did not cancel a stalled named-pipe upgrade")
	}
}
