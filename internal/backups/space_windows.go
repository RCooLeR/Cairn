//go:build windows

package backups

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func defaultAvailableBytes(path string) (uint64, bool) {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	root := filepath.VolumeName(abs)
	if root == "" {
		root = abs
	}
	if !strings.HasSuffix(root, `\`) {
		root += `\`
	}
	var free uint64
	if err := windows.GetDiskFreeSpaceEx(windows.StringToUTF16Ptr(root), &free, nil, nil); err != nil {
		return 0, false
	}
	return free, true
}
