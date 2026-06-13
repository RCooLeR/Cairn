package logsvc

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
)

const maxDockerLogFrame = 16 * 1024 * 1024

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
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		header := make([]byte, 8)
		if _, err := io.ReadFull(reader, header); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				assembler.flush()
				return nil
			}
			return err
		}
		if !validDockerLogHeader(header) {
			assembler.add("stdout", header)
			continue
		}
		size := binary.BigEndian.Uint32(header[4:])
		payload := make([]byte, size)
		if _, err := io.ReadFull(reader, payload); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				assembler.add(streamName(header[0]), payload)
				assembler.flush()
				return nil
			}
			return err
		}
		assembler.add(streamName(header[0]), payload)
	}
}

func readPlainDockerLogs(ctx context.Context, reader io.Reader, source sourceInfo, now func() time.Time, emit func(models.LogLine) bool) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if !emit(ParseRawLogLine(scanner.Text(), "stdout", source, now)) {
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return nil
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
	source  sourceInfo
	now     func() time.Time
	emit    func(models.LogLine) bool
	pending map[string]string
	stopped bool
}

func newLineAssembler(source sourceInfo, now func() time.Time, emit func(models.LogLine) bool) *lineAssembler {
	return &lineAssembler{
		source:  source,
		now:     now,
		emit:    emit,
		pending: map[string]string{},
	}
}

func (a *lineAssembler) add(stream string, chunk []byte) {
	if a.stopped || len(chunk) == 0 {
		return
	}
	text := a.pending[stream] + string(chunk)
	parts := strings.Split(text, "\n")
	for _, part := range parts[:len(parts)-1] {
		part = strings.TrimSuffix(part, "\r")
		if !a.emit(ParseRawLogLine(part, stream, a.source, a.now)) {
			a.stopped = true
			return
		}
	}
	a.pending[stream] = parts[len(parts)-1]
}

func (a *lineAssembler) flush() {
	if a.stopped {
		return
	}
	for stream, text := range a.pending {
		text = strings.TrimSuffix(text, "\r")
		if text == "" {
			continue
		}
		if !a.emit(ParseRawLogLine(text, stream, a.source, a.now)) {
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
	prefix, rest, ok := strings.Cut(raw, " ")
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
