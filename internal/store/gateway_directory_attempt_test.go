package store

import "testing"

func TestGatewayDirectoryLifecycleFailureClass(t *testing.T) {
	t.Parallel()
	tests := []struct {
		status string
		reason string
		want   gatewaydirectoryFailureClass
	}{
		{gatewayLifecycleRetired, "administrator_retire", gatewaydirectoryFailureClassGatewayRetired},
		{gatewayLifecycleRetired, "replacement", gatewaydirectoryFailureClassGatewayReplaced},
		{gatewayLifecycleRetired, "corrupt", gatewaydirectoryFailureClassContractInvalid},
		{gatewayLifecycleActive, "", gatewaydirectoryFailureClassContractInvalid},
		{"", "", gatewaydirectoryFailureClassContractInvalid},
	}
	for _, test := range tests {
		if got := lifecycleFailureClass(test.status, test.reason); got != test.want {
			t.Fatalf("lifecycleFailureClass(%q,%q) = %q, want %q", test.status, test.reason, got, test.want)
		}
	}
}
