package terminal

import (
	"context"
	"io"
)

type PTYSpec struct {
	Argv       []string
	Env        map[string]string
	WorkingDir string
	Cols       int
	Rows       int
}

type PTYSession interface {
	io.ReadWriteCloser
	Resize(cols int, rows int) error
	Wait() int
}

type PTYStarter interface {
	Start(context.Context, PTYSpec) (PTYSession, error)
}
