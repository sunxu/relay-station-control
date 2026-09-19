package main

import (
	"reflect"
	"testing"
)

func TestDriverCapabilitiesMatchDatabaseCanonicalOrder(t *testing.T) {
	want := []string{
		"management_account_inventory_read",
		"management_health_read",
	}
	if !reflect.DeepEqual(driverCapabilities, want) {
		t.Fatalf("driver capabilities = %#v, want canonical order %#v", driverCapabilities, want)
	}
}

func TestCSVEnvEmptyProviderFallbackIsNonNil(t *testing.T) {
	got := csvEnv("PHASE9_TEST_UNSET_PROVIDER_LIST", []string{})
	if got == nil {
		t.Fatal("empty provider fallback must encode as an empty PostgreSQL array, not NULL")
	}
}
