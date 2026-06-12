package logging

import (
	"bufio"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestRotatingLoggerWritesJSONAndRotates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cairn.log")
	logger, writer, err := NewRotatingLogger(path, 180, 2, slog.LevelDebug)
	if err != nil {
		t.Fatalf("NewRotatingLogger: %v", err)
	}
	defer closeWriter(t, writer)

	logger.Info("startup", "phase", "test", "count", 1)
	logger.Info("second message with enough padding to rotate the file", "phase", "test", "count", 2)
	logger.Info("third message with enough padding to rotate the file again", "phase", "test", "count", 3)

	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("current log missing: %v", err)
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("rotated log missing: %v", err)
	}
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Fatalf("unexpected third backup: %v", err)
	}

	record := readFirstJSONRecord(t, path)
	if record["msg"] == "" {
		t.Fatalf("record missing msg field: %#v", record)
	}
	if record["phase"] != "test" {
		t.Fatalf("phase = %#v, want test", record["phase"])
	}
}

func readFirstJSONRecord(t *testing.T, path string) map[string]any {
	t.Helper()

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Errorf("close log: %v", err)
		}
	}()

	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		t.Fatalf("log has no records")
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan log: %v", err)
	}

	var record map[string]any
	if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
		t.Fatalf("decode log JSON: %v", err)
	}
	return record
}

func closeWriter(t *testing.T, writer *RotatingFile) {
	t.Helper()

	if err := writer.Close(); err != nil {
		t.Errorf("close writer: %v", err)
	}
}
