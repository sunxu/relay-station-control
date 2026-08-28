package history

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// AccountFinalSegmentV1 is one immutable account-policy segment consumed by a
// final daily rollup. Its canonical checksum excludes generated row ids and
// created_at while including every persisted identity and aggregate value.
type AccountFinalSegmentV1 struct {
	SummaryDate UTCDay
	InstanceID  uuid.UUID
	Provider    string
	AccountKey  string
	AccountSegment
}

// ProviderFinalSegmentV1 is one immutable provider-policy segment consumed by
// a final daily rollup.
type ProviderFinalSegmentV1 struct {
	SummaryDate UTCDay
	InstanceID  uuid.UUID
	Provider    string
	ProviderSegment
}

// FinalRollupSegmentChecksumV1 chains account segments first and Provider
// segments second. Within each row family it uses the exact OrderBy sequence
// exposed by SegmentChecksumReadSchemasV1. Input order never changes the sum.
func FinalRollupSegmentChecksumV1(
	accounts []AccountFinalSegmentV1, providers []ProviderFinalSegmentV1,
) ([32]byte, error) {
	accountRows := append([]AccountFinalSegmentV1(nil), accounts...)
	providerRows := append([]ProviderFinalSegmentV1(nil), providers...)
	for index, row := range accountRows {
		if err := validateAccountFinalSegmentV1(row); err != nil {
			return [32]byte{}, fmt.Errorf("account final segment %d: %w", index, err)
		}
	}
	for index, row := range providerRows {
		if err := validateProviderFinalSegmentV1(row); err != nil {
			return [32]byte{}, fmt.Errorf("provider final segment %d: %w", index, err)
		}
	}
	sort.Slice(accountRows, func(i, j int) bool { return accountFinalSegmentLess(accountRows[i], accountRows[j]) })
	sort.Slice(providerRows, func(i, j int) bool { return providerFinalSegmentLess(providerRows[i], providerRows[j]) })
	for index := 1; index < len(accountRows); index++ {
		if !accountFinalSegmentLess(accountRows[index-1], accountRows[index]) {
			return [32]byte{}, fmt.Errorf("account final segment %d duplicate: %w", index, ErrInvalidCount)
		}
	}
	for index := 1; index < len(providerRows); index++ {
		if !providerFinalSegmentLess(providerRows[index-1], providerRows[index]) {
			return [32]byte{}, fmt.Errorf("provider final segment %d duplicate: %w", index, ErrInvalidCount)
		}
	}

	var chain ChainV1
	for _, row := range accountRows {
		if err := chain.Add(accountFinalSegmentFieldsV1(row)...); err != nil {
			return [32]byte{}, err
		}
	}
	for _, row := range providerRows {
		if err := chain.Add(providerFinalSegmentFieldsV1(row)...); err != nil {
			return [32]byte{}, err
		}
	}
	return chain.Sum(), nil
}

func accountFinalSegmentFieldsV1(row AccountFinalSegmentV1) []CanonicalField {
	value := row.AccountAggregate
	return []CanonicalField{
		TextField("account_segment"), TextField(row.SummaryDate.String()), TextField(row.InstanceID.String()),
		TextField(row.Provider), TextField(row.AccountKey), TextField(row.PolicyVersion.String()),
		TimeField(value.FirstScheduledAt), TimeField(value.LastScheduledAt),
		TimeField(value.FirstObservedAt), TimeField(value.LastObservedAt), TextField(string(value.LastBasicStatus)),
		Uint64Field(value.SampleCount), Uint64Field(value.StatusCounts.Disabled),
		Uint64Field(value.StatusCounts.Unavailable), Uint64Field(value.StatusCounts.Error),
		Uint64Field(value.StatusCounts.Active), Uint64Field(value.StatusCounts.Unknown),
		Uint64Field(value.FirstSuccess), Uint64Field(value.LastSuccess), Uint64Field(value.SuccessResets),
		Uint64Field(value.FirstFailed), Uint64Field(value.LastFailed), Uint64Field(value.FailedResets),
	}
}

func providerFinalSegmentFieldsV1(row ProviderFinalSegmentV1) []CanonicalField {
	value := row.ProviderAggregate
	return []CanonicalField{
		TextField("provider_segment"), TextField(row.SummaryDate.String()), TextField(row.InstanceID.String()),
		TextField(row.Provider), TextField(row.PolicyVersion.String()), Uint64Field(value.ExpectedCount),
		Uint64Field(value.TransportSuccessCount), Uint64Field(value.ContractValidCount),
		Uint64Field(value.SnapshotCompleteCount), Uint64Field(value.PromotionAppliedCount),
		Uint64Field(value.PromotionSkippedCount), Uint64Field(value.PolicyChangedCount),
		Uint64Field(value.AbandonedCount), Uint64Field(value.DegradedCount),
		nullableTimeField(value.FirstPromotionAt), nullableTimeField(value.LastPromotionAt),
		Uint64Field(value.Coverage.Applied), Uint64Field(value.Coverage.Expected),
		Uint64Field(uint64(CoverageThresholdBasisPoints)), TextField(string(value.Coverage.Status)),
	}
}

func validateAccountFinalSegmentV1(row AccountFinalSegmentV1) error {
	if !row.SummaryDate.valid() || row.InstanceID == uuid.Nil || row.PolicyVersion == uuid.Nil ||
		row.Provider == "" || row.AccountKey == "" || !strings.HasPrefix(row.AccountKey, row.Provider+":") ||
		!validAccountAggregate(row.AccountAggregate) {
		return ErrInvalidState
	}
	for _, timestamp := range []time.Time{
		row.FirstScheduledAt, row.LastScheduledAt, row.FirstObservedAt, row.LastObservedAt,
	} {
		inside, err := row.SummaryDate.Contains(timestamp)
		if err != nil || !inside {
			return ErrInvalidTime
		}
	}
	return nil
}

func validateProviderFinalSegmentV1(row ProviderFinalSegmentV1) error {
	if !row.SummaryDate.valid() || row.InstanceID == uuid.Nil || row.PolicyVersion == uuid.Nil ||
		row.Provider == "" || !validProviderAggregate(row.ProviderAggregate) {
		return ErrInvalidState
	}
	for _, timestamp := range []*time.Time{row.FirstPromotionAt, row.LastPromotionAt} {
		if timestamp == nil {
			continue
		}
		inside, err := row.SummaryDate.Contains(*timestamp)
		if err != nil || !inside {
			return ErrInvalidTime
		}
	}
	return nil
}

func accountFinalSegmentLess(left, right AccountFinalSegmentV1) bool {
	if left.SummaryDate.start != right.SummaryDate.start {
		return left.SummaryDate.start.Before(right.SummaryDate.start)
	}
	if left.InstanceID != right.InstanceID {
		return uuidLess(left.InstanceID, right.InstanceID)
	}
	if left.AccountKey != right.AccountKey {
		return left.AccountKey < right.AccountKey
	}
	return uuidLess(left.PolicyVersion, right.PolicyVersion)
}

func providerFinalSegmentLess(left, right ProviderFinalSegmentV1) bool {
	if left.SummaryDate.start != right.SummaryDate.start {
		return left.SummaryDate.start.Before(right.SummaryDate.start)
	}
	if left.InstanceID != right.InstanceID {
		return uuidLess(left.InstanceID, right.InstanceID)
	}
	if left.Provider != right.Provider {
		return left.Provider < right.Provider
	}
	return uuidLess(left.PolicyVersion, right.PolicyVersion)
}

func nullableTimeField(value *time.Time) CanonicalField {
	if value == nil {
		return NullField()
	}
	return TimeField(*value)
}
