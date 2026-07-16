//go:build server && cairn_server_dev

package main

import (
	"os"
	"strings"
	"testing"
)

func TestConfigureServerModeRequiresExplicitAcknowledgement(t *testing.T) {
	t.Setenv(unsafeServerDevelopmentAcknowledgementEnv, "")
	t.Setenv(serverHostEnv, "")

	err := configureServerMode()
	if err == nil || !strings.Contains(err.Error(), unsafeServerDevelopmentAcknowledgementEnv) {
		t.Fatalf("configureServerMode() error = %v, want acknowledgement error", err)
	}
}

func TestConfigureServerModeRejectsNonLoopbackHost(t *testing.T) {
	t.Setenv(unsafeServerDevelopmentAcknowledgementEnv, unsafeServerDevelopmentAcknowledgement)
	t.Setenv(serverHostEnv, "0.0.0.0")

	err := configureServerMode()
	if err == nil || !strings.Contains(err.Error(), serverDevelopmentHost) {
		t.Fatalf("configureServerMode() error = %v, want loopback-only error", err)
	}
}

func TestConfigureServerModeForcesLoopbackHost(t *testing.T) {
	t.Setenv(unsafeServerDevelopmentAcknowledgementEnv, unsafeServerDevelopmentAcknowledgement)
	t.Setenv(serverHostEnv, "")

	if err := configureServerMode(); err != nil {
		t.Fatalf("configureServerMode() error = %v", err)
	}
	if got := strings.TrimSpace(os.Getenv(serverHostEnv)); got != serverDevelopmentHost {
		t.Fatalf("%s = %q, want %q", serverHostEnv, got, serverDevelopmentHost)
	}
}
