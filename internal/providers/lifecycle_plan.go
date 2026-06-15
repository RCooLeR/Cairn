package providers

import (
	"strings"

	"github.com/RCooLeR/Cairn/internal/apperror"
)

func lifecycleCommand(provider PlatformProvider, action string) (string, error) {
	action = strings.ToLower(strings.TrimSpace(action))
	if action != "start" && action != "stop" && action != "restart" {
		return "", apperror.New(apperror.Conflict, "Unsupported provider action", apperror.WithDetail(action))
	}
	var command []string
	switch p := provider.(type) {
	case *LinuxNativeProvider:
		command = []string{"systemctl", action, "docker"}
	case *WindowsWSLProvider:
		command = []string{wslCommandName, "-d", p.configuredDistro(), "--", "systemctl", action, "docker"}
	case *MacOSColimaProvider:
		switch action {
		case "start":
			command = append([]string{colimaCommandName}, p.colimaStartArgs()...)
		case "stop":
			command = []string{colimaCommandName, "stop", "-p", p.configuredProfile()}
		case "restart":
			command = []string{colimaCommandName, "restart", "-p", p.configuredProfile()}
		}
	case *ExistingContextProvider:
		return "", apperror.New(apperror.ProviderNotReady, "Cairn cannot "+action+" an existing Docker context")
	default:
		return "", apperror.New(apperror.ProviderNotReady, "Provider lifecycle planning is not supported")
	}
	return displayCommand(command), nil
}
