//go:build windows

package dockerbridge

import (
	"net"

	winio "github.com/Microsoft/go-winio"
)

const (
	supported = true

	cairnDockerPipe = `\\.\pipe\cairn_docker_engine`
)

func defaultPipes() []string {
	return []string{cairnDockerPipe}
}

func listenPipe(path string) (net.Listener, error) {
	return winio.ListenPipe(path, &winio.PipeConfig{})
}
