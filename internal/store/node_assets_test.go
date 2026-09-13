package store

import (
	"testing"

	"github.com/google/uuid"
)

func TestNodeCanonicalIntentV1Fixtures(t *testing.T) {
	repository := &NodeLifecycleRepository{key: []byte("01234567890123456789012345678901")}
	oldID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	newID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	tests := []struct {
		kind    string
		command NodeCommand
		want    string
	}{
		{
			kind: "node.register",
			command: NodeCommand{NewInstanceID: newID, DisplayName: StringPatch{Present: true, Value: "Node A"},
				ManagementEndpoint: StringPatch{Present: true, Value: "https://node.example"}, NodeType: "cliproxyapi",
				DriverContractVersion: "v1", Capabilities: []string{"a", "b"},
				Secret: SecretPatch{Operation: SecretSet, Value: "vault://node/reader"}},
			want: `[1,"node.register","22222222-2222-2222-2222-222222222222","Node A","https://node.example","cliproxyapi","v1",["a","b"],["set",1,"c657bb16505a37eea53dfa53e49f996b763aa66d2192a62a2b5e6be3875d1194"]]`,
		},
		{
			kind: "node.edit",
			command: NodeCommand{InstanceID: oldID, ExpectedRevision: 42,
				DisplayName: StringPatch{}, ManagementEndpoint: StringPatch{Present: true, Value: "http://node.example"},
				Secret: SecretPatch{Operation: SecretClear}},
			want: `[1,"node.edit","11111111-1111-1111-1111-111111111111","42",["absent",null],["set","http://node.example"],["clear",null,null]]`,
		},
		{
			kind:    "node.retire",
			command: NodeCommand{InstanceID: oldID, ExpectedRevision: 43, Secret: SecretPatch{Operation: SecretAbsent}},
			want:    `[1,"node.retire","11111111-1111-1111-1111-111111111111","43","administrator_retire"]`,
		},
		{
			kind: "node.replace",
			command: NodeCommand{InstanceID: oldID, ExpectedRevision: 44, NewInstanceID: newID,
				DisplayName: StringPatch{Present: true, Value: "Node B"}, ManagementEndpoint: StringPatch{Present: true, Value: "https://node-b.example"},
				NodeType: "cliproxyapi", DriverContractVersion: "v1", Capabilities: []string{"a"}, Secret: SecretPatch{Operation: SecretAbsent}},
			want: `[1,"node.replace","11111111-1111-1111-1111-111111111111","44","22222222-2222-2222-2222-222222222222","Node B","https://node-b.example","cliproxyapi","v1",["a"],["absent",null,null],"replacement"]`,
		},
	}
	for _, test := range tests {
		t.Run(test.kind, func(t *testing.T) {
			got, _, err := repository.intent(test.kind, test.command, false, nil)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != test.want {
				t.Fatalf("canonical intent changed\ngot:  %s\nwant: %s", got, test.want)
			}
		})
	}
}

func TestNormalizeNodeEndpointMatchesAssetContract(t *testing.T) {
	valid := map[string]string{
		"HTTPS://NODE.EXAMPLE:443/a/%2e%41": "https://node.example/a/%2E%41",
		"http://[2001:0db8::1]:80/path/":    "http://[2001:db8::1]/path/",
	}
	for input, want := range valid {
		got, err := normalizeNodeEndpoint(input)
		if err != nil || got != want {
			t.Fatalf("normalize %q=%q err=%v want=%q", input, got, err, want)
		}
	}
	for _, input := range []string{
		"ftp://node.example", "https://node.example/a/../b", "https://node.example/%2fadmin",
		"https://node.example/%5cadmin", "https://node.example:0", "https://node..example",
	} {
		if _, err := normalizeNodeEndpoint(input); err != ErrInvalidNodeEndpoint {
			t.Fatalf("invalid endpoint %q error=%v", input, err)
		}
	}
}

func TestNodeSecretSetRequiresK1OnlyWhenUsed(t *testing.T) {
	repository := &NodeLifecycleRepository{}
	id := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	if _, version, err := repository.intent("node.retire", NodeCommand{InstanceID: id, ExpectedRevision: 1, Secret: SecretPatch{Operation: SecretAbsent}}, false, nil); err != nil || version != nil {
		t.Fatalf("non-secret intent version=%v err=%v", version, err)
	}
	if _, version, err := repository.intent("node.edit", NodeCommand{InstanceID: id, ExpectedRevision: 1, Secret: SecretPatch{Operation: SecretClear}}, false, nil); err != nil || version != nil {
		t.Fatalf("clear intent version=%v err=%v", version, err)
	}
	if _, _, err := repository.intent("node.edit", NodeCommand{InstanceID: id, ExpectedRevision: 1, Secret: SecretPatch{Operation: SecretSet, Value: "vault://node/reader"}}, false, nil); err != ErrReceiptKeyUnavailable {
		t.Fatalf("SecretSet without K1 error=%v", err)
	}
}
