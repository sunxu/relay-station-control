package environment

import (
	"context"
	"errors"
	"regexp"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	store "github.com/sunxu/relay-station-control/internal/store/sqlc"
)

const maxEnvironmentIDBytes = 128

var environmentIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

type Reason string

const (
	ReasonMissingConfig       Reason = "missing_config"
	ReasonInvalidConfig       Reason = "invalid_config"
	ReasonMissingRow          Reason = "missing_row"
	ReasonIDMismatch          Reason = "id_mismatch"
	ReasonTypeMismatch        Reason = "type_mismatch"
	ReasonDatabaseUnavailable Reason = "database_unavailable"
)

type Expected struct {
	ID   string
	Type string
}

type identityError struct {
	reason Reason
	cause  error
}

func (e *identityError) Error() string {
	return "environment identity verification failed"
}

func (e *identityError) Unwrap() error {
	return e.cause
}

func ReasonOf(err error) Reason {
	var identityErr *identityError
	if errors.As(err, &identityErr) {
		return identityErr.reason
	}
	return ReasonDatabaseUnavailable
}

func Validate(id, environmentType string) (Expected, error) {
	if id == "" {
		return Expected{}, &identityError{reason: ReasonMissingConfig}
	}
	if len(id) > maxEnvironmentIDBytes || !utf8.ValidString(id) || !environmentIDPattern.MatchString(id) {
		return Expected{}, &identityError{reason: ReasonInvalidConfig}
	}
	switch environmentType {
	case "dev", "staging", "production":
	default:
		return Expected{}, &identityError{reason: ReasonInvalidConfig}
	}
	return Expected{ID: id, Type: environmentType}, nil
}

type Reader interface {
	GetEnvironment(context.Context) (store.Environment, error)
}

func Verify(ctx context.Context, reader Reader, expected Expected) error {
	environment, err := reader.GetEnvironment(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return &identityError{reason: ReasonMissingRow, cause: err}
	}
	if err != nil {
		return &identityError{reason: ReasonDatabaseUnavailable, cause: err}
	}
	if environment.EnvironmentID != expected.ID {
		return &identityError{reason: ReasonIDMismatch}
	}
	if environment.EnvironmentType != expected.Type {
		return &identityError{reason: ReasonTypeMismatch}
	}
	return nil
}
