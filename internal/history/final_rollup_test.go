package history

import (
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestFinalRollupSegmentChecksumV1GoldenAndOrderIndependence(t *testing.T) {
	day := mustDay(t, 2026, time.August, 20)
	base, _, _ := day.Bounds()
	instanceID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	accountSegments := []AccountFinalSegmentV1{
		accountFinalChecksumFixture(day, instanceID, testPolicyUUID(2), base.Add(10*time.Minute), BasicStatusError, 4, 1),
		accountFinalChecksumFixture(day, instanceID, testPolicyUUID(1), base, BasicStatusActive, 9, 0),
	}
	providerSegments := []ProviderFinalSegmentV1{
		providerFinalChecksumFixture(t, day, instanceID, testPolicyUUID(2), base.Add(10*time.Minute), 10, 10),
		providerFinalChecksumFixture(t, day, instanceID, testPolicyUUID(1), base, 10, 9),
	}

	sum, err := FinalRollupSegmentChecksumV1(accountSegments, providerSegments)
	if err != nil {
		t.Fatal(err)
	}
	const golden = "fe4d0d137bc6db7df3d667f5e7e226780cec7441add4503a197dd11fb97abea0"
	if got := hex.EncodeToString(sum[:]); got != golden {
		t.Fatalf("final rollup segment checksum = %s, want %s", got, golden)
	}
	reordered, err := FinalRollupSegmentChecksumV1(
		[]AccountFinalSegmentV1{accountSegments[1], accountSegments[0]},
		[]ProviderFinalSegmentV1{providerSegments[1], providerSegments[0]},
	)
	if err != nil || reordered != sum {
		t.Fatalf("reordered checksum = %x, %v; want %x", reordered, err, sum)
	}

	changed := append([]AccountFinalSegmentV1(nil), accountSegments...)
	changed[0].LastSuccess++
	changedSum, err := FinalRollupSegmentChecksumV1(changed, providerSegments)
	if err != nil || changedSum == sum {
		t.Fatalf("field-sensitive checksum = %x, %v", changedSum, err)
	}
}

func TestFinalRollupSegmentChecksumV1EmptyGoldenAndRejectsDuplicateAndCrossDay(t *testing.T) {
	if sum, err := FinalRollupSegmentChecksumV1(nil, nil); err != nil || sum != ([32]byte{}) {
		t.Fatalf("empty checksum = %x, %v; want H0", sum, err)
	}
	day := mustDay(t, 2026, time.August, 20)
	base, _, _ := day.Bounds()
	instanceID := uuid.MustParse("10000000-0000-0000-0000-000000000001")
	row := accountFinalChecksumFixture(day, instanceID, testPolicyUUID(1), base, BasicStatusActive, 1, 0)
	if _, err := FinalRollupSegmentChecksumV1([]AccountFinalSegmentV1{row, row}, nil); !errors.Is(err, ErrInvalidCount) {
		t.Fatalf("duplicate checksum row error = %v", err)
	}
	row.LastObservedAt = base.Add(24 * time.Hour)
	if _, err := FinalRollupSegmentChecksumV1([]AccountFinalSegmentV1{row}, nil); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("cross-day checksum row error = %v", err)
	}
}

func TestSegmentChecksumReadSchemasV1ExactFieldsAndOrder(t *testing.T) {
	schemas := SegmentChecksumReadSchemasV1()
	if len(schemas) != 2 {
		t.Fatalf("segment schemas = %+v", schemas)
	}
	wantAccountOrder := []string{"summary_date", "instance_id", "account_key", "provider_policy_version"}
	wantProviderOrder := []string{"summary_date", "instance_id", "provider", "provider_policy_version"}
	wantAccountFields := []string{
		"row_kind", "summary_date", "instance_id", "provider", "account_key", "provider_policy_version",
		"first_scheduled_at", "last_scheduled_at", "first_observed_at", "last_observed_at",
		"last_basic_status", "sample_count", "disabled_count", "unavailable_count", "error_count",
		"active_count", "unknown_count", "first_success_count", "last_success_count",
		"success_reset_count", "first_failed_count", "last_failed_count", "failed_reset_count",
	}
	wantProviderFields := []string{
		"row_kind", "summary_date", "instance_id", "provider", "provider_policy_version",
		"expected_poll_count", "transport_success_count", "contract_valid_count", "snapshot_complete_count",
		"promotion_applied_count", "promotion_skipped_count", "policy_changed_count", "abandoned_count",
		"degraded_count", "first_promotion_at", "last_promotion_at", "coverage_numerator",
		"coverage_denominator", "coverage_threshold_basis_points", "coverage_status",
	}
	for index, want := range []struct {
		name   string
		order  []string
		fields []string
	}{
		{"account_segment", wantAccountOrder, wantAccountFields},
		{"provider_segment", wantProviderOrder, wantProviderFields},
	} {
		if schemas[index].Name != want.name || !equalStrings(schemas[index].OrderBy, want.order) ||
			!equalStrings(schemas[index].Fields, want.fields) {
			t.Fatalf("segment schema %d = %+v", index, schemas[index])
		}
	}
}

func accountFinalChecksumFixture(
	day UTCDay, instanceID, policy uuid.UUID, scheduled time.Time,
	status BasicStatus, lastSuccess, lastFailed uint64,
) AccountFinalSegmentV1 {
	counts := StatusCounts{Active: 2}
	if status == BasicStatusError {
		counts = StatusCounts{Error: 2}
	}
	return AccountFinalSegmentV1{
		SummaryDate: day, InstanceID: instanceID, Provider: "openai", AccountKey: "openai:account-a",
		AccountSegment: AccountSegment{PolicyVersion: policy, AccountAggregate: AccountAggregate{
			FirstScheduledAt: scheduled, LastScheduledAt: scheduled.Add(5 * time.Minute),
			FirstObservedAt: scheduled.Add(time.Second), LastObservedAt: scheduled.Add(5*time.Minute + time.Second),
			LastBasicStatus: status, SampleCount: 2, StatusCounts: counts,
			FirstSuccess: lastSuccess + 1, LastSuccess: lastSuccess, SuccessResets: 1,
			FirstFailed: lastFailed, LastFailed: lastFailed,
		}},
	}
}

func providerFinalChecksumFixture(
	t *testing.T, day UTCDay, instanceID, policy uuid.UUID, promotionAt time.Time,
	expected, applied uint64,
) ProviderFinalSegmentV1 {
	t.Helper()
	coverage, err := CalculateCoverage(applied, expected)
	if err != nil {
		t.Fatal(err)
	}
	return ProviderFinalSegmentV1{
		SummaryDate: day, InstanceID: instanceID, Provider: "openai",
		ProviderSegment: ProviderSegment{PolicyVersion: policy, ProviderAggregate: ProviderAggregate{
			ExpectedCount: expected, TransportSuccessCount: applied, ContractValidCount: applied,
			SnapshotCompleteCount: applied, PromotionAppliedCount: applied,
			PromotionSkippedCount: expected - applied, FirstPromotionAt: &promotionAt,
			LastPromotionAt: &promotionAt, Coverage: coverage,
		}},
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
