package providers

import (
	"strings"
	"time"
)

func composeTimeoutForArgs(args []string) time.Duration {
	switch composeVerb(args) {
	case "build", "create", "down", "kill", "pause", "pull", "push", "restart", "rm", "run", "scale", "start", "stop", "unpause", "up", "watch":
		return dockerOperationTimeout
	default:
		return composeCommandTimeout
	}
}

func composeVerb(args []string) string {
	for i := 0; i < len(args); i++ {
		token := strings.TrimSpace(args[i])
		if token == "" {
			continue
		}
		if strings.HasPrefix(token, "-") {
			if composeFlagTakesValue(token) && i+1 < len(args) {
				i++
			}
			continue
		}
		return strings.ToLower(token)
	}
	return ""
}

func composeFlagTakesValue(flag string) bool {
	if strings.Contains(flag, "=") {
		return false
	}
	switch flag {
	case "-f", "--file",
		"-p", "--project-name",
		"--profile",
		"--project-directory",
		"--env-file",
		"--ansi",
		"--progress",
		"--parallel":
		return true
	default:
		return false
	}
}
