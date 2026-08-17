package backups

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
	"github.com/RCooLeR/Cairn/internal/runtimescope"
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
	stagingArchiveName    = "archive.tar.gz"
	stagingMetadataName   = "archive.json"
	stagingOwnerName      = ".cairn-owner"
	reservationSuffix     = ".cairn-reservation"
	maxReservationBytes   = 256
	maxSidecarBytes       = 64 * 1024
	maxSidecarNameBytes   = 255
	maxSidecarFieldBytes  = 1024
	maxSidecarContainers  = 128
	restoreCleanupTimeout = 15 * time.Second
	restoreOwnerLabel     = "io.cairn.restore.owner"
	maxPendingBackupPlans = 128
)

type ProviderResolver interface {
	ActiveProvider(context.Context) (providers.PlatformProvider, error)
}

type DockerClient interface {
	ProviderID() string
	GetVolume(context.Context, string) (*models.VolumeDetail, error)
	CreateVolume(context.Context, models.CreateVolumeRequest) (*models.VolumeSummary, error)
}

type sidecarFile interface {
	io.Writer
	Sync() error
	Close() error
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
	IDs       *security.IDSource
	Version   string

	AvailableBytes func(string) (uint64, bool)

	mu             sync.Mutex
	plans          map[string]planRecord
	planGeneration uint64

	jobsMu  sync.Mutex
	rootCtx context.Context
	jobs    map[string]context.CancelFunc
	jobsWG  sync.WaitGroup
	stopped bool
}

type planRecord struct {
	Plan                    models.CommandPlan
	Operation               string
	Provider                providers.PlatformProvider
	ProviderID              string
	Scope                   runtimescope.Scope
	ProjectID               string
	VolumeName              string
	TargetVolumeName        string
	TargetVolumeFingerprint string
	RestoreOwnerToken       string
	BackupDirHost           string
	BackupDirBackend        string
	ArchiveName             string
	ArchivePath             string
	ArchiveIdentity         os.FileInfo
	ArchiveHandle           *os.File
	MetadataPath            string
	MetadataIdentity        os.FileInfo
	MetadataHandle          *os.File
	ReservationPath         string
	ReservationOwner        string
	ReservationIdentity     os.FileInfo
	StagingDirHost          string
	StagingDirBackend       string
	StagingDirIdentity      os.FileInfo
	StagingArchivePath      string
	StagingArchiveIdentity  os.FileInfo
	StagingMetadataPath     string
	StagingMetadataIdentity os.FileInfo
	StagingOwnerPath        string
	StagingOwnerIdentity    os.FileInfo
	BackupID                string
	UsingContainers         []string
	Overwrite               bool
	CreateTargetFirst       bool
	Sidecar                 BackupSidecar
	expiryTimer             *time.Timer
	generation              uint64
}

type backupReservation struct {
	ArchiveName          string
	ArchivePath          string
	MetadataPath         string
	ReservationPath      string
	ReservationIdentity  os.FileInfo
	Owner                string
	StagingDir           string
	StagingDirIdentity   os.FileInfo
	StagingArchivePath   string
	StagingMetadataPath  string
	StagingOwnerPath     string
	StagingOwnerIdentity os.FileInfo
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
var sidecarVolumePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

var errPreserveBackupReservation = errors.New("preserve backup reservation ownership evidence")

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
	if ctx == nil {
		ctx = context.Background()
	}
	m.jobsMu.Lock()
	if !m.stopped {
		m.rootCtx = ctx
	}
	m.jobsMu.Unlock()
}

func (m *Manager) StopAll() {
	if m == nil {
		return
	}
	m.jobsMu.Lock()
	m.stopped = true
	cancels := make([]context.CancelFunc, 0, len(m.jobs))
	for _, cancel := range m.jobs {
		cancels = append(cancels, cancel)
	}
	m.jobsMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	m.jobsWG.Wait()
	for _, record := range m.discardPlans() {
		_ = releaseBackupReservation(record)
		closePlanArtifactHandles(record)
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
	scope, err := providers.ResolveRuntimeScope(ctx, provider)
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
	planID, err := m.IDs.NewPlanID()
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
	reservation, err := reserveBackupPaths(backupDirHost, volumeName, now, planID)
	if err != nil {
		return nil, err
	}
	stagingDirBackend, err := provider.MapPathToBackend(reservation.StagingDir)
	if err != nil {
		return nil, errors.Join(err, releaseBackupReservationRecord(reservation))
	}
	containers := runningContainerNames(detail.Containers)
	plan := models.CommandPlan{
		PlanID:    planID,
		Title:     "Back up " + volumeName,
		Risk:      models.RiskSafe,
		Commands:  []models.PlannedCommand{backupCommand(1, volumeName, stagingDirBackend, stagingArchiveName, models.RiskSafe)},
		Effects:   backupEffects(volumeName, reservation.ArchivePath, reservation.MetadataPath, containers),
		ExpiresAt: now.Add(security.DefaultPlanTTL),
	}
	record := planRecord{
		Plan:                 plan,
		Operation:            "backup",
		Provider:             provider,
		ProviderID:           provider.ID(),
		Scope:                scope,
		ProjectID:            firstNonEmpty(req.ProjectID, detail.Summary.Labels["com.docker.compose.project"]),
		VolumeName:           volumeName,
		BackupDirHost:        backupDirHost,
		BackupDirBackend:     backupDirBackend,
		ArchiveName:          reservation.ArchiveName,
		ArchivePath:          reservation.ArchivePath,
		MetadataPath:         reservation.MetadataPath,
		ReservationPath:      reservation.ReservationPath,
		ReservationOwner:     reservation.Owner,
		ReservationIdentity:  reservation.ReservationIdentity,
		StagingDirHost:       reservation.StagingDir,
		StagingDirBackend:    stagingDirBackend,
		StagingDirIdentity:   reservation.StagingDirIdentity,
		StagingArchivePath:   reservation.StagingArchivePath,
		StagingMetadataPath:  reservation.StagingMetadataPath,
		StagingOwnerPath:     reservation.StagingOwnerPath,
		StagingOwnerIdentity: reservation.StagingOwnerIdentity,
		UsingContainers:      containers,
	}
	if err := m.savePlan(record); err != nil {
		return nil, err
	}
	return &plan, nil
}

func (m *Manager) ApplyBackup(ctx context.Context, planID string) (string, error) {
	record, err := m.takePlan(ctx, planID, "")
	if err != nil {
		return "", err
	}
	if record.Operation != "backup" {
		operationErr := apperror.New(apperror.Conflict, "Plan is not a backup plan")
		return "", errors.Join(operationErr, m.savePlan(record))
	}
	if err := validateBackupReservation(record); err != nil {
		return "", errors.Join(err, releaseBackupReservation(record))
	}
	jobID := "backup-" + m.newID()
	if !m.startJob(jobID, func(jobCtx context.Context) {
		_ = m.runBackup(jobCtx, jobID, record)
	}) {
		return "", errors.Join(notReady(), releaseBackupReservation(record))
	}
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
		operationErr := apperror.New(apperror.Conflict, "Plan is not a backup plan")
		return errors.Join(operationErr, m.savePlan(record))
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
	scope, err := providers.ResolveRuntimeScope(ctx, provider)
	if err != nil {
		return nil, err
	}
	archivePath, metadataPath, err := m.restoreSource(ctx, req)
	if err != nil {
		return nil, err
	}
	archiveHandle, archiveIdentity, err := openRequiredRestoreArtifact(archivePath, "archive")
	if err != nil {
		return nil, err
	}
	keepArtifactHandles := false
	defer func() {
		if !keepArtifactHandles {
			_ = archiveHandle.Close()
		}
	}()
	metadataHandle, metadataIdentity, err := openRequiredRestoreArtifact(metadataPath, "metadata")
	if err != nil {
		return nil, err
	}
	defer func() {
		if !keepArtifactHandles {
			_ = metadataHandle.Close()
		}
	}()
	sidecar, err := readSidecar(metadataPath)
	if err != nil {
		return nil, err
	}
	if err := verifyPathIdentity(metadataPath, metadataIdentity, false); err != nil {
		return nil, apperror.Wrap(apperror.Conflict, "Backup metadata identity changed while planning restore", err)
	}
	if err := verifyArchiveChecksumWithIdentity(archivePath, archiveIdentity, sidecar.SHA256); err != nil {
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
	targetFingerprint := ""
	if req.Overwrite {
		targetFingerprint, err = security.VolumeIncarnationFingerprint(*target)
		if err != nil {
			return nil, err
		}
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
	containers := []string{}
	if target != nil {
		containers = runningContainerNames(target.Containers)
	}
	now := m.now()
	planID, err := m.IDs.NewPlanID()
	if err != nil {
		return nil, err
	}
	restoreOwnerToken := ""
	if !req.Overwrite {
		restoreOwnerToken, err = m.IDs.NewTypedPlanID("restore-owner")
		if err != nil {
			return nil, err
		}
	}
	commands := []models.PlannedCommand{}
	order := 1
	if !req.Overwrite {
		commands = append(commands, createVolumeCommand(order, targetName, risk))
		order++
	}
	commands = append(commands, restoreCommand(order, targetName, backupDirBackend, filepath.Base(archivePath), risk))
	plan := models.CommandPlan{
		PlanID:            planID,
		Title:             restoreTitle(targetName, req.Overwrite),
		Risk:              risk,
		Commands:          commands,
		Effects:           restoreEffects(targetName, archivePath, req.Overwrite, containers),
		RequiresTypedName: requiresTypedName,
		ExpiresAt:         now.Add(security.DefaultPlanTTL),
	}
	record := planRecord{
		Plan:                    plan,
		Operation:               "restore",
		Provider:                provider,
		ProviderID:              provider.ID(),
		Scope:                   scope,
		ProjectID:               sidecar.Project,
		VolumeName:              sidecar.Volume,
		TargetVolumeName:        targetName,
		TargetVolumeFingerprint: targetFingerprint,
		RestoreOwnerToken:       restoreOwnerToken,
		BackupDirHost:           backupDirHost,
		BackupDirBackend:        backupDirBackend,
		ArchiveName:             filepath.Base(archivePath),
		ArchivePath:             archivePath,
		ArchiveIdentity:         archiveIdentity,
		ArchiveHandle:           archiveHandle,
		MetadataPath:            metadataPath,
		MetadataIdentity:        metadataIdentity,
		MetadataHandle:          metadataHandle,
		Overwrite:               req.Overwrite,
		CreateTargetFirst:       !req.Overwrite,
		Sidecar:                 sidecar,
	}
	if err := m.savePlan(record); err != nil {
		return nil, err
	}
	keepArtifactHandles = true
	return &plan, nil
}

func (m *Manager) ApplyRestore(ctx context.Context, planID string, typedName string) (string, error) {
	record, err := m.takePlan(ctx, planID, typedName)
	if err != nil {
		return "", err
	}
	if record.Operation != "restore" {
		operationErr := apperror.New(apperror.Conflict, "Plan is not a restore plan")
		return "", errors.Join(operationErr, m.savePlan(record))
	}
	if err := m.validateRestorePlan(ctx, record, record.Overwrite); err != nil {
		closePlanArtifactHandles(record)
		return "", err
	}
	jobID := "restore-" + m.newID()
	if !m.startJob(jobID, func(jobCtx context.Context) {
		defer closePlanArtifactHandles(record)
		m.runRestore(jobCtx, jobID, record)
	}) {
		closePlanArtifactHandles(record)
		return "", notReady()
	}
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
	archiveHandle, archiveIdentity, err := openPlannedBackupArtifact(record.BackupPath, "archive")
	if err != nil {
		return nil, err
	}
	keepArtifactHandles := false
	defer func() {
		if !keepArtifactHandles && archiveHandle != nil {
			_ = archiveHandle.Close()
		}
	}()
	metadataHandle, metadataIdentity, err := openPlannedBackupArtifact(record.MetadataPath, "metadata")
	if err != nil {
		return nil, err
	}
	defer func() {
		if !keepArtifactHandles && metadataHandle != nil {
			_ = metadataHandle.Close()
		}
	}()
	if archiveIdentity != nil && metadataIdentity != nil && os.SameFile(archiveIdentity, metadataIdentity) {
		return nil, apperror.New(
			apperror.Conflict,
			"Backup archive and metadata resolve to the same file",
			apperror.WithDetail(record.BackupPath),
		)
	}
	now := m.now()
	planID, err := m.IDs.NewPlanID()
	if err != nil {
		return nil, err
	}
	plan := models.CommandPlan{
		PlanID:    planID,
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
	if err := m.savePlan(planRecord{
		Plan:             plan,
		Operation:        "delete",
		ProviderID:       record.ProviderID,
		ProjectID:        record.ProjectID,
		VolumeName:       record.VolumeName,
		ArchivePath:      record.BackupPath,
		ArchiveIdentity:  archiveIdentity,
		ArchiveHandle:    archiveHandle,
		MetadataPath:     record.MetadataPath,
		MetadataIdentity: metadataIdentity,
		MetadataHandle:   metadataHandle,
		BackupID:         record.ID,
	}); err != nil {
		return nil, err
	}
	keepArtifactHandles = true
	return &plan, nil
}

func (m *Manager) ApplyDeleteBackup(ctx context.Context, planID string) error {
	record, err := m.takePlan(ctx, planID, "")
	if err != nil {
		return err
	}
	if record.Operation != "delete" {
		operationErr := apperror.New(apperror.Conflict, "Plan is not a backup delete plan")
		return errors.Join(operationErr, m.savePlan(record))
	}
	defer closePlanArtifactHandles(record)
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
	err := removePlannedBackupArtifacts(record)
	duration := time.Since(started)
	if err != nil {
		_ = m.recordAudit(ctx, "backup.delete", "backup", record.BackupID, record.ProviderID, record.ProjectID, command, models.RiskNeedsConfirmation, "failed", duration, err)
		return err
	}
	err = m.Backups.Delete(ctx, record.BackupID)
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
	if err := validateBackupReservation(record); err != nil {
		return m.finishBackupFailure(ctx, jobID, started, command, record, 0, err)
	}
	provider, err := m.planProvider(ctx, record)
	if err == nil {
		err = runProviderDocker(ctx, provider, dockerRunBackupArgs(record.VolumeName, record.StagingDirBackend, stagingArchiveName)...)
	}
	archiveIdentity, identityErr := optionalRegularFileIdentity(record.StagingArchivePath)
	record.StagingArchiveIdentity = archiveIdentity
	if identityErr != nil {
		err = errors.Join(err, apperror.Wrap(apperror.Conflict, "Capture staged backup archive ownership failed", identityErr))
	}
	if err != nil {
		return m.finishBackupFailure(ctx, jobID, started, command, record, 0, err)
	}
	sum, size, err := fileSHA256WithIdentity(record.StagingArchivePath, record.StagingArchiveIdentity)
	if err != nil {
		return m.finishBackupFailure(ctx, jobID, started, command, record, 0, err)
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
	if err := writeSidecar(record.StagingMetadataPath, sidecar); err != nil {
		return m.finishBackupFailure(ctx, jobID, started, command, record, size, err)
	}
	record.StagingMetadataIdentity, err = regularFileIdentity(record.StagingMetadataPath)
	if err != nil {
		return m.finishBackupFailure(ctx, jobID, started, command, record, size, err)
	}
	metadataSum, _, err := fileSHA256WithIdentity(record.StagingMetadataPath, record.StagingMetadataIdentity)
	if err != nil {
		return m.finishBackupFailure(ctx, jobID, started, command, record, size, err)
	}
	if err := publishStagedBackupFile(record.StagingArchivePath, record.ArchivePath, record.StagingArchiveIdentity); err != nil {
		if errors.Is(err, errPreserveBackupReservation) {
			return m.recordBackupFailure(ctx, jobID, started, command, record, size, err)
		}
		return m.finishBackupFailure(ctx, jobID, started, command, record, size, err)
	}
	if err := publishStagedBackupFile(record.StagingMetadataPath, record.MetadataPath, record.StagingMetadataIdentity); err != nil {
		preserveEvidence := errors.Is(err, errPreserveBackupReservation)
		if cleanupErr := removePublishedBackupFileWithIdentity(record.StagingArchiveIdentity, record.ArchivePath); cleanupErr != nil {
			err = errors.Join(err, apperror.Wrap(apperror.Internal, "Clean up published backup archive failed", cleanupErr))
			preserveEvidence = true
		}
		if preserveEvidence {
			return m.recordBackupFailure(ctx, jobID, started, command, record, size, err)
		}
		return m.finishBackupFailure(ctx, jobID, started, command, record, size, err)
	}
	if err := verifyPublishedBackupPair(record, sum, metadataSum); err != nil {
		if cleanupErr := removePublishedBackupPair(record); cleanupErr != nil {
			err = errors.Join(err, apperror.Wrap(apperror.Internal, "Clean up invalid published backup failed", cleanupErr))
			return m.recordBackupFailure(ctx, jobID, started, command, record, size, err)
		}
		return m.finishBackupFailure(ctx, jobID, started, command, record, size, err)
	}
	record.Sidecar = sidecar
	backupID, err := m.insertBackupRecord(ctx, record, backupResultOK, size, nil)
	if err != nil {
		if cleanupErr := cleanupUntrackedPublishedBackup(record); cleanupErr != nil {
			err = errors.Join(err, apperror.Wrap(apperror.Internal, "Clean up untracked published backup failed", cleanupErr))
			duration := time.Since(started)
			_ = m.recordAudit(ctx, "backup.volume", "volume", record.VolumeName, record.ProviderID, record.ProjectID, command, record.Plan.Risk, "failed", duration, err)
			m.publishDone(jobID, "", err)
			return err
		}
		duration := time.Since(started)
		_ = m.recordAudit(ctx, "backup.volume", "volume", record.VolumeName, record.ProviderID, record.ProjectID, command, record.Plan.Risk, "failed", duration, err)
		m.publishDone(jobID, "", err)
		return err
	}
	if cleanupErr := releaseBackupReservation(record); cleanupErr != nil {
		err = apperror.Wrap(
			apperror.Internal,
			"Backup was created but its reservation cleanup failed",
			cleanupErr,
			apperror.WithPartialResource("backup", firstNonEmpty(backupID, record.ArchivePath), "created", true),
			apperror.WithRepairHints("The backup archive and metadata are valid and recorded. Remove only the matching hidden reservation and staging files after verifying ownership."),
		)
		duration := time.Since(started)
		_ = m.recordAudit(ctx, "backup.volume", "volume", record.VolumeName, record.ProviderID, record.ProjectID, command, record.Plan.Risk, "failed", duration, err)
		m.publishDone(jobID, "", err)
		return err
	}
	duration := time.Since(started)
	m.publishProgress(jobID, "backup", "Volume backup complete", floatPtr(100))
	_ = m.recordAudit(ctx, "backup.volume", "volume", record.VolumeName, record.ProviderID, record.ProjectID, command, record.Plan.Risk, "success", duration, nil)
	m.publishDone(jobID, record.ArchivePath, nil)
	return nil
}

func (m *Manager) finishBackupFailure(ctx context.Context, jobID string, started time.Time, command string, record planRecord, size int64, actionErr error) error {
	if cleanupErr := releaseBackupReservation(record); cleanupErr != nil {
		actionErr = errors.Join(actionErr, apperror.Wrap(apperror.Internal, "Release backup reservation failed", cleanupErr))
	}
	return m.recordBackupFailure(ctx, jobID, started, command, record, size, actionErr)
}

func (m *Manager) recordBackupFailure(ctx context.Context, jobID string, started time.Time, command string, record planRecord, size int64, actionErr error) error {
	duration := time.Since(started)
	_, _ = m.insertBackupRecord(ctx, record, backupResultFailed, size, actionErr)
	_ = m.recordAudit(ctx, "backup.volume", "volume", record.VolumeName, record.ProviderID, record.ProjectID, command, record.Plan.Risk, "failed", duration, actionErr)
	m.publishDone(jobID, "", actionErr)
	return actionErr
}

func cleanupUntrackedPublishedBackup(record planRecord) error {
	if err := removePublishedBackupPair(record); err != nil {
		return err
	}
	return releaseBackupReservation(record)
}

func removePublishedBackupPair(record planRecord) error {
	return errors.Join(
		removePublishedBackupFileWithIdentity(record.StagingMetadataIdentity, record.MetadataPath),
		removePublishedBackupFileWithIdentity(record.StagingArchiveIdentity, record.ArchivePath),
	)
}

func verifyPublishedBackupPair(record planRecord, archiveSHA256 string, metadataSHA256 string) error {
	gotArchive, _, err := fileSHA256WithIdentity(record.ArchivePath, record.StagingArchiveIdentity)
	if err != nil {
		return err
	}
	if !strings.EqualFold(gotArchive, archiveSHA256) {
		return apperror.New(apperror.Conflict, "Published backup archive changed during publication")
	}
	gotMetadata, _, err := fileSHA256WithIdentity(record.MetadataPath, record.StagingMetadataIdentity)
	if err != nil {
		return err
	}
	if !strings.EqualFold(gotMetadata, metadataSHA256) {
		return apperror.New(apperror.Conflict, "Published backup metadata changed during publication")
	}
	return nil
}

func (m *Manager) runRestore(ctx context.Context, jobID string, record planRecord) {
	started := m.now()
	command := plannedCommandText(record.Plan)
	_ = m.recordAudit(ctx, "backup.restore", "volume", record.TargetVolumeName, record.ProviderID, record.ProjectID, command, record.Plan.Risk, "started", 0, nil)
	m.publishProgress(jobID, "restore", "Starting volume restore", nil)
	provider, err := m.planProvider(ctx, record)
	if err == nil {
		err = m.validateRestorePlan(ctx, record, true)
	}
	targetCreated := false
	if err == nil && record.CreateTargetFirst {
		targetCreated, err = m.createOwnedRestoreVolume(ctx, &record)
	}
	if err == nil {
		// Hash the identity-bound source again after a potentially slow volume
		// create, then bind the target at the final helper boundary. For a new
		// target this proves both Cairn ownership and the exact incarnation.
		err = m.validateRestorePlan(ctx, record, false)
	}
	if err == nil && record.CreateTargetFirst {
		err = m.validateCreatedRestoreTarget(ctx, record)
	}
	if err == nil && !record.CreateTargetFirst {
		err = m.validateRestoreTarget(ctx, record)
	}
	if err == nil {
		err = runProviderDocker(ctx, provider, dockerRunRestoreArgs(record.TargetVolumeName, record.BackupDirBackend, record.ArchiveName)...)
	}
	if err != nil {
		if targetCreated {
			cleanupErr := m.cleanupCreatedRestoreVolume(ctx, provider, record)
			m.publishVolumeChanged(record.TargetVolumeName)
			if cleanupErr != nil {
				err = apperror.Wrap(
					apperror.Internal,
					fmt.Sprintf("Volume restore failed and new target volume %q could not be removed", record.TargetVolumeName),
					errors.Join(err, cleanupErr),
					apperror.WithDetail("The Cairn-created volume could not be safely identified or removed and may contain partial restore data."),
					apperror.WithRepairHints("Inspect the current target volume and its labels before removing anything manually."),
					apperror.WithPartialResource("volume", record.TargetVolumeName, "created_restore_failed", true),
				)
			}
		}
		duration := time.Since(started)
		_ = m.recordAudit(ctx, "backup.restore", "volume", record.TargetVolumeName, record.ProviderID, record.ProjectID, command, record.Plan.Risk, "failed", duration, err)
		m.publishDone(jobID, "", err)
		return
	}
	duration := time.Since(started)
	m.publishProgress(jobID, "restore", "Volume restore complete", floatPtr(100))
	m.publishVolumeChanged(record.TargetVolumeName)
	_ = m.recordAudit(ctx, "backup.restore", "volume", record.TargetVolumeName, record.ProviderID, record.ProjectID, command, record.Plan.Risk, "success", duration, nil)
	m.publishDone(jobID, record.TargetVolumeName, nil)
}

func (m *Manager) validateRestorePlan(ctx context.Context, record planRecord, validateTarget bool) error {
	if record.Operation != "restore" {
		return apperror.New(apperror.Conflict, "Plan is not a restore plan")
	}
	if _, err := m.planProvider(ctx, record); err != nil {
		return err
	}
	if err := verifyHeldPlanArtifact(record.MetadataPath, record.MetadataHandle, record.MetadataIdentity); err != nil {
		return apperror.Wrap(apperror.Conflict, "Backup metadata identity changed after restore planning", err)
	}
	if err := verifyHeldPlanArtifact(record.ArchivePath, record.ArchiveHandle, record.ArchiveIdentity); err != nil {
		return apperror.Wrap(apperror.Conflict, "Backup archive identity changed after restore planning", err)
	}
	if err := verifyArchiveChecksumWithIdentity(record.ArchivePath, record.ArchiveIdentity, record.Sidecar.SHA256); err != nil {
		return err
	}
	if validateTarget {
		return m.validateRestoreTarget(ctx, record)
	}
	return nil
}

func (m *Manager) validateRestoreTarget(ctx context.Context, record planRecord) error {
	target, exists, err := m.getVolumeIfExists(ctx, record.TargetVolumeName)
	if err != nil {
		return err
	}
	if !record.Overwrite {
		if exists {
			return apperror.New(
				apperror.Conflict,
				"Target volume already exists",
				apperror.WithDetail(record.TargetVolumeName),
			)
		}
		return nil
	}
	if !exists || target == nil {
		return apperror.New(apperror.Conflict, "Restore target volume changed after planning", apperror.WithDetail(record.TargetVolumeName))
	}
	expected := strings.TrimSpace(record.TargetVolumeFingerprint)
	if expected == "" {
		return apperror.New(apperror.Conflict, "Restore target volume identity is missing")
	}
	actual, err := security.VolumeIncarnationFingerprint(*target)
	if err != nil {
		return err
	}
	if actual != expected {
		return apperror.New(
			apperror.Conflict,
			"Restore target volume changed after planning",
			apperror.WithDetail("Review the current volume and create a new restore plan."),
		)
	}
	return nil
}

func (m *Manager) createOwnedRestoreVolume(ctx context.Context, record *planRecord) (bool, error) {
	if record == nil || strings.TrimSpace(record.RestoreOwnerToken) == "" {
		return false, apperror.New(apperror.Conflict, "Restore target ownership token is missing")
	}
	_, createErr := m.Docker.CreateVolume(ctx, models.CreateVolumeRequest{
		Name: record.TargetVolumeName,
		Labels: map[string]string{
			restoreOwnerLabel: record.RestoreOwnerToken,
		},
	})
	fingerprint, ownershipErr := m.captureCreatedRestoreTarget(ctx, *record)
	if ownershipErr != nil {
		if createErr != nil {
			return false, errors.Join(createErr, ownershipErr)
		}
		return false, ownershipErr
	}
	record.TargetVolumeFingerprint = fingerprint
	if createErr != nil {
		// The daemon may have created the labeled volume before a cache or
		// transport error was reported. Its verified identity makes cleanup safe.
		return true, createErr
	}
	return true, nil
}

func (m *Manager) captureCreatedRestoreTarget(ctx context.Context, record planRecord) (string, error) {
	target, exists, err := m.getVolumeIfExists(ctx, record.TargetVolumeName)
	if err != nil {
		return "", err
	}
	if !exists || target == nil {
		return "", apperror.New(apperror.Conflict, "Created restore target could not be inspected", apperror.WithDetail(record.TargetVolumeName))
	}
	if strings.TrimSpace(record.RestoreOwnerToken) == "" || target.Summary.Labels[restoreOwnerLabel] != record.RestoreOwnerToken {
		return "", apperror.New(
			apperror.Conflict,
			"Target volume was not created by this restore operation",
			apperror.WithDetail(record.TargetVolumeName),
			apperror.WithRepairHints("Review the current target volume and create a new restore plan with another name."),
		)
	}
	fingerprint, err := security.VolumeIncarnationFingerprint(*target)
	if err != nil {
		return "", err
	}
	return fingerprint, nil
}

func (m *Manager) validateCreatedRestoreTarget(ctx context.Context, record planRecord) error {
	expected := strings.TrimSpace(record.TargetVolumeFingerprint)
	if expected == "" {
		return apperror.New(apperror.Conflict, "Created restore target identity is missing")
	}
	actual, err := m.captureCreatedRestoreTarget(ctx, record)
	if err != nil {
		return err
	}
	if actual != expected {
		return apperror.New(
			apperror.Conflict,
			"Created restore target changed before use",
			apperror.WithDetail(record.TargetVolumeName),
		)
	}
	return nil
}

func (m *Manager) cleanupCreatedRestoreVolume(ctx context.Context, provider providers.PlatformProvider, record planRecord) error {
	base := context.Background()
	if ctx != nil {
		base = context.WithoutCancel(ctx)
	}
	cleanupCtx, cancel := context.WithTimeout(base, restoreCleanupTimeout)
	defer cancel()
	target, exists, err := m.getVolumeIfExists(cleanupCtx, record.TargetVolumeName)
	if err != nil {
		return err
	}
	if !exists || target == nil {
		return nil
	}
	if err := m.validateCreatedRestoreTarget(cleanupCtx, record); err != nil {
		return err
	}
	return runProviderDocker(cleanupCtx, provider, "volume", "rm", record.TargetVolumeName)
}

func (m *Manager) startJob(jobID string, run func(context.Context)) bool {
	base := context.Background()
	m.jobsMu.Lock()
	if m.rootCtx != nil {
		base = m.rootCtx
	}
	if m.stopped || base.Err() != nil {
		m.jobsMu.Unlock()
		return false
	}
	ctx, cancel := context.WithCancel(base)
	if m.jobs == nil {
		m.jobs = map[string]context.CancelFunc{}
	}
	m.jobs[jobID] = cancel
	m.jobsWG.Add(1)
	m.jobsMu.Unlock()

	go func() {
		defer m.jobsWG.Done()
		defer cancel()
		defer m.forgetJob(jobID)
		run(ctx)
	}()
	return true
}

func (m *Manager) forgetJob(jobID string) {
	m.jobsMu.Lock()
	defer m.jobsMu.Unlock()
	delete(m.jobs, jobID)
}

func (m *Manager) savePlan(record planRecord) error {
	now := m.now()
	expired := make([]planRecord, 0)
	m.mu.Lock()
	if m.plans == nil {
		m.plans = map[string]planRecord{}
	}
	for planID, existing := range m.plans {
		if now.Before(existing.Plan.ExpiresAt) {
			continue
		}
		delete(m.plans, planID)
		if existing.expiryTimer != nil {
			existing.expiryTimer.Stop()
		}
		expired = append(expired, existing)
	}
	if _, exists := m.plans[record.Plan.PlanID]; exists {
		m.mu.Unlock()
		for _, existing := range expired {
			cleanupDiscardedPlan(existing)
		}
		cleanupErr := cleanupDiscardedPlan(record)
		return errors.Join(
			apperror.New(apperror.Conflict, "A pending backup plan already uses this identifier"),
			cleanupErr,
		)
	}
	if len(m.plans) >= maxPendingBackupPlans {
		m.mu.Unlock()
		for _, existing := range expired {
			cleanupDiscardedPlan(existing)
		}
		cleanupErr := cleanupDiscardedPlan(record)
		return errors.Join(
			apperror.New(
				apperror.Conflict,
				"Too many backup plans are pending confirmation",
				apperror.WithRepairHints("Apply or allow existing plans to expire, then retry."),
			),
			cleanupErr,
		)
	}
	m.planGeneration++
	record.generation = m.planGeneration
	delay := record.Plan.ExpiresAt.Sub(now)
	if delay < 0 {
		delay = 0
	}
	generation := record.generation
	planID := record.Plan.PlanID
	record.expiryTimer = time.AfterFunc(delay, func() {
		m.expirePlan(planID, generation)
	})
	m.plans[record.Plan.PlanID] = record
	m.mu.Unlock()
	for _, existing := range expired {
		cleanupDiscardedPlan(existing)
	}
	return nil
}

func cleanupDiscardedPlan(record planRecord) error {
	closePlanArtifactHandles(record)
	if err := releaseBackupReservation(record); err != nil {
		return apperror.Wrap(apperror.Internal, "Release rejected or expired backup plan resources failed", err)
	}
	return nil
}

func (m *Manager) takePlan(ctx context.Context, planID string, typedName string) (planRecord, error) {
	if err := ctx.Err(); err != nil {
		return planRecord{}, err
	}
	m.mu.Lock()
	record, ok := m.plans[planID]
	if !ok {
		m.mu.Unlock()
		return planRecord{}, apperror.New(apperror.PlanExpired, "Plan expired or was not found")
	}
	if m.now().After(record.Plan.ExpiresAt) {
		delete(m.plans, planID)
		if record.expiryTimer != nil {
			record.expiryTimer.Stop()
		}
		m.mu.Unlock()
		var err error = apperror.New(apperror.PlanExpired, "Plan expired")
		if cleanupErr := releaseBackupReservation(record); cleanupErr != nil {
			err = errors.Join(err, apperror.Wrap(apperror.Internal, "Release expired backup reservation failed", cleanupErr))
		}
		closePlanArtifactHandles(record)
		return planRecord{}, err
	}
	if err := security.RequireConfirmation(record.Plan, typedName); err != nil {
		m.mu.Unlock()
		return planRecord{}, err
	}
	delete(m.plans, planID)
	if record.expiryTimer != nil {
		record.expiryTimer.Stop()
	}
	m.mu.Unlock()
	return record, nil
}

func (m *Manager) expirePlan(planID string, generation uint64) {
	m.mu.Lock()
	record, ok := m.plans[planID]
	if !ok || record.generation != generation {
		m.mu.Unlock()
		return
	}
	delete(m.plans, planID)
	m.mu.Unlock()
	_ = releaseBackupReservation(record)
	closePlanArtifactHandles(record)
}

func (m *Manager) discardPlans() []planRecord {
	m.mu.Lock()
	records := make([]planRecord, 0, len(m.plans))
	for planID, record := range m.plans {
		if record.expiryTimer != nil {
			record.expiryTimer.Stop()
		}
		records = append(records, record)
		delete(m.plans, planID)
	}
	m.mu.Unlock()
	return records
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

func (m *Manager) insertBackupRecord(ctx context.Context, record planRecord, result string, size int64, actionErr error) (string, error) {
	if m.Backups == nil {
		return "", nil
	}
	errText := ""
	if actionErr != nil {
		errText = actionErr.Error()
	}
	backupID := "backup-" + m.newID()
	err := m.Backups.Insert(ctx, store.BackupRecord{
		ID:                  backupID,
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
	return backupID, err
}

func (m *Manager) planProvider(ctx context.Context, record planRecord) (providers.PlatformProvider, error) {
	if m.Providers == nil {
		return nil, notReady()
	}
	active, err := m.Providers.ActiveProvider(ctx)
	if err != nil {
		return nil, err
	}
	activeScope, err := providers.ResolveRuntimeScope(ctx, active)
	if err != nil {
		return nil, err
	}
	if !record.Scope.Valid() || !record.Scope.Equal(activeScope) {
		return nil, apperror.New(
			apperror.Conflict,
			"Docker runtime changed after the backup plan was created",
			apperror.WithRepairHints("Return to the reviewed provider and context, then create a new backup or restore plan."),
		)
	}
	if record.Provider == nil {
		return active, nil
	}
	plannedScope, err := providers.ResolveRuntimeScope(ctx, record.Provider)
	if err != nil || !record.Scope.Equal(plannedScope) {
		return nil, apperror.New(
			apperror.Conflict,
			"Planned Docker runtime identity can no longer be verified",
			apperror.WithCause(err),
		)
	}
	return record.Provider, nil
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
	if err := bus.PublishCriticalBounded(m.Events, bus.Event{Topic: bus.TopicJobDone, Payload: payload}); err != nil {
		slog.Warn("publish critical backup completion failed", "jobID", jobID, "error", err)
	}
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
	return apperror.WithDetail(providers.SafeCommandDiagnostic(detail, 8<<10))
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
		Command:     shellJoin([]string{"docker", "volume", "create", "--label", restoreOwnerLabel + "=<generated-token>", volumeName}),
		Risk:        risk,
		Explanation: "Creates the target volume with a per-plan Cairn ownership label before restoring backup contents.",
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
		"sh", "-c",
		backupHelperScript,
		"cairn-backup",
		"/backup/" + archiveName,
	}
}

// The helper needs root access to read arbitrary volume contents. On native
// Linux that also makes a newly-created bind-mounted archive owned by root,
// which protected_hardlinks prevents the Cairn process from publishing with a
// hard link. Transfer the completed archive to the private staging directory's
// owner and restore its private mode before the helper exits.
const backupHelperScript = `set -eu
archive=$1
tar czf "$archive" --exclude=.cairn-restore-old-* -C /source .
owner=$(stat -c '%u:%g' /backup)
chown "$owner" "$archive"
chmod 0600 "$archive"`

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
restore_stash_dir() {
  old_stash=$1
  old_name=$(basename "$old_stash")
  find /restore -mindepth 1 -maxdepth 1 ! -name "$old_name" -exec rm -rf {} +
  find "$old_stash" -mindepth 1 -maxdepth 1 -exec sh -c 'dest=$1; shift; for path do mv "$path" "$dest"/; done' sh /restore {} +
  rmdir "$old_stash"
}
for old_stash in /restore/.cairn-restore-old-*; do
  [ -e "$old_stash" ] || continue
  [ -d "$old_stash" ] || { rm -f "$old_stash"; continue; }
  restore_stash_dir "$old_stash"
done
stash_name=".cairn-restore-old-$$"
stash="/restore/$stash_name"
mkdir "$stash"
cleanup() {
  code=$?
  trap - EXIT HUP INT TERM
  if [ "$code" -ne 0 ] && [ -d "$stash" ]; then
    restore_stash_dir "$stash" || true
  fi
  exit "$code"
}
trap cleanup EXIT HUP INT TERM
find /restore -mindepth 1 -maxdepth 1 ! -name "$stash_name" -exec sh -c 'stash=$1; shift; for path do mv "$path" "$stash"/; done' sh "$stash" {} +
tar xzf "$archive" -C /restore
rm -rf "$stash"
trap - EXIT HUP INT TERM`

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

func reserveBackupPaths(dir string, volumeName string, ts time.Time, owner string) (backupReservation, error) {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return backupReservation{}, apperror.New(apperror.Internal, "Backup reservation owner is required")
	}
	if len(owner)+1 > maxReservationBytes {
		return backupReservation{}, apperror.New(apperror.Internal, "Backup reservation owner is too long")
	}
	base := sanitizeFilename(volumeName)
	if base == "" {
		base = "volume"
	}
	ownerName := sanitizeFilename(owner)
	if ownerName == "" {
		return backupReservation{}, apperror.New(apperror.Internal, "Backup reservation owner is invalid")
	}
	stamp := ts.UTC().Format(backupTimestampLayout)
	for i := 0; i < maxBackupPathAttempts; i++ {
		suffix := ""
		if i > 0 {
			suffix = fmt.Sprintf("-%d", i+1)
		}
		archiveName := fmt.Sprintf("%s-%s%s.tar.gz", base, stamp, suffix)
		archivePath := filepath.Join(dir, archiveName)
		metadataPath := metadataPathForArchive(archivePath)
		if unavailable, err := backupPathUnavailable(archivePath); err != nil {
			return backupReservation{}, err
		} else if unavailable {
			continue
		}
		if unavailable, err := backupPathUnavailable(metadataPath); err != nil {
			return backupReservation{}, err
		} else if unavailable {
			continue
		}

		reservationPath := reservationPathForArchive(archivePath)
		if err := createReservationOwnerFile(reservationPath, owner); err != nil {
			if os.IsExist(err) {
				continue
			}
			return backupReservation{}, apperror.Wrap(apperror.Internal, "Create backup path reservation failed", err)
		}
		reservationIdentity, err := regularFileIdentity(reservationPath)
		if err != nil {
			cleanupErr := removeReservationOwnerFile(reservationPath, owner)
			return backupReservation{}, errors.Join(err, cleanupErr)
		}

		collision := false
		for _, path := range []string{archivePath, metadataPath} {
			unavailable, err := backupPathUnavailable(path)
			if err != nil {
				cleanupErr := removeReservationOwnerFile(reservationPath, owner)
				return backupReservation{}, errors.Join(err, cleanupErr)
			}
			if unavailable {
				collision = true
				break
			}
		}
		if collision {
			if err := removeReservationOwnerFile(reservationPath, owner); err != nil {
				return backupReservation{}, err
			}
			continue
		}

		stagingDir := filepath.Join(dir, ".cairn-backup-"+ownerName)
		if err := os.Mkdir(stagingDir, 0o700); err != nil {
			cleanupErr := removeReservationOwnerFile(reservationPath, owner)
			if os.IsExist(err) {
				err = apperror.New(apperror.Conflict, "Backup staging directory already exists", apperror.WithDetail(stagingDir))
			} else {
				err = apperror.Wrap(apperror.Internal, "Create backup staging directory failed", err)
			}
			return backupReservation{}, errors.Join(err, cleanupErr)
		}
		stagingDirIdentity, err := directoryIdentity(stagingDir)
		if err != nil {
			cleanupErr := errors.Join(os.Remove(stagingDir), removeReservationOwnerFile(reservationPath, owner))
			return backupReservation{}, errors.Join(err, cleanupErr)
		}
		stagingOwnerPath := filepath.Join(stagingDir, stagingOwnerName)
		if err := createReservationOwnerFile(stagingOwnerPath, owner); err != nil {
			cleanupErr := errors.Join(removeFileIfExists(stagingOwnerPath), os.Remove(stagingDir), removeReservationOwnerFile(reservationPath, owner))
			return backupReservation{}, errors.Join(
				apperror.Wrap(apperror.Internal, "Create backup staging ownership marker failed", err),
				cleanupErr,
			)
		}
		stagingOwnerIdentity, err := regularFileIdentity(stagingOwnerPath)
		if err != nil {
			cleanupErr := errors.Join(removeFileIfExists(stagingOwnerPath), os.Remove(stagingDir), removeReservationOwnerFile(reservationPath, owner))
			return backupReservation{}, errors.Join(err, cleanupErr)
		}
		return backupReservation{
			ArchiveName:          archiveName,
			ArchivePath:          archivePath,
			MetadataPath:         metadataPath,
			ReservationPath:      reservationPath,
			ReservationIdentity:  reservationIdentity,
			Owner:                owner,
			StagingDir:           stagingDir,
			StagingDirIdentity:   stagingDirIdentity,
			StagingArchivePath:   filepath.Join(stagingDir, stagingArchiveName),
			StagingMetadataPath:  filepath.Join(stagingDir, stagingMetadataName),
			StagingOwnerPath:     stagingOwnerPath,
			StagingOwnerIdentity: stagingOwnerIdentity,
		}, nil
	}
	return backupReservation{}, apperror.New(apperror.Conflict, "Could not reserve a unique backup filename")
}

func backupPathUnavailable(path string) (bool, error) {
	if _, err := os.Lstat(path); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, apperror.Wrap(apperror.Internal, "Check backup path failed", err)
	}
	return true, nil
}

func regularFileIdentity(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, apperror.New(apperror.Conflict, "Expected an owned regular file", apperror.WithDetail(path))
	}
	if !stabilizeFileIdentity(info) {
		return nil, apperror.New(apperror.Conflict, "Capture regular file identity failed", apperror.WithDetail(path))
	}
	return info, nil
}

func optionalRegularFileIdentity(path string) (os.FileInfo, error) {
	if path == "" {
		return nil, nil
	}
	info, err := regularFileIdentity(path)
	if err != nil && os.IsNotExist(err) {
		return nil, nil
	}
	return info, err
}

func openPlannedBackupArtifact(path string, kind string) (*os.File, os.FileInfo, error) {
	identity, err := optionalRegularFileIdentity(path)
	if err != nil {
		return nil, nil, apperror.Wrap(
			apperror.Conflict,
			"Backup "+kind+" is not a stable regular file",
			err,
			apperror.WithDetail(path),
		)
	}
	if identity == nil {
		return nil, nil, nil
	}
	file, err := openStableDeleteFile(path)
	if err != nil {
		return nil, nil, apperror.Wrap(apperror.Conflict, "Open backup "+kind+" for stable identity failed", err)
	}
	opened, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, apperror.Wrap(apperror.Conflict, "Inspect opened backup "+kind+" failed", err)
	}
	if !opened.Mode().IsRegular() || !os.SameFile(identity, opened) {
		_ = file.Close()
		return nil, nil, apperror.New(apperror.Conflict, "Backup "+kind+" identity changed while opening")
	}
	if err := verifyPathIdentity(path, opened, false); err != nil {
		_ = file.Close()
		return nil, nil, apperror.Wrap(apperror.Conflict, "Backup "+kind+" identity changed while opening", err)
	}
	return file, opened, nil
}

func openRequiredRestoreArtifact(path string, kind string) (*os.File, os.FileInfo, error) {
	expected, err := regularFileIdentity(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, apperror.Wrap(apperror.NotFound, "Backup "+kind+" was not found", err, apperror.WithDetail(path))
		}
		return nil, nil, apperror.Wrap(
			apperror.Conflict,
			"Backup "+kind+" is not a stable regular file",
			err,
			apperror.WithDetail(path),
		)
	}
	file, err := openStableFile(path)
	if err != nil {
		return nil, nil, apperror.Wrap(apperror.Conflict, "Open backup "+kind+" for stable identity failed", err)
	}
	opened, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, apperror.Wrap(apperror.Conflict, "Inspect opened backup "+kind+" failed", err)
	}
	if !opened.Mode().IsRegular() || !os.SameFile(expected, opened) {
		_ = file.Close()
		return nil, nil, apperror.New(apperror.Conflict, "Backup "+kind+" identity changed while opening")
	}
	if err := verifyPathIdentity(path, opened, false); err != nil {
		_ = file.Close()
		return nil, nil, apperror.Wrap(apperror.Conflict, "Backup "+kind+" identity changed while opening", err)
	}
	return file, opened, nil
}

func verifyHeldPlanArtifact(path string, handle *os.File, expected os.FileInfo) error {
	if handle == nil || expected == nil {
		return apperror.New(apperror.Conflict, "Planned file identity handle is missing")
	}
	held, err := handle.Stat()
	if err != nil {
		return err
	}
	if !held.Mode().IsRegular() || !os.SameFile(expected, held) {
		return apperror.New(apperror.Conflict, "Held planned file identity changed")
	}
	if held.Size() != expected.Size() || !held.ModTime().Equal(expected.ModTime()) {
		return apperror.New(apperror.Conflict, "Held planned file metadata changed")
	}
	return verifyPathIdentity(path, held, false)
}

func closePlanArtifactHandles(record planRecord) {
	if record.MetadataHandle != nil {
		_ = record.MetadataHandle.Close()
	}
	if record.ArchiveHandle != nil {
		_ = record.ArchiveHandle.Close()
	}
}

func directoryIdentity(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, apperror.New(apperror.Conflict, "Expected an owned directory", apperror.WithDetail(path))
	}
	if !stabilizeFileIdentity(info) {
		return nil, apperror.New(apperror.Conflict, "Capture directory identity failed", apperror.WithDetail(path))
	}
	return info, nil
}

func stabilizeFileIdentity(info os.FileInfo) bool {
	// Windows can defer resolving the file ID returned by Lstat until the first
	// SameFile call. Resolve it at capture time so a later path replacement
	// cannot become the identity that the plan appears to own.
	return info != nil && os.SameFile(info, info)
}

func verifyPathIdentity(path string, expected os.FileInfo, wantDirectory bool) error {
	if expected == nil {
		return apperror.New(apperror.Conflict, "Owned path identity is missing", apperror.WithDetail(path))
	}
	current, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if wantDirectory {
		if !current.IsDir() {
			return apperror.New(apperror.Conflict, "Owned path is no longer a directory", apperror.WithDetail(path))
		}
	} else if !current.Mode().IsRegular() {
		return apperror.New(apperror.Conflict, "Owned path is no longer a regular file", apperror.WithDetail(path))
	}
	if !os.SameFile(expected, current) {
		return apperror.New(apperror.Conflict, "Owned path identity changed", apperror.WithDetail(path))
	}
	return nil
}

func reservationPathForArchive(archivePath string) string {
	dir, name := filepath.Split(archivePath)
	return filepath.Join(dir, "."+name+reservationSuffix)
}

func createReservationOwnerFile(path string, owner string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	payload := []byte(owner + "\n")
	if _, err := file.Write(payload); err != nil {
		return errors.Join(err, closeReservationOwnerFile(file), removeFileIfExists(path))
	}
	if err := file.Sync(); err != nil {
		return errors.Join(err, closeReservationOwnerFile(file), removeFileIfExists(path))
	}
	if err := file.Close(); err != nil {
		return errors.Join(err, removeFileIfExists(path))
	}
	return nil
}

func closeReservationOwnerFile(file *os.File) error {
	if file == nil {
		return nil
	}
	return file.Close()
}

func verifyReservationOwner(path string, owner string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() {
		_ = file.Close()
	}()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > maxReservationBytes {
		return apperror.New(apperror.Conflict, "Backup reservation ownership marker is invalid", apperror.WithDetail(path))
	}
	payload, err := io.ReadAll(io.LimitReader(file, maxReservationBytes+1))
	if err != nil {
		return err
	}
	if len(payload) > maxReservationBytes || string(payload) != owner+"\n" {
		return apperror.New(apperror.Conflict, "Backup reservation is owned by another operation", apperror.WithDetail(path))
	}
	return nil
}

func removeReservationOwnerFile(path string, owner string) error {
	if err := verifyReservationOwner(path, owner); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func validateBackupReservation(record planRecord) error {
	if record.Operation != "backup" {
		return nil
	}
	if record.ReservationPath == "" || record.ReservationOwner == "" || record.StagingDirHost == "" ||
		record.StagingArchivePath == "" || record.StagingMetadataPath == "" || record.StagingOwnerPath == "" ||
		record.ReservationIdentity == nil || record.StagingDirIdentity == nil || record.StagingOwnerIdentity == nil {
		return apperror.New(apperror.Conflict, "Backup reservation is incomplete")
	}
	if err := verifyPathIdentity(record.ReservationPath, record.ReservationIdentity, false); err != nil {
		return apperror.Wrap(apperror.Conflict, "Backup path reservation identity changed", err)
	}
	if err := verifyReservationOwner(record.ReservationPath, record.ReservationOwner); err != nil {
		return apperror.Wrap(apperror.Conflict, "Backup path reservation is no longer valid", err)
	}
	if err := verifyPathIdentity(record.StagingDirHost, record.StagingDirIdentity, true); err != nil {
		return apperror.Wrap(apperror.Conflict, "Backup staging directory is no longer valid", err)
	}
	if err := verifyPathIdentity(record.StagingOwnerPath, record.StagingOwnerIdentity, false); err != nil {
		return apperror.Wrap(apperror.Conflict, "Backup staging ownership identity changed", err)
	}
	if err := verifyReservationOwner(record.StagingOwnerPath, record.ReservationOwner); err != nil {
		return apperror.Wrap(apperror.Conflict, "Backup staging ownership is no longer valid", err)
	}
	if err := verifyOwnedStagingEntries(record); err != nil {
		return err
	}
	for _, path := range []string{record.ArchivePath, record.MetadataPath, record.StagingArchivePath, record.StagingMetadataPath} {
		unavailable, err := backupPathUnavailable(path)
		if err != nil {
			return err
		}
		if unavailable {
			return apperror.New(apperror.Conflict, "Backup destination changed after planning", apperror.WithDetail(path))
		}
	}
	return nil
}

func releaseBackupReservationRecord(reservation backupReservation) error {
	return releaseBackupReservation(planRecord{
		Operation:            "backup",
		ReservationPath:      reservation.ReservationPath,
		ReservationOwner:     reservation.Owner,
		ReservationIdentity:  reservation.ReservationIdentity,
		StagingDirHost:       reservation.StagingDir,
		StagingDirIdentity:   reservation.StagingDirIdentity,
		StagingArchivePath:   reservation.StagingArchivePath,
		StagingMetadataPath:  reservation.StagingMetadataPath,
		StagingOwnerPath:     reservation.StagingOwnerPath,
		StagingOwnerIdentity: reservation.StagingOwnerIdentity,
	})
}

func releaseBackupReservation(record planRecord) error {
	if record.Operation != "backup" || record.ReservationPath == "" {
		return nil
	}
	if err := verifyPathIdentity(record.ReservationPath, record.ReservationIdentity, false); err != nil {
		if os.IsNotExist(err) {
			if _, stageErr := os.Lstat(record.StagingDirHost); os.IsNotExist(stageErr) {
				return nil
			}
		}
		return apperror.Wrap(apperror.Conflict, "Refusing to release a backup reservation whose identity changed", err)
	}
	if err := verifyReservationOwner(record.ReservationPath, record.ReservationOwner); err != nil {
		return apperror.Wrap(apperror.Conflict, "Refusing to release a backup reservation without matching ownership", err)
	}
	_, err := os.Lstat(record.StagingDirHost)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
	} else {
		if err := verifyPathIdentity(record.StagingDirHost, record.StagingDirIdentity, true); err != nil {
			return apperror.Wrap(apperror.Conflict, "Refusing to remove a backup staging directory whose identity changed", err)
		}
		if err := verifyPathIdentity(record.StagingOwnerPath, record.StagingOwnerIdentity, false); err != nil {
			return apperror.Wrap(apperror.Conflict, "Refusing to remove a backup staging ownership marker whose identity changed", err)
		}
		if err := verifyReservationOwner(record.StagingOwnerPath, record.ReservationOwner); err != nil {
			return apperror.Wrap(apperror.Conflict, "Refusing to remove a backup staging directory without matching ownership", err)
		}
		if err := verifyOwnedStagingEntries(record); err != nil {
			return err
		}
		if err := removeFileWithIdentity(record.StagingMetadataPath, record.StagingMetadataIdentity); err != nil {
			return err
		}
		if err := removeFileWithIdentity(record.StagingArchivePath, record.StagingArchiveIdentity); err != nil {
			return err
		}
		if err := removeOwnedMarkerWithIdentity(record.StagingOwnerPath, record.ReservationOwner, record.StagingOwnerIdentity); err != nil {
			return err
		}
		if err := os.Remove(record.StagingDirHost); err != nil {
			return err
		}
	}
	if err := removeOwnedMarkerWithIdentity(record.ReservationPath, record.ReservationOwner, record.ReservationIdentity); err != nil {
		return err
	}
	return nil
}

func verifyOwnedStagingEntries(record planRecord) error {
	entries, err := os.ReadDir(record.StagingDirHost)
	if err != nil {
		return err
	}
	allowed := map[string]os.FileInfo{
		stagingOwnerName: record.StagingOwnerIdentity,
	}
	if record.StagingArchiveIdentity != nil {
		allowed[stagingArchiveName] = record.StagingArchiveIdentity
	}
	if record.StagingMetadataIdentity != nil {
		allowed[stagingMetadataName] = record.StagingMetadataIdentity
	}
	for _, entry := range entries {
		identity, ok := allowed[entry.Name()]
		if !ok || identity == nil {
			return apperror.New(
				apperror.Conflict,
				"Refusing to remove a backup staging directory containing an unowned entry",
				apperror.WithDetail(filepath.Join(record.StagingDirHost, entry.Name())),
			)
		}
		if err := verifyPathIdentity(filepath.Join(record.StagingDirHost, entry.Name()), identity, false); err != nil {
			return apperror.Wrap(apperror.Conflict, "Refusing to remove a backup staging entry whose identity changed", err)
		}
	}
	return nil
}

func removeFileWithIdentity(path string, expected os.FileInfo) error {
	if expected == nil {
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			return nil
		} else if err != nil {
			return err
		}
		return apperror.New(apperror.Conflict, "Refusing to remove a file without captured ownership", apperror.WithDetail(path))
	}
	if err := verifyPathIdentity(path, expected, false); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func removeOwnedMarkerWithIdentity(path string, owner string, expected os.FileInfo) error {
	if err := verifyPathIdentity(path, expected, false); err != nil {
		return err
	}
	if err := verifyReservationOwner(path, owner); err != nil {
		return err
	}
	return removeFileWithIdentity(path, expected)
}

func publishStagedBackupFile(stagingPath string, destinationPath string, expected os.FileInfo) error {
	return publishStagedBackupFileWithLink(stagingPath, destinationPath, expected, os.Link)
}

func publishStagedBackupFileWithLink(stagingPath string, destinationPath string, expected os.FileInfo, link func(string, string) error) error {
	if err := verifyPathIdentity(stagingPath, expected, false); err != nil {
		return apperror.Wrap(apperror.Conflict, "Staged backup file identity changed before publication", err)
	}
	if err := link(stagingPath, destinationPath); err != nil {
		if os.IsExist(err) {
			return apperror.New(apperror.Conflict, "Backup destination already exists", apperror.WithDetail(destinationPath))
		}
		return apperror.Wrap(apperror.Internal, "Publish staged backup file failed", err, apperror.WithDetail(destinationPath))
	}
	if err := verifyPathIdentity(destinationPath, expected, false); err != nil {
		cleanupErr := removeFileWithIdentity(destinationPath, expected)
		publicationErr := apperror.Wrap(apperror.Conflict, "Published backup file identity did not match the staged file", err)
		if cleanupErr != nil {
			return errors.Join(publicationErr, cleanupErr, errPreserveBackupReservation)
		}
		return publicationErr
	}
	if err := verifyPathIdentity(stagingPath, expected, false); err != nil {
		cleanupErr := removeFileWithIdentity(destinationPath, expected)
		publicationErr := apperror.Wrap(apperror.Conflict, "Staged backup file identity changed during publication", err)
		if cleanupErr != nil {
			return errors.Join(publicationErr, cleanupErr, errPreserveBackupReservation)
		}
		return publicationErr
	}
	return nil
}

func removePublishedBackupFile(stagingPath string, destinationPath string) error {
	stagingInfo, err := regularFileIdentity(stagingPath)
	if err != nil {
		return err
	}
	return removePublishedBackupFileWithIdentity(stagingInfo, destinationPath)
}

func removePublishedBackupFileWithIdentity(expected os.FileInfo, destinationPath string) error {
	return removeFileWithIdentity(destinationPath, expected)
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
	return readSidecarWithOpen(path, os.Open)
}

func readSidecarWithOpen(path string, openFile func(string) (*os.File, error)) (BackupSidecar, error) {
	expected, err := regularFileIdentity(path)
	if err != nil {
		if apperror.IsCode(err, apperror.Conflict) {
			return BackupSidecar{}, apperror.Wrap(apperror.Conflict, "Backup metadata is not a stable regular file", err)
		}
		return BackupSidecar{}, apperror.Wrap(apperror.NotFound, "Open backup metadata failed", err)
	}
	if expected.Size() > maxSidecarBytes {
		return BackupSidecar{}, backupMetadataTooLarge(path)
	}
	file, err := openFile(path)
	if err != nil {
		return BackupSidecar{}, apperror.Wrap(apperror.NotFound, "Open backup metadata failed", err)
	}
	defer func() {
		_ = file.Close()
	}()
	opened, err := file.Stat()
	if err != nil {
		return BackupSidecar{}, apperror.Wrap(apperror.Conflict, "Inspect opened backup metadata failed", err)
	}
	if !opened.Mode().IsRegular() || !os.SameFile(expected, opened) {
		return BackupSidecar{}, apperror.New(
			apperror.Conflict,
			"Backup metadata identity changed while opening",
			apperror.WithDetail(path),
		)
	}
	if opened.Size() > maxSidecarBytes {
		return BackupSidecar{}, backupMetadataTooLarge(path)
	}
	payload, err := io.ReadAll(io.LimitReader(file, maxSidecarBytes+1))
	if err != nil {
		return BackupSidecar{}, apperror.Wrap(apperror.Conflict, "Read backup metadata failed", err)
	}
	if len(payload) > maxSidecarBytes {
		return BackupSidecar{}, backupMetadataTooLarge(path)
	}
	openedAfter, err := file.Stat()
	if err != nil {
		return BackupSidecar{}, apperror.Wrap(apperror.Conflict, "Reinspect opened backup metadata failed", err)
	}
	if !openedAfter.Mode().IsRegular() || !os.SameFile(opened, openedAfter) || openedAfter.Size() > maxSidecarBytes {
		return BackupSidecar{}, apperror.New(
			apperror.Conflict,
			"Backup metadata changed while reading",
			apperror.WithDetail(path),
		)
	}
	if err := verifyPathIdentity(path, expected, false); err != nil {
		return BackupSidecar{}, apperror.Wrap(apperror.Conflict, "Backup metadata identity changed while reading", err)
	}

	var sidecar BackupSidecar
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if err := decoder.Decode(&sidecar); err != nil {
		return BackupSidecar{}, apperror.Wrap(apperror.Conflict, "Backup metadata is invalid", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values are not allowed")
		}
		return BackupSidecar{}, apperror.Wrap(apperror.Conflict, "Backup metadata has trailing content", err)
	}
	if err := validateBackupSidecar(sidecar); err != nil {
		return BackupSidecar{}, err
	}
	return sidecar, nil
}

func backupMetadataTooLarge(path string) error {
	return apperror.New(
		apperror.Conflict,
		"Backup metadata exceeds the size limit",
		apperror.WithDetail(fmt.Sprintf("%s (maximum %d bytes)", path, maxSidecarBytes)),
	)
}

func validateBackupSidecar(sidecar BackupSidecar) error {
	if sidecar.FormatVersion != formatVersion {
		return apperror.New(apperror.Conflict, "Backup metadata format is unsupported")
	}
	if sidecar.Volume == "" || len(sidecar.Volume) > maxSidecarNameBytes || !sidecarVolumePattern.MatchString(sidecar.Volume) {
		return invalidBackupMetadataField("volume")
	}
	if len(sidecar.SHA256) != sha256.Size*2 || strings.TrimSpace(sidecar.SHA256) != sidecar.SHA256 {
		return invalidBackupMetadataField("sha256")
	}
	if _, err := hex.DecodeString(sidecar.SHA256); err != nil {
		return invalidBackupMetadataField("sha256")
	}
	if sidecar.CompressedSizeBytes < 0 {
		return invalidBackupMetadataField("compressed_size_bytes")
	}
	if sidecar.ArchiveFormatVersion != 0 && sidecar.ArchiveFormatVersion != formatVersion {
		return invalidBackupMetadataField("archive_format_version")
	}
	for _, field := range []struct {
		name  string
		value string
		limit int
	}{
		{name: "project", value: sidecar.Project, limit: maxSidecarFieldBytes},
		{name: "docker_context", value: sidecar.DockerContext, limit: maxSidecarNameBytes},
		{name: "provider", value: sidecar.Provider, limit: maxSidecarNameBytes},
		{name: "cairn_version", value: sidecar.CairnVersion, limit: maxSidecarNameBytes},
	} {
		if len(field.value) > field.limit {
			return invalidBackupMetadataField(field.name)
		}
	}
	if len(sidecar.UsingContainers) > maxSidecarContainers {
		return invalidBackupMetadataField("using_containers")
	}
	for _, name := range sidecar.UsingContainers {
		if len(name) > maxSidecarNameBytes {
			return invalidBackupMetadataField("using_containers")
		}
	}
	return nil
}

func invalidBackupMetadataField(field string) error {
	return apperror.New(
		apperror.Conflict,
		"Backup metadata contains an invalid field",
		apperror.WithDetail(field),
	)
}

func writeSidecar(path string, sidecar BackupSidecar) error {
	if err := validateBackupSidecar(sidecar); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return apperror.Wrap(apperror.Internal, "Create backup metadata failed", err)
	}
	identity, err := file.Stat()
	if err != nil {
		return errors.Join(
			apperror.Wrap(apperror.Internal, "Capture backup metadata ownership failed", err),
			closeReservationOwnerFile(file),
		)
	}
	if !identity.Mode().IsRegular() {
		return errors.Join(
			apperror.New(apperror.Conflict, "Backup metadata path is not a regular file", apperror.WithDetail(path)),
			closeReservationOwnerFile(file),
		)
	}
	if err := writeSidecarContents(file, sidecar); err != nil {
		cleanupErr := removeFileWithIdentity(path, identity)
		if cleanupErr != nil {
			cleanupErr = apperror.Wrap(apperror.Internal, "Remove incomplete backup metadata failed", cleanupErr)
		}
		return errors.Join(err, cleanupErr)
	}
	if err := verifyPathIdentity(path, identity, false); err != nil {
		cleanupErr := removeFileWithIdentity(path, identity)
		return errors.Join(
			apperror.Wrap(apperror.Conflict, "Backup metadata identity changed while writing", err),
			cleanupErr,
		)
	}
	return nil
}

func writeSidecarContents(file sidecarFile, sidecar BackupSidecar) error {
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(sidecar); err != nil {
		return errors.Join(
			apperror.Wrap(apperror.Internal, "Write backup metadata failed", err),
			closeSidecarFile(file),
		)
	}
	if err := file.Sync(); err != nil {
		return errors.Join(
			apperror.Wrap(apperror.Internal, "Flush backup metadata failed", err),
			closeSidecarFile(file),
		)
	}
	if err := file.Close(); err != nil {
		return apperror.Wrap(apperror.Internal, "Close backup metadata failed", err)
	}
	return nil
}

func closeSidecarFile(file sidecarFile) error {
	if err := file.Close(); err != nil {
		return apperror.Wrap(apperror.Internal, "Close backup metadata failed", err)
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

func verifyArchiveChecksumWithIdentity(path string, expected os.FileInfo, want string) error {
	got, _, err := fileSHA256WithIdentity(path, expected)
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

func fileSHA256WithIdentity(path string, expected os.FileInfo) (string, int64, error) {
	if err := verifyPathIdentity(path, expected, false); err != nil {
		return "", 0, apperror.Wrap(apperror.Conflict, "Backup file identity changed before checksum verification", err)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", 0, apperror.Wrap(apperror.NotFound, "Open backup file for checksum verification failed", err)
	}
	defer func() {
		_ = file.Close()
	}()
	openedInfo, err := file.Stat()
	if err != nil {
		return "", 0, err
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(expected, openedInfo) ||
		openedInfo.Size() != expected.Size() || !openedInfo.ModTime().Equal(expected.ModTime()) {
		return "", 0, apperror.New(apperror.Conflict, "Opened backup file identity did not match the published file", apperror.WithDetail(path))
	}
	hash := sha256.New()
	n, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, apperror.Wrap(apperror.Internal, "Read published backup file failed", err)
	}
	openedAfter, err := file.Stat()
	if err != nil {
		return "", 0, err
	}
	if !openedAfter.Mode().IsRegular() || !os.SameFile(expected, openedAfter) ||
		openedAfter.Size() != openedInfo.Size() || !openedAfter.ModTime().Equal(openedInfo.ModTime()) {
		return "", 0, apperror.New(apperror.Conflict, "Published backup file identity changed while hashing", apperror.WithDetail(path))
	}
	if err := verifyPathIdentity(path, expected, false); err != nil {
		return "", 0, apperror.Wrap(apperror.Conflict, "Backup file identity changed after checksum verification", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), n, nil
}

func removeBackupFiles(record store.BackupRecord) error {
	return removeBackupArtifacts(record.BackupPath, record.MetadataPath)
}

func removePlannedBackupArtifacts(record planRecord) error {
	return removePlannedBackupArtifactsWithRemove(record, removeStableFile)
}

func removePlannedBackupArtifactsWithRemove(
	record planRecord,
	remove func(string, *os.File) error,
) error {
	artifacts := []struct {
		kind     string
		path     string
		identity os.FileInfo
		handle   *os.File
	}{
		{kind: "archive", path: record.ArchivePath, identity: record.ArchiveIdentity, handle: record.ArchiveHandle},
		{kind: "metadata", path: record.MetadataPath, identity: record.MetadataIdentity, handle: record.MetadataHandle},
	}
	for _, artifact := range artifacts {
		if err := verifyPlannedBackupArtifact(artifact.path, artifact.kind, artifact.handle, artifact.identity); err != nil {
			return err
		}
	}
	for _, artifact := range artifacts {
		if artifact.identity == nil {
			continue
		}
		if err := verifyPlannedBackupArtifact(artifact.path, artifact.kind, artifact.handle, artifact.identity); err != nil {
			return err
		}
		if err := remove(artifact.path, artifact.handle); err != nil {
			return apperror.Wrap(
				apperror.Conflict,
				"Delete verified backup "+artifact.kind+" failed",
				err,
				apperror.WithRepairHints("Check file permissions and retry with a fresh delete plan."),
			)
		}
	}
	for _, artifact := range artifacts {
		if artifact.path == "" {
			continue
		}
		if _, err := os.Lstat(artifact.path); !os.IsNotExist(err) {
			if err != nil {
				return apperror.Wrap(apperror.Conflict, "Verify backup "+artifact.kind+" removal failed", err)
			}
			return apperror.New(
				apperror.Conflict,
				"Backup "+artifact.kind+" was replaced during deletion",
			)
		}
	}
	return nil
}

func verifyPlannedBackupArtifact(path string, kind string, handle *os.File, expected os.FileInfo) error {
	if path == "" {
		if expected == nil && handle == nil {
			return nil
		}
		return apperror.New(apperror.Conflict, "Backup "+kind+" path changed after planning")
	}
	if expected == nil {
		if handle != nil {
			return apperror.New(apperror.Conflict, "Backup "+kind+" identity handle changed after planning")
		}
		_, err := os.Lstat(path)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return apperror.Wrap(apperror.Conflict, "Recheck backup "+kind+" failed", err, apperror.WithDetail(path))
		}
		return apperror.New(
			apperror.Conflict,
			"Backup "+kind+" appeared after planning",
			apperror.WithDetail(path),
		)
	}
	if err := verifyHeldPlanArtifact(path, handle, expected); err != nil {
		return apperror.Wrap(
			apperror.Conflict,
			"Backup "+kind+" identity changed after planning",
			err,
			apperror.WithDetail(path),
		)
	}
	return nil
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
