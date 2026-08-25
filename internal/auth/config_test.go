package auth

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func writeKeyring(t *testing.T, environment Environment) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "keyring.json")
	key := base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	document := `{"format_version":1,"environment":"` + string(environment) + `","current":1,"keys":[{"version":1,"key":"` + key + `"}]}`
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestConfigFailClosed(t *testing.T) {
	productionKeyring := writeKeyring(t, EnvironmentProduction)
	stagingKeyring := writeKeyring(t, EnvironmentStaging)
	tests := []struct {
		name    string
		config  Config
		wantErr bool
	}{
		{name: "production valid", config: Config{Environment: EnvironmentProduction, BindAddress: ":8080", AuthKeyringFile: productionKeyring, CookieSecure: true, MFARequired: true}},
		{name: "production missing keyring", config: Config{Environment: EnvironmentProduction, BindAddress: ":8080", CookieSecure: true, MFARequired: true}, wantErr: true},
		{name: "production insecure cookie", config: Config{Environment: EnvironmentProduction, BindAddress: ":8080", AuthKeyringFile: productionKeyring, MFARequired: true}, wantErr: true},
		{name: "production MFA disabled", config: Config{Environment: EnvironmentProduction, BindAddress: ":8080", AuthKeyringFile: productionKeyring, CookieSecure: true}, wantErr: true},
		{name: "staging explicit MFA disabled", config: Config{Environment: EnvironmentStaging, BindAddress: ":8080", AuthKeyringFile: stagingKeyring, CookieSecure: true}},
		{name: "staging missing keyring", config: Config{Environment: EnvironmentStaging, BindAddress: ":8080", CookieSecure: true}, wantErr: true},
		{name: "staging insecure cookie", config: Config{Environment: EnvironmentStaging, BindAddress: ":8080", AuthKeyringFile: stagingKeyring}, wantErr: true},
		{name: "dev loopback insecure", config: Config{Environment: EnvironmentDev, BindAddress: "127.0.0.1:8080"}},
		{name: "dev wildcard insecure", config: Config{Environment: EnvironmentDev, BindAddress: "0.0.0.0:8080"}, wantErr: true},
		{name: "unknown environment", config: Config{Environment: "preview", BindAddress: ":8080", CookieSecure: true}, wantErr: true},
		{name: "invalid trusted proxy", config: Config{Environment: EnvironmentDev, BindAddress: "127.0.0.1:8080", TrustedProxyCIDRs: []string{"not-a-cidr"}}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := test.config.Validate()
			if (err != nil) != test.wantErr {
				t.Fatalf("Config.Validate() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestBootstrapSecretFileValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bootstrap-secret")
	if err := os.WriteFile(path, []byte("a-secret-that-is-at-least-thirty-two-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := Config{Environment: EnvironmentDev, BindAddress: "127.0.0.1:8080", BootstrapSecretFile: path}
	if _, err := config.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Validate(); err == nil {
		t.Fatal("broad bootstrap secret permissions accepted")
	}
}
