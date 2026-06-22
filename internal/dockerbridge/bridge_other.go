//go:build !windows

package dockerbridge

import (
	"errors"
	"net"
)

const supported = false

func defaultPipes() []string {
	return nil
}

func listenPipe(string) (net.Listener, error) {
	return nil, errors.New("Windows named pipes are not available on this platform")
}
