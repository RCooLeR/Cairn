package apperror

import (
	"errors"
	"testing"
)

func TestWrapPreservesCodeAndCause(t *testing.T) {
	cause := errors.New("dial unix socket")
	err := Wrap(DockerUnreachable, "Docker daemon is unreachable", cause, WithDetail("ping failed"), WithRepairHints("Start Docker"))

	if !errors.Is(err, cause) {
		t.Fatalf("wrapped error does not preserve cause")
	}
	if !IsCode(err, DockerUnreachable) {
		t.Fatalf("IsCode = false, want true")
	}
	if code, ok := CodeOf(err); !ok || code != DockerUnreachable {
		t.Fatalf("CodeOf = %q, %v; want %q, true", code, ok, DockerUnreachable)
	}
	if err.Detail != "ping failed" {
		t.Fatalf("detail = %q, want ping failed", err.Detail)
	}
	if len(err.RepairHints) != 1 || err.RepairHints[0] != "Start Docker" {
		t.Fatalf("repair hints = %#v", err.RepairHints)
	}
}

func TestCodeHelpersIgnorePlainErrors(t *testing.T) {
	err := errors.New("plain")
	if IsCode(err, Internal) {
		t.Fatalf("plain error matched app code")
	}
	if code, ok := CodeOf(err); ok || code != "" {
		t.Fatalf("CodeOf plain error = %q, %v; want empty, false", code, ok)
	}
}
