package history

import (
	"math"
	"testing"
	"time"
)

func TestRetentionStageOrderAtInclusiveBoundaries(t *testing.T) {
	lineage := retentionTestLineage(t)

	assertRetentionDecision(t, lineage, RetentionDeletePoll, RetentionRefusalNone)
	lineage.PollRows = 0
	lineage.SegmentRows = 1
	assertRetentionDecision(t, lineage, RetentionDeleteSegments, RetentionRefusalNone)
	lineage.SegmentRows = 0
	lineage.FinalRows = 1
	assertRetentionDecision(t, lineage, RetentionDeleteFinals, RetentionRefusalNone)
	lineage.FinalRows = 0
	assertRetentionDecision(t, lineage, RetentionDeleteRollup, RetentionRefusalNone)
	lineage.RollupRunPresent = false
	lineage.RollupStatus = ""
	lineage.RollupCompletedAt = time.Time{}
	lineage.RollupDependencyRows = 0
	assertRetentionDecision(t, lineage, RetentionDeleteCompaction, RetentionRefusalNone)
	lineage.CompactionRunPresent = false
	lineage.CompactionStatus = ""
	lineage.CompactionCompletedAt = time.Time{}
	assertRetentionDecision(t, lineage, RetentionDone, RetentionRefusalNone)
}

func TestRetentionThirtyDayBoundariesFailClosedOneNanosecondEarly(t *testing.T) {
	base := retentionTestLineage(t)

	t.Run("poll", func(t *testing.T) {
		lineage := base
		lineage.NextPollScheduledAt = retentionTestCutoff().Add(time.Nanosecond)
		assertRetentionDecision(t, lineage, RetentionBlocked, RetentionRefusalPollTooRecent)
	})

	t.Run("UTC day end", func(t *testing.T) {
		lineage := base
		lineage.PollRows = 0
		lineage.SegmentRows = 1
		lineage.SummaryDay = mustDay(t, 2026, time.August, 1)
		assertRetentionDecision(t, lineage, RetentionBlocked, RetentionRefusalDayTooRecent)
	})

	t.Run("rollup completion for segment", func(t *testing.T) {
		lineage := base
		lineage.PollRows = 0
		lineage.SegmentRows = 1
		lineage.RollupCompletedAt = retentionTestCutoff().Add(time.Nanosecond)
		assertRetentionDecision(t, lineage, RetentionBlocked, RetentionRefusalRollupTooRecent)
	})

	t.Run("rollup run", func(t *testing.T) {
		lineage := base
		lineage.PollRows = 0
		lineage.RollupCompletedAt = retentionTestCutoff().Add(time.Nanosecond)
		assertRetentionDecision(t, lineage, RetentionBlocked, RetentionRefusalRollupTooRecent)
	})

	t.Run("compaction run", func(t *testing.T) {
		lineage := retentionCompactionOnly(base)
		lineage.CompactionCompletedAt = retentionTestCutoff().Add(time.Nanosecond)
		assertRetentionDecision(t, lineage, RetentionBlocked, RetentionRefusalCompactionTooRecent)
	})
}

func TestRetentionUsesDatabaseInstantAndNaturalUTCDay(t *testing.T) {
	shanghai := time.FixedZone("Asia/Shanghai", 8*60*60)
	lineage := retentionTestLineage(t)
	lineage.DatabaseNow = time.Date(2026, time.August, 31, 8, 0, 0, 0, shanghai)
	lineage.NextPollScheduledAt = time.Date(2026, time.August, 1, 8, 0, 0, 0, shanghai)
	lineage.PollRows = 0
	lineage.SegmentRows = 1
	assertRetentionDecision(t, lineage, RetentionDeleteSegments, RetentionRefusalNone)

	// This local July 31 instant is August 1 UTC, so it is not an older UTC
	// calendar day merely because its host-facing date says July 31.
	losAngeles := time.FixedZone("America/Los_Angeles", -7*60*60)
	day, err := UTCDayOf(time.Date(2026, time.July, 31, 17, 0, 0, 0, losAngeles))
	if err != nil {
		t.Fatal(err)
	}
	lineage.SummaryDay = day
	assertRetentionDecision(t, lineage, RetentionBlocked, RetentionRefusalDayTooRecent)
}

func TestRetentionNeverDeletesNonCompletedOrInconsistentRuns(t *testing.T) {
	base := retentionTestLineage(t)
	base.PollRows = 0
	base.SegmentRows = 0
	base.FinalRows = 0

	for _, status := range []RollupStatus{RollupPending, RollupFailed} {
		t.Run("rollup "+string(status), func(t *testing.T) {
			lineage := base
			lineage.RollupStatus = status
			lineage.RollupCompletedAt = time.Time{}
			assertRetentionDecision(t, lineage, RetentionBlocked, RetentionRefusalRollupNotCompleted)
		})
	}

	for _, status := range []CompactionStatus{
		CompactionPending, CompactionSummarized, CompactionDeleting, CompactionFailed,
	} {
		t.Run("compaction "+string(status), func(t *testing.T) {
			lineage := retentionCompactionOnly(base)
			lineage.CompactionStatus = status
			lineage.CompactionCompletedAt = time.Time{}
			assertRetentionDecision(t, lineage, RetentionBlocked, RetentionRefusalCompactionNotDone)
		})
	}

	t.Run("failed run with completed timestamp", func(t *testing.T) {
		lineage := base
		lineage.RollupStatus = RollupFailed
		assertRetentionDecision(t, lineage, RetentionBlocked, RetentionRefusalInconsistent)
	})

	t.Run("unknown status", func(t *testing.T) {
		lineage := base
		lineage.RollupStatus = RollupStatus("unknown")
		lineage.RollupCompletedAt = time.Time{}
		assertRetentionDecision(t, lineage, RetentionBlocked, RetentionRefusalInconsistent)
	})

	t.Run("metadata without run", func(t *testing.T) {
		lineage := base
		lineage.RollupRunPresent = false
		assertRetentionDecision(t, lineage, RetentionBlocked, RetentionRefusalInconsistent)
	})
}

func TestRetentionDependenciesAndConservation(t *testing.T) {
	base := retentionTestLineage(t)
	base.PollRows = 0

	t.Run("segments require completed rollup", func(t *testing.T) {
		lineage := base
		lineage.SegmentRows = 1
		lineage.RollupStatus = RollupPending
		lineage.RollupCompletedAt = time.Time{}
		assertRetentionDecision(t, lineage, RetentionBlocked, RetentionRefusalRollupNotCompleted)
	})

	t.Run("segments require empty polls", func(t *testing.T) {
		lineage := base
		lineage.PollRows = math.MaxUint64
		lineage.NextPollScheduledAt = retentionTestCutoff().Add(time.Nanosecond)
		lineage.SegmentRows = math.MaxUint64
		assertRetentionDecision(t, lineage, RetentionBlocked, RetentionRefusalPollTooRecent)
	})

	t.Run("maximum counts do not overflow", func(t *testing.T) {
		lineage := base
		lineage.SegmentRows = math.MaxUint64
		lineage.FinalRows = math.MaxUint64
		assertRetentionDecision(t, lineage, RetentionDeleteSegments, RetentionRefusalNone)
	})

	t.Run("source delete mismatch", func(t *testing.T) {
		lineage := retentionCompactionOnly(base)
		lineage.SourceRows = math.MaxUint64
		lineage.DeletedRows = math.MaxUint64 - 1
		assertRetentionDecision(t, lineage, RetentionBlocked, RetentionRefusalConservation)
	})

	t.Run("maximum source delete conservation", func(t *testing.T) {
		lineage := retentionCompactionOnly(base)
		lineage.SourceRows = math.MaxUint64
		lineage.DeletedRows = math.MaxUint64
		assertRetentionDecision(t, lineage, RetentionDeleteCompaction, RetentionRefusalNone)
	})
}

func TestRetentionPollRequiresCompletedConservedCompaction(t *testing.T) {
	base := retentionTestLineage(t)

	t.Run("missing compaction", func(t *testing.T) {
		lineage := base
		lineage.CompactionRunPresent = false
		lineage.CompactionStatus = ""
		lineage.CompactionCompletedAt = time.Time{}
		assertRetentionDecision(t, lineage, RetentionBlocked, RetentionRefusalCompactionNotDone)
	})

	for _, status := range []CompactionStatus{
		CompactionPending, CompactionSummarized, CompactionDeleting, CompactionFailed,
	} {
		t.Run(string(status), func(t *testing.T) {
			lineage := base
			lineage.CompactionStatus = status
			lineage.CompactionCompletedAt = time.Time{}
			assertRetentionDecision(t, lineage, RetentionBlocked, RetentionRefusalCompactionNotDone)
		})
	}

	t.Run("source delete mismatch", func(t *testing.T) {
		lineage := base
		lineage.SourceRows++
		assertRetentionDecision(t, lineage, RetentionBlocked, RetentionRefusalConservation)
	})

	t.Run("compaction age does not gate poll", func(t *testing.T) {
		lineage := base
		lineage.CompactionCompletedAt = lineage.DatabaseNow.Add(-time.Hour)
		assertRetentionDecision(t, lineage, RetentionDeletePoll, RetentionRefusalNone)
	})
}

func TestRetentionInvalidAndOverflowingTimesFailClosed(t *testing.T) {
	lineage := retentionTestLineage(t)
	lineage.DatabaseNow = time.Time{}
	assertRetentionDecision(t, lineage, RetentionBlocked, RetentionRefusalInvalidTime)

	lineage = retentionTestLineage(t)
	lineage.DatabaseNow = time.Date(1, time.January, 2, 0, 0, 0, 0, time.UTC)
	assertRetentionDecision(t, lineage, RetentionBlocked, RetentionRefusalTimeOverflow)

	lineage = retentionTestLineage(t)
	lineage.PollRows = 0
	lineage.SegmentRows = 1
	lineage.SummaryDay = mustDay(t, 9999, time.December, 31)
	lineage.DatabaseNow = time.Date(9999, time.December, 31, 0, 0, 0, 0, time.UTC)
	assertRetentionDecision(t, lineage, RetentionBlocked, RetentionRefusalTimeOverflow)
}

func retentionTestLineage(t *testing.T) RetentionLineage {
	t.Helper()
	cutoff := retentionTestCutoff()
	return RetentionLineage{
		DatabaseNow:           time.Date(2026, time.August, 31, 0, 0, 0, 0, time.UTC),
		NextPollScheduledAt:   cutoff,
		SummaryDay:            mustDay(t, 2026, time.July, 31),
		RollupRunPresent:      true,
		RollupStatus:          RollupCompleted,
		RollupCompletedAt:     cutoff,
		CompactionRunPresent:  true,
		CompactionStatus:      CompactionCompleted,
		CompactionCompletedAt: cutoff,
		PollRows:              1,
		RollupDependencyRows:  1,
		SourceRows:            10,
		DeletedRows:           10,
	}
}

func retentionCompactionOnly(lineage RetentionLineage) RetentionLineage {
	lineage.PollRows = 0
	lineage.SegmentRows = 0
	lineage.FinalRows = 0
	lineage.RollupRunPresent = false
	lineage.RollupStatus = ""
	lineage.RollupCompletedAt = time.Time{}
	lineage.RollupDependencyRows = 0
	return lineage
}

func retentionTestCutoff() time.Time {
	return time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
}

func assertRetentionDecision(t *testing.T, lineage RetentionLineage, stage RetentionStage, reason RetentionRefusal) {
	t.Helper()
	got := NextRetentionStage(lineage)
	if got.Stage != stage || got.Reason != reason {
		t.Fatalf("NextRetentionStage = %#v, want stage=%q reason=%q", got, stage, reason)
	}
}
