package cliproxyapi

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadRootCAsAcceptsProtectedPEMAndRejectsUnsafeFiles(t *testing.T) {
	directory := t.TempDir()
	protected := filepath.Join(directory, "ca.pem")
	encoded := testCAPEM(t)
	if err := os.WriteFile(protected, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	pool, err := LoadRootCAs(protected)
	if err != nil || pool == nil {
		t.Fatalf("load protected CA: %v", err)
	}

	symlink := filepath.Join(directory, "ca-link.pem")
	if err = os.Symlink(protected, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err = LoadRootCAs(symlink); err == nil {
		t.Fatal("CA symlink accepted")
	}
	intermediateTarget := filepath.Join(directory, "intermediate-target")
	if err = os.Mkdir(intermediateTarget, 0o700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(intermediateTarget, "ca.pem"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	intermediateLink := filepath.Join(directory, "intermediate-link")
	if err = os.Symlink(intermediateTarget, intermediateLink); err != nil {
		t.Fatal(err)
	}
	if _, err = LoadRootCAs(filepath.Join(intermediateLink, "ca.pem")); err == nil {
		t.Fatal("CA intermediate symlink accepted")
	}
	unsafeParent := filepath.Join(directory, "unsafe-parent")
	if err = os.Mkdir(unsafeParent, 0o700); err != nil {
		t.Fatal(err)
	}
	unsafeParentCA := filepath.Join(unsafeParent, "ca.pem")
	if err = os.WriteFile(unsafeParentCA, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(unsafeParent, 0o770); err != nil {
		t.Fatal(err)
	}
	if _, err = LoadRootCAs(unsafeParentCA); err == nil {
		t.Fatal("CA below group-writable parent accepted")
	}
	if err = os.Chmod(protected, 0o666); err != nil {
		t.Fatal(err)
	}
	if _, err = LoadRootCAs(protected); err == nil {
		t.Fatal("group/other-writable CA accepted")
	}

	canary := filepath.Join(directory, "ca-path-canary")
	if _, err = LoadRootCAs(canary); err == nil || strings.Contains(err.Error(), canary) {
		t.Fatalf("CA error leaked path: %v", err)
	}
}

func testCAPEM(t *testing.T) []byte {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		NotBefore:             time.Unix(1, 0),
		NotAfter:              time.Unix(2, 0),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	encoded, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: encoded})
}

func TestCAFileOwnerMustBeRootOrCurrentUID(t *testing.T) {
	current := uint32(os.Geteuid())
	if !caFileOwnerAllowed(0) || !caFileOwnerAllowed(current) {
		t.Fatal("root or current UID rejected")
	}
	other := current + 1
	if other == 0 || other == current {
		other = current - 1
	}
	if caFileOwnerAllowed(other) {
		t.Fatalf("unrelated owner UID %d accepted", other)
	}
}
