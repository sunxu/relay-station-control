package gatewaydirectory

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	rootdrivers "github.com/sunxu/relay-station-control/internal/drivers"
)

func requireSourceTimeInvalid(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, ErrSourceTimeInvalid) {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateSourceTimeBoundaries(t *testing.T) {
	receivedAt := time.Date(2026, time.September, 3, 12, 0, 0, 0, time.UTC)
	previous := receivedAt.Add(-time.Hour)
	cases := []struct {
		name      string
		generated time.Time
		previous  *time.Time
		wantErr   bool
	}{
		{name: "future exact +30s accept", generated: receivedAt.Add(30 * time.Second)},
		{name: "future over +30s reject", generated: receivedAt.Add(30*time.Second + time.Nanosecond), wantErr: true},
		{name: "age exact -24h accept", generated: receivedAt.Add(-24 * time.Hour)},
		{name: "age over -24h reject", generated: receivedAt.Add(-24*time.Hour - time.Nanosecond), wantErr: true},
		{name: "backward exact 5m accept", generated: previous.Add(-5 * time.Minute), previous: &previous},
		{name: "backward over 5m reject", generated: previous.Add(-5*time.Minute - time.Nanosecond), previous: &previous, wantErr: true},
		{name: "previous nil skips backward comparison", generated: previous.Add(-6 * time.Minute), previous: nil},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := ValidateSourceTime(DefaultSourceTimePolicy(), testCase.generated, receivedAt, testCase.previous)
			if testCase.wantErr {
				requireSourceTimeInvalid(t, err)
				return
			}
			if err != nil {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestParseDirectoryResponseAccountsHardLimit(t *testing.T) {
	build := func(count int) []byte {
		var body bytes.Buffer
		body.WriteString(`{"schema_version":1,"generated_at":"2026-09-03T12:00:00Z","accounts":[`)
		for i := 1; i <= count; i++ {
			if i > 1 {
				body.WriteByte(',')
			}
			account := map[string]any{
				"id":       i,
				"name":     fmt.Sprintf("account-%d", i),
				"platform": "vendor",
				"type":     "apikey",
				"url":      nil,
				"status":   "active",
			}
			encoded, err := json.Marshal(account)
			if err != nil {
				t.Fatalf("marshal account %d: %v", i, err)
			}
			body.Write(encoded)
		}
		body.WriteString(`]}`)
		return body.Bytes()
	}

	t.Run("10k accept", func(t *testing.T) {
		_, _, err := ParseDirectoryResponse(build(10_000))
		if err != nil {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("10001 reject", func(t *testing.T) {
		_, _, err := ParseDirectoryResponse(build(10_001))
		var fetchErr *FetchError
		if !errors.As(err, &fetchErr) || fetchErr.Reason != rootdrivers.ReasonRecordLimit {
			t.Fatalf("error = %v", err)
		}
	})
}
