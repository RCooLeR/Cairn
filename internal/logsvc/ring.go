package logsvc

import (
	"encoding/base64"
	"encoding/json"
	"sort"

	"github.com/RCooLeR/Cairn/internal/models"
)

type ringBuffer struct {
	limit         int
	byteLimit     int64
	entries       []ringEntry
	start         int
	retainedBytes int64
	dropped       int64
	nextSequence  uint64
}

type ringEntry struct {
	line models.LogLine
	size int64
}

func newRingBuffer(limit int, byteLimits ...int64) *ringBuffer {
	if limit <= 0 {
		limit = defaultRingSize
	}
	byteLimit := defaultRingBytes
	if len(byteLimits) > 0 && byteLimits[0] > 0 {
		byteLimit = byteLimits[0]
	}
	return &ringBuffer{limit: limit, byteLimit: byteLimit}
}

func (r *ringBuffer) add(line models.LogLine) models.LogLine {
	r.nextSequence++
	line.Sequence = r.nextSequence
	size := retainedLogLineBytes(line)
	if size > r.byteLimit {
		r.dropped++
		return line
	}
	for r.retainedCount() > 0 && (r.retainedCount() >= r.limit || r.retainedBytes+size > r.byteLimit) {
		r.evictOldest()
	}
	r.compact()
	r.entries = append(r.entries, ringEntry{line: line, size: size})
	r.retainedBytes += size
	return line
}

func (r *ringBuffer) snapshot() []models.LogLine {
	if r.retainedCount() == 0 {
		return []models.LogLine{}
	}
	lines := make([]models.LogLine, 0, r.retainedCount())
	for _, entry := range r.entries[r.start:] {
		lines = append(lines, entry.line)
	}
	return lines
}

func (r *ringBuffer) retainedCount() int {
	return len(r.entries) - r.start
}

func (r *ringBuffer) evictOldest() {
	entry := &r.entries[r.start]
	r.retainedBytes -= entry.size
	*entry = ringEntry{}
	r.start++
	r.dropped++
}

func (r *ringBuffer) compact() {
	if r.start == 0 {
		return
	}
	if r.start == len(r.entries) {
		r.entries = r.entries[:0]
		r.start = 0
		return
	}
	if r.start < 1024 && r.start*2 < len(r.entries) {
		return
	}
	copy(r.entries, r.entries[r.start:])
	r.entries = r.entries[:len(r.entries)-r.start]
	r.start = 0
}

func retainedLogLineBytes(line models.LogLine) int64 {
	const retainedStructOverhead = 192
	return int64(retainedStructOverhead + len(line.ContainerID) + len(line.ContainerName) +
		len(line.Service) + len(line.Stream) + len(line.Level) + len(line.Text))
}

func SortLines(lines []models.LogLine) {
	sort.SliceStable(lines, func(i, j int) bool {
		a := lines[i]
		b := lines[j]
		if !a.TS.Equal(b.TS) {
			return a.TS.Before(b.TS)
		}
		if a.ContainerID != b.ContainerID {
			return a.ContainerID < b.ContainerID
		}
		if a.Stream != b.Stream {
			return a.Stream < b.Stream
		}
		if a.Text != b.Text {
			return a.Text < b.Text
		}
		return a.Sequence < b.Sequence
	})
}

type lineCursor struct {
	TS          int64  `json:"ts"`
	ContainerID string `json:"containerID"`
	Stream      string `json:"stream"`
	Text        string `json:"text"`
	Sequence    uint64 `json:"sequence"`
}

func pageLines(lines []models.LogLine, cursor string, limit int) models.LogPage {
	if limit <= 0 {
		limit = 200
	}
	start := 0
	if decoded, ok := decodeCursor(cursor); ok {
		for index, line := range lines {
			if compareCursor(line, decoded) > 0 {
				start = index
				break
			}
			start = index + 1
		}
	}
	if start >= len(lines) {
		return models.LogPage{Lines: []models.LogLine{}}
	}
	end := start + limit
	if end > len(lines) {
		end = len(lines)
	}
	page := models.LogPage{Lines: append([]models.LogLine(nil), lines[start:end]...)}
	if end < len(lines) && len(page.Lines) > 0 {
		page.NextCursor = encodeCursor(page.Lines[len(page.Lines)-1])
	}
	return page
}

func encodeCursor(line models.LogLine) string {
	payload, err := json.Marshal(lineCursor{
		TS:          line.TS.UnixNano(),
		ContainerID: line.ContainerID,
		Stream:      line.Stream,
		Text:        line.Text,
		Sequence:    line.Sequence,
	})
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeCursor(value string) (lineCursor, bool) {
	if value == "" {
		return lineCursor{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return lineCursor{}, false
	}
	var cursor lineCursor
	if err := json.Unmarshal(raw, &cursor); err != nil {
		return lineCursor{}, false
	}
	return cursor, true
}

func compareCursor(line models.LogLine, cursor lineCursor) int {
	lineCursor := lineCursor{
		TS:          line.TS.UnixNano(),
		ContainerID: line.ContainerID,
		Stream:      line.Stream,
		Text:        line.Text,
		Sequence:    line.Sequence,
	}
	return compareLineCursor(lineCursor, cursor)
}

func compareLineCursor(a lineCursor, b lineCursor) int {
	if a.TS < b.TS {
		return -1
	}
	if a.TS > b.TS {
		return 1
	}
	if a.ContainerID < b.ContainerID {
		return -1
	}
	if a.ContainerID > b.ContainerID {
		return 1
	}
	if a.Stream < b.Stream {
		return -1
	}
	if a.Stream > b.Stream {
		return 1
	}
	if a.Text < b.Text {
		return -1
	}
	if a.Text > b.Text {
		return 1
	}
	if a.Sequence < b.Sequence {
		return -1
	}
	if a.Sequence > b.Sequence {
		return 1
	}
	return 0
}
