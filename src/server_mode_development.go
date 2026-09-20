//go:build server && cairn_server_dev

package main

import (
	"fmt"
	"os"
	"strings"
)

const (
	unsafeServerDevelopmentAcknowledgementEnv = "CAIRN_ENABLE_UNSAFE_SERVER_DEVELOPMENT"
	unsafeServerDevelopmentAcknowledgement    = "I_ACKNOWLEDGE_THIS_HAS_NO_AUTHENTICATION"
	serverHostEnv                             = "WAILS_SERVER_HOST"
	serverDevelopmentHost                     = "127.0.0.1"
)

func configureServerMode() error {
	if os.Getenv(unsafeServerDevelopmentAcknowledgementEnv) != unsafeServerDevelopmentAcknowledgement {
		return fmt.Errorf(
			"development server mode is disabled; set %s=%s only in an isolated development environment",
			unsafeServerDevelopmentAcknowledgementEnv,
			unsafeServerDevelopmentAcknowledgement,
		)
	}

	configuredHost := strings.TrimSpace(os.Getenv(serverHostEnv))
	if configuredHost != "" && configuredHost != serverDevelopmentHost {
		return fmt.Errorf(
			"development server mode may only bind to %s; %s was %q",
			serverDevelopmentHost,
			serverHostEnv,
			configuredHost,
		)
	}
	if err := os.Setenv(serverHostEnv, serverDevelopmentHost); err != nil {
		return fmt.Errorf("force development server loopback binding: %w", err)
	}

	return nil
}
