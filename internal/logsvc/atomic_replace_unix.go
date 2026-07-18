//go:build !windows

package logsvc

import (
	"os"
	"path/filepath"
)

// publishFileAtomic reports whether the destination was published even when a
// later durability step fails. Callers must not retry a published operation.
func publishFileAtomic(source string, destination string) (bool, error) {
	if err := os.Rename(source, destination); err != nil {
		return false, err
	}
	return true, syncExportDirectory(filepath.Dir(destination))
}
