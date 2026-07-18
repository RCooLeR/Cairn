//go:build !windows

package logsvc

import (
	"fmt"
	"os"
	"path/filepath"
)

func ensurePrivateExportDirectory(path string) error {
	created, err := missingDirectoryChain(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	if err := validateExportDirectoryChain(path); err != nil {
		return err
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return err
	}
	for index := len(created) - 1; index >= 0; index-- {
		if err := syncExportDirectory(filepath.Dir(created[index])); err != nil {
			return err
		}
	}
	return syncExportDirectory(path)
}

func validateExportDirectoryChain(path string) error {
	current := filepath.Clean(path)
	// The export leaf and Cairn-owned parent must never be redirected. Higher
	// platform directories may legitimately be symlinks (for example /var on
	// macOS), and are outside the application-owned suffix.
	for depth := 0; depth < 2; depth++ {
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("export path component is not a regular directory")
		}
		parent := filepath.Dir(current)
		if parent == current {
			return nil
		}
		current = parent
	}
	return nil
}

func missingDirectoryChain(path string) ([]string, error) {
	var missing []string
	current := filepath.Clean(path)
	for {
		info, err := os.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return nil, fmt.Errorf("export path component is not a regular directory")
			}
			return missing, nil
		}
		if !os.IsNotExist(err) {
			return nil, err
		}
		missing = append(missing, current)
		parent := filepath.Dir(current)
		if parent == current {
			return nil, fmt.Errorf("export directory has no existing parent")
		}
		current = parent
	}
}

func syncExportDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func securePrivateExportFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("export temporary file is not regular")
	}
	return os.Chmod(path, 0o600)
}
