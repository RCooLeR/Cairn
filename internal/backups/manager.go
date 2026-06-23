package backups

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/bus"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
	"github.com/RCooLeR/Cairn/internal/security"
	"github.com/RCooLeR/Cairn/internal/store"
	"github.com/google/uuid"
)

const (
	helperImage           = "alpine:3"
	formatVersion         = 1
	backupResultOK        = "success"
	backupResultFailed    = "failed"
	backupTimestampLayout = "20060102T150405Z"
)

type ProviderResolver interface {
	ActiveProvider(context.Context) (providers.PlatformProvider, error)
}

type DockerClient interface {
	ProviderID() string
	GetVolume(context.Context, string) (*models.VolumeDetail, error)
	CreateVolume(context.Context, models.CreateVolumeRequest) (*models.VolumeSummary, error)
}

type Manager struct {
	Providers ProviderResolver
	Docker    DockerClient
	Settings  *store.SettingsRepository
	Backups   *store.BackupRepository
	Audit     *store.AuditRepository
	Events    bus.Bus
	Now       func() time.Time
	NewID     func() string
	Version   string

	AvailableBytes func(string) (uint64, bool)

	mu    sync.Mutex
	plans map[string]planRecord

	jobsMu  sync.Mutex
	rootCtx context.Context
	jobs    map[string]context.CancelFunc
}

type planRecord struct {
	Plan              models.CommandPlan
	Operation         string
	Provider          providers.PlatformProvider
	ProviderID        string
	ProjectID         string
	VolumeName        string
	TargetVolumeName  string
	BackupDirHost     string
	BackupDirBackend  string
	ArchiveName       string
	ArchivePath       string
	MetadataPath      string
	BackupID          string
	UsingContainers   []string
	Overwrite         bool
	CreateTargetFirst bool
	Sidecar           BackupSidecar
}

type BackupSidecar struct {
	FormatVersion        int       `json:"format_version"`
	Volume               string    `json:"volume"`
	Project              string    `json:"project,omitempty"`
	UsingContainers      []string  `json:"using_containers,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
	CompressedSizeBytes  int64     `json:"compressed_size_bytes"`
	SHA256               string    `json:"sha256"`
	DockerContext        string    `json:"docker_context,omitempty"`
	Provider             string    `json:"provider"`
	CairnVersion         string    `json:"cairn_version"`
	ArchiveFormatVersion int       `json:"archive_format_version"`
}

type jobProgressPayload struct {
	JobID   string   `json:"jobID"`
	Phase   string   `json:"phase"`
	Message string   `json:"message"`
	Pct     *float64 `json:"pct,omitempty"`
}

type jobDonePayload struct {
	JobID  string `json:"jobID"`
	Result string `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

type objectsChangedPayload struct {
	Kind string   `json:"kind"`
	IDs  []string `json:"ids"`
}

var safeFilenamePattern = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)

const maxBackupPathAttempts = 10000

func NewManager(providers ProviderResolver, docker DockerClient, settings *store.SettingsRepository, backups *store.BackupRepository, audit *store.AuditRepository, events bus.Bus, version string) *Manager {
	return &Manager{
		Providers:      providers,
		Docker:         docker,
		Settings:       settings,
		Backups:        backups,
		Audit:          audit,
		Events:         events,
		Now:            func() time.Time { return time.Now().UTC() },
		NewID:          uuid.NewString,
		Version:        version,
		AvailableBytes: defaultAvailableBytes,
		plans:          map[string]planRecord{},
		jobs:           map[string]context.CancelFunc{},
	}
}

func (m *Manager) Start(ctx context.Context) {
	if m == nil {
		return
	}
	m.jobsMu.Lock()
	m.rootCtx = ctx
	m.jobsMu.Unlock()
}

func (m *Manager) StopAll() {
	if m == nil {
		return
	}
	m.jobsMu.Lock()
	cancels := make([]context.CancelFunc, 0, len(m.jobs))
	for jobID, cancel := range m.jobs {
		cancels = append(cancels, cancel)
		delete(m.jobs, jobID)
	}
	m.jobsMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (m *Manager) PlanBackupVolume(ctx context.Context, req models.BackupVolumeRequest) (*models.CommandPlan, error) {
	if m.Docker == nil || m.Providers == nil {
		return nil, notReady()
	}
	volumeName := strings.TrimSpace(req.VolumeName)
	if volumeName == "" {
		return nil, apperror.New(apperror.Conflict, "Volume name is required")
	}
	provider, err := m.Providers.ActiveProvider(ctx)
	if err != nil {
		return nil, err
	}
	detail, err := m.Docker.GetVolume(ctx, volumeName)
	if err != nil {
		return nil, err
	}
	backupDirHost, backupDirBackend, err := m.backupDir(ctx, provider, req.DestPath)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(backupDirHost, 0o755); err != nil {
		return nil, apperror.Wrap(apperror.Internal, "Create backup directory failed", err)
	}
	if err := m.checkFreeSpace(backupDirHost, detail.Summary.SizeBytes); err != nil {
		return nil, err
	}

	now := m.now()
	archiveName, archivePath, metadataPath, err := backupPaths(backupDirHost, volumeName, now)
	if err != nil {
		return nil, err
	}
	containers := runningContainerNames(detail.Containers)
	plan := models.CommandPlan{
		PlanID:    security.NewPlanID(),
		Title:     "Back up " + volumeName,
		Risk:      models.RiskSafe,
		Commands:  []models.PlannedCommand{backupCommand(1, volumeName, backupDirBackend, archiveName, models.RiskSafe)},
		Effects:   backupEffects(volumeName, archivePath, metadataPath, containers),
		ExpiresAt: now.Add(security.DefaultPlanTTL),
	}
	record := planRecord{
		Plan:             plan,
		Operation:        "backup",
		Provider:         provider,
		ProviderID:       provider.ID(),
		ProjectID:        firstNonEmpty(req.ProjectID, detail.Summary.Labels["com.docker.compose.project"]),
		VolumeName:       volumeName,
		BackupDirHost:    backupDirHost,
		BackupDirBackend: backupDirBackend,
		ArchiveName:      archiveName,
		ArchivePath:      archivePath,
		MetadataPath:     metadataPath,
		UsingContainers:  containers,
	}
	m.savePlan(record)
	return &plan, nil
}

func (m *Manager) ApplyBackup(ctx context.Context, planID string) (string, error) {
	record, err := m.takePlan(ctx, planID, "")
	if err != nil {
		return "", err
	}
	if record.Operation != "backup" {
		return "", apperror.New(apperror.Conflict, "Plan is not a backup plan")
	}
	jobID := "backup-" + m.newID()
	m.startJob(jobID, func(jobCtx context.Context) {
		_ = m.runBackup(jobCtx, jobID, record)
	})
	return jobID, nil
}

func (m *Manager) RunBackupVolume(ctx context.Context, req models.BackupVolumeRequest) error {
	plan, err := m.PlanBackupVolume(ctx, req)
	if err != nil {
		return err
	}
	record, err := m.takePlan(ctx, plan.PlanID, "")
	if err != nil {
		return err
	}
	if record.Operation != "backup" {
		return apperror.New(apperror.Conflict, "Plan is not a backup plan")
	}
	return m.runBackup(ctx, "backup-"+m.newID(), record)
}

func (m *Manager) PlanRestoreVolume(ctx context.Context, req models.RestoreVolumeRequest) (*models.CommandPlan, error) {
	if m.Docker == nil || m.Providers == nil {
		return nil, notReady()
	}
	provider, err := m.Providers.ActiveProvider(ctx)
	if err != nil {
		return nil, err
	}
	archivePath, metadataPath, err := m.restoreSource(ctx, req)
	if err != nil {
		return nil, err
	}
	sidecar, err := readSidecar(metadataPath)
	if err != nil {
		return nil, err
	}
	if err := verifyArchiveChecksum(archivePath, sidecar.SHA256); err != nil {
		return nil, err
	}
	targetName := strings.TrimSpace(req.VolumeName)
	if targetName == "" {
		targetName = sidecar.Volume
	}
	if targetName == "" {
		return nil, apperror.New(apperror.Conflict, "Target volume name is required")
	}
	target, exists, err := m.getVolumeIfExists(ctx, targetName)
	if err != nil {
		return nil, err
	}
	if req.Overwrite && !exists {
		return nil, apperror.New(apperror.NotFound, "Target volume was not found")
	}
	if !req.Overwrite && exists {
		return nil, apperror.New(apperror.Conflict, "Target volume already exists", apperror.WithDetail(targetName))
	}
	backupDirHost := filepath.Dir(archivePath)
	backupDirBackend, err := provider.MapPathToBackend(backupDirHost)
	if err != nil {
		return nil, err
	}
	risk := models.RiskNeedsConfirmation
	requiresTypedName := ""
	if req.Overwrite {
		risk = models.RiskDangerous
		requiresTypedName = targetName
	}
	commands := []models.PlannedCommand{}
	order := 1
	if !req.Overwrite {
		commands = append(commands, createVolumeCommand(order, targetName, risk))
		order++
	}
	commands = append(commands, restoreCommand(order, targetName, backupDirBackend, filepath.Base(archivePath), risk))
	containers := []string{}
	if target != nil {
		containers = runningContainerNames(target.Containers)
	}
	now := m.now()
	plan := models.CommandPlan{
		PlanID:            security.NewPlanID(),
		Title:             restoreTitle(targetName, req.Overwrite),
		Risk:              risk,
		Commands:          commands,
		Effects:           restoreEffects(targetName, archivePath, req.Overwrite, containers),
		RequiresTypedName: requiresTypedName,
		ExpiresAt:         now.Add(security.DefaultPlanTTL),
	}
	record := planRecord{
		Plan:              plan,
		Operation:         "restore",
		Provider:          provider,
		ProviderID:        provider.ID(),
		ProjectID:         sidecar.Project,
		VolumeName:        sidecar.Volume,
		TargetVolumeName:  targetName,
		BackupDirHost:     backupDirHost,
		BackupDirBackend:  backupDirBackend,
		ArchiveName:       filepath.Base(archivePath),
		ArchivePath:       archivePath,
		MetadataPath:      metadataPath,
		Overwrite:         req.Overwrite,
		CreateTargetFirst: !req.Overwrite,
		Sidecar:           sidecar,
	}
	m.savePlan(record)
	return &plan, nil
}

func (m *Manager) ApplyRestore(ctx context.Context, planID string, typedName string) (string, error) {
	record, err := m.takePlan(ctx, planID, typedName)
	if err != nil {
		return "", err
	}
	if record.Operation != "restore" {
		return "", apperror.New(apperror.Conflict, "Plan is not a restore plan")
	}
	jobID := "restore-" + m.newID()
	m.startJob(jobID, func(jobCtx context.Context) {
		m.runRestore(jobCtx, jobID, record)
	})
	return jobID, nil
}

func (m *Manager) PlanDeleteBackup(ctx context.Context, backupID string) (*models.CommandPlan, error) {
	if m.Backups == nil {
		return nil, notReady()
	}
	record, err := m.Backups.Get(ctx, backupID)
	if err != nil {
		return nil, apperror.Wrap(apperror.NotFound, "Backup was not found", err)
	}
	now := m.now()
	plan := models.CommandPlan{
		PlanID:    security.NewPlanID(),
		Title:     "Delete backup " + record.ID,
		Risk:      models.RiskNeedsConfirmation,
		ExpiresAt: now.Add(security.DefaultPlanTTL),
		Commands: []models.PlannedCommand{
			{
				Order:       1,
				Command:     "delete backup " + record.BackupPath,
				Risk:        models.RiskNeedsConfirmation,
				Explanation: "Deletes the selected backup archive and metadata from disk.",
			},
		},
		Effects: []string{
			"Backup " + record.ID + " will be removed from Cairn and deleted from disk.",
			"Archive: " + record.BackupPath,
			"Metadata: " + record.MetadataPath,
		},
	}
	m.savePlan(planRecord{
		Plan:         plan,
		Operation:    "delete",
		ProviderID:   record.ProviderID,
		ProjectID:    record.ProjectID,
		VolumeName:   record.VolumeName,
		ArchivePath:  record.BackupPath,
		MetadataPath: record.MetadataPath,
		BackupID:     record.ID,
	})
	return &plan, nil
}

func (m *Manager) ApplyDeleteBackup(ctx context.Context, planID string) error {
	record, err := m.takePlan(ctx, planID, "")
	if err != nil {
		return err
	}
	if record.Operation != "delete" {
		return apperror.New(apperror.Conflict, "Plan is not a backup delete plan")
	}
	return m.deleteBackupRecord(ctx, record)
}

func (m *Manager) ListBackups(ctx context.Context, filter models.BackupFilter) ([]models.BackupSummary, error) {
	if m.Backups == nil {
		return []models.BackupSummary{}, nil
	}
	records, err := m.Backups.List(ctx, filter)
	if err != nil {
		return nil, err
	}
	out := make([]models.BackupSummary, 0, len(records))
	for _, record := range records {
		out = append(out, backupSummary(record))
	}
	return out, nil
}

func (m *Manager) DeleteBackup(ctx context.Context, backupID string) error {
	backupID = strings.TrimSpace(backupID)
	err := apperror.New(
		apperror.ConfirmationRequired,
		"Backup delete requires a confirmed plan",
		apperror.WithDetail("Call PlanDeleteBackup and ApplyDeleteBackup before deleting backups."),
	)
	_ = m.recordAudit(ctx, "backup.delete", "backup", backupID, "", "", "delete backup "+backupID, models.RiskNeedsConfirmation, "failed", 0, err)
	return err
}

func (m *Manager) deleteBackupRecord(ctx context.Context, record planRecord) error {
	if m.Backups == nil {
		return notReady()
	}
	started := m.now()
	command := "delete backup " + record.ArchivePath
	if err := m.recordAudit(ctx, "backup.delete", "backup", record.BackupID, record.ProviderID, record.ProjectID, command, models.RiskNeedsConfirmation, "started", 0, nil); err != nil {
		return err
	}
	err := m.Backups.Delete(ctx, record.BackupID)
	duration := time.Since(started)
	if err != nil {
		_ = m.recordAudit(ctx, "backup.delete", "backup", record.BackupID, record.ProviderID, record.ProjectID, command, models.RiskNeedsConfirmation, "failed", duration, err)
		return err
	}
	err = removeBackupArtifacts(record.ArchivePath, record.MetadataPath)
	duration = time.Since(started)
	if err != nil {
		_ = m.recordAudit(ctx, "backup.delete", "backup", record.BackupID, record.ProviderID, record.ProjectID, command, models.RiskNeedsConfirmation, "failed", duration, err)
		return err
	}
	return m.recordAudit(ctx, "backup.delete", "backup", record.BackupID, record.ProviderID, record.ProjectID, command, models.RiskNeedsConfirmation, "success", duration, nil)
}

func (m *Manager) runBackup(ctx context.Context, jobID string, record planRecord) error {
	started := m.now()
	command := plannedCommandText(record.Plan)
	_ = m.recordAudit(ctx, "backup.volume", "volume", record.VolumeName, record.ProviderID, record.ProjectID, command, record.Plan.Risk, "started", 0, nil)
	m.publishProgress(jobID, "backup", "Starting volume backup", nil)
	provider, err := m.planProvider(ctx, record)
	if err == nil {
		err = runProviderDocker(ctx, provider, dockerRunBackupArgs(record.VolumeName, record.BackupDirBackend, record.ArchiveName)...)
	}
	duration := time.Since(started)
	if err != nil {
		_ = m.insertBackupRecord(ctx, record, backupResultFailed, 0, err)
		_ = m.recordAudit(ctx, "backup.volume", "volume", record.VolumeName, record.ProviderID, record.ProjectID, command, record.Plan.Risk, "failed", duration, err)
		m.publishDone(jobID, "", err)
		return err
	}
	sum, size, err := fileSHA256(record.ArchivePath)
	if err != nil {
		_ = m.insertBackupRecord(ctx, record, backupResultFailed, 0, err)
		_ = m.recordAudit(ctx, "backup.volume", "volume", record.VolumeName, record.ProviderID, record.ProjectID, command, record.Plan.Risk, "failed", duration, err)
		m.publishDone(jobID, "", err)
		return err
	}
	contextName, _ := provider.DockerContext(ctx)
	sidecar := BackupSidecar{
		FormatVersion:        formatVersion,
		Volume:               record.VolumeName,
		Project:              record.ProjectID,
		UsingContainers:      record.UsingContainers,
		CreatedAt:            started,
		CompressedSizeBytes:  size,
		SHA256:               sum,
		DockerContext:        contextName,
		Provider:             provider.ID(),
		CairnVersion:         m.Version,
		ArchiveFormatVersion: formatVersion,
	}
	if err := writeSidecar(record.MetadataPath, sidecar); err != nil {
		if cleanupErr := removeBackupArtifacts(record.ArchivePath, record.MetadataPath); cleanupErr != nil {
			err = errors.Join(err, apperror.Wrap(apperror.Internal, "Clean up failed backup artifacts failed", cleanupErr))
		}
		_ = m.insertBackupRecord(ctx, record, backupResultFailed, size, err)
		_ = m.recordAudit(ctx, "backup.volume", "volume", record.VolumeName, record.ProviderID, record.ProjectID, command, record.Plan.Risk, "failed", duration, err)
		m.publishDone(jobID, "", err)
		return err
	}
	record.Sidecar = sidecar
	if err := m.insertBackupRecord(ctx, record, backupResultOK, size, nil); err != nil {
		_ = m.recordAudit(ctx, "backup.volume", "volume", record.VolumeName, record.ProviderID, record.ProjectID, command, record.Plan.Risk, "failed", duration, err)
		m.publishDone(jobID, "", err)
		return err
	}
	m.publishProgress(jobID, "backup", "Volume backup complete", floatPtr(100))
	_ = m.recordAudit(ctx, "backup.volume", "volume", record.VolumeName, record.ProviderID, record.ProjectID, command, record.Plan.Risk, "success", duration, nil)
	m.publishDone(jobID, record.ArchivePath, nil)
	return nil
}

func (m *Manager) runRestore(ctx context.Context, jobID string, record planRecord) {
	started := m.now()
	command := plannedCommandText(record.Plan)
	_ = m.recordAudit(ctx, "backup.restore", "volume", record.TargetVolumeName, record.ProviderID, record.ProjectID, command, record.Plan.Risk, "started", 0, nil)
	m.publishProgress(jobID, "restore", "Starting volume restore", nil)
	provider, err := m.planProvider(ctx, record)
	if err == nil && record.CreateTargetFirst {
		err = runProviderDocker(ctx, provider, "volume", "create", record.TargetVolumeName)
	}
	if err == nil {
		err = verifyArchiveChecksum(record.ArchivePath, record.Sidecar.SHA256)
	}
	if err == nil {
		err = runProviderDocker(ctx, provider, dockerRunRestoreArgs(record.TargetVolumeName, record.BackupDirBackend, record.ArchiveName)...)
	}
	duration := time.Since(started)
	if err != nil {
		_ = m.recordAudit(ctx, "backup.restore", "volume", record.TargetVolumeName, record.ProviderID, record.ProjectID, command, record.Plan.Risk, "failed", duration, err)
		m.publishDone(jobID, "", err)
		return
	}
	m.publishProgress(jobID, "restore", "Volume restore complete", floatPtr(100))
	m.publishVolumeChanged(record.TargetVolumeName)
	_ = m.recordAudit(ctx, "backup.restore", "volume", record.TargetVolumeName, record.ProviderID, record.ProjectID, command, record.Plan.Risk, "success", duration, nil)
	m.publishDone(jobID, record.TargetVolumeName, nil)
}

func (m *Manager) startJob(jobID string, run func(context.Context)) {
	base := context.Background()
	m.jobsMu.Lock()
	if m.rootCtx != nil {
		base = m.rootCtx
	}
	ctx, cancel := context.WithCancel(base)
	if m.jobs == nil {
		m.jobs = map[string]context.CancelFunc{}
	}
	m.jobs[jobID] = cancel
	m.jobsMu.Unlock()

	go func() {
		defer cancel()
		defer m.forgetJob(jobID)
		run(ctx)
	}()
}

func (m *Manager) forgetJob(jobID string) {
	m.jobsMu.Lock()
	defer m.jobsMu.Unlock()
	delete(m.jobs, jobID)
}

func (m *Manager) savePlan(record planRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.plans[record.Plan.PlanID] = record
}

func (m *Manager) takePlan(ctx context.Context, planID string, typedName string) (planRecord, error) {
	if err := ctx.Err(); err != nil {
		return planRecord{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	record, ok := m.plans[planID]
	if !ok {
		return planRecord{}, apperror.New(apperror.PlanExpired, "Plan expired or was not found")
	}
	if m.now().After(record.Plan.ExpiresAt) {
		delete(m.plans, planID)
		return planRecord{}, apperror.New(apperror.PlanExpired, "Plan expired")
	}
	if err := security.RequireConfirmation(record.Plan, typedName); err != nil {
		return planRecord{}, err
	}
	delete(m.plans, planID)
	return record, nil
}

func (m *Manager) backupDir(ctx context.Context, provider providers.PlatformProvider, requested string) (string, string, error) {
	hostPath := strings.TrimSpace(requested)
	if hostPath == "" && m.Settings != nil {
		value, err := m.Settings.GetString(ctx, "backups.directory")
		if err == nil {
			hostPath = strings.TrimSpace(value)
		}
	}
	if hostPath == "" {
		hostPath = defaultBackupDirectory()
	}
	hostPath = filepath.Clean(hostPath)
	backendPath, err := provider.MapPathToBackend(hostPath)
	if err != nil {
		return "", "", err
	}
	return hostPath, backendPath, nil
}

func (m *Manager) checkFreeSpace(path string, estimatedBytes int64) error {
	if estimatedBytes <= 0 || m.AvailableBytes == nil {
		return nil
	}
	free, ok := m.AvailableBytes(path)
	if !ok {
		return nil
	}
	if free < uint64(estimatedBytes) {
		return apperror.New(
			apperror.Conflict,
			"Backup directory does not have enough free space",
			apperror.WithDetail(fmt.Sprintf("need at least %d bytes, available %d", estimatedBytes, free)),
		)
	}
	return nil
}

func (m *Manager) restoreSource(ctx context.Context, req models.RestoreVolumeRequest) (string, string, error) {
	if strings.TrimSpace(req.BackupID) != "" {
		if m.Backups == nil {
			return "", "", notReady()
		}
		record, err := m.Backups.Get(ctx, strings.TrimSpace(req.BackupID))
		if err != nil {
			return "", "", apperror.Wrap(apperror.NotFound, "Backup was not found", err)
		}
		return record.BackupPath, firstNonEmpty(record.MetadataPath, metadataPathForArchive(record.BackupPath)), nil
	}
	archivePath := filepath.Clean(strings.TrimSpace(req.SourcePath))
	if archivePath == "." || archivePath == "" {
		return "", "", apperror.New(apperror.Conflict, "Backup archive path is required")
	}
	return archivePath, metadataPathForArchive(archivePath), nil
}

func (m *Manager) getVolumeIfExists(ctx context.Context, name string) (*models.VolumeDetail, bool, error) {
	detail, err := m.Docker.GetVolume(ctx, name)
	if err == nil {
		return detail, true, nil
	}
	if apperror.IsCode(err, apperror.NotFound) {
		return nil, false, nil
	}
	return nil, false, err
}

func (m *Manager) insertBackupRecord(ctx context.Context, record planRecord, result string, size int64, actionErr error) error {
	if m.Backups == nil {
		return nil
	}
	errText := ""
	if actionErr != nil {
		errText = actionErr.Error()
	}
	return m.Backups.Insert(ctx, store.BackupRecord{
		ID:                  "backup-" + m.newID(),
		ProviderID:          record.ProviderID,
		ProjectID:           record.ProjectID,
		VolumeName:          record.VolumeName,
		BackupPath:          record.ArchivePath,
		MetadataPath:        record.MetadataPath,
		CompressedSizeBytes: size,
		Result:              result,
		CreatedAt:           m.now(),
		Error:               errText,
	})
}

func (m *Manager) planProvider(ctx context.Context, record planRecord) (providers.PlatformProvider, error) {
	if record.Provider != nil {
		return record.Provider, nil
	}
	if m.Providers == nil {
		return nil, notReady()
	}
	return m.Providers.ActiveProvider(ctx)
}

func (m *Manager) recordAudit(ctx context.Context, action string, targetType string, targetID string, providerID string, projectID string, command string, risk models.Risk, status string, duration time.Duration, actionErr error) error {
	if m.Audit == nil {
		return nil
	}
	var exitCode *int
	if status == "success" {
		code := 0
		exitCode = &code
	}
	message := ""
	if actionErr != nil {
		message = actionErr.Error()
	}
	_, err := m.Audit.Insert(ctx, store.AuditRecord{
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		ProviderID: firstNonEmpty(providerID, m.providerID(), targetID),
		ProjectID:  projectID,
		Command:    command,
		Risk:       risk,
		Status:     status,
		ExitCode:   exitCode,
		Duration:   duration,
		Error:      message,
		CreatedAt:  m.now(),
	})
	if err != nil {
		return apperror.Wrap(apperror.Internal, "Record audit entry failed", err)
	}
	return nil
}

func (m *Manager) publishProgress(jobID string, phase string, message string, pct *float64) {
	if m.Events == nil {
		return
	}
	m.Events.Publish(bus.Event{Topic: bus.TopicJobProgress, Payload: jobProgressPayload{
		JobID: jobID, Phase: phase, Message: message, Pct: pct,
	}})
}

func (m *Manager) publishDone(jobID string, result string, actionErr error) {
	if m.Events == nil {
		return
	}
	payload := jobDonePayload{JobID: jobID, Result: result}
	if actionErr != nil {
		payload.Error = actionErr.Error()
	}
	m.Events.Publish(bus.Event{Topic: bus.TopicJobDone, Payload: payload})
}

func (m *Manager) publishVolumeChanged(name string) {
	if m.Events == nil {
		return
	}
	m.Events.Publish(bus.Event{Topic: bus.TopicObjectsChanged, Payload: objectsChangedPayload{
		Kind: "volume",
		IDs:  []string{name},
	}})
}

func (m *Manager) now() time.Time {
	if m.Now == nil {
		return time.Now().UTC()
	}
	return m.Now().UTC()
}

func (m *Manager) newID() string {
	if m.NewID == nil {
		return uuid.NewString()
	}
	return m.NewID()
}

func (m *Manager) providerID() string {
	if m.Docker == nil {
		return ""
	}
	return m.Docker.ProviderID()
}

func runProviderDocker(ctx context.Context, provider providers.PlatformProvider, args ...string) error {
	result, err := provider.RunDocker(ctx, args...)
	if err != nil {
		return apperror.Wrap(apperror.DockerUnreachable, "Docker helper command failed", err, commandDetail(result))
	}
	if result != nil && result.ExitCode != 0 {
		return apperror.New(apperror.DockerUnreachable, "Docker helper command failed", commandDetail(result))
	}
	return nil
}

func commandDetail(result *providers.CommandResult) apperror.Option {
	if result == nil {
		return apperror.WithDetail("")
	}
	detail := strings.TrimSpace(result.Stderr)
	if detail == "" {
		detail = strings.TrimSpace(result.Stdout)
	}
	return apperror.WithDetail(detail)
}

func backupCommand(order int, volumeName string, backupDir string, archiveName string, risk models.Risk) models.PlannedCommand {
	return models.PlannedCommand{
		Order:       order,
		Command:     shellJoin(append([]string{"docker"}, dockerRunBackupArgs(volumeName, backupDir, archiveName)...)),
		Risk:        risk,
		Explanation: "Runs a temporary Alpine helper container that archives the named volume as tar.gz.",
	}
}

func createVolumeCommand(order int, volumeName string, risk models.Risk) models.PlannedCommand {
	return models.PlannedCommand{
		Order:       order,
		Command:     shellJoin([]string{"docker", "volume", "create", volumeName}),
		Risk:        risk,
		Explanation: "Creates the target volume before restoring backup contents.",
	}
}

func restoreCommand(order int, targetName string, backupDir string, archiveName string, risk models.Risk) models.PlannedCommand {
	return models.PlannedCommand{
		Order:       order,
		Command:     shellJoin(append([]string{"docker"}, dockerRunRestoreArgs(targetName, backupDir, archiveName)...)),
		Risk:        risk,
		Explanation: "Runs a temporary Alpine helper container that moves existing contents aside, extracts the backup archive, and restores the original contents if extraction fails.",
	}
}

func dockerRunBackupArgs(volumeName string, backupDir string, archiveName string) []string {
	return []string{
		"run", "--rm",
		"-v", volumeName + ":/source:ro",
		"-v", backupDir + ":/backup",
		helperImage,
		"tar", "czf", "/backup/" + archiveName,
		"-C", "/source", ".",
	}
}

func dockerRunRestoreArgs(targetName string, backupDir string, archiveName string) []string {
	return []string{
		"run", "--rm",
		"-v", targetName + ":/restore",
		"-v", backupDir + ":/backup:ro",
		helperImage,
		"sh", "-c",
		restoreHelperScript,
		"cairn-restore",
		"/backup/" + archiveName,
	}
}

const restoreHelperScript = `set -eu
archive=$1
stash_name=".cairn-restore-old-$$"
stash="/restore/$stash_name"
mkdir "$stash"
find /restore -mindepth 1 -maxdepth 1 ! -name "$stash_name" -exec sh -c 'stash=$1; shift; for path do mv "$path" "$stash"/; done' sh "$stash" {} +
if tar xzf "$archive" -C /restore; then
  rm -rf "$stash"
else
  find /restore -mindepth 1 -maxdepth 1 ! -name "$stash_name" -exec rm -rf {} +
  find "$stash" -mindepth 1 -maxdepth 1 -exec sh -c 'dest=$1; shift; for path do mv "$path" "$dest"/; done' sh /restore {} +
  rmdir "$stash"
  exit 1
fi`

func backupEffects(volumeName string, archivePath string, metadataPath string, running []string) []string {
	effects := []string{
		"Creates compressed backup archive for volume " + volumeName + ".",
		"Writes metadata sidecar " + metadataPath + ".",
		"Destination: " + archivePath,
	}
	if len(running) > 0 {
		effects = append(effects, "Consistency warning: running containers currently use this volume: "+strings.Join(running, ", ")+". Stop the project first for database-consistent backups.")
	}
	return effects
}

func restoreEffects(targetName string, archivePath string, overwrite bool, running []string) []string {
	effects := []string{
		"Restores archive " + archivePath + " into volume " + targetName + ".",
	}
	if overwrite {
		effects = append(effects, "Existing contents of "+targetName+" are moved aside during extraction and restored automatically if extraction fails.")
	} else {
		effects = append(effects, "Creates a new volume named "+targetName+".")
	}
	if len(running) > 0 {
		effects = append(effects, "Consistency warning: running containers currently use the target volume: "+strings.Join(running, ", ")+".")
	}
	return effects
}

func restoreTitle(targetName string, overwrite bool) string {
	if overwrite {
		return "Restore over " + targetName
	}
	return "Restore into " + targetName
}

func backupPaths(dir string, volumeName string, ts time.Time) (string, string, string, error) {
	return backupPathsWithStat(dir, volumeName, ts, os.Stat, maxBackupPathAttempts)
}

func backupPathsWithStat(dir string, volumeName string, ts time.Time, stat func(string) (os.FileInfo, error), maxAttempts int) (string, string, string, error) {
	base := sanitizeFilename(volumeName)
	if base == "" {
		base = "volume"
	}
	stamp := ts.UTC().Format(backupTimestampLayout)
	for i := 0; i < maxAttempts; i++ {
		suffix := ""
		if i > 0 {
			suffix = fmt.Sprintf("-%d", i+1)
		}
		archive := fmt.Sprintf("%s-%s%s.tar.gz", base, stamp, suffix)
		archivePath := filepath.Join(dir, archive)
		metadataPath := metadataPathForArchive(archivePath)
		if err := requirePathAvailable(archivePath, stat); err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return "", "", "", err
		}
		if err := requirePathAvailable(metadataPath, stat); err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return "", "", "", err
		}
		return archive, archivePath, metadataPath, nil
	}
	return "", "", "", apperror.New(apperror.Conflict, "Could not allocate a unique backup filename")
}

func requirePathAvailable(path string, stat func(string) (os.FileInfo, error)) error {
	if _, err := stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return apperror.Wrap(apperror.Internal, "Check backup path failed", err)
	}
	return os.ErrExist
}

func sanitizeFilename(value string) string {
	value = strings.TrimSpace(value)
	value = safeFilenamePattern.ReplaceAllString(value, "-")
	value = strings.Trim(value, ".-")
	if len(value) > 80 {
		value = value[:80]
	}
	return value
}

func metadataPathForArchive(archivePath string) string {
	if strings.HasSuffix(archivePath, ".tar.gz") {
		return strings.TrimSuffix(archivePath, ".tar.gz") + ".json"
	}
	return archivePath + ".json"
}

func readSidecar(path string) (BackupSidecar, error) {
	file, err := os.Open(path)
	if err != nil {
		return BackupSidecar{}, apperror.Wrap(apperror.NotFound, "Open backup metadata failed", err)
	}
	defer func() {
		_ = file.Close()
	}()
	var sidecar BackupSidecar
	if err := json.NewDecoder(file).Decode(&sidecar); err != nil {
		return BackupSidecar{}, apperror.Wrap(apperror.Conflict, "Backup metadata is invalid", err)
	}
	if sidecar.FormatVersion != formatVersion {
		return BackupSidecar{}, apperror.New(apperror.Conflict, "Backup metadata format is unsupported")
	}
	return sidecar, nil
}

func writeSidecar(path string, sidecar BackupSidecar) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return apperror.Wrap(apperror.Internal, "Create backup metadata failed", err)
	}
	defer func() {
		_ = file.Close()
	}()
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(sidecar); err != nil {
		return apperror.Wrap(apperror.Internal, "Write backup metadata failed", err)
	}
	return nil
}

func verifyArchiveChecksum(path string, want string) error {
	got, _, err := fileSHA256(path)
	if err != nil {
		return err
	}
	if !strings.EqualFold(got, strings.TrimSpace(want)) {
		return apperror.New(apperror.Conflict, "Backup archive checksum does not match metadata")
	}
	return nil
}

func fileSHA256(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, apperror.Wrap(apperror.NotFound, "Open backup archive failed", err)
	}
	defer func() {
		_ = file.Close()
	}()
	hash := sha256.New()
	n, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, apperror.Wrap(apperror.Internal, "Read backup archive failed", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), n, nil
}

func removeBackupFiles(record store.BackupRecord) error {
	return removeBackupArtifacts(record.BackupPath, record.MetadataPath)
}

func removeBackupArtifacts(archivePath string, metadataPath string) error {
	return errors.Join(removeFileIfExists(archivePath), removeFileIfExists(metadataPath))
}

func removeFileIfExists(path string) error {
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func backupSummary(record store.BackupRecord) models.BackupSummary {
	return models.BackupSummary{
		ID:           record.ID,
		ProviderID:   record.ProviderID,
		VolumeName:   record.VolumeName,
		ProjectID:    record.ProjectID,
		Path:         record.BackupPath,
		MetadataPath: record.MetadataPath,
		SizeBytes:    record.CompressedSizeBytes,
		Result:       record.Result,
		Error:        record.Error,
		CreatedAt:    record.CreatedAt,
	}
}

func defaultBackupDirectory() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "Cairn Backups")
	}
	return filepath.Join(home, "Cairn Backups")
}

func runningContainerNames(containers []models.ContainerSummary) []string {
	names := []string{}
	for _, container := range containers {
		state := strings.ToLower(firstNonEmpty(container.State, container.Status))
		if state != "running" && state != "restarting" {
			continue
		}
		name := container.Name
		if name == "" {
			name = container.ID
		}
		names = append(names, name)
	}
	return names
}

func plannedCommandText(plan models.CommandPlan) string {
	parts := make([]string, 0, len(plan.Commands))
	for _, command := range plan.Commands {
		parts = append(parts, command.Command)
	}
	return strings.Join(parts, "\n")
}

func shellJoin(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "" {
			quoted = append(quoted, "''")
			continue
		}
		if strings.ContainsAny(arg, " \t\n\"'`;()[]{}$&|<>*?!") {
			quoted = append(quoted, "'"+strings.ReplaceAll(arg, "'", "'\"'\"'")+"'")
		} else {
			quoted = append(quoted, arg)
		}
	}
	return strings.Join(quoted, " ")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func floatPtr(value float64) *float64 {
	return &value
}

func notReady() error {
	return apperror.New(apperror.ProviderNotReady, "Backup engine is not ready")
}
