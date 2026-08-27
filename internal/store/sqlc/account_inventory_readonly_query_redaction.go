package store

import "fmt"

// Format prevents the generated product-query SQL parameters from exposing
// normalized email or the continuation account key through ordinary logging,
// test failures, or retained diagnostics.
func (QueryCurrentAccountInventoryV1Params) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED QueryCurrentAccountInventoryV1Params]"))
}
