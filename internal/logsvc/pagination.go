package logsvc

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/google/uuid"
)

const logPageCursorVersion = 1

type logPageSnapshot struct {
	id          string
	fingerprint [sha256.Size]byte
	lines       []models.LogLine
	bytes       int64
	truncated   bool
	lastAccess  time.Time
	expiresAt   time.Time
	expiryTimer *time.Timer
}

type logPageSnapshotCursor struct {
	Version    int    `json:"v"`
	SnapshotID string `json:"snapshot"`
	Offset     int    `json:"offset"`
}

func (m *Manager) fetchLogPage(ctx context.Context, req models.LogPageRequest) (*models.LogPage, error) {
	m.ensureReady()
	if err := m.requireDocker(); err != nil {
		return nil, err
	}
	limit, err := normalizeLogPageLimit(req.Limit)
	if err != nil {
		return nil, err
	}
	normalized, err := normalizeLogStreamRequest(models.LogStreamRequest{
		Scope: req.Scope,
		IDs:   req.IDs,
	}, m.maxReadersPerStream)
	if err != nil {
		return nil, err
	}
	fingerprint := logPageRequestFingerprint(normalized)

	var cursor logPageSnapshotCursor
	if req.Cursor != "" {
		cursor, err = m.decodeLogPageSnapshotCursor(req.Cursor)
		if err != nil {
			return nil, err
		}
	}

	operation, err := m.beginOneShotOperation(ctx, m.fetchTimeout)
	if err != nil {
		return nil, err
	}
	defer operation.finish()
	if req.Cursor != "" {
		if err := operation.ctx.Err(); err != nil {
			return nil, wrapOperationContextError("Fetch log page canceled", err)
		}
		return m.pageFromSnapshot(cursor, fingerprint, limit)
	}
	lines, retainedBytes, truncated, err := m.collectLogsBounded(operation, models.LogStreamRequest{
		Scope:      normalized.Scope,
		IDs:        normalized.IDs,
		Follow:     false,
		Tail:       defaultFetchTail,
		Timestamps: true,
	}, m.pageSnapshotLines, m.pageSnapshotBytes)
	if err != nil {
		return nil, err
	}

	end := min(limit, len(lines))
	page := &models.LogPage{
		Lines:     append([]models.LogLine(nil), lines[:end]...),
		Truncated: truncated,
	}
	if end == len(lines) {
		return page, nil
	}

	now := m.now()
	snapshot := &logPageSnapshot{
		fingerprint: fingerprint,
		lines:       lines,
		bytes:       retainedBytes,
		truncated:   truncated,
		lastAccess:  now,
		expiresAt:   now.Add(m.pageSnapshotTTL),
	}
	m.mu.Lock()
	if m.closed || m.rootCtx == nil || m.rootCtx.Err() != nil {
		m.mu.Unlock()
		return nil, apperror.New(apperror.ProviderNotReady, "Log runtime is stopping")
	}
	m.prunePageSnapshotsLocked(now)
	m.storePageSnapshotLocked(snapshot)
	m.mu.Unlock()
	page.NextCursor = m.encodeLogPageSnapshotCursor(snapshot.id, end)
	return page, nil
}

func normalizeLogPageLimit(limit int) (int, error) {
	if limit <= 0 {
		return defaultPageLimit, nil
	}
	if limit > maximumPageLimit {
		return 0, apperror.New(
			apperror.Conflict,
			"Log page limit is too large",
			apperror.WithDetail(fmt.Sprintf("At most %d log lines can be returned in one page.", maximumPageLimit)),
		)
	}
	return limit, nil
}

func logPageRequestFingerprint(req models.LogStreamRequest) [sha256.Size]byte {
	ids := append([]string(nil), req.IDs...)
	sort.Strings(ids)
	payload, _ := json.Marshal(struct {
		Scope string   `json:"scope"`
		IDs   []string `json:"ids"`
	}{Scope: req.Scope, IDs: ids})
	return sha256.Sum256(payload)
}

func newLogPageCursorKey() [sha256.Size]byte {
	return sha256.Sum256([]byte(uuid.NewString() + uuid.NewString()))
}

func (m *Manager) encodeLogPageSnapshotCursor(snapshotID string, offset int) string {
	payload, _ := json.Marshal(logPageSnapshotCursor{
		Version:    logPageCursorVersion,
		SnapshotID: snapshotID,
		Offset:     offset,
	})
	mac := hmac.New(sha256.New, m.pageCursorKey[:])
	_, _ = mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (m *Manager) decodeLogPageSnapshotCursor(value string) (logPageSnapshotCursor, error) {
	if len(value) == 0 || len(value) > 512 {
		return logPageSnapshotCursor{}, invalidLogPageCursorError()
	}
	payloadValue, signatureValue, ok := strings.Cut(value, ".")
	if !ok || strings.Contains(signatureValue, ".") {
		return logPageSnapshotCursor{}, invalidLogPageCursorError()
	}
	raw, err := base64.RawURLEncoding.DecodeString(payloadValue)
	if err != nil {
		return logPageSnapshotCursor{}, invalidLogPageCursorError()
	}
	signature, err := base64.RawURLEncoding.DecodeString(signatureValue)
	if err != nil {
		return logPageSnapshotCursor{}, invalidLogPageCursorError()
	}
	mac := hmac.New(sha256.New, m.pageCursorKey[:])
	_, _ = mac.Write(raw)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return logPageSnapshotCursor{}, invalidLogPageCursorError()
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var cursor logPageSnapshotCursor
	if err := decoder.Decode(&cursor); err != nil {
		return logPageSnapshotCursor{}, invalidLogPageCursorError()
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return logPageSnapshotCursor{}, invalidLogPageCursorError()
	}
	if cursor.Version != logPageCursorVersion || cursor.Offset <= 0 {
		return logPageSnapshotCursor{}, invalidLogPageCursorError()
	}
	parsedID, err := uuid.Parse(cursor.SnapshotID)
	if err != nil || parsedID.String() != cursor.SnapshotID {
		return logPageSnapshotCursor{}, invalidLogPageCursorError()
	}
	return cursor, nil
}

func invalidLogPageCursorError() error {
	return apperror.New(
		apperror.Conflict,
		"Log page cursor is invalid",
		apperror.WithRepairHints("Restart log pagination without a cursor."),
	)
}

func (m *Manager) pageFromSnapshot(
	cursor logPageSnapshotCursor,
	fingerprint [sha256.Size]byte,
	limit int,
) (*models.LogPage, error) {
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.prunePageSnapshotsLocked(now)
	snapshot := m.pageSnapshots[cursor.SnapshotID]
	if snapshot == nil {
		return nil, apperror.New(
			apperror.PlanExpired,
			"Log page cursor expired or was evicted",
			apperror.WithRepairHints("Restart log pagination without a cursor."),
		)
	}
	if snapshot.fingerprint != fingerprint {
		return nil, apperror.New(
			apperror.Conflict,
			"Log page cursor belongs to a different request",
			apperror.WithRepairHints("Restart log pagination after changing the log scope."),
		)
	}
	if cursor.Offset >= len(snapshot.lines) {
		return nil, invalidLogPageCursorError()
	}

	end := min(cursor.Offset+limit, len(snapshot.lines))
	page := &models.LogPage{
		Lines:     append([]models.LogLine(nil), snapshot.lines[cursor.Offset:end]...),
		Truncated: snapshot.truncated,
	}
	if end < len(snapshot.lines) {
		snapshot.lastAccess = now
		snapshot.expiresAt = now.Add(m.pageSnapshotTTL)
		m.schedulePageSnapshotExpiryLocked(snapshot, now)
		page.NextCursor = m.encodeLogPageSnapshotCursor(snapshot.id, end)
	} else {
		m.removePageSnapshotLocked(snapshot.id)
	}
	return page, nil
}

func (m *Manager) storePageSnapshotLocked(snapshot *logPageSnapshot) {
	for len(m.pageSnapshots) >= m.maxPageSnapshots || m.pageSnapshotBytesInUse+snapshot.bytes > m.pageSnapshotsBytes {
		oldestID := ""
		var oldestAccess time.Time
		for id, candidate := range m.pageSnapshots {
			if oldestID == "" || candidate.lastAccess.Before(oldestAccess) ||
				(candidate.lastAccess.Equal(oldestAccess) && id < oldestID) {
				oldestID = id
				oldestAccess = candidate.lastAccess
			}
		}
		if oldestID == "" {
			break
		}
		m.removePageSnapshotLocked(oldestID)
	}
	for snapshot.id == "" || m.pageSnapshots[snapshot.id] != nil {
		snapshot.id = uuid.NewString()
	}
	m.pageSnapshots[snapshot.id] = snapshot
	m.pageSnapshotBytesInUse += snapshot.bytes
	m.schedulePageSnapshotExpiryLocked(snapshot, snapshot.lastAccess)
}

func (m *Manager) prunePageSnapshotsLocked(now time.Time) {
	for id, snapshot := range m.pageSnapshots {
		if !now.Before(snapshot.expiresAt) {
			m.removePageSnapshotLocked(id)
		}
	}
}

func (m *Manager) removePageSnapshotLocked(id string) {
	snapshot := m.pageSnapshots[id]
	if snapshot == nil {
		return
	}
	delete(m.pageSnapshots, id)
	if snapshot.expiryTimer != nil {
		snapshot.expiryTimer.Stop()
		snapshot.expiryTimer = nil
	}
	m.pageSnapshotBytesInUse -= snapshot.bytes
	if m.pageSnapshotBytesInUse < 0 {
		m.pageSnapshotBytesInUse = 0
	}
}

func (m *Manager) schedulePageSnapshotExpiryLocked(snapshot *logPageSnapshot, now time.Time) {
	delay := snapshot.expiresAt.Sub(now)
	if delay <= 0 {
		delay = time.Nanosecond
	}
	if snapshot.expiryTimer == nil {
		id := snapshot.id
		snapshot.expiryTimer = time.AfterFunc(delay, func() {
			m.expirePageSnapshot(id)
		})
		return
	}
	snapshot.expiryTimer.Reset(delay)
}

func (m *Manager) expirePageSnapshot(id string) {
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	snapshot := m.pageSnapshots[id]
	if snapshot == nil {
		return
	}
	if now.Before(snapshot.expiresAt) {
		m.schedulePageSnapshotExpiryLocked(snapshot, now)
		return
	}
	m.removePageSnapshotLocked(id)
}
