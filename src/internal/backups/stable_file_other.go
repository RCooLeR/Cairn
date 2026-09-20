//go:build !windows

package backups

import "os"

func openStableFile(path string) (*os.File, error) {
	return os.Open(path)
}

func openStableDeleteFile(path string) (*os.File, error) {
	return os.Open(path)
}

// POSIX unlink remains pathname-based even while an FD pins the inode. The
// caller performs an identity check immediately before this operation; there
// is no portable unlink-by-FD primitive for regular files.
func removeStableFile(path string, _ *os.File) error {
	return os.Remove(path)
}
