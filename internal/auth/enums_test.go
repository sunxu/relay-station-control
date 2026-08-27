package auth

import "testing"

func TestFixedEnumsRejectUnknownValues(t *testing.T) {
	tests := []struct {
		name  string
		valid bool
	}{
		{"environment", Environment("customer-1").Valid()},
		{"error code", ErrorCode("database: secret").Valid()},
		{"audit category", AuditCategory("login_name").Valid()},
		{"audit action", AuditAction("custom.action").Valid()},
		{"audit result", AuditResult("arbitrary").Valid()},
		{"MFA method", MFAMethod("sms:+123").Valid()},
		{"session revoke reason", SessionRevokeReason("operator text").Valid()},
		{"metric operation", MetricOperation("admin-id").Valid()},
		{"metric result", MetricResult("raw error").Valid()},
		{"rate-limit dimension", RateLimitDimension("IP address").Valid()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.valid {
				t.Fatal("unknown enum was accepted")
			}
		})
	}
}

func TestAccountInventoryAuditEnumMapping(t *testing.T) {
	if !AuditCategoryAccountInventory.Valid() {
		t.Fatal("account inventory audit category is invalid")
	}
	if !AuditAccountInventoryView.Valid() {
		t.Fatal("account inventory view audit action is invalid")
	}
	category, err := AuditCategoryFor(AuditAccountInventoryView)
	if err != nil || category != AuditCategoryAccountInventory {
		t.Fatalf("AuditCategoryFor(account_inventory.view) = %q, %v", category, err)
	}
}
