package store

import "fmt"

// History transport types contain policy, run, fencing, checksum, and
// lease recovery evidence. None of those fields may be rendered by ordinary
// logging or assertion formatting.
func (ClaimAccountInventoryHistoryCompactionParams) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED ClaimAccountInventoryHistoryCompactionParams]"))
}

func (RenewAccountInventoryHistoryCompactionParams) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED RenewAccountInventoryHistoryCompactionParams]"))
}

func (SummarizeAccountInventoryHistoryCompactionParams) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED SummarizeAccountInventoryHistoryCompactionParams]"))
}

func (DeleteAccountInventoryHistorySnapshotBatchParams) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED DeleteAccountInventoryHistorySnapshotBatchParams]"))
}

func (CompleteAccountInventoryHistoryCompactionParams) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED CompleteAccountInventoryHistoryCompactionParams]"))
}

func (FailAccountInventoryHistoryCompactionParams) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED FailAccountInventoryHistoryCompactionParams]"))
}

func (ClaimAccountInventoryHistoryDailyRollupParams) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED ClaimAccountInventoryHistoryDailyRollupParams]"))
}

func (RenewAccountInventoryHistoryDailyRollupParams) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED RenewAccountInventoryHistoryDailyRollupParams]"))
}

func (FinalizeAccountInventoryHistoryDailyRollupParams) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED FinalizeAccountInventoryHistoryDailyRollupParams]"))
}

func (FailAccountInventoryHistoryDailyRollupParams) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED FailAccountInventoryHistoryDailyRollupParams]"))
}

func (AccountInventoryCompactionRun) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED AccountInventoryCompactionRun]"))
}

func (AccountInventoryDailyRollupRun) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED AccountInventoryDailyRollupRun]"))
}

func (AccountInventoryDailySummary) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED AccountInventoryDailySummary]"))
}

func (AccountInventoryDailyProviderSummary) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED AccountInventoryDailyProviderSummary]"))
}

func (AccountInventoryDailyAccountRollup) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED AccountInventoryDailyAccountRollup]"))
}

func (AccountInventoryDailyProviderRollup) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED AccountInventoryDailyProviderRollup]"))
}

func (ListAccountInventoryHistoryCoverageMetricsRow) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED ListAccountInventoryHistoryCoverageMetricsRow]"))
}
