package store_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/sunxu/relay-station-control/internal/inventorypoll"
	pollstore "github.com/sunxu/relay-station-control/internal/store"
	generated "github.com/sunxu/relay-station-control/internal/store/sqlc"
)

// Redaction must survive the common mistake of formatting a batch/container,
// not only a DTO directly. This does not authorize logging these values; it is
// defense in depth for test failures and ordinary fmt-based diagnostics.
func TestAccountInventorySnapshotNestedDTOsRedactIdentity(t *testing.T) {
	const canary = "nested-snapshot-redaction-canary@example.invalid"
	generatedItem := generated.AccountInventorySnapshotItem{
		AccountKey: "openai:" + canary, NormalizedEmail: canary,
	}
	generatedDuplicate := generated.AccountInventoryPollDuplicate{AccountKey: "openai:" + canary}
	generatedFinalize := generated.FinalizeAccountInventoryPollRunParams{
		SnapshotItems: []byte(canary), DuplicateEvidence: []byte(canary),
	}
	generatedLifecycleFinalize := generated.FinalizeAccountInventoryPollRunWithLifecycleParams{
		SnapshotItems: []byte(canary), DuplicateEvidence: []byte(canary),
	}
	generatedLifecycle := generated.AccountInventory{AccountKey: "openai:" + canary, NormalizedEmail: canary}
	generatedLifecycleRow := generated.ListCurrentAccountInventoryLifecycleRow{
		AccountKey: "openai:" + canary, NormalizedEmail: canary,
	}
	currentItem := pollstore.CurrentAccountInventorySnapshotItem{
		AccountKey: "openai:" + canary, NormalizedEmail: canary,
	}
	currentLifecycleItem := pollstore.CurrentAccountInventoryLifecycleItem{
		AccountKey: "openai:" + canary, NormalizedEmail: canary,
	}
	runtimeRequest := inventorypoll.FinalizeRequest{SnapshotItems: []inventorypoll.SnapshotCandidate{{
		Provider: "openai", AccountKey: "openai:" + canary, Email: canary,
	}}}
	values := []any{
		[]generated.AccountInventorySnapshotItem{generatedItem},
		map[string]generated.AccountInventorySnapshotItem{"item": generatedItem},
		[]generated.AccountInventoryPollDuplicate{generatedDuplicate},
		map[string]generated.FinalizeAccountInventoryPollRunParams{"finalize": generatedFinalize},
		map[string]generated.FinalizeAccountInventoryPollRunWithLifecycleParams{"finalize": generatedLifecycleFinalize},
		[]generated.AccountInventory{generatedLifecycle},
		[]generated.ListCurrentAccountInventoryLifecycleRow{generatedLifecycleRow},
		[]pollstore.CurrentAccountInventorySnapshotItem{currentItem},
		[]pollstore.CurrentAccountInventoryLifecycleItem{currentLifecycleItem},
		[]inventorypoll.FinalizeRequest{runtimeRequest},
	}
	for _, value := range values {
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
			if output := fmt.Sprintf(format, value); strings.Contains(output, canary) {
				t.Fatalf("nested format %s exposed snapshot identity", format)
			}
		}
	}
}
