//go:build windows

package terminal

import (
	"context"
	"errors"
)

type unsupportedPTYStarter struct{}

func newDefaultPTYStarter() PTYStarter {
	return unsupportedPTYStarter{}
}

func (unsupportedPTYStarter) Start(context.Context, PTYSpec) (PTYSession, error) {
	return nil, errors.New("windows PTY support requires the WSL provider phase")
}
