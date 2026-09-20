package providers

import (
	"context"
	"fmt"
	"slices"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/models"
)

type detectionKey struct {
	id       string
	settings string
}

type providerDetection struct {
	done    chan struct{}
	cancel  context.CancelFunc
	waiters int
	status  *models.ProviderStatus
	err     error
}

// detectShared joins only an in-flight probe for the same provider settings.
// Each caller may cancel independently; the underlying commands are cancelled
// when the last caller leaves. Completed probes are never cached here.
func (m *Manager) detectShared(ctx context.Context, provider PlatformProvider) (*models.ProviderStatus, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.providerConfigMu.Lock()
	m.applyProviderSettingsLocked(ctx, provider)
	key := providerDetectionKey(provider)
	m.mu.Lock()
	if m.detections == nil {
		m.detections = make(map[detectionKey]*providerDetection)
	}
	call := m.detections[key]
	if call == nil {
		probeCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		call = &providerDetection{done: make(chan struct{}), cancel: cancel}
		m.detections[key] = call
		probe := snapshotDetectionProvider(provider)
		go m.runDetection(probeCtx, provider, probe, key, call)
	}
	call.waiters++
	m.mu.Unlock()
	m.providerConfigMu.Unlock()
	defer func() {
		m.mu.Lock()
		call.waiters--
		if call.waiters == 0 {
			call.cancel()
			if m.detections[key] == call {
				delete(m.detections, key)
			}
		}
		m.mu.Unlock()
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-call.done:
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if call.status == nil {
			return nil, call.err
		}
		status := *call.status
		status.Problems = slices.Clone(status.Problems)
		status.Warnings = slices.Clone(status.Warnings)
		return &status, call.err
	}
}

func (m *Manager) runDetection(ctx context.Context, provider, probe PlatformProvider, key detectionKey, call *providerDetection) {
	defer call.cancel()
	status, err := detectProvider(ctx, probe)
	if err == nil {
		m.providerConfigMu.Lock()
		// Settings can change while WSL boots. Never write the old target's
		// health over a newly selected distribution/profile.
		m.applyProviderSettingsLocked(ctx, provider)
		if ctx.Err() != nil {
			err = ctx.Err()
		} else if providerDetectionKey(provider) != key {
			err = apperror.New(apperror.Conflict, "Provider settings changed during detection; retry detection")
		} else {
			if original, ok := provider.(*WindowsWSLProvider); ok {
				original.SetDistro(probe.(*WindowsWSLProvider).configuredDistro())
			}
			if m.repo != nil {
				err = m.repo.SaveStatus(ctx, provider.ID(), status, m.now())
			}
		}
		m.providerConfigMu.Unlock()
	}
	if err != nil {
		status = nil
	}
	m.mu.Lock()
	call.status, call.err = status, err
	if m.detections[key] == call {
		delete(m.detections, key)
	}
	close(call.done)
	m.mu.Unlock()
}

func providerDetectionKey(provider PlatformProvider) detectionKey {
	key := detectionKey{id: provider.ID()}
	switch p := provider.(type) {
	case *WindowsWSLProvider:
		key.settings = p.configuredDistro()
	case *MacOSColimaProvider:
		p.configMu.RLock()
		key.settings = fmt.Sprintf("%s\x00%d\x00%d\x00%d", p.profile, p.cpu, p.memoryGB, p.diskGB)
		p.configMu.RUnlock()
	}
	return key
}

func snapshotDetectionProvider(provider PlatformProvider) PlatformProvider {
	switch p := provider.(type) {
	case *WindowsWSLProvider:
		return NewWindowsWSL(WindowsWSLOptions{Distro: p.configuredDistro(), Runner: p.runner, StdioDialer: p.stdioDialer, IDs: p.ids})
	case *MacOSColimaProvider:
		p.configMu.RLock()
		defer p.configMu.RUnlock()
		return NewMacOSColima(MacOSColimaOptions{
			Profile: p.profile, CPU: p.cpu, MemoryGB: p.memoryGB, DiskGB: p.diskGB,
			Runner: p.runner, HomeDir: p.homeDir, IDs: p.ids,
		})
	default:
		return provider
	}
}
