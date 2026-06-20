//go:build !windows

package metrics

import "os/exec"

func configureBackgroundCommand(*exec.Cmd) {}
