//go:build !windows && !linux && !darwin

package services

import (
	"context"
	"errors"
)

type unsupportedAutostartManager struct{}

func NewAutostartManager() AutostartManager {
	return unsupportedAutostartManager{}
}

func (unsupportedAutostartManager) Enabled(context.Context) (bool, error) {
	return false, nil
}

func (unsupportedAutostartManager) SetEnabled(context.Context, bool) error {
	return errors.New("login autostart is not supported on this platform")
}
