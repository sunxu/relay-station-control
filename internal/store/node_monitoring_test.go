package store

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/google/uuid"
)

func TestNodeMonitoringCanonicalIntentV1(t *testing.T) {
	id := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	tests := []struct {
		name, kind, reason, wantHash string
		wantBytes                    int
	}{
		{"enable", nodeMonitoringEnableKind, "administrator_enable", "f41f88d2693f203399544c5cd210d48057abfd845f3d09ef3c0f5ca276cb25fe", 90},
		{"disable", nodeMonitoringDisableKind, "administrator_disable", "b226ed7bfcc3cf1ec1c10eaf4084965217f3ce2fa914eef338b01c71732fbaf2", 92},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := nodeMonitoringIntent(test.kind, id, test.reason)
			if err != nil {
				t.Fatal(err)
			}
			if len(encoded) != test.wantBytes {
				t.Fatalf("bytes=%d want=%d: %s", len(encoded), test.wantBytes, encoded)
			}
			sum := sha256.Sum256(encoded)
			if got := hex.EncodeToString(sum[:]); got != test.wantHash {
				t.Fatalf("hash=%s want=%s", got, test.wantHash)
			}
		})
	}
}
