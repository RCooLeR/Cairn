package services

import "context"

// AutostartManager owns the operating-system integration behind the
// "Launch Cairn at login" setting.
type AutostartManager interface {
	Enabled(context.Context) (bool, error)
	SetEnabled(context.Context, bool) error
}
