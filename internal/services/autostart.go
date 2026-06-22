package services

import "context"

const appAutostartName = "Cairn"

// AutostartManager owns the operating-system integration behind the
// "Launch Cairn at login" setting.
type AutostartManager interface {
	Enabled(context.Context) (bool, error)
	SetEnabled(context.Context, bool) error
}
