package security

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"strings"
	"sync"

	"github.com/RCooLeR/Cairn/internal/apperror"
)

// IDSource generates opaque plan and job identifiers from an injected entropy
// reader. A nil *IDSource uses crypto/rand.Reader, so optional dependency fields
// can safely retain their zero value in production.
type IDSource struct {
	mu     sync.Mutex
	reader io.Reader
}

// NewIDSource creates an identifier source backed by reader. Passing nil uses
// the operating system's cryptographic random source.
func NewIDSource(reader io.Reader) *IDSource {
	return &IDSource{reader: reader}
}

// NewPlanID returns a cryptographically opaque plan identifier.
func (s *IDSource) NewPlanID() (string, error) {
	var buf [16]byte
	reader := rand.Reader
	if s != nil && s.reader != nil {
		reader = s.reader
	}
	if s != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
	}
	if _, err := io.ReadFull(reader, buf[:]); err != nil {
		return "", idGenerationError(err)
	}
	return "plan-" + hex.EncodeToString(buf[:]), nil
}

// NewTypedPlanID returns a plan identifier with a normalized operation kind.
func (s *IDSource) NewTypedPlanID(kind string) (string, error) {
	kind = normalizeIDKind(kind)
	planID, err := s.NewPlanID()
	if err != nil {
		return "", err
	}
	if kind == "" {
		return planID, nil
	}
	return "plan-" + kind + "-" + strings.TrimPrefix(planID, "plan-"), nil
}

// NewJobID returns an opaque job identifier with the requested prefix.
func (s *IDSource) NewJobID(prefix string) (string, error) {
	prefix = strings.TrimSpace(strings.TrimSuffix(prefix, "-"))
	if prefix == "" {
		prefix = "job"
	}
	planID, err := s.NewPlanID()
	if err != nil {
		return "", err
	}
	return prefix + "-" + strings.TrimPrefix(planID, "plan-"), nil
}

func NewPlanID() (string, error) {
	return (*IDSource)(nil).NewPlanID()
}

func NewTypedPlanID(kind string) (string, error) {
	return (*IDSource)(nil).NewTypedPlanID(kind)
}

func NewJobID(prefix string) (string, error) {
	return (*IDSource)(nil).NewJobID(prefix)
}

func normalizeIDKind(kind string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	kind = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '-' || r == '_':
			return '-'
		default:
			return -1
		}
	}, kind)
	return strings.Trim(kind, "-")
}

func idGenerationError(cause error) error {
	return apperror.Wrap(
		apperror.Internal,
		"Generate operation identifier failed",
		cause,
		apperror.WithDetail("The operating system's secure random source was unavailable."),
		apperror.WithRepairHints("Retry the operation. If the error persists, restart Cairn and check the operating system cryptographic services."),
	)
}
