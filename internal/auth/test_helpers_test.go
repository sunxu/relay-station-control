package auth

import (
	"encoding/base64"
	"fmt"
	"testing"
)

func testKeyring(t *testing.T, environment Environment, current KeyVersion, versions ...KeyVersion) *Keyring {
	t.Helper()
	keys := ""
	for index, version := range versions {
		if index > 0 {
			keys += ","
		}
		key := make([]byte, 32)
		for offset := range key {
			key[offset] = byte(int(version) + offset)
		}
		keys += fmt.Sprintf(`{"version":%d,"key":%q}`, version, base64.RawStdEncoding.EncodeToString(key))
	}
	document := fmt.Sprintf(`{"format_version":1,"environment":%q,"current":%d,"keys":[%s]}`, environment, current, keys)
	keyring, err := ParseKeyring([]byte(document), environment)
	if err != nil {
		t.Fatalf("ParseKeyring() error = %v", err)
	}
	return keyring
}
