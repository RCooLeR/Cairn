package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/RCooLeR/Cairn/internal/models"
)

const (
	MetricsResolutionRaw = "raw"
	MetricsResolution1m  = "1m"
	MetricsResolution15m = "15m"
)

type MetricsRepository struct {
	writer *sql.DB
	reader *sql.DB
}

type MetricsSampleRecord struct {
	ProviderID        string
	ProjectID         string
	ServiceID         string
	ContainerID       string
	CPUPercent        float64
	CPUPercentMax     float64
	MemoryBytes       int64
	MemoryBytesMax    int64
	MemoryLimitBytes  int64
	GPUMemoryBytes    int64
	GPUMemoryBytesMax int64
	NetworkRXBytes    int64
	NetworkTXBytes    int64
	BlockReadBytes    int64
	BlockWriteBytes   int64
	PIDs              int64
	Resolution        string
	SampledAt         time.Time
}

type MetricsSeriesFilter struct {
	ProviderID  string
	ProjectID   string
	ContainerID string
	Resolution  string
	From        time.Time
	To          time.Time
}

func (s *Store) Metrics() *MetricsRepository {
	return &MetricsRepository{writer: s.writer, reader: s.reader}
}

func (r *MetricsRepository) InsertBatch(ctx context.Context, records []MetricsSampleRecord) error {
	if len(records) == 0 {
		return nil
	}
	tx, err := r.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	if err := insertMetricsRecords(ctx, tx, records); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *MetricsRepository) QuerySeries(ctx context.Context, filter MetricsSeriesFilter) (*models.SeriesBundle, error) {
	resolution := filter.Resolution
	if resolution == "" {
		resolution = ResolutionForRange(filter.From, filter.To)
	}
	from := filter.From
	to := filter.To
	if to.IsZero() {
		to = time.Now().UTC()
	}
	if from.IsZero() {
		from = to.Add(-time.Hour)
	}

	query := `
		SELECT sampled_at,
			COALESCE(SUM(cpu_percent), 0),
			COALESCE(SUM(memory_bytes), 0),
			COALESCE(SUM(gpu_memory_bytes), 0),
			COALESCE(SUM(network_rx_bytes), 0),
			COALESCE(SUM(network_tx_bytes), 0),
			COALESCE(SUM(block_read_bytes), 0),
			COALESCE(SUM(block_write_bytes), 0)
		FROM metrics_samples
		WHERE resolution = ?
			AND sampled_at >= ?
			AND sampled_at <= ?
			AND (? = '' OR provider_id = ?)
			AND (? = '' OR project_id = ?)
			AND (? = '' OR container_id = ?)
		GROUP BY sampled_at
		ORDER BY sampled_at ASC
	`
	rows, err := r.reader.QueryContext(ctx, query,
		resolution, formatTime(from), formatTime(to),
		filter.ProviderID, filter.ProviderID,
		filter.ProjectID, filter.ProjectID,
		filter.ContainerID, filter.ContainerID,
	)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	bundle := emptySeriesBundle()
	for rows.Next() {
		var (
			tsText                             string
			cpu, mem, gpu, rx, txBytes, br, bw float64
		)
		if err := rows.Scan(&tsText, &cpu, &mem, &gpu, &rx, &txBytes, &br, &bw); err != nil {
			return nil, err
		}
		ts, err := parseMetricTime(tsText)
		if err != nil {
			return nil, err
		}
		bundle.Series[0].Points = append(bundle.Series[0].Points, models.Point{TS: ts, Value: cpu})
		bundle.Series[1].Points = append(bundle.Series[1].Points, models.Point{TS: ts, Value: mem})
		bundle.Series[2].Points = append(bundle.Series[2].Points, models.Point{TS: ts, Value: gpu})
		bundle.Series[3].Points = append(bundle.Series[3].Points, models.Point{TS: ts, Value: rx})
		bundle.Series[4].Points = append(bundle.Series[4].Points, models.Point{TS: ts, Value: txBytes})
		bundle.Series[5].Points = append(bundle.Series[5].Points, models.Point{TS: ts, Value: br})
		bundle.Series[6].Points = append(bundle.Series[6].Points, models.Point{TS: ts, Value: bw})
	}
	return bundle, rows.Err()
}

func (r *MetricsRepository) RetainAndDownsample(ctx context.Context, now time.Time) error {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	tx, err := r.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if err := downsampleMetrics(ctx, tx, MetricsResolutionRaw, MetricsResolution1m, now.Add(-time.Hour), time.Minute); err != nil {
		return err
	}
	if err := downsampleMetrics(ctx, tx, MetricsResolution1m, MetricsResolution15m, now.Add(-24*time.Hour), 15*time.Minute); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM metrics_samples
		WHERE resolution = ? AND sampled_at < ?
	`, MetricsResolution15m, formatTime(now.Add(-7*24*time.Hour))); err != nil {
		return err
	}
	return tx.Commit()
}

func ResolutionForRange(from time.Time, to time.Time) string {
	if to.IsZero() {
		to = time.Now().UTC()
	}
	if from.IsZero() {
		from = to.Add(-time.Hour)
	}
	duration := to.Sub(from)
	if duration <= time.Hour {
		return MetricsResolutionRaw
	}
	if duration <= 24*time.Hour {
		return MetricsResolution1m
	}
	return MetricsResolution15m
}

func insertMetricsRecords(ctx context.Context, tx *sql.Tx, records []MetricsSampleRecord) error {
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO metrics_samples (
			provider_id, project_id, service_id, container_id,
			cpu_percent, cpu_percent_max, memory_bytes, memory_bytes_max,
			memory_limit_bytes, gpu_memory_bytes, network_rx_bytes, network_tx_bytes,
			block_read_bytes, block_write_bytes, pids, resolution, sampled_at
		)
		VALUES (?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''),
			?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer func() {
		_ = stmt.Close()
	}()
	for _, record := range records {
		if record.Resolution == "" {
			record.Resolution = MetricsResolutionRaw
		}
		if record.CPUPercentMax == 0 {
			record.CPUPercentMax = record.CPUPercent
		}
		if record.MemoryBytesMax == 0 {
			record.MemoryBytesMax = record.MemoryBytes
		}
		if record.SampledAt.IsZero() {
			record.SampledAt = time.Now().UTC()
		}
		if _, err := stmt.ExecContext(ctx,
			record.ProviderID, record.ProjectID, record.ServiceID, record.ContainerID,
			record.CPUPercent, record.CPUPercentMax, record.MemoryBytes, record.MemoryBytesMax,
			record.MemoryLimitBytes, record.GPUMemoryBytes, record.NetworkRXBytes, record.NetworkTXBytes,
			record.BlockReadBytes, record.BlockWriteBytes, record.PIDs, record.Resolution,
			formatTime(record.SampledAt),
		); err != nil {
			return err
		}
	}
	return nil
}

func downsampleMetrics(ctx context.Context, tx *sql.Tx, fromResolution string, toResolution string, cutoff time.Time, bucketSize time.Duration) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT provider_id, COALESCE(project_id, ''), COALESCE(service_id, ''),
			COALESCE(container_id, ''), COALESCE(cpu_percent, 0),
			COALESCE(cpu_percent_max, 0), COALESCE(memory_bytes, 0),
			COALESCE(memory_bytes_max, 0), COALESCE(memory_limit_bytes, 0),
			COALESCE(gpu_memory_bytes, 0), COALESCE(network_rx_bytes, 0), COALESCE(network_tx_bytes, 0),
			COALESCE(block_read_bytes, 0), COALESCE(block_write_bytes, 0),
			COALESCE(pids, 0), sampled_at
		FROM metrics_samples
		WHERE resolution = ? AND sampled_at < ?
	`, fromResolution, formatTime(cutoff))
	if err != nil {
		return err
	}
	defer func() {
		_ = rows.Close()
	}()

	aggregates := map[metricsBucketKey]*metricsAggregate{}
	for rows.Next() {
		var record MetricsSampleRecord
		var tsText string
		if err := rows.Scan(
			&record.ProviderID,
			&record.ProjectID,
			&record.ServiceID,
			&record.ContainerID,
			&record.CPUPercent,
			&record.CPUPercentMax,
			&record.MemoryBytes,
			&record.MemoryBytesMax,
			&record.MemoryLimitBytes,
			&record.GPUMemoryBytes,
			&record.NetworkRXBytes,
			&record.NetworkTXBytes,
			&record.BlockReadBytes,
			&record.BlockWriteBytes,
			&record.PIDs,
			&tsText,
		); err != nil {
			return err
		}
		sampledAt, err := parseMetricTime(tsText)
		if err != nil {
			return err
		}
		record.SampledAt = sampledAt
		key := metricsBucketKey{
			providerID:  record.ProviderID,
			projectID:   record.ProjectID,
			serviceID:   record.ServiceID,
			containerID: record.ContainerID,
			bucket:      record.SampledAt.UTC().Truncate(bucketSize),
		}
		aggregate := aggregates[key]
		if aggregate == nil {
			aggregate = &metricsAggregate{}
			aggregates[key] = aggregate
		}
		aggregate.add(record)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(aggregates) == 0 {
		return nil
	}

	records := make([]MetricsSampleRecord, 0, len(aggregates))
	for key, aggregate := range aggregates {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM metrics_samples
			WHERE resolution = ?
				AND provider_id = ?
				AND COALESCE(project_id, '') = ?
				AND COALESCE(service_id, '') = ?
				AND COALESCE(container_id, '') = ?
				AND sampled_at = ?
		`, toResolution, key.providerID, key.projectID, key.serviceID, key.containerID, formatTime(key.bucket)); err != nil {
			return err
		}
		records = append(records, aggregate.record(key, toResolution))
	}
	if err := insertMetricsRecords(ctx, tx, records); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		DELETE FROM metrics_samples
		WHERE resolution = ? AND sampled_at < ?
	`, fromResolution, formatTime(cutoff))
	return err
}

type metricsBucketKey struct {
	providerID  string
	projectID   string
	serviceID   string
	containerID string
	bucket      time.Time
}

type metricsAggregate struct {
	count          int64
	cpuSum         float64
	cpuMax         float64
	memorySum      int64
	memoryMax      int64
	memoryLimitMax int64
	gpuMemoryMax   int64
	networkRXMax   int64
	networkTXMax   int64
	blockReadMax   int64
	blockWriteMax  int64
	pidsMax        int64
}

func (a *metricsAggregate) add(record MetricsSampleRecord) {
	a.count++
	a.cpuSum += record.CPUPercent
	a.cpuMax = maxFloat(a.cpuMax, maxFloat(record.CPUPercentMax, record.CPUPercent))
	a.memorySum += record.MemoryBytes
	a.memoryMax = maxInt64(a.memoryMax, maxInt64(record.MemoryBytesMax, record.MemoryBytes))
	a.memoryLimitMax = maxInt64(a.memoryLimitMax, record.MemoryLimitBytes)
	a.gpuMemoryMax = maxInt64(a.gpuMemoryMax, maxInt64(record.GPUMemoryBytesMax, record.GPUMemoryBytes))
	a.networkRXMax = maxInt64(a.networkRXMax, record.NetworkRXBytes)
	a.networkTXMax = maxInt64(a.networkTXMax, record.NetworkTXBytes)
	a.blockReadMax = maxInt64(a.blockReadMax, record.BlockReadBytes)
	a.blockWriteMax = maxInt64(a.blockWriteMax, record.BlockWriteBytes)
	a.pidsMax = maxInt64(a.pidsMax, record.PIDs)
}

func (a *metricsAggregate) record(key metricsBucketKey, resolution string) MetricsSampleRecord {
	memory := int64(0)
	cpu := 0.0
	if a.count > 0 {
		memory = a.memorySum / a.count
		cpu = a.cpuSum / float64(a.count)
	}
	return MetricsSampleRecord{
		ProviderID:        key.providerID,
		ProjectID:         key.projectID,
		ServiceID:         key.serviceID,
		ContainerID:       key.containerID,
		CPUPercent:        cpu,
		CPUPercentMax:     a.cpuMax,
		MemoryBytes:       memory,
		MemoryBytesMax:    a.memoryMax,
		MemoryLimitBytes:  a.memoryLimitMax,
		GPUMemoryBytes:    a.gpuMemoryMax,
		GPUMemoryBytesMax: a.gpuMemoryMax,
		NetworkRXBytes:    a.networkRXMax,
		NetworkTXBytes:    a.networkTXMax,
		BlockReadBytes:    a.blockReadMax,
		BlockWriteBytes:   a.blockWriteMax,
		PIDs:              a.pidsMax,
		Resolution:        resolution,
		SampledAt:         key.bucket,
	}
}

func emptySeriesBundle() *models.SeriesBundle {
	return &models.SeriesBundle{Series: []models.Series{
		{Name: "cpu", Unit: "percent"},
		{Name: "mem", Unit: "bytes"},
		{Name: "gpu", Unit: "bytes"},
		{Name: "netRx", Unit: "bytes"},
		{Name: "netTx", Unit: "bytes"},
		{Name: "blockR", Unit: "bytes"},
		{Name: "blockW", Unit: "bytes"},
	}}
}

func parseMetricTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("parse metric timestamp %q: invalid timestamp", value)
}

func maxFloat(a float64, b float64) float64 {
	if b > a {
		return b
	}
	return a
}

func maxInt64(a int64, b int64) int64 {
	if b > a {
		return b
	}
	return a
}
