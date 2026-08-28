package logsvc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"uuid"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
)

const exportDurabilityWarning = "The export was saved, but its directory metadata could not be fully synchronized."

func (m *Manager) exportLogs(ctx context.Context, req models.ExportLogsRequest) (*models.ExportResult, error) {
	m.ensureReady()
	if err := m.requireDocker(); err != nil {
		return nil, err
	}
	format, err := normalizeExportFormat(req.Format)
	if err != nil {
		return nil, err
	}
	exportDirectory, err := m.resolveExportDirectory()
	if err != nil {
		return nil, err
	}

	operation, err := m.beginOneShotOperation(ctx, m.exportTimeout)
	if err != nil {
		return nil, err
	}
	defer operation.finish()
	tail := -1
	if req.Tail > 0 {
		tail = req.Tail
	}
	lines, _, truncated, err := m.collectLogsBounded(operation, models.LogStreamRequest{
		Scope:      req.Scope,
		IDs:        req.IDs,
		Follow:     false,
		Tail:       tail,
		Timestamps: true,
	}, m.exportLines, m.exportBytes)
	if err != nil {
		return nil, err
	}
	if err := operation.ctx.Err(); err != nil {
		return nil, wrapOperationContextError("Export logs canceled", err)
	}

	if err := ensurePrivateExportDirectory(exportDirectory); err != nil {
		return nil, apperror.Wrap(apperror.Internal, "Prepare private log export directory failed", err)
	}
	if err := operation.ctx.Err(); err != nil {
		return nil, wrapOperationContextError("Export logs canceled", err)
	}
	filename := "cairn-logs-" + uuid.New().String() + "." + format
	targetPath := filepath.Join(exportDirectory, filename)
	temporary, err := os.CreateTemp(exportDirectory, ".cairn-logs-*.tmp")
	if err != nil {
		return nil, apperror.Wrap(apperror.Internal, "Create temporary log export failed", err)
	}
	temporaryPath := temporary.Name()
	published := false
	defer func() {
		if !published {
			_ = temporary.Close()
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := securePrivateExportFile(temporaryPath); err != nil {
		return nil, apperror.Wrap(apperror.Internal, "Secure temporary log export failed", err)
	}

	jsonLines := format == "jsonl"
	var bytesWritten int64
	lineCount := 0
	for _, line := range lines {
		if err := operation.ctx.Err(); err != nil {
			return nil, wrapOperationContextError("Export logs canceled", err)
		}
		encoded, err := encodeExportLine(line, jsonLines)
		if err != nil {
			return nil, apperror.Wrap(apperror.Internal, "Encode log export failed", err)
		}
		if int64(len(encoded)) > m.exportBytes-bytesWritten {
			truncated = true
			break
		}
		n, err := temporary.Write(encoded)
		bytesWritten += int64(n)
		if err != nil {
			return nil, apperror.Wrap(apperror.Internal, "Write log export failed", err)
		}
		if n != len(encoded) {
			return nil, apperror.New(apperror.Internal, "Write log export was incomplete")
		}
		lineCount++
	}
	if err := operation.ctx.Err(); err != nil {
		return nil, wrapOperationContextError("Export logs canceled", err)
	}
	if err := temporary.Sync(); err != nil {
		return nil, apperror.Wrap(apperror.Internal, "Flush log export failed", err)
	}
	if err := temporary.Close(); err != nil {
		return nil, apperror.Wrap(apperror.Internal, "Close log export failed", err)
	}
	if err := operation.ctx.Err(); err != nil {
		return nil, wrapOperationContextError("Export logs canceled", err)
	}
	committed, publishErr := publishFileAtomic(temporaryPath, targetPath)
	published = committed
	if !committed {
		if publishErr == nil {
			publishErr = fmt.Errorf("export was not published")
		}
		return nil, apperror.Wrap(apperror.Internal, "Publish log export failed", publishErr)
	}
	durabilityWarning := ""
	if publishErr != nil {
		durabilityWarning = exportDurabilityWarning
	}
	return &models.ExportResult{
		Path:              targetPath,
		Bytes:             bytesWritten,
		LineCount:         lineCount,
		Truncated:         truncated,
		DurabilityWarning: durabilityWarning,
	}, nil
}

func normalizeExportFormat(format string) (string, error) {
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "" {
		format = "jsonl"
	}
	switch format {
	case "jsonl", "log":
		return format, nil
	default:
		return "", apperror.New(
			apperror.Conflict,
			"Unsupported log export format",
			apperror.WithDetail("Choose JSON Lines or plain text."),
		)
	}
}

func (m *Manager) resolveExportDirectory() (string, error) {
	directory := strings.TrimSpace(m.exportDirectory)
	if directory == "" {
		configDirectory, err := os.UserConfigDir()
		if err != nil {
			return "", apperror.Wrap(apperror.Internal, "Resolve log export directory failed", err)
		}
		directory = filepath.Join(configDirectory, "Cairn", "exports")
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return "", apperror.Wrap(apperror.Internal, "Resolve log export directory failed", err)
	}
	return filepath.Clean(absolute), nil
}

func encodeExportLine(line models.LogLine, jsonLines bool) ([]byte, error) {
	if jsonLines {
		raw, err := json.Marshal(line)
		if err != nil {
			return nil, err
		}
		return append(raw, '\n'), nil
	}
	return []byte(fmt.Sprintf(
		"%s %s container=%q container_id=%q service=%q %s\n",
		line.TS.Format(time.RFC3339Nano),
		line.Stream,
		line.ContainerName,
		line.ContainerID,
		line.Service,
		line.Text,
	)), nil
}
