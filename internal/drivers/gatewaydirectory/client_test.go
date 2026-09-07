package gatewaydirectory

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	rootdrivers "github.com/sunxu/relay-station-control/internal/drivers"
)

func writeTestSecretResolver(t *testing.T, reference, token string) rootdrivers.SecretResolver {
	t.Helper()
	directory := t.TempDir()
	secretPath := filepath.Join(directory, "reader-token")
	if err := os.WriteFile(secretPath, []byte(token+"\n"), 0o600); err != nil {
		t.Fatalf("write secret file: %v", err)
	}
	mapping := filepath.Join(directory, "mapping.json")
	encoded, err := json.Marshal(map[string]any{
		"provider": "file",
		"references": []map[string]string{{
			"reference": reference,
			"path":      secretPath,
		}},
	})
	if err != nil {
		t.Fatalf("marshal mapping: %v", err)
	}
	if err := os.WriteFile(mapping, encoded, 0o600); err != nil {
		t.Fatalf("write mapping: %v", err)
	}
	resolver, err := rootdrivers.NewFileSecretResolver(rootdrivers.FileSecretResolverConfig{MappingFile: mapping})
	if err != nil {
		t.Fatalf("construct secret resolver: %v", err)
	}
	return resolver
}

func trustTestServerCertificate(t *testing.T, server *httptest.Server) {
	t.Helper()
	if server == nil || server.TLS == nil || len(server.TLS.Certificates) == 0 || len(server.TLS.Certificates[0].Certificate) == 0 {
		t.Fatal("missing test server certificate")
	}
	path := filepath.Join(t.TempDir(), "directory-server.pem")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create certificate file: %v", err)
	}
	if err := pem.Encode(file, &pem.Block{Type: "CERTIFICATE", Bytes: server.TLS.Certificates[0].Certificate[0]}); err != nil {
		_ = file.Close()
		t.Fatalf("encode certificate file: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close certificate file: %v", err)
	}
	t.Setenv("SSL_CERT_FILE", path)
	t.Setenv("SSL_CERT_DIR", "")
}

func requireFetchReason(t *testing.T, err error, want rootdrivers.Reason) {
	t.Helper()
	var fetchErr *FetchError
	if !errors.As(err, &fetchErr) {
		t.Fatalf("error %T is not a FetchError: %v", err, err)
	}
	if fetchErr.Reason != want {
		t.Fatalf("reason = %s, want %s", fetchErr.Reason, want)
	}
}

type partialReadCloser struct {
	data []byte
	done bool
}

func (reader *partialReadCloser) Read(buffer []byte) (int, error) {
	if reader.done {
		return 0, io.ErrUnexpectedEOF
	}
	reader.done = true
	n := copy(buffer, reader.data)
	return n, io.ErrUnexpectedEOF
}

func (reader *partialReadCloser) Close() error { return nil }

func TestClientFetchAndTransportGuards(t *testing.T) {
	t.Run("unsupported scheme", func(t *testing.T) {
		_, err := NewClient("ftp://gateway.example.invalid", nil)
		requireFetchReason(t, err, rootdrivers.ReasonTLSRejected)
	})

	t.Run("redirect rejected", func(t *testing.T) {
		resolver := writeTestSecretResolver(t, "file://gateway-directory/reader", "reader-token")
		server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			http.Redirect(writer, request, "https://example.invalid/redirect", http.StatusFound)
		}))
		defer server.Close()
		trustTestServerCertificate(t, server)

		client, err := NewClient(server.URL, resolver)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = client.Fetch(context.Background(), rootdrivers.NewSecretReference("file://gateway-directory/reader"))
		requireFetchReason(t, err, rootdrivers.ReasonRedirectRejected)
	})

	t.Run("successful fetch", func(t *testing.T) {
		resolver := writeTestSecretResolver(t, "file://gateway-directory/reader", "reader-token")
		expected := DirectoryResponse{
			SchemaVersion: 1,
			GeneratedAt:   time.Date(2026, time.January, 2, 3, 4, 5, 6, time.UTC),
			Accounts: []Account{{
				ID:       1,
				Name:     "Alpha",
				Platform: "linux",
				Type:     "apikey",
				URL:      nil,
				Status:   "active",
			}},
		}
		body, err := json.Marshal(map[string]any{
			"schema_version": expected.SchemaVersion,
			"generated_at":   expected.GeneratedAt.Format(time.RFC3339Nano),
			"accounts": []map[string]any{{
				"id":       1,
				"name":     "Alpha",
				"platform": "linux",
				"type":     "apikey",
				"url":      nil,
				"status":   "active",
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.URL.Path != directoryPath {
				t.Fatalf("path = %s, want %s", request.URL.Path, directoryPath)
			}
			if got := request.Header.Get("Authorization"); got != "Bearer reader-token" {
				t.Fatalf("authorization header = %q", got)
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write(body)
		}))
		defer server.Close()
		trustTestServerCertificate(t, server)

		client, err := NewClient(server.URL, resolver)
		if err != nil {
			t.Fatal(err)
		}
		response, fingerprint, err := client.Fetch(context.Background(), rootdrivers.NewSecretReference("file://gateway-directory/reader"))
		if err != nil {
			t.Fatal(err)
		}
		if response.SchemaVersion != expected.SchemaVersion || !response.GeneratedAt.Equal(expected.GeneratedAt) {
			t.Fatalf("response = %#v, want %#v", response, expected)
		}
		if len(response.Accounts) != 1 || response.Accounts[0].URL != nil || response.Accounts[0].Status != "active" {
			t.Fatalf("response accounts = %#v", response.Accounts)
		}
		if fingerprint != response.FingerprintV1() {
			t.Fatal("fingerprint changed on round-trip")
		}
		preimage, err := response.CanonicalPreimageV1()
		if err != nil {
			t.Fatal(err)
		}
		want := sha256.Sum256(preimage)
		if fingerprint != want {
			t.Fatal("fingerprint does not match canonical preimage")
		}
	})

	t.Run("body limit", func(t *testing.T) {
		resolver := writeTestSecretResolver(t, "file://gateway-directory/reader", "reader-token")
		server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write(bytes.Repeat([]byte("x"), int(DefaultBodyLimitBytes)+1))
		}))
		defer server.Close()
		trustTestServerCertificate(t, server)

		client, err := NewClient(server.URL, resolver)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = client.Fetch(context.Background(), rootdrivers.NewSecretReference("file://gateway-directory/reader"))
		requireFetchReason(t, err, rootdrivers.ReasonResponseTooLarge)
	})
}

func TestDirectoryResponseValidationAndFingerprint(t *testing.T) {
	t.Run("malformed utf8", func(t *testing.T) {
		_, _, err := ParseDirectoryResponse([]byte("{\xff}"))
		requireFetchReason(t, err, rootdrivers.ReasonContractInvalid)
	})

	t.Run("unknown field", func(t *testing.T) {
		_, _, err := ParseDirectoryResponse([]byte(`{"schema_version":1,"generated_at":"2026-01-02T03:04:05Z","accounts":[],"extra":1}`))
		requireFetchReason(t, err, rootdrivers.ReasonContractInvalid)
	})

	t.Run("malformed json", func(t *testing.T) {
		_, _, err := ParseDirectoryResponse([]byte(`{"schema_version":1`))
		requireFetchReason(t, err, rootdrivers.ReasonContractInvalid)
	})

	t.Run("strict account ids and types", func(t *testing.T) {
		cases := []struct {
			name string
			body string
		}{
			{
				name: "zero id",
				body: `{"schema_version":1,"generated_at":"2026-01-02T03:04:05Z","accounts":[{"id":0,"name":"A","platform":"linux","type":"apikey","url":null,"status":"active"}]}`,
			},
			{
				name: "duplicate id",
				body: `{"schema_version":1,"generated_at":"2026-01-02T03:04:05Z","accounts":[{"id":1,"name":"A","platform":"linux","type":"apikey","url":null,"status":"active"},{"id":1,"name":"B","platform":"linux","type":"apikey","url":null,"status":"active"}]}`,
			},
			{
				name: "descending id",
				body: `{"schema_version":1,"generated_at":"2026-01-02T03:04:05Z","accounts":[{"id":2,"name":"A","platform":"linux","type":"apikey","url":null,"status":"active"},{"id":1,"name":"B","platform":"linux","type":"apikey","url":null,"status":"active"}]}`,
			},
			{
				name: "unknown type",
				body: `{"schema_version":1,"generated_at":"2026-01-02T03:04:05Z","accounts":[{"id":1,"name":"A","platform":"linux","type":"partner","url":null,"status":"active"}]}`,
			},
		}
		for _, testCase := range cases {
			t.Run(testCase.name, func(t *testing.T) {
				_, _, err := ParseDirectoryResponse([]byte(testCase.body))
				requireFetchReason(t, err, rootdrivers.ReasonContractInvalid)
			})
		}
	})

	t.Run("unknown platform and status are preserved", func(t *testing.T) {
		body := []byte(`{"schema_version":1,"generated_at":"2026-01-02T03:04:05Z","accounts":[{"id":1,"name":"A","platform":"vendor-x","type":"upstream","url":null,"status":"paused"}]}`)
		response, fingerprint, err := ParseDirectoryResponse(body)
		if err != nil {
			t.Fatal(err)
		}
		if response.Accounts[0].Platform != "vendor-x" || response.Accounts[0].Status != "paused" {
			t.Fatalf("response = %#v", response.Accounts[0])
		}
		if fingerprint != response.FingerprintV1() {
			t.Fatal("fingerprint changed on round-trip")
		}
	})

	t.Run("url canonical validate only", func(t *testing.T) {
		cases := []struct {
			name string
			url  string
			ok   bool
		}{
			{name: "ipv6 bracket", url: "https://[2001:db8::1]:8443", ok: true},
			{name: "low port", url: "https://example.com:1", ok: true},
			{name: "high port", url: "https://example.com:65535", ok: true},
			{name: "uppercase host not repaired", url: "https://EXAMPLE.com", ok: false},
			{name: "out of range port", url: "https://example.com:65536", ok: false},
		}
		for _, testCase := range cases {
			t.Run(testCase.name, func(t *testing.T) {
				body := []byte(`{"schema_version":1,"generated_at":"2026-01-02T03:04:05Z","accounts":[{"id":1,"name":"A","platform":"linux","type":"apikey","url":"` + testCase.url + `","status":"active"}]}`)
				response, _, err := ParseDirectoryResponse(body)
				if testCase.ok {
					if err != nil {
						t.Fatal(err)
					}
					if response.Accounts[0].URL == nil || *response.Accounts[0].URL != testCase.url {
						t.Fatalf("url = %#v", response.Accounts[0].URL)
					}
					return
				}
				requireFetchReason(t, err, rootdrivers.ReasonContractInvalid)
			})
		}
	})

	t.Run("canonical preimage and fingerprint", func(t *testing.T) {
		response := DirectoryResponse{
			SchemaVersion: 1,
			GeneratedAt:   time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC),
			Accounts: []Account{{
				ID:       1,
				Name:     "Alpha",
				Platform: "linux",
				Type:     "apikey",
				URL:      nil,
				Status:   "active",
			}},
		}
		preimage, err := response.CanonicalPreimageV1()
		if err != nil {
			t.Fatal(err)
		}
		if got := string(preimage); got != `[1,1,[[1,"Alpha","linux","apikey",null,"active"]]]` {
			t.Fatalf("preimage = %s", got)
		}
		if response.FingerprintV1() != sha256.Sum256(preimage) {
			t.Fatal("fingerprint not derived from canonical preimage")
		}
		clone := response
		clone.GeneratedAt = clone.GeneratedAt.Add(42 * time.Minute)
		if clone.FingerprintV1() != response.FingerprintV1() {
			t.Fatal("generated_at should not affect fingerprint")
		}
	})

	t.Run("partial read stays retryable", func(t *testing.T) {
		_, _, err := parseDirectoryResponse(&partialReadCloser{data: []byte(`{"schema_version":1,"generated_at":"2026-01-02T03:04:05Z","accounts":[`)}, 2048)
		requireFetchReason(t, err, rootdrivers.ReasonPartialRead)
	})
}
