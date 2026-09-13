package store

import "testing"

func TestValidAssetSecretReference(t *testing.T) {
	t.Parallel()
	tests := []struct {
		value string
		want  bool
	}{
		{"vault://valid/reference", true},
		{"file://secret", true},
		{"http://secret", false},
		{"https://secret", false},
		{"vault://has space", false},
		{"vault://has\tcontrol", false},
		{"vault://has?query", false},
		{"vault://has#fragment", false},
		{"vault://has@identity", false},
		{" vault://trimmed", false},
		{"vault://trimmed ", false},
		{"x://a", false},
	}
	for _, test := range tests {
		test := test
		t.Run(test.value, func(t *testing.T) {
			t.Parallel()
			if got := ValidAssetSecretReference(test.value); got != test.want {
				t.Fatalf("ValidAssetSecretReference(%q) = %v, want %v", test.value, got, test.want)
			}
		})
	}
}
