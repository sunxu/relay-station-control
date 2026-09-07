package store

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProviderStateEnvelopeDecodeValues(t *testing.T) {
	var providers []AccountInventoryProviderState
	decoder := json.NewDecoder(strings.NewReader(`[{"provider":"openai","monitoring_status":"active","state":"current","snapshot_freshness":"fresh","health_degraded":false,"health_reason":"none"}]`))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&providers); err != nil || len(providers) != 1 || providers[0].Provider != "openai" || providers[0].HealthDegraded == nil || *providers[0].HealthDegraded {
		t.Fatalf("decoded providers = %+v, err=%v", providers, err)
	}
}
