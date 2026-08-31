package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFakeNodeContractAndRedactedSummary(t *testing.T) {
	const managementKey = "reader-secret-canary"
	const email = "old-control-poll@example.invalid"
	const token = "upstream-token-canary"
	configuration := config{Listen: "127.0.0.1:32123", ManagementKey: managementKey, Email: email, Token: token}
	encoded, err := json.Marshal(configuration)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	if err = os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "config.json")
	if err = os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadConfig(path)
	if err != nil || loaded != configuration {
		t.Fatalf("load config: %v", err)
	}
	if err = os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err = loadConfig(path); err == nil {
		t.Fatal("unsafe config accepted")
	}

	node, err := newFakeNode(loaded)
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/v0/management/auth-files", nil)
	request.Header.Set("X-Management-Key", "wrong")
	response := httptest.NewRecorder()
	node.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d", response.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "/v0/management/auth-files", nil)
	request.Header.Set("X-Management-Key", managementKey)
	response = httptest.NewRecorder()
	node.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), email) ||
		!strings.Contains(response.Body.String(), token) {
		t.Fatalf("inventory status=%d", response.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response = httptest.NewRecorder()
	node.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != `{"status":"ok"}` {
		t.Fatalf("health status=%d body=%q", response.Code, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/v0/management/auth-files", nil)
	response = httptest.NewRecorder()
	node.ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("write status=%d", response.Code)
	}

	summary := node.summary()
	if summary != "account_inventory_history_fake_node=stopped total=4 health=1 inventory=1 unauthorized=1 rejected=1" ||
		strings.Contains(summary, managementKey) || strings.Contains(summary, email) || strings.Contains(summary, token) {
		t.Fatalf("unsafe summary=%q", summary)
	}
}

func TestLoadConfigAllowsContainerWildcardOnly(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "config.json")
	write := func(listen string) error {
		encoded, err := json.Marshal(config{Listen: listen, ManagementKey: "key", Email: "node@example.invalid", Token: "token"})
		if err != nil {
			return err
		}
		return os.WriteFile(path, encoded, 0o600)
	}
	if err := write("0.0.0.0:8081"); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(path); err != nil {
		t.Fatalf("container wildcard rejected: %v", err)
	}
	if err := write("192.0.2.1:8081"); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(path); err == nil {
		t.Fatal("non-loopback concrete bind accepted")
	}
}
