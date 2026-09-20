package logging

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
)

const (
	defaultMaxBytes   int64 = 10 * 1024 * 1024
	defaultMaxBackups       = 5
)

type RotatingFile struct {
	mu         sync.Mutex
	path       string
	maxBytes   int64
	maxBackups int
	file       *os.File
	size       int64
}

func OpenRotatingFile(path string, maxBytes int64, maxBackups int) (*RotatingFile, error) {
	if maxBytes <= 0 {
		maxBytes = defaultMaxBytes
	}
	if maxBackups < 1 {
		maxBackups = defaultMaxBackups
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}

	writer := &RotatingFile{
		path:       path,
		maxBytes:   maxBytes,
		maxBackups: maxBackups,
	}
	if err := writer.open(); err != nil {
		return nil, err
	}
	return writer, nil
}

func NewLogger(writer io.Writer, level slog.Leveler) *slog.Logger {
	if level == nil {
		level = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: level}))
}

func NewRotatingLogger(path string, maxBytes int64, maxBackups int, level slog.Leveler) (*slog.Logger, *RotatingFile, error) {
	writer, err := OpenRotatingFile(path, maxBytes, maxBackups)
	if err != nil {
		return nil, nil, err
	}
	return NewLogger(writer, level), writer, nil
}

func (w *RotatingFile) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return 0, os.ErrClosed
	}
	if w.size > 0 && w.size+int64(len(p)) > w.maxBytes {
		if err := w.rotateLocked(); err != nil {
			return 0, err
		}
	}

	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *RotatingFile) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func (w *RotatingFile) open() error {
	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		closeErr := file.Close()
		return errors.Join(err, closeErr)
	}
	w.file = file
	w.size = info.Size()
	return nil
}

func (w *RotatingFile) rotateLocked() error {
	if w.file != nil {
		if err := w.file.Close(); err != nil {
			return err
		}
		w.file = nil
	}

	for i := w.maxBackups - 1; i >= 1; i-- {
		src := backupPath(w.path, i)
		dst := backupPath(w.path, i+1)
		if _, err := os.Stat(src); err == nil {
			if err := os.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if err := os.Rename(src, dst); err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}

	if _, err := os.Stat(w.path); err == nil {
		dst := backupPath(w.path, 1)
		if err := os.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Rename(w.path, backupPath(w.path, 1)); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	if err := pruneBackups(w.path, w.maxBackups); err != nil {
		return err
	}
	return w.open()
}

func backupPath(path string, n int) string {
	return path + "." + strconv.Itoa(n)
}

func pruneBackups(path string, keep int) error {
	for i := keep + 1; ; i++ {
		if err := os.Remove(backupPath(path, i)); errors.Is(err, os.ErrNotExist) {
			return nil
		} else if err != nil {
			return err
		}
	}
}
