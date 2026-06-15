//go:build !windows

package providers

import "os/exec"

func configureBackgroundCommand(*exec.Cmd) {}
