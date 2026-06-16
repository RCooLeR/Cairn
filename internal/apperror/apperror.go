package apperror

import (
	"encoding/json"
	"errors"
	"fmt"
)

type Code string

const (
	DockerUnreachable    Code = "E_DOCKER_UNREACHABLE"
	ProviderNotReady     Code = "E_PROVIDER_NOT_READY"
	ProviderDetectFailed Code = "E_PROVIDER_DETECT_FAILED"
	ComposeNotFound      Code = "E_COMPOSE_NOT_FOUND"
	ComposeInvalid       Code = "E_COMPOSE_INVALID"
	WorkdirMissing       Code = "E_WORKDIR_MISSING"
	PermissionDenied     Code = "E_PERMISSION_DENIED"
	RegistryAuth         Code = "E_REGISTRY_AUTH"
	RegistryRateLimit    Code = "E_REGISTRY_RATE_LIMIT"
	RegistryUnreachable  Code = "E_REGISTRY_UNREACHABLE"
	NotFound             Code = "E_NOT_FOUND"
	Conflict             Code = "E_CONFLICT"
	PlanExpired          Code = "E_PLAN_EXPIRED"
	ConfirmationRequired Code = "E_CONFIRMATION_REQUIRED"
	Timeout              Code = "E_TIMEOUT"
	Cancelled            Code = "E_CANCELLED"
	Internal             Code = "E_INTERNAL"
)

type AppError struct {
	Code        Code     `json:"code"`
	Message     string   `json:"message"`
	Detail      string   `json:"detail,omitempty"`
	RepairHints []string `json:"repairHints,omitempty"`
	Cause       error    `json:"-"`
}

type Option func(*AppError)

func New(code Code, message string, opts ...Option) *AppError {
	err := &AppError{Code: code, Message: message}
	for _, opt := range opts {
		opt(err)
	}
	return err
}

func Wrap(code Code, message string, cause error, opts ...Option) *AppError {
	err := &AppError{Code: code, Message: message, Cause: cause}
	for _, opt := range opts {
		opt(err)
	}
	return err
}

func WithDetail(detail string) Option {
	return func(err *AppError) {
		err.Detail = detail
	}
}

func WithRepairHints(hints ...string) Option {
	return func(err *AppError) {
		err.RepairHints = append([]string(nil), hints...)
	}
}

func WithCause(cause error) Option {
	return func(err *AppError) {
		err.Cause = cause
	}
}

func (e *AppError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Message == "" {
		return string(e.Code)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *AppError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func IsCode(err error, code Code) bool {
	var appErr *AppError
	if !errors.As(err, &appErr) {
		return false
	}
	return appErr.Code == code
}

func CodeOf(err error) (Code, bool) {
	var appErr *AppError
	if !errors.As(err, &appErr) {
		return "", false
	}
	return appErr.Code, true
}

func Marshal(err error) []byte {
	if err == nil {
		return nil
	}

	var appErr *AppError
	if !errors.As(err, &appErr) {
		appErr = New(Internal, "Internal error")
	}

	return marshalAppError(appErr, json.Marshal)
}

func marshalAppError(appErr *AppError, marshal func(any) ([]byte, error)) []byte {
	out, marshalErr := marshal(appErr)
	if marshalErr != nil {
		return []byte(`{"code":"E_INTERNAL","message":"Internal error"}`)
	}
	return out
}
