package store

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sunxu/relay-station-control/internal/drivers"
)

func TestCurrentAccountInventoryLifecycleValidationMatrix(t *testing.T) {
	now := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)
	base := CurrentAccountInventoryLifecycleItem{
		Provider: "openai", AccountKey: "openai:operator@example.invalid",
		NormalizedEmail: "operator@example.invalid", BasicStatus: drivers.AccountStateActive,
		Lifecycle: AccountInventoryPresent, FirstSeenAt: now, LastSeenAt: now,
		CurrentScheduledAt: now, SourceObservedAt: now, UpdatedAt: now,
		SourceNodeVersion: "unknown", SourceNodeCommit: "unknown",
	}
	missingAt := now.Add(5 * time.Minute)
	tests := []struct {
		name  string
		item  CurrentAccountInventoryLifecycleItem
		valid bool
	}{
		{name: "present", item: base, valid: true},
		{name: "suspected missing", item: func() CurrentAccountInventoryLifecycleItem {
			item := base
			item.Lifecycle, item.ConsecutiveMissingCount = AccountInventorySuspectedMissing, 1
			return item
		}(), valid: true},
		{name: "missing", item: func() CurrentAccountInventoryLifecycleItem {
			item := base
			item.Lifecycle, item.ConsecutiveMissingCount, item.MissingSince = AccountInventoryMissing, 2, &missingAt
			item.UpdatedAt = missingAt
			return item
		}(), valid: true},
		{name: "out of scope", item: func() CurrentAccountInventoryLifecycleItem {
			item := base
			item.Lifecycle, item.OutOfScopeSince = AccountInventoryOutOfScope, &now
			return item
		}(), valid: true},
		{name: "unknown lifecycle", item: func() CurrentAccountInventoryLifecycleItem {
			item := base
			item.Lifecycle = "other"
			return item
		}()},
		{name: "present count", item: func() CurrentAccountInventoryLifecycleItem {
			item := base
			item.ConsecutiveMissingCount = 1
			return item
		}()},
		{name: "suspected has missing since", item: func() CurrentAccountInventoryLifecycleItem {
			item := base
			item.Lifecycle, item.ConsecutiveMissingCount, item.MissingSince = AccountInventorySuspectedMissing, 1, &now
			return item
		}()},
		{name: "missing lacks since", item: func() CurrentAccountInventoryLifecycleItem {
			item := base
			item.Lifecycle, item.ConsecutiveMissingCount = AccountInventoryMissing, 2
			return item
		}()},
		{name: "out of scope retains missing", item: func() CurrentAccountInventoryLifecycleItem {
			item := base
			item.Lifecycle, item.ConsecutiveMissingCount, item.MissingSince, item.OutOfScopeSince =
				AccountInventoryOutOfScope, 2, &now, &now
			return item
		}()},
		{name: "identity mismatch", item: func() CurrentAccountInventoryLifecycleItem {
			item := base
			item.AccountKey = "openai:different@example.invalid"
			return item
		}()},
		{name: "first seen changes order", item: func() CurrentAccountInventoryLifecycleItem {
			item := base
			item.FirstSeenAt = now.Add(time.Second)
			return item
		}()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := validCurrentLifecycleItem(test.item); got != test.valid {
				t.Fatalf("valid=%v, want %v", got, test.valid)
			}
		})
	}
}

func TestCurrentAccountInventoryLifecycleFormattingIsRedacted(t *testing.T) {
	item := CurrentAccountInventoryLifecycleItem{
		Provider: "identity-marker", AccountKey: "account-key-marker", NormalizedEmail: "email-marker",
	}
	for _, format := range []string{"%v", "%+v", "%#v"} {
		text := strings.ToLower(fmt.Sprintf(format, item))
		if strings.Contains(text, "marker") || !strings.Contains(text, "redacted") {
			t.Fatalf("protected lifecycle DTO formatting was not redacted")
		}
	}
}

func TestAccountInventoryLifecycleEnumIsClosed(t *testing.T) {
	for _, lifecycle := range []AccountInventoryLifecycle{
		AccountInventoryPresent, AccountInventorySuspectedMissing,
		AccountInventoryMissing, AccountInventoryOutOfScope,
	} {
		if !validAccountInventoryLifecycle(lifecycle) {
			t.Fatalf("registered lifecycle %q is invalid", lifecycle)
		}
	}
	if validAccountInventoryLifecycle("") || validAccountInventoryLifecycle("raw-error") {
		t.Fatal("unregistered lifecycle was accepted")
	}
}
