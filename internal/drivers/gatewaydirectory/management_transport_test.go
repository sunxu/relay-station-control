package gatewaydirectory

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	drivers "github.com/sunxu/relay-station-control/internal/drivers"
)

func TestManagementHTTPAndUnverifiedHTTPS(t *testing.T) {
	for _, mode := range []string{"http", "self-signed", "expired", "wrong-host"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != directoryPath || r.Header.Get("Authorization") != "Bearer test-reader" {
					t.Error("fixed request contract violated")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"schema_version":1,"generated_at":"2026-09-07T00:00:00Z","accounts":[{"id":9007199254740993,"name":"test","platform":"openai","type":"apikey","url":null,"status":"active"}]}`))
			}))
			if mode == "http" {
				server.Start()
			} else {
				key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
				if err != nil {
					t.Fatal(err)
				}
				cert := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"wrong-host.invalid"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
				if mode == "expired" {
					cert.NotBefore = time.Now().Add(-48 * time.Hour)
					cert.NotAfter = time.Now().Add(-24 * time.Hour)
				}
				der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
				if err != nil {
					t.Fatal(err)
				}
				keyDER, err := x509.MarshalECPrivateKey(key)
				if err != nil {
					t.Fatal(err)
				}
				pair, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))
				if err != nil {
					t.Fatal(err)
				}
				server.TLS = &tls.Config{Certificates: []tls.Certificate{pair}}
				server.StartTLS()
			}
			defer server.Close()
			client, err := NewClient(server.URL, writeTestSecretResolver(t, "file://test/reader", "test-reader"))
			if err != nil {
				t.Fatal(err)
			}
			result, _, err := client.Fetch(context.Background(), drivers.NewSecretReference("file://test/reader"))
			if err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 1 || len(result.Accounts) != 1 || result.Accounts[0].ID != 9007199254740993 {
				t.Fatal("request count or source identity mismatch")
			}
		})
	}
}

func TestManagementRedirectAndHandshakeFailure(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprintf("redirect_tls=%t", secure), func(t *testing.T) {
			var redirected atomic.Int32
			trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
			defer trap.Close()
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, trap.URL, http.StatusFound)
			}))
			if secure {
				server.StartTLS()
			} else {
				server.Start()
			}
			defer server.Close()
			client, err := NewClient(server.URL, writeTestSecretResolver(t, "file://test/reader", "synthetic-canary"))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_, _, err = client.Fetch(ctx, drivers.NewSecretReference("file://test/reader"))
			requireFetchReason(t, err, drivers.ReasonRedirectRejected)
			if redirected.Load() != 0 {
				t.Fatal("redirect forwarded the authenticated request")
			}
			if strings.Contains(fmt.Sprintf("%+v", err), "synthetic-canary") {
				t.Fatal("error leaked token")
			}
		})
	}
	t.Run("handshake does not downgrade", func(t *testing.T) {
		var requests atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
		defer server.Close()
		client, err := NewClient(strings.Replace(server.URL, "http://", "https://", 1), writeTestSecretResolver(t, "file://test/reader", "synthetic-canary"))
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, _, err = client.Fetch(ctx, drivers.NewSecretReference("file://test/reader"))
		if err == nil || requests.Load() != 0 {
			t.Fatal("TLS failure downgraded to an HTTP request")
		}
	})
}
