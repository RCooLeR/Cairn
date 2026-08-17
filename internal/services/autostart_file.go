package services

import (
	"fmt"
	"io"
	"os"
)

const maxAutostartFileBytes int64 = 64 * 1024

// readAutostartFile treats the per-user startup entry as untrusted local
// input. It rejects special, replaced, and oversized files instead of letting
// a settings refresh block on a device/FIFO or allocate without a bound.
func readAutostartFile(path string) (content []byte, err error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, fmt.Errorf("autostart file is not a regular file")
	}
	if before.Size() > maxAutostartFileBytes {
		return nil, fmt.Errorf("autostart file exceeds %d bytes", maxAutostartFileBytes)
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := file.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()

	opened, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return nil, fmt.Errorf("autostart file changed before it could be read safely")
	}

	content, err = io.ReadAll(io.LimitReader(file, maxAutostartFileBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > maxAutostartFileBytes {
		return nil, fmt.Errorf("autostart file exceeds %d bytes", maxAutostartFileBytes)
	}

	after, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(opened, after) || after.Size() != opened.Size() || !after.ModTime().Equal(opened.ModTime()) {
		return nil, fmt.Errorf("autostart file changed while it was being read")
	}
	current, err := os.Lstat(path)
	if err != nil || !current.Mode().IsRegular() || !os.SameFile(after, current) {
		return nil, fmt.Errorf("autostart file path changed while it was being read")
	}
	return content, nil
}
