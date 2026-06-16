package apperror

import (
	"encoding/json"
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

func TestMarshalProducesContractJSON(t *testing.T) {
	err := New(PermissionDenied, "permission denied", WithRepairHints("Fix permissions"))

	var payload map[string]any
	if marshalErr := json.Unmarshal(Marshal(err), &payload); marshalErr != nil {
		t.Fatalf("Marshal returned invalid JSON: %v", marshalErr)
	}

	if payload["code"] != string(PermissionDenied) {
		t.Fatalf("code = %#v, want %s", payload["code"], PermissionDenied)
	}
	if payload["message"] != "permission denied" {
		t.Fatalf("message = %#v, want permission denied", payload["message"])
	}
}

func TestMarshalPlainErrorBecomesInternal(t *testing.T) {
	var payload map[string]any
	if marshalErr := json.Unmarshal(Marshal(errors.New("secret raw error")), &payload); marshalErr != nil {
		t.Fatalf("Marshal returned invalid JSON: %v", marshalErr)
	}

	if payload["code"] != string(Internal) {
		t.Fatalf("code = %#v, want %s", payload["code"], Internal)
	}
	if _, ok := payload["detail"]; ok {
		t.Fatalf("plain error detail leaked: %#v", payload["detail"])
	}
}

func TestMarshalAppErrorFallsBackWhenJSONMarshalFails(t *testing.T) {
	out := marshalAppError(New(Conflict, "will not encode"), func(any) ([]byte, error) {
		return nil, errors.New("json encoder failed")
	})

	var payload map[string]any
	if marshalErr := json.Unmarshal(out, &payload); marshalErr != nil {
		t.Fatalf("fallback returned invalid JSON: %v", marshalErr)
	}
	if payload["code"] != string(Internal) {
		t.Fatalf("code = %#v, want %s", payload["code"], Internal)
	}
	if payload["message"] != "Internal error" {
		t.Fatalf("message = %#v, want Internal error", payload["message"])
	}
}
