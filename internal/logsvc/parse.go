package logsvc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
)

const (
	maxDockerLogFrame       = 16 * 1024 * 1024
	maxDockerLogRecordBytes = 1024 * 1024
	dockerLogReadChunkBytes = 64 * 1024
	truncatedRecordSuffix   = " … [truncated: log record exceeded 1 MiB]"
)

var (
	levelJSONPattern  = regexp.MustCompile(`(?i)"level"\s*:\s*"([a-z]+)"`)
	levelTokenPattern = regexp.MustCompile(`(?i)\b(error|warn|warning|info|debug|fatal)\b`)
)

func ReadDockerLogStream(ctx context.Context, reader io.Reader, source sourceInfo, now func() time.Time, emit func(models.LogLine) bool) error {
	buffered := bufio.NewReader(reader)
	header, err := buffered.Peek(8)
	if err == nil && validDockerLogHeader(header) {
		return readFramedDockerLogs(ctx, buffered, source, now, emit)
	}
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
		return err
	}
	return readPlainDockerLogs(ctx, buffered, source, now, emit)
}

func readFramedDockerLogs(ctx context.Context, reader *bufio.Reader, source sourceInfo, now func() time.Time, emit func(models.LogLine) bool) error {
	assembler := newLineAssembler(source, now, emit)
	scratch := make([]byte, dockerLogReadChunkBytes)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		header := make([]byte, 8)
		n, err := io.ReadFull(reader, header)
		if err != nil {
			if errors.Is(err, io.EOF) && n == 0 {
				assembler.flush()
				return nil
			}
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				assembler.flush()
				return io.ErrUnexpectedEOF
			}
			return err
		}
		if !validDockerLogHeader(header) {
			assembler.add("stdout", header)
			continue
		}
		size := binary.BigEndian.Uint32(header[4:])
		stream := streamName(header[0])
		remaining := int64(size)
		for remaining > 0 {
			chunkSize := min(remaining, int64(len(scratch)))
			n, readErr := io.ReadFull(reader, scratch[:chunkSize])
			if n > 0 {
				assembler.add(stream, scratch[:n])
				remaining -= int64(n)
				if assembler.stopped {
					return nil
				}
			}
			if readErr != nil {
				assembler.flush()
				if errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF) {
					return io.ErrUnexpectedEOF
				}
				return readErr
			}
		}
	}
}

func readPlainDockerLogs(ctx context.Context, reader io.Reader, source sourceInfo, now func() time.Time, emit func(models.LogLine) bool) error {
	assembler := newLineAssembler(source, now, emit)
	buffer := make([]byte, 64*1024)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		count, err := reader.Read(buffer)
		if count > 0 {
			assembler.add("stdout", buffer[:count])
			if assembler.stopped {
				return nil
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				assembler.flush()
				return nil
			}
			return err
		}
		if count == 0 {
			// Readers should not return (0, nil), but avoid a tight loop if a
			// non-conforming provider does so repeatedly.
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Millisecond):
			}
		}
	}
}

func validDockerLogHeader(header []byte) bool {
	if len(header) < 8 {
		return false
	}
	if header[0] != 1 && header[0] != 2 {
		return false
	}
	if header[1] != 0 || header[2] != 0 || header[3] != 0 {
		return false
	}
	return binary.BigEndian.Uint32(header[4:]) <= maxDockerLogFrame
}

func streamName(value byte) string {
	if value == 2 {
		return "stderr"
	}
	return "stdout"
}

type lineAssembler struct {
	source     sourceInfo
	now        func() time.Time
	emit       func(models.LogLine) bool
	pending    map[string][]byte
	discarding map[string]bool
	stopped    bool
}

func newLineAssembler(source sourceInfo, now func() time.Time, emit func(models.LogLine) bool) *lineAssembler {
	return &lineAssembler{
		source:     source,
		now:        now,
		emit:       emit,
		pending:    map[string][]byte{},
		discarding: map[string]bool{},
	}
}

func (a *lineAssembler) add(stream string, chunk []byte) {
	if a.stopped || len(chunk) == 0 {
		return
	}
	for len(chunk) > 0 && !a.stopped {
		newline := bytes.IndexByte(chunk, '\n')
		if newline < 0 {
			a.addFragment(stream, chunk, false)
			return
		}
		a.addFragment(stream, chunk[:newline], true)
		chunk = chunk[newline+1:]
	}
}

func (a *lineAssembler) addFragment(stream string, fragment []byte, recordEnd bool) {
	if a.discarding[stream] {
		if recordEnd {
			a.discarding[stream] = false
		}
		return
	}

	pending := a.pending[stream]
	remaining := maxDockerLogRecordBytes - len(pending)
	if len(fragment) > remaining {
		pending = append(pending, fragment[:remaining]...)
		a.pending[stream] = pending
		a.emitPending(stream, true)
		if !recordEnd {
			a.discarding[stream] = true
		}
		return
	}

	a.pending[stream] = append(pending, fragment...)
	if recordEnd {
		a.emitPending(stream, false)
	}
}

func (a *lineAssembler) emitPending(stream string, truncated bool) {
	text := strings.TrimSuffix(string(a.pending[stream]), "\r")
	a.pending[stream] = a.pending[stream][:0]
	if truncated {
		text += truncatedRecordSuffix
	}
	line := ParseRawLogLine(text, stream, a.source, a.now)
	line.Truncated = truncated
	if !a.emit(line) {
		a.stopped = true
	}
}

func (a *lineAssembler) flush() {
	if a.stopped {
		return
	}
	for stream, pending := range a.pending {
		if len(pending) == 0 || a.discarding[stream] {
			continue
		}
		a.emitPending(stream, false)
		if a.stopped {
			return
		}
	}
}

func ParseRawLogLine(raw string, stream string, source sourceInfo, now func() time.Time) models.LogLine {
	ts, text := parseDockerTimestamp(raw)
	if ts.IsZero() {
		if now == nil {
			now = func() time.Time { return time.Now().UTC() }
		}
		ts = now()
	}
	return models.LogLine{
		TS:            ts,
		ContainerID:   source.ContainerID,
		ContainerName: source.ContainerName,
		Service:       source.Service,
		Stream:        stream,
		Level:         DetectLevel(text),
		Text:          text,
	}
}

func parseDockerTimestamp(raw string) (time.Time, string) {
	prefix, rest, ok := strings.Cut(strings.TrimLeft(raw, " \t"), " ")
	if !ok {
		return time.Time{}, raw
	}
	ts, err := time.Parse(time.RFC3339Nano, prefix)
	if err != nil {
		return time.Time{}, raw
	}
	return ts.UTC(), rest
}

func DetectLevel(text string) string {
	if match := levelJSONPattern.FindStringSubmatch(text); len(match) == 2 {
		return normalizeLevel(match[1])
	}
	if match := levelTokenPattern.FindStringSubmatch(text); len(match) == 2 {
		return normalizeLevel(match[1])
	}
	return ""
}

func normalizeLevel(value string) string {
	switch strings.ToLower(value) {
	case "warning":
		return "warn"
	case "error", "warn", "info", "debug", "fatal":
		return strings.ToLower(value)
	default:
		return ""
	}
}
