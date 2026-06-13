package logsvc

import (
	"encoding/base64"
	"encoding/json"
	"sort"

	"github.com/RCooLeR/Cairn/internal/models"
)

type ringBuffer struct {
	limit   int
	lines   []models.LogLine
	dropped int64
}

func newRingBuffer(limit int) *ringBuffer {
	if limit <= 0 {
		limit = defaultRingSize
	}
	return &ringBuffer{limit: limit}
}

func (r *ringBuffer) add(line models.LogLine) {
	if len(r.lines) >= r.limit {
		copy(r.lines, r.lines[1:])
		r.lines[len(r.lines)-1] = line
		r.dropped++
		return
	}
	r.lines = append(r.lines, line)
}

func (r *ringBuffer) snapshot() []models.LogLine {
	return append([]models.LogLine(nil), r.lines...)
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
		return a.Text < b.Text
	})
}

type lineCursor struct {
	TS          int64  `json:"ts"`
	ContainerID string `json:"containerID"`
	Stream      string `json:"stream"`
	Text        string `json:"text"`
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
	return 0
}
