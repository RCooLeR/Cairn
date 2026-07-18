package compose

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RCooLeR/Cairn/internal/apperror"
	"github.com/RCooLeR/Cairn/internal/providers"
)

type fixedComposeOutputRunner struct {
	stdout          string
	stdoutTruncated bool
}

func (runner fixedComposeOutputRunner) RunCompose(context.Context, string, ...string) (*providers.CommandResult, error) {
	return &providers.CommandResult{Stdout: runner.stdout, StdoutTruncated: runner.stdoutTruncated}, nil
}

func (runner fixedComposeOutputRunner) RunComposeEnv(ctx context.Context, workdir string, _ []string, args ...string) (*providers.CommandResult, error) {
	return runner.RunCompose(ctx, workdir, args...)
}

func TestComposeParsersRejectCapturedOutputTruncationMarkers(t *testing.T) {
	marker := composeOutputTruncationToken + "17 bytes]..."
	tests := []struct {
		name   string
		stdout string
		run    func(*Client) error
	}{
		{
			name:   "version",
			stdout: `{"version":"v2.40.3","note":"` + marker + `"}`,
			run: func(client *Client) error {
				_, err := client.Version(context.Background())
				return err
			},
		},
		{
			name:   "projects",
			stdout: `[{"Name":"demo","Status":"` + marker + `"}]`,
			run: func(client *Client) error {
				_, err := client.Ls(context.Background(), ListOptions{})
				return err
			},
		},
		{
			name:   "services",
			stdout: `{"Name":"app","State":"running","Status":"` + marker + `"}`,
			run: func(client *Client) error {
				_, err := client.Ps(context.Background(), ProjectOptions{})
				return err
			},
		},
		{
			name:   "config",
			stdout: "services:\n  app:\n    image: 'example/" + marker + "'\n",
			run: func(client *Client) error {
				_, err := client.Config(context.Background(), ProjectOptions{})
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.run(NewClient(fixedComposeOutputRunner{stdout: test.stdout}))
			if !apperror.IsCode(err, apperror.ComposeInvalid) {
				t.Fatalf("error = %v, want compose invalid", err)
			}
			if strings.Contains(err.Error(), marker) {
				t.Fatalf("error exposed the captured truncation payload: %v", err)
			}
		})
	}
}

func TestComposeConfigRejectsOversizedInjectedRunnerOutput(t *testing.T) {
	stdout := strings.Repeat(" ", maxComposeParseOutputBytes) + "services: {}\n"
	_, err := NewClient(fixedComposeOutputRunner{stdout: stdout}).Config(context.Background(), ProjectOptions{})
	if !apperror.IsCode(err, apperror.ComposeInvalid) {
		t.Fatalf("Config() error = %v, want compose invalid", err)
	}
}

func TestConfigVerifiedRejectsExplicitStdoutTruncationFlag(t *testing.T) {
	root := t.TempDir()
	composePath := filepath.Join(root, "compose.yaml")
	if err := os.WriteFile(composePath, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatalf("write Compose file: %v", err)
	}
	client := NewClient(fixedComposeOutputRunner{stdout: "services: {}\n", stdoutTruncated: true})
	config, _, err := client.ConfigVerified(context.Background(), ProjectOptions{Workdir: root, Files: []string{composePath}})
	if config != nil || !apperror.IsCode(err, apperror.ComposeInvalid) {
		t.Fatalf("ConfigVerified() = (%#v, %v), want bounded compose-invalid rejection", config, err)
	}
}
