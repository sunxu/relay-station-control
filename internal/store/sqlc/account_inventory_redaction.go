package store

import "fmt"

// Format prevents sqlc transport/model types that contain protected account
// identity from being projected through ordinary logging and test formatting.
func (AccountInventorySnapshotItem) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED AccountInventorySnapshotItem]"))
}

func (AccountInventoryPollDuplicate) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED AccountInventoryPollDuplicate]"))
}

func (ListCurrentAccountInventorySnapshotRow) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED ListCurrentAccountInventorySnapshotRow]"))
}

func (FinalizeAccountInventoryPollRunParams) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED FinalizeAccountInventoryPollRunParams]"))
}
