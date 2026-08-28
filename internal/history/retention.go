package history

import "time"

// RetentionAge is the fixed age used by every history-retention boundary.
// PostgreSQL supplies DatabaseNow; callers must not substitute a host clock.
const RetentionAge = 30 * 24 * time.Hour

// RetentionStage is the next and only legal mutation for one history lineage.
// The zero value is fail-closed.
type RetentionStage string

const (
	RetentionBlocked          RetentionStage = "blocked"
	RetentionDeletePoll       RetentionStage = "delete_poll"
	RetentionDeleteSegments   RetentionStage = "delete_segments"
	RetentionDeleteFinals     RetentionStage = "delete_finals"
	RetentionDeleteRollup     RetentionStage = "delete_rollup"
	RetentionDeleteCompaction RetentionStage = "delete_compaction"
	RetentionDone             RetentionStage = "done"
)

// RetentionRefusal identifies why a lineage has no legal deletion now. The
// empty value means the returned stage is executable (or already done).
type RetentionRefusal string

const (
	RetentionRefusalNone                RetentionRefusal = ""
	RetentionRefusalInvalidTime         RetentionRefusal = "invalid_time"
	RetentionRefusalTimeOverflow        RetentionRefusal = "time_overflow"
	RetentionRefusalInconsistent        RetentionRefusal = "inconsistent"
	RetentionRefusalPollTooRecent       RetentionRefusal = "poll_too_recent"
	RetentionRefusalDayTooRecent        RetentionRefusal = "day_too_recent"
	RetentionRefusalRollupTooRecent     RetentionRefusal = "rollup_too_recent"
	RetentionRefusalCompactionTooRecent RetentionRefusal = "compaction_too_recent"
	RetentionRefusalRollupNotCompleted  RetentionRefusal = "rollup_not_completed"
	RetentionRefusalCompactionNotDone   RetentionRefusal = "compaction_not_completed"
	RetentionRefusalPollsRemain         RetentionRefusal = "polls_remain"
	RetentionRefusalSegmentsRemain      RetentionRefusal = "segments_remain"
	RetentionRefusalFinalsRemain        RetentionRefusal = "finals_remain"
	RetentionRefusalRollupRemains       RetentionRefusal = "rollup_remains"
	RetentionRefusalConservation        RetentionRefusal = "source_deleted_mismatch"
)

type RetentionDecision struct {
	Stage  RetentionStage
	Reason RetentionRefusal
}

// RetentionLineage is a read-only snapshot used to choose one bounded
// deletion phase. Counts may be exact counts or non-zero sentinels: the model
// never adds them, so even MaxUint64 cannot wrap and become eligible.
type RetentionLineage struct {
	DatabaseNow time.Time

	// NextPollScheduledAt is the oldest remaining poll candidate whenever
	// PollRows is non-zero.
	NextPollScheduledAt time.Time
	SummaryDay          UTCDay

	RollupRunPresent  bool
	RollupStatus      RollupStatus
	RollupCompletedAt time.Time

	CompactionRunPresent  bool
	CompactionStatus      CompactionStatus
	CompactionCompletedAt time.Time

	PollRows             uint64
	SegmentRows          uint64
	FinalRows            uint64
	RollupDependencyRows uint64

	SourceRows  uint64
	DeletedRows uint64
}

// NextRetentionStage returns exactly one legal phase. Equality at every
// 30-day boundary is eligible. A blocked result always includes a reason.
func NextRetentionStage(lineage RetentionLineage) RetentionDecision {
	cutoff, reason := retentionCutoff(lineage.DatabaseNow)
	if reason != RetentionRefusalNone {
		return retentionBlocked(reason)
	}
	if reason = validateRetentionRunShapes(lineage); reason != RetentionRefusalNone {
		return retentionBlocked(reason)
	}

	if lineage.PollRows != 0 {
		if !lineage.CompactionRunPresent || lineage.CompactionStatus != CompactionCompleted {
			return retentionBlocked(RetentionRefusalCompactionNotDone)
		}
		if lineage.SourceRows != lineage.DeletedRows {
			return retentionBlocked(RetentionRefusalConservation)
		}
		if reason = validRetentionTimestamp(lineage.NextPollScheduledAt); reason != RetentionRefusalNone {
			return retentionBlocked(reason)
		}
		if lineage.NextPollScheduledAt.UTC().After(cutoff) {
			return retentionBlocked(RetentionRefusalPollTooRecent)
		}
		return RetentionDecision{Stage: RetentionDeletePoll}
	}

	if lineage.SegmentRows != 0 {
		if reason = retainedDayReady(lineage, cutoff); reason != RetentionRefusalNone {
			return retentionBlocked(reason)
		}
		return RetentionDecision{Stage: RetentionDeleteSegments}
	}

	if lineage.FinalRows != 0 {
		if reason = retainedDayReady(lineage, cutoff); reason != RetentionRefusalNone {
			return retentionBlocked(reason)
		}
		return RetentionDecision{Stage: RetentionDeleteFinals}
	}

	if lineage.RollupRunPresent {
		if lineage.RollupStatus != RollupCompleted {
			return retentionBlocked(RetentionRefusalRollupNotCompleted)
		}
		if lineage.RollupCompletedAt.UTC().After(cutoff) {
			return retentionBlocked(RetentionRefusalRollupTooRecent)
		}
		if lineage.SegmentRows != 0 {
			return retentionBlocked(RetentionRefusalSegmentsRemain)
		}
		if lineage.FinalRows != 0 {
			return retentionBlocked(RetentionRefusalFinalsRemain)
		}
		return RetentionDecision{Stage: RetentionDeleteRollup}
	}

	if lineage.CompactionRunPresent {
		if lineage.CompactionStatus != CompactionCompleted {
			return retentionBlocked(RetentionRefusalCompactionNotDone)
		}
		if lineage.CompactionCompletedAt.UTC().After(cutoff) {
			return retentionBlocked(RetentionRefusalCompactionTooRecent)
		}
		if lineage.PollRows != 0 {
			return retentionBlocked(RetentionRefusalPollsRemain)
		}
		if lineage.SegmentRows != 0 {
			return retentionBlocked(RetentionRefusalSegmentsRemain)
		}
		if lineage.FinalRows != 0 {
			return retentionBlocked(RetentionRefusalFinalsRemain)
		}
		if lineage.RollupDependencyRows != 0 {
			return retentionBlocked(RetentionRefusalRollupRemains)
		}
		if lineage.SourceRows != lineage.DeletedRows {
			return retentionBlocked(RetentionRefusalConservation)
		}
		return RetentionDecision{Stage: RetentionDeleteCompaction}
	}

	return RetentionDecision{Stage: RetentionDone}
}

func retainedDayReady(lineage RetentionLineage, cutoff time.Time) RetentionRefusal {
	if lineage.PollRows != 0 {
		return RetentionRefusalPollsRemain
	}
	if !lineage.RollupRunPresent || lineage.RollupStatus != RollupCompleted {
		return RetentionRefusalRollupNotCompleted
	}
	_, end, err := lineage.SummaryDay.Bounds()
	if err != nil {
		return RetentionRefusalInvalidTime
	}
	if end.Year() < 1 || end.Year() > 9999 {
		return RetentionRefusalTimeOverflow
	}
	if end.After(cutoff) {
		return RetentionRefusalDayTooRecent
	}
	if lineage.RollupCompletedAt.UTC().After(cutoff) {
		return RetentionRefusalRollupTooRecent
	}
	return RetentionRefusalNone
}

func validateRetentionRunShapes(lineage RetentionLineage) RetentionRefusal {
	if lineage.RollupRunPresent {
		switch lineage.RollupStatus {
		case RollupPending, RollupFailed:
			if !lineage.RollupCompletedAt.IsZero() {
				return RetentionRefusalInconsistent
			}
		case RollupCompleted:
			if reason := validRetentionTimestamp(lineage.RollupCompletedAt); reason != RetentionRefusalNone {
				return reason
			}
		default:
			return RetentionRefusalInconsistent
		}
	} else if lineage.RollupStatus != "" || !lineage.RollupCompletedAt.IsZero() || lineage.RollupDependencyRows != 0 {
		return RetentionRefusalInconsistent
	}

	if lineage.CompactionRunPresent {
		switch lineage.CompactionStatus {
		case CompactionPending, CompactionSummarized, CompactionDeleting, CompactionFailed:
			if !lineage.CompactionCompletedAt.IsZero() {
				return RetentionRefusalInconsistent
			}
		case CompactionCompleted:
			if reason := validRetentionTimestamp(lineage.CompactionCompletedAt); reason != RetentionRefusalNone {
				return reason
			}
		default:
			return RetentionRefusalInconsistent
		}
	} else if lineage.CompactionStatus != "" || !lineage.CompactionCompletedAt.IsZero() {
		return RetentionRefusalInconsistent
	}
	return RetentionRefusalNone
}

func retentionCutoff(databaseNow time.Time) (time.Time, RetentionRefusal) {
	if reason := validRetentionTimestamp(databaseNow); reason != RetentionRefusalNone {
		return time.Time{}, reason
	}
	now := databaseNow.UTC()
	cutoff := now.Add(-RetentionAge)
	if cutoff.Year() < 1 || cutoff.Year() > 9999 || !cutoff.Add(RetentionAge).Equal(now) {
		return time.Time{}, RetentionRefusalTimeOverflow
	}
	return cutoff, RetentionRefusalNone
}

func validRetentionTimestamp(value time.Time) RetentionRefusal {
	if value.IsZero() {
		return RetentionRefusalInvalidTime
	}
	year := value.UTC().Year()
	if year < 1 || year > 9999 {
		return RetentionRefusalTimeOverflow
	}
	return RetentionRefusalNone
}

func retentionBlocked(reason RetentionRefusal) RetentionDecision {
	return RetentionDecision{Stage: RetentionBlocked, Reason: reason}
}
