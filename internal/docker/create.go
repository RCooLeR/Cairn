package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/bus"
	"github.com/RCooLeR/Cairn/internal/models"
	"github.com/RCooLeR/Cairn/internal/providers"
	registrycore "github.com/RCooLeR/Cairn/internal/registry"
	"github.com/RCooLeR/Cairn/internal/security"
	"github.com/RCooLeR/Cairn/internal/store"
	cerrdefs "github.com/containerd/errdefs"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	dockermount "github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/registry"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/go-connections/nat"
	"github.com/google/uuid"
)

const (
	defaultImageSearchLimit      = 25
	maxImageSearchLimit          = 100
	maxImageLoadResponseBytes    = 4 * 1024 * 1024
	maxImageLoadResponseMessages = 4096
	maxImageLoadResultBytes      = 64 * 1024
	restartPolicyNone            = "no"
)

var dockerNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

type ImageProgressPayload struct {
	StreamID string `json:"streamID"`
	LayerID  string `json:"layerID,omitempty"`
	Status   string `json:"status"`
	Current  int64  `json:"current,omitempty"`
	Total    int64  `json:"total,omitempty"`
}

type JobProgressPayload struct {
	JobID   string   `json:"jobID"`
	Phase   string   `json:"phase"`
	Message string   `json:"message"`
	Pct     *float64 `json:"pct,omitempty"`
}

type JobDonePayload struct {
	JobID  string `json:"jobID"`
	Result string `json:"result,omitempty"`
	Error  string `json:"error,omitempty"`
}

func (c *Client) PullImage(ctx context.Context, imageRef string) (string, error) {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return "", err
	}
	ref := strings.TrimSpace(imageRef)
	if ref == "" {
		return "", apperror.New(apperror.Conflict, "Image reference is required")
	}
	normalized, err := registrycore.NormalizeImageRef(ref)
	if err != nil {
		return "", err
	}
	streamID := newJobID("pull")
	auth, err := c.registryAuthFor(ctx, normalized.Registry)
	if err != nil {
		return streamID, err
	}
	if err := c.pullImage(ctx, api, ref, normalized.Registry, streamID, auth); err != nil {
		return streamID, err
	}
	c.publishImageChanged(ref)
	return streamID, nil
}

func (c *Client) TagImage(ctx context.Context, imageID string, newRef string) error {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return err
	}
	imageID = strings.TrimSpace(imageID)
	if imageID == "" {
		return apperror.New(apperror.Conflict, "Image ID is required")
	}
	ref, err := registrycore.NormalizeImageRef(newRef)
	if err != nil {
		return err
	}
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()
	if err := api.ImageTag(callCtx, imageID, ref.Normalized); err != nil {
		return mapDockerError("tag image", err)
	}
	c.publishImageChanged(imageID)
	c.publishImageChanged(ref.Normalized)
	return nil
}

func (c *Client) PushImage(ctx context.Context, imageRef string) (string, error) {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return "", err
	}
	ref, err := registrycore.NormalizeImageRef(imageRef)
	if err != nil {
		return "", err
	}
	streamID := newJobID("push")
	auth, err := c.registryAuthFor(ctx, ref.Registry)
	if err != nil {
		return streamID, err
	}
	if err := c.pushImage(ctx, api, ref.Normalized, ref.Registry, streamID, auth); err != nil {
		return streamID, err
	}
	c.publishImageChanged(ref.Normalized)
	return streamID, nil
}

func (c *Client) RunImage(ctx context.Context, req models.RunImageRequest) (string, error) {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return "", err
	}
	req.ImageRef = strings.TrimSpace(req.ImageRef)
	req.Name = strings.TrimSpace(req.Name)
	req.NetworkID = strings.TrimSpace(req.NetworkID)
	req.RestartPolicy = strings.TrimSpace(req.RestartPolicy)
	req.User = strings.TrimSpace(req.User)
	if req.ImageRef == "" {
		return "", apperror.New(apperror.Conflict, "Image reference is required")
	}
	if req.Name != "" && !dockerNamePattern.MatchString(req.Name) {
		return "", apperror.New(apperror.Conflict, "Container name must start with a letter or number and use only letters, numbers, '.', '_' or '-'")
	}
	if req.Name != "" {
		if err := c.validateContainerNameAvailable(ctx, api, req.Name); err != nil {
			return "", err
		}
	}
	if err := validateRunImagePorts(req.Ports); err != nil {
		return "", err
	}
	if err := c.ensureImagePresent(ctx, api, req.ImageRef, req.PullIfMissing); err != nil {
		return "", err
	}

	config, hostConfig, networkingConfig, err := runImageConfig(req)
	if err != nil {
		return "", err
	}
	created, err := api.ContainerCreate(ctx, config, hostConfig, networkingConfig, nil, req.Name)
	if err != nil {
		return "", mapDockerError("create container", err)
	}
	if err := api.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return c.handleContainerStartFailure(ctx, api, created.ID, err)
	}
	c.publishContainerChanged(created.ID)
	return created.ID, nil
}

func (c *Client) handleContainerStartFailure(ctx context.Context, api APIClient, containerID string, cause error) (string, error) {
	startErr := mapDockerError("start container", cause)
	cleanupCtx, cancel := c.withTimeout(context.WithoutCancel(ctx))
	defer cancel()

	inspect, _, inspectErr := api.ContainerInspectWithRaw(cleanupCtx, containerID, false)
	if cerrdefs.IsNotFound(inspectErr) {
		// Another actor already removed the just-created container. The desired
		// rollback state is satisfied even though the original start still failed.
		c.publishContainerChanged(containerID)
		return "", startErr
	}
	state := inspectedContainerState(inspect)
	if inspectErr == nil && state == "created" {
		cleanupErr := api.ContainerRemove(cleanupCtx, containerID, container.RemoveOptions{RemoveVolumes: true})
		c.publishContainerChanged(containerID)
		if cleanupErr == nil || cerrdefs.IsNotFound(cleanupErr) {
			return "", startErr
		}
		return containerID, partialContainerStartError(
			startErr,
			containerID,
			state,
			fmt.Sprintf("automatic cleanup failed: %v", cleanupErr),
		)
	}

	// A transport/cancellation error can lose a successful start response. If
	// inspect cannot prove the object is still in Docker's never-started
	// "created" state, preserve it for reconciliation rather than deleting a
	// workload that may have run.
	c.publishContainerChanged(containerID)
	detail := "Docker could not confirm the created container state"
	if inspectErr != nil {
		detail = "inspect after start failure failed: " + inspectErr.Error()
	} else if state != "" {
		detail = "container state after start failure: " + state
	}
	return containerID, partialContainerStartError(startErr, containerID, state, detail)
}

func inspectedContainerState(inspect container.InspectResponse) string {
	if inspect.ContainerJSONBase == nil || inspect.State == nil {
		return "unknown"
	}
	state := strings.ToLower(strings.TrimSpace(inspect.State.Status))
	if state == "" {
		return "unknown"
	}
	return state
}

func partialContainerStartError(startErr error, containerID string, state string, detail string) error {
	code, ok := apperror.CodeOf(startErr)
	if !ok {
		code = apperror.DockerUnreachable
	}
	return apperror.Wrap(
		code,
		"Container start outcome requires reconciliation",
		startErr,
		apperror.WithDetail(fmt.Sprintf("container %s: %s", containerID, detail)),
		apperror.WithRepairHints("Inspect the created container's state, then stop or remove it if appropriate before retrying."),
		apperror.WithPartialResource("container", containerID, state, true),
	)
}

func (c *Client) RenameContainer(ctx context.Context, id string, newName string) error {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return err
	}
	name := strings.TrimSpace(newName)
	if !dockerNamePattern.MatchString(name) {
		return apperror.New(apperror.Conflict, "Container name must start with a letter or number and use only letters, numbers, '.', '_' or '-'")
	}
	if err := c.validateContainerNameAvailable(ctx, api, name); err != nil {
		return err
	}
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()
	if err := api.ContainerRename(callCtx, id, name); err != nil {
		return mapDockerError("rename container", err)
	}
	c.publishContainerChanged(id)
	return nil
}

func (c *Client) SaveImage(ctx context.Context, imageRefs []string, destPath string) (string, error) {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return "", err
	}
	refs := cleanImageRefs(imageRefs)
	if len(refs) == 0 {
		return "", apperror.New(apperror.Conflict, "At least one image reference is required")
	}
	path := strings.TrimSpace(destPath)
	if path == "" {
		return "", apperror.New(apperror.Conflict, "Destination path is required")
	}

	jobID := newJobID("save-image")
	c.publishJobProgress(jobID, "save", "Starting image save", nil)
	reader, err := api.ImageSave(ctx, refs)
	if err != nil {
		actionErr := mapDockerError("save image", err)
		c.publishJobDone(jobID, "", actionErr)
		return jobID, actionErr
	}
	if reader == nil {
		actionErr := apperror.New(apperror.DockerUnreachable, "Docker returned an empty image save stream")
		c.publishJobDone(jobID, "", actionErr)
		return jobID, actionErr
	}
	readerClosed := false
	defer func() {
		if !readerClosed {
			_ = reader.Close()
		}
	}()

	file, err := os.CreateTemp(filepath.Dir(path), ".cairn-image-save-*.tmp")
	if err != nil {
		actionErr := apperror.Wrap(apperror.Internal, "Create temporary image archive failed", err)
		c.publishJobDone(jobID, "", actionErr)
		return jobID, actionErr
	}
	temporaryPath := file.Name()
	defer func() {
		_ = os.Remove(temporaryPath)
	}()

	counter := &progressWriter{
		every: 1 << 20,
		onProgress: func(bytes int64) {
			c.publishJobProgress(jobID, "save", fmt.Sprintf("Saved %d bytes", bytes), nil)
		},
	}
	_, writeErr := writeSyncedImageArchive(file, io.TeeReader(reader, counter))
	closeReaderErr := reader.Close()
	readerClosed = true
	if err := errors.Join(writeErr, closeReaderErr); err != nil {
		actionErr := apperror.Wrap(apperror.Internal, "Write image archive failed", err)
		c.publishJobDone(jobID, "", actionErr)
		return jobID, actionErr
	}
	committed, err := publishImageArchiveNoClobber(temporaryPath, path)
	if err != nil {
		result := ""
		if committed {
			result = path
		}
		c.publishJobDone(jobID, result, err)
		return jobID, err
	}
	c.publishJobProgress(jobID, "save", "Image archive saved", floatPtr(100))
	c.publishJobDone(jobID, path, nil)
	return jobID, nil
}

func (c *Client) LoadImage(ctx context.Context, srcPath string) (string, error) {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(srcPath)
	if path == "" {
		return "", apperror.New(apperror.Conflict, "Source path is required")
	}
	file, identity, err := openVerifiedImageArchive(path)
	if err != nil {
		return "", err
	}
	fileClosed := false
	defer func() {
		if !fileClosed {
			_ = file.Close()
		}
	}()

	jobID := newJobID("load-image")
	c.publishJobProgress(jobID, "load", "Starting image load", nil)
	reader := &progressReader{
		reader: file,
		total:  identity.Size(),
		every:  1 << 20,
		onProgress: func(bytes int64, pct *float64) {
			c.publishJobProgress(jobID, "load", fmt.Sprintf("Read %d bytes", bytes), pct)
		},
	}
	// Once ImageLoad is invoked, Docker may have imported one or more images
	// even when the upload call, local archive verification, response stream, or
	// response close later fails. Reconcile from a bounded detached context on
	// every return after this mutation boundary, and notify inventory consumers
	// only after that reconciliation attempt has completed.
	reconcileCtx := context.WithoutCancel(ctx)
	defer func() {
		c.reconcileKind(reconcileCtx, objectKindImage)
		c.publishImageChanged("")
	}()

	response, loadErr := api.ImageLoad(ctx, reader)
	verifyErr := verifyOpenedImageArchive(path, file, identity)
	fileCloseErr := file.Close()
	fileClosed = true

	var outcome imageLoadResponseOutcome
	var responseErr error
	if response.Body != nil {
		var decodeErr error
		outcome, decodeErr = decodeImageLoadResponse(response.Body)
		responseCloseErr := response.Body.Close()
		if responseCloseErr != nil {
			responseCloseErr = apperror.Wrap(apperror.Internal, "Close image load response failed", responseCloseErr)
		}
		responseErr = errors.Join(decodeErr, responseCloseErr)
	} else if loadErr == nil {
		responseErr = apperror.New(apperror.DockerUnreachable, "Docker returned an empty image load response")
	}

	var mappedLoadErr error
	if loadErr != nil {
		mappedLoadErr = mapDockerError("load image", loadErr)
	}
	if fileCloseErr != nil {
		fileCloseErr = apperror.Wrap(apperror.Internal, "Close image archive failed", fileCloseErr)
	}
	if actionCause := errors.Join(mappedLoadErr, verifyErr, fileCloseErr, responseErr); actionCause != nil {
		actionErr := partialImageLoadError(actionCause, outcome)
		result := ""
		if outcome.Completed {
			result = outcome.Result
		}
		c.publishJobDone(jobID, result, actionErr)
		return jobID, actionErr
	}
	c.publishJobProgress(jobID, "load", "Image archive loaded", floatPtr(100))
	c.publishJobDone(jobID, outcome.Result, nil)
	return jobID, nil
}

type syncedWriteCloser interface {
	io.Writer
	Sync() error
	Close() error
}

func writeSyncedImageArchive(destination syncedWriteCloser, source io.Reader) (int64, error) {
	written, copyErr := io.Copy(destination, source)
	if copyErr != nil {
		return written, errors.Join(copyErr, destination.Close())
	}
	syncErr := destination.Sync()
	closeErr := destination.Close()
	return written, errors.Join(syncErr, closeErr)
}

func publishImageArchiveNoClobber(temporaryPath string, destinationPath string) (bool, error) {
	if err := os.Link(temporaryPath, destinationPath); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return false, apperror.New(
				apperror.Conflict,
				"Image archive destination already exists",
				apperror.WithDetail(destinationPath),
			)
		}
		return false, apperror.Wrap(
			apperror.Internal,
			"Publish image archive failed",
			err,
			apperror.WithDetail("The destination filesystem must support same-directory hard links for atomic no-clobber publication."),
		)
	}
	if err := os.Remove(temporaryPath); err != nil {
		return true, apperror.Wrap(
			apperror.Internal,
			"Image archive was saved but temporary file cleanup failed",
			err,
			apperror.WithPartialResource("file", destinationPath, "created", false),
		)
	}
	return true, nil
}

func openVerifiedImageArchive(path string) (*os.File, fs.FileInfo, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, nil, apperror.Wrap(apperror.NotFound, "Open image archive failed", err)
	}
	if err := validateImageArchiveInfo(before); err != nil {
		return nil, nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, apperror.Wrap(apperror.NotFound, "Open image archive failed", err)
	}
	opened, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, nil, apperror.Wrap(apperror.Internal, "Inspect opened image archive failed", err)
	}
	if err := validateImageArchiveInfo(opened); err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	if !os.SameFile(before, opened) {
		_ = file.Close()
		return nil, nil, changedImageArchiveError()
	}
	current, err := os.Lstat(path)
	if err != nil {
		_ = file.Close()
		return nil, nil, changedImageArchiveError()
	}
	if err := validateImageArchiveInfo(current); err != nil {
		_ = file.Close()
		return nil, nil, err
	}
	if !os.SameFile(opened, current) {
		_ = file.Close()
		return nil, nil, changedImageArchiveError()
	}
	return file, opened, nil
}

func verifyOpenedImageArchive(path string, file *os.File, expected fs.FileInfo) error {
	opened, err := file.Stat()
	if err != nil {
		return apperror.Wrap(apperror.Internal, "Reinspect opened image archive failed", err)
	}
	if err := validateImageArchiveInfo(opened); err != nil {
		return err
	}
	if !os.SameFile(expected, opened) ||
		expected.Size() != opened.Size() ||
		!expected.ModTime().Equal(opened.ModTime()) {
		return changedImageArchiveError()
	}
	current, err := os.Lstat(path)
	if err != nil {
		return changedImageArchiveError()
	}
	if err := validateImageArchiveInfo(current); err != nil {
		return err
	}
	if !os.SameFile(opened, current) {
		return changedImageArchiveError()
	}
	return nil
}

func validateImageArchiveInfo(info fs.FileInfo) error {
	if info == nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return apperror.New(
			apperror.Conflict,
			"Image archive must be a regular file",
			apperror.WithDetail("Symbolic links, directories, devices, and other non-regular files are rejected."),
		)
	}
	return nil
}

func changedImageArchiveError() error {
	return apperror.New(
		apperror.Conflict,
		"Image archive changed while it was being opened or loaded",
		apperror.WithDetail("Choose the archive again after confirming that no other process is replacing or modifying it."),
	)
}

type imageLoadResponseMessage struct {
	Stream       string `json:"stream"`
	Status       string `json:"status"`
	ErrorMessage string `json:"error"`
	ErrorDetail  *struct {
		Message string `json:"message"`
	} `json:"errorDetail"`
}

type imageLoadResponseOutcome struct {
	Result    string
	ImageID   string
	Completed bool
}

func decodeImageLoadResponse(body io.Reader) (imageLoadResponseOutcome, error) {
	if body == nil {
		return imageLoadResponseOutcome{}, apperror.New(apperror.DockerUnreachable, "Docker returned an empty image load response")
	}

	limited := &io.LimitedReader{R: body, N: maxImageLoadResponseBytes + 1}
	decoder := json.NewDecoder(limited)
	messages := make([]string, 0, 8)
	messageCount := 0
	loadCompleted := false
	loadedImageID := ""
	outcome := func() imageLoadResponseOutcome {
		return imageLoadResponseOutcome{
			Result:    providers.SafeCommandDiagnostic(strings.Join(messages, "\n"), maxImageLoadResultBytes),
			ImageID:   loadedImageID,
			Completed: loadCompleted,
		}
	}
	for {
		var raw json.RawMessage
		err := decoder.Decode(&raw)
		if limited.N == 0 {
			return outcome(), apperror.New(
				apperror.DockerUnreachable,
				"Docker image load response exceeded the safe size limit",
				apperror.WithDetail(fmt.Sprintf("Image load responses are limited to %d bytes.", maxImageLoadResponseBytes)),
			)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return outcome(), mapDockerError("read image load response", err)
			}
			return outcome(), apperror.Wrap(
				apperror.DockerUnreachable,
				"Docker returned a malformed image load response",
				err,
				apperror.WithDetail("The daemon response was not a complete JSON message stream."),
			)
		}

		messageCount++
		if messageCount > maxImageLoadResponseMessages {
			return outcome(), apperror.New(
				apperror.DockerUnreachable,
				"Docker image load response contained too many messages",
				apperror.WithDetail(fmt.Sprintf("Image load responses are limited to %d JSON messages.", maxImageLoadResponseMessages)),
			)
		}

		var message *imageLoadResponseMessage
		if err := json.Unmarshal(raw, &message); err != nil {
			return outcome(), apperror.Wrap(
				apperror.DockerUnreachable,
				"Docker returned an invalid image load message",
				err,
				apperror.WithDetail("Every image load response entry must be a JSON object."),
			)
		}
		if message == nil {
			return outcome(), apperror.New(
				apperror.DockerUnreachable,
				"Docker returned an invalid image load message",
				apperror.WithDetail("Every image load response entry must be a JSON object."),
			)
		}
		if message.ErrorMessage != "" || message.ErrorDetail != nil {
			detail := strings.TrimSpace(message.ErrorMessage)
			if detail == "" && message.ErrorDetail != nil {
				detail = strings.TrimSpace(message.ErrorDetail.Message)
			}
			if detail == "" {
				detail = "Docker reported an image load error without additional detail."
			}
			return outcome(), apperror.New(
				apperror.Conflict,
				"Docker rejected the image archive",
				apperror.WithDetail(providers.SafeCommandDiagnostic(detail, 8<<10)),
			)
		}
		if text := strings.TrimSpace(message.Stream); text != "" {
			messages = append(messages, text)
			if imageID, completed := imageLoadCompletion(text); completed {
				loadCompleted = true
				if loadedImageID == "" {
					loadedImageID = imageID
				}
			}
		} else if text := strings.TrimSpace(message.Status); text != "" {
			messages = append(messages, text)
		}
	}
	if messageCount == 0 {
		return outcome(), apperror.New(
			apperror.DockerUnreachable,
			"Docker returned an empty image load response",
			apperror.WithDetail("The daemon did not return any JSON status messages."),
		)
	}
	if !loadCompleted {
		return outcome(), apperror.New(
			apperror.DockerUnreachable,
			"Docker returned an incomplete image load response",
			apperror.WithDetail("The daemon response ended before confirming that an image was loaded."),
		)
	}
	return outcome(), nil
}

func imageLoadCompletion(stream string) (string, bool) {
	for line := range strings.Lines(stream) {
		line = strings.TrimSpace(line)
		for _, prefix := range []string{"Loaded image:", "Loaded image ID:"} {
			if strings.HasPrefix(line, prefix) {
				imageID := strings.TrimSpace(strings.TrimPrefix(line, prefix))
				if imageID != "" {
					return imageID, true
				}
			}
		}
	}
	return "", false
}

func partialImageLoadError(cause error, outcome imageLoadResponseOutcome) error {
	code, ok := apperror.CodeOf(cause)
	if !ok {
		code = apperror.DockerUnreachable
	}
	imageID := strings.TrimSpace(outcome.ImageID)
	state := "unknown"
	message := "Image load outcome requires reconciliation"
	detail := "Docker may have loaded one or more images, but Cairn could not confirm a complete response."
	hint := "Refresh the image inventory before retrying the archive."
	if outcome.Completed {
		state = "loaded"
		message = "Docker reported the image loaded but response finalization was incomplete"
		detail = "Docker emitted a terminal load record, but Cairn could not finish local verification or response processing."
		hint = "Treat the reported image as loaded and refresh the image inventory before deciding whether to retry."
	}
	if imageID == "" {
		imageID = "unknown"
	}
	return apperror.Wrap(
		code,
		message,
		cause,
		apperror.WithDetail(detail),
		apperror.WithRepairHints(hint),
		apperror.WithPartialResource("image", imageID, state, false),
	)
}

func (c *Client) SearchHub(ctx context.Context, query string, limit int) ([]models.HubSearchResult, error) {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return nil, err
	}
	term := strings.TrimSpace(query)
	if term == "" {
		return []models.HubSearchResult{}, nil
	}
	if limit <= 0 {
		limit = defaultImageSearchLimit
	}
	if limit > maxImageSearchLimit {
		limit = maxImageSearchLimit
	}
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()
	results, err := api.ImageSearch(callCtx, term, registry.SearchOptions{Limit: limit})
	if err != nil {
		return nil, mapDockerError("search Docker Hub", err)
	}
	out := make([]models.HubSearchResult, 0, len(results))
	for _, result := range results {
		out = append(out, models.HubSearchResult{
			Name:        result.Name,
			Description: result.Description,
			Stars:       result.StarCount,
			Official:    result.IsOfficial,
			Automated:   false,
		})
	}
	return out, nil
}

func (c *Client) CreateVolume(ctx context.Context, req models.CreateVolumeRequest) (*models.VolumeSummary, error) {
	var err error
	req, err = security.NormalizeCreateVolumeRequest(req)
	if err != nil {
		return nil, err
	}
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return nil, err
	}
	if req.Name == "" {
		return nil, apperror.New(apperror.Conflict, "Volume name is required")
	}
	if !dockerNamePattern.MatchString(req.Name) {
		return nil, apperror.New(apperror.Conflict, "Volume name must start with a letter or number and use only letters, numbers, '.', '_' or '-'")
	}
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()
	raw, err := api.VolumeCreate(callCtx, volume.CreateOptions{
		Name:       req.Name,
		Driver:     req.Driver,
		DriverOpts: req.DriverOpts,
		Labels:     req.Labels,
	})
	if err != nil {
		return nil, mapDockerError("create volume", err)
	}
	summary := mapVolumeSummary(raw)
	if err := c.saveVolumes(ctx, []store.VolumeCacheRecord{{
		Summary:   summary,
		CreatedAt: volumeCreatedAt(raw),
	}}, false); err != nil {
		return nil, err
	}
	c.publishVolumeChanged(summary.Name)
	return &summary, nil
}

func (c *Client) CreateNetwork(ctx context.Context, req models.CreateNetworkRequest) (*models.NetworkSummary, error) {
	api, err := c.ensureConnected(ctx)
	if err != nil {
		return nil, err
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Driver = strings.TrimSpace(req.Driver)
	req.Subnet = strings.TrimSpace(req.Subnet)
	req.Gateway = strings.TrimSpace(req.Gateway)
	if req.Name == "" {
		return nil, apperror.New(apperror.Conflict, "Network name is required")
	}
	if !dockerNamePattern.MatchString(req.Name) {
		return nil, apperror.New(apperror.Conflict, "Network name must start with a letter or number and use only letters, numbers, '.', '_' or '-'")
	}
	if req.Driver == "" {
		req.Driver = "bridge"
	}
	ipam, err := createIPAM(req.Subnet, req.Gateway)
	if err != nil {
		return nil, err
	}
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()
	created, err := api.NetworkCreate(callCtx, req.Name, network.CreateOptions{
		Driver:     req.Driver,
		IPAM:       ipam,
		Internal:   req.Internal,
		Attachable: req.Attachable,
		Labels:     req.Labels,
	})
	if err != nil {
		return nil, mapDockerError("create network", err)
	}
	raw, _, err := api.NetworkInspectWithRaw(callCtx, created.ID, network.InspectOptions{})
	if err != nil {
		return nil, mapDockerError("inspect created network", err)
	}
	summary := mapNetworkSummary(raw)
	subnet, gateway := networkIPAM(raw)
	if err := c.saveNetworks(ctx, []store.NetworkCacheRecord{{
		Summary:    summary,
		Subnet:     subnet,
		Gateway:    gateway,
		Containers: networkContainerIDs(raw),
	}}, false); err != nil {
		return nil, err
	}
	c.publishNetworkChanged(summary.ID)
	return &summary, nil
}

func (c *Client) ensureImagePresent(ctx context.Context, api APIClient, imageRef string, pullIfMissing bool) error {
	callCtx, cancel := c.withTimeout(ctx)
	_, _, err := api.ImageInspectWithRaw(callCtx, imageRef)
	cancel()
	if err == nil {
		return nil
	}
	if !cerrdefs.IsNotFound(err) {
		return mapDockerError("inspect image", err)
	}
	if !pullIfMissing {
		return apperror.Wrap(apperror.NotFound, "Image is not present locally", err, apperror.WithDetail(imageRef))
	}
	ref, err := registrycore.NormalizeImageRef(imageRef)
	if err != nil {
		return err
	}
	auth, err := c.registryAuthFor(ctx, ref.Registry)
	if err != nil {
		return err
	}
	return c.pullImage(ctx, api, imageRef, ref.Registry, newJobID("pull"), auth)
}

func (c *Client) validateContainerNameAvailable(ctx context.Context, api APIClient, name string) error {
	callCtx, cancel := c.withTimeout(ctx)
	defer cancel()
	_, _, err := api.ContainerInspectWithRaw(callCtx, name, false)
	if err == nil {
		return apperror.New(apperror.Conflict, "Container name is already in use", apperror.WithDetail(name))
	}
	if cerrdefs.IsNotFound(err) {
		return nil
	}
	return mapDockerError("inspect container name", err)
}

func (c *Client) pullImage(ctx context.Context, api APIClient, imageRef string, registry string, streamID string, auth string) error {
	c.publishImageProgress(bus.TopicImagePullProgress, streamID, "", "starting", 0, 0)
	reader, err := api.ImagePull(ctx, imageRef, image.PullOptions{RegistryAuth: auth})
	if err != nil {
		return mapRegistryPullError(registry, err)
	}
	defer func() {
		_ = reader.Close()
	}()
	decoder := json.NewDecoder(reader)
	for {
		var message struct {
			ID             string `json:"id"`
			Status         string `json:"status"`
			ErrorMessage   string `json:"error"`
			ProgressDetail struct {
				Current int64 `json:"current"`
				Total   int64 `json:"total"`
			} `json:"progressDetail"`
		}
		if err := decoder.Decode(&message); err != nil {
			if err == io.EOF {
				break
			}
			return mapDockerError("read pull progress", err)
		}
		if message.ErrorMessage != "" {
			return registryPullStreamError(registry, message.ErrorMessage)
		}
		status := message.Status
		if status == "" {
			status = "progress"
		}
		c.publishImageProgress(bus.TopicImagePullProgress, streamID, message.ID, status, message.ProgressDetail.Current, message.ProgressDetail.Total)
	}
	c.publishImageProgress(bus.TopicImagePullProgress, streamID, "", "done", 0, 0)
	return nil
}

func (c *Client) registryAuthFor(ctx context.Context, registry string) (string, error) {
	if c.registryAuth != nil {
		return c.registryAuth(ctx, registry)
	}
	provider, ok := c.provider.(providers.PlatformProvider)
	if !ok {
		return "", nil
	}
	return registrycore.EncodeDockerAuthConfig(ctx, provider, registry)
}

func (c *Client) pushImage(ctx context.Context, api APIClient, imageRef string, registry string, streamID string, auth string) error {
	c.publishImageProgress(bus.TopicImagePushProgress, streamID, "", "starting", 0, 0)
	reader, err := api.ImagePush(ctx, imageRef, image.PushOptions{RegistryAuth: auth})
	if err != nil {
		return mapRegistryPushError(registry, err)
	}
	defer func() {
		_ = reader.Close()
	}()
	decoder := json.NewDecoder(reader)
	for {
		var message struct {
			ID           string `json:"id"`
			Status       string `json:"status"`
			ErrorMessage string `json:"error"`
			ErrorDetail  struct {
				Message string `json:"message"`
			} `json:"errorDetail"`
			ProgressDetail struct {
				Current int64 `json:"current"`
				Total   int64 `json:"total"`
			} `json:"progressDetail"`
		}
		if err := decoder.Decode(&message); err != nil {
			if err == io.EOF {
				break
			}
			return mapDockerError("read push progress", err)
		}
		if message.ErrorMessage != "" || message.ErrorDetail.Message != "" {
			detail := message.ErrorMessage
			if detail == "" {
				detail = message.ErrorDetail.Message
			}
			return registryPushStreamError(registry, detail)
		}
		status := message.Status
		if status == "" {
			status = "progress"
		}
		c.publishImageProgress(bus.TopicImagePushProgress, streamID, message.ID, status, message.ProgressDetail.Current, message.ProgressDetail.Total)
	}
	c.publishImageProgress(bus.TopicImagePushProgress, streamID, "", "done", 0, 0)
	return nil
}

func runImageConfig(req models.RunImageRequest) (*container.Config, *container.HostConfig, *network.NetworkingConfig, error) {
	exposedPorts := nat.PortSet{}
	portBindings := nat.PortMap{}
	for _, mapping := range req.Ports {
		port, err := nat.NewPort(protocolOrDefault(mapping.Protocol), strings.TrimSpace(mapping.ContainerPort))
		if err != nil {
			return nil, nil, nil, apperror.Wrap(apperror.Conflict, "Invalid container port", err)
		}
		exposedPorts[port] = struct{}{}
		portBindings[port] = append(portBindings[port], nat.PortBinding{
			HostIP:   strings.TrimSpace(mapping.HostIP),
			HostPort: strings.TrimSpace(mapping.HostPort),
		})
	}

	mounts := make([]dockermount.Mount, 0, len(req.Volumes))
	volumes := map[string]struct{}{}
	for _, spec := range req.Volumes {
		mountSpec, err := mountFromSpec(spec)
		if err != nil {
			return nil, nil, nil, err
		}
		mounts = append(mounts, mountSpec)
		volumes[mountSpec.Target] = struct{}{}
	}

	hostConfig := &container.HostConfig{
		PortBindings: portBindings,
		Mounts:       mounts,
	}
	if req.NetworkID != "" {
		hostConfig.NetworkMode = container.NetworkMode(req.NetworkID)
	}
	if policy := restartPolicy(req.RestartPolicy); policy != "" {
		hostConfig.RestartPolicy = container.RestartPolicy{Name: policy}
	}
	networkingConfig := &network.NetworkingConfig{}
	if req.NetworkID != "" {
		networkingConfig.EndpointsConfig = map[string]*network.EndpointSettings{
			req.NetworkID: {},
		}
	}
	config := &container.Config{
		Image:        req.ImageRef,
		Env:          envList(req.Env),
		Cmd:          req.Command,
		User:         req.User,
		ExposedPorts: exposedPorts,
		Volumes:      volumes,
	}
	return config, hostConfig, networkingConfig, nil
}

func mountFromSpec(spec models.MountSpec) (dockermount.Mount, error) {
	target := strings.TrimSpace(spec.Target)
	if target == "" {
		return dockermount.Mount{}, apperror.New(apperror.Conflict, "Mount target is required")
	}
	mountType := strings.TrimSpace(spec.Type)
	if mountType == "" {
		mountType = "volume"
	}
	source := strings.TrimSpace(spec.Source)
	if mountType == "volume" && strings.TrimSpace(spec.VolumeName) != "" {
		source = strings.TrimSpace(spec.VolumeName)
	}
	switch mountType {
	case "volume":
		if source == "" {
			return dockermount.Mount{}, apperror.New(apperror.Conflict, "Volume source is required")
		}
		return dockermount.Mount{Type: dockermount.TypeVolume, Source: source, Target: target, ReadOnly: spec.ReadOnly}, nil
	case "bind":
		if source == "" {
			return dockermount.Mount{}, apperror.New(apperror.Conflict, "Bind source is required")
		}
		return dockermount.Mount{Type: dockermount.TypeBind, Source: source, Target: target, ReadOnly: spec.ReadOnly}, nil
	default:
		return dockermount.Mount{}, apperror.New(apperror.Conflict, "Unsupported mount type", apperror.WithDetail(mountType))
	}
}

func validateRunImagePorts(ports []models.PortMapping) error {
	for _, mapping := range ports {
		protocol := protocolOrDefault(mapping.Protocol)
		if protocol != "tcp" && protocol != "udp" && protocol != "sctp" {
			return apperror.New(apperror.Conflict, "Unsupported port protocol", apperror.WithDetail(protocol))
		}
		if err := validatePort("container port", mapping.ContainerPort, false); err != nil {
			return err
		}
		if err := validatePort("host port", mapping.HostPort, true); err != nil {
			return err
		}
	}
	return nil
}

func validatePort(label string, value string, allowEmpty bool) error {
	value = strings.TrimSpace(value)
	if value == "" {
		if allowEmpty {
			return nil
		}
		return apperror.New(apperror.Conflict, label+" is required")
	}
	port, err := strconv.Atoi(value)
	if err != nil || port < 0 || port > 65535 || (!allowEmpty && port == 0) {
		return apperror.New(apperror.Conflict, "Invalid "+label, apperror.WithDetail(value))
	}
	return nil
}

func createIPAM(subnet string, gateway string) (*network.IPAM, error) {
	if subnet == "" && gateway == "" {
		return nil, nil
	}
	cfg := network.IPAMConfig{}
	if subnet != "" {
		if _, _, err := net.ParseCIDR(subnet); err != nil {
			return nil, apperror.Wrap(apperror.Conflict, "Invalid subnet CIDR", err)
		}
		cfg.Subnet = subnet
	}
	if gateway != "" {
		if net.ParseIP(gateway) == nil {
			return nil, apperror.New(apperror.Conflict, "Invalid gateway IP", apperror.WithDetail(gateway))
		}
		cfg.Gateway = gateway
	}
	return &network.IPAM{Config: []network.IPAMConfig{cfg}}, nil
}

func restartPolicy(value string) container.RestartPolicyMode {
	switch strings.TrimSpace(value) {
	case "", restartPolicyNone:
		return ""
	case "on-failure":
		return container.RestartPolicyOnFailure
	case "unless-stopped":
		return container.RestartPolicyUnlessStopped
	case "always":
		return container.RestartPolicyAlways
	default:
		return container.RestartPolicyMode(value)
	}
}

func envList(values []models.EnvVar) []string {
	byName := make(map[string]string, len(values))
	for _, item := range values {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		byName[name] = item.Value
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, name+"="+byName[name])
	}
	return out
}

func protocolOrDefault(protocol string) string {
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	if protocol == "" {
		return "tcp"
	}
	return protocol
}

func cleanImageRefs(refs []string) []string {
	out := make([]string, 0, len(refs))
	seen := map[string]struct{}{}
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		if _, ok := seen[ref]; ok {
			continue
		}
		seen[ref] = struct{}{}
		out = append(out, ref)
	}
	return out
}

func newJobID(prefix string) string {
	return prefix + "-" + uuid.NewString()
}

func (c *Client) publishImageProgress(topic bus.Topic, streamID string, layerID string, status string, current int64, total int64) {
	c.publish(topic, ImageProgressPayload{
		StreamID: streamID,
		LayerID:  layerID,
		Status:   status,
		Current:  current,
		Total:    total,
	})
}

func (c *Client) publishJobProgress(jobID string, phase string, message string, pct *float64) {
	c.publish(bus.TopicJobProgress, JobProgressPayload{
		JobID:   jobID,
		Phase:   phase,
		Message: message,
		Pct:     pct,
	})
}

func (c *Client) publishJobDone(jobID string, result string, actionErr error) {
	payload := JobDonePayload{JobID: jobID, Result: result}
	if actionErr != nil {
		payload.Error = actionErr.Error()
	}
	c.publishCritical(bus.TopicJobDone, payload)
}

func (c *Client) publishImageChanged(id string) {
	c.publish(bus.TopicObjectsChanged, ObjectsChangedPayload{Kind: objectKindImage, IDs: []string{id}})
}

func (c *Client) publishVolumeChanged(name string) {
	c.publish(bus.TopicObjectsChanged, ObjectsChangedPayload{Kind: objectKindVolume, IDs: []string{name}})
}

func (c *Client) publishNetworkChanged(id string) {
	c.publish(bus.TopicObjectsChanged, ObjectsChangedPayload{Kind: objectKindNetwork, IDs: []string{id}})
}

func floatPtr(value float64) *float64 {
	return &value
}

func mapRegistryPushError(registry string, err error) error {
	return mapRegistryOperationError("push", registry, err)
}

func mapRegistryPullError(registry string, err error) error {
	return mapRegistryOperationError("pull", registry, err)
}

func mapRegistryOperationError(operation string, registry string, err error) error {
	if err == nil {
		return nil
	}
	if registryAuthMessage(err.Error()) {
		return apperror.Wrap(apperror.RegistryAuth, "Registry authentication failed", err, apperror.WithDetail(registry+": "+err.Error()))
	}
	if registryRateLimitMessage(err.Error()) {
		return apperror.Wrap(apperror.RegistryRateLimit, "Registry rate limit reached", err, apperror.WithDetail(registry+": "+err.Error()))
	}
	return mapDockerError(operation+" image", err)
}

func registryPushStreamError(registry string, detail string) error {
	return registryStreamError("Push", registry, detail)
}

func registryPullStreamError(registry string, detail string) error {
	return registryStreamError("Pull", registry, detail)
}

func registryStreamError(operation string, registry string, detail string) error {
	if registryAuthMessage(detail) {
		return apperror.New(apperror.RegistryAuth, "Registry authentication failed", apperror.WithDetail(registry+": "+detail))
	}
	if registryRateLimitMessage(detail) {
		return apperror.New(apperror.RegistryRateLimit, "Registry rate limit reached", apperror.WithDetail(registry+": "+detail))
	}
	return apperror.New(apperror.RegistryUnreachable, operation+" image failed", apperror.WithDetail(detail))
}

func registryAuthMessage(detail string) bool {
	lower := strings.ToLower(detail)
	for _, marker := range []string{
		"authentication required",
		"authorization required",
		"unauthorized",
		"denied",
		"forbidden",
		"no basic auth credentials",
		"invalid username",
		"invalid password",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func registryRateLimitMessage(detail string) bool {
	lower := strings.ToLower(detail)
	return strings.Contains(lower, "too many requests") || strings.Contains(lower, "rate limit")
}

type progressWriter struct {
	written    int64
	last       int64
	every      int64
	onProgress func(int64)
}

func (w *progressWriter) Write(p []byte) (int, error) {
	w.written += int64(len(p))
	if w.every <= 0 || w.written-w.last >= w.every {
		w.last = w.written
		w.onProgress(w.written)
	}
	return len(p), nil
}

type progressReader struct {
	reader     io.Reader
	read       int64
	last       int64
	total      int64
	every      int64
	onProgress func(int64, *float64)
}

func (r *progressReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.read += int64(n)
	}
	if n > 0 && (r.every <= 0 || r.read-r.last >= r.every || err == io.EOF) {
		r.emitProgress()
	} else if err == io.EOF && r.read != r.last {
		r.emitProgress()
	}
	return n, err
}

func (r *progressReader) emitProgress() {
	r.last = r.read
	var pct *float64
	if r.total > 0 {
		value := float64(r.read) / float64(r.total) * 100
		pct = &value
	}
	r.onProgress(r.read, pct)
}
