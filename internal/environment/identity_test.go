package environment

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	store "github.com/sunxu/relay-station-control/internal/store/sqlc"
)

func TestValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		id         string
		typeName   string
		wantReason Reason
	}{
		{name: "valid development", id: "development", typeName: "dev"},
		{name: "valid production", id: "sg-production_1", typeName: "production"},
		{name: "missing", typeName: "dev", wantReason: ReasonMissingConfig},
		{name: "leading whitespace", id: " development", typeName: "dev", wantReason: ReasonInvalidConfig},
		{name: "control character", id: "dev\nprod", typeName: "dev", wantReason: ReasonInvalidConfig},
		{name: "too long", id: strings.Repeat("a", maxEnvironmentIDBytes+1), typeName: "dev", wantReason: ReasonInvalidConfig},
		{name: "unknown type", id: "development", typeName: "preview", wantReason: ReasonInvalidConfig},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := Validate(test.id, test.typeName)
			if test.wantReason == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				if got.ID != test.id || got.Type != test.typeName {
					t.Fatalf("Validate() = %#v", got)
				}
				return
			}
			if err == nil || ReasonOf(err) != test.wantReason {
				t.Fatalf("Validate() error = %v, reason = %q", err, ReasonOf(err))
			}
			if strings.Contains(err.Error(), test.id) && test.id != "" {
				t.Fatal("validation error exposed the configured environment ID")
			}
		})
	}
}

type stubReader struct {
	environment store.Environment
	err         error
}

func (s stubReader) GetEnvironment(context.Context) (store.Environment, error) {
	return s.environment, s.err
}

func TestVerify(t *testing.T) {
	t.Parallel()
	expected := Expected{ID: "development", Type: "dev"}
	tests := []struct {
		name       string
		reader     stubReader
		wantReason Reason
	}{
		{name: "match", reader: stubReader{environment: store.Environment{EnvironmentID: "development", EnvironmentType: "dev"}}},
		{name: "missing", reader: stubReader{err: pgx.ErrNoRows}, wantReason: ReasonMissingRow},
		{name: "database unavailable", reader: stubReader{err: errors.New("postgres://secret@db/internal")}, wantReason: ReasonDatabaseUnavailable},
		{name: "id mismatch", reader: stubReader{environment: store.Environment{EnvironmentID: "production", EnvironmentType: "dev"}}, wantReason: ReasonIDMismatch},
		{name: "type mismatch", reader: stubReader{environment: store.Environment{EnvironmentID: "development", EnvironmentType: "production"}}, wantReason: ReasonTypeMismatch},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := Verify(context.Background(), test.reader, expected)
			if test.wantReason == "" {
				if err != nil {
					t.Fatalf("Verify() error = %v", err)
				}
				return
			}
			if err == nil || ReasonOf(err) != test.wantReason {
				t.Fatalf("Verify() error = %v, reason = %q", err, ReasonOf(err))
			}
			for _, secret := range []string{"development", "production", "postgres://"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("Verify() exposed sensitive value %q", secret)
				}
			}
		})
	}
}
