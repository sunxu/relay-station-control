package assetcredential

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

func TestLoadKeyFileStructuralMatrixAndLoadOnce(t *testing.T) {
	dir := t.TempDir()
	valid := bytes.Repeat([]byte{0x42}, KeySize)
	uid := uint32(os.Getuid())
	cases := []struct {
		name string
		make func(string) error
		mode os.FileMode
		want bool
	}{
		{"missing", func(string) error { return nil }, 0, false},
		{"31 bytes", func(p string) error { return os.WriteFile(p, valid[:31], 0o600) }, 0o600, false},
		{"32 bytes", func(p string) error { return os.WriteFile(p, valid, 0o600) }, 0o600, true},
		{"33 bytes", func(p string) error { return os.WriteFile(p, append(valid, 0), 0o600) }, 0o600, false},
		{"unsafe permissions", func(p string) error { return os.WriteFile(p, valid, 0o644) }, 0o644, false},
		{"unreadable", func(p string) error { return os.WriteFile(p, valid, 0o000) }, 0, false},
		{"0400", func(p string) error { return os.WriteFile(p, valid, 0o400) }, 0o400, true},
		{"0600", func(p string) error { return os.WriteFile(p, valid, 0o600) }, 0o600, true},
		{"non regular", func(p string) error { return os.Mkdir(p, 0o700) }, 0o700, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name)
			if err := tc.make(path); err != nil {
				if tc.name == "missing" { /* intentional */
				} else {
					t.Fatal(err)
				}
			}
			got, err := LoadKeyFile(path, uid)
			if (err == nil) != tc.want {
				t.Fatalf("LoadKeyFile err=%v, want success=%v", err, tc.want)
			}
			if tc.want && !bytes.Equal(got, valid) {
				t.Fatalf("key mismatch")
			}
		})
	}

	path := filepath.Join(dir, "once")
	if err := os.WriteFile(path, valid, 0o600); err != nil {
		t.Fatal(err)
	}
	loader := NewKeyLoader(path, uid)
	first, err := loader.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bytes.Repeat([]byte{0x99}, KeySize), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := loader.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("loader re-read key after first load")
	}
	if _, err := LoadKeyFile(path, uid+1); err == nil {
		t.Fatal("wrong expected owner accepted")
	}
}

func TestLoadKeyFileRejectsSymlinkToValidTarget(t *testing.T) {
	dir := t.TempDir()
	uid := uint32(os.Getuid())
	target := filepath.Join(dir, "target")
	path := filepath.Join(dir, "k2")
	if err := os.WriteFile(target, bytes.Repeat([]byte{0x42}, KeySize), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadKeyFile(path, uid); !errors.Is(err, ErrInvalidKeyFile) {
		t.Fatalf("symlink accepted: %v", err)
	}
}

func TestLoadKeyFileRejectsSpecialPermissionBits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "k2")
	if err := os.WriteFile(path, bytes.Repeat([]byte{0x42}, KeySize), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := unix.Chmod(path, 0o600|unix.S_ISVTX); err != nil {
		t.Fatal(err)
	}
	var stat unix.Stat_t
	if err := unix.Stat(path, &stat); err != nil {
		t.Fatal(err)
	}
	if stat.Mode&unix.S_ISVTX == 0 {
		t.Fatal("filesystem did not preserve sticky bit")
	}
	if _, err := LoadKeyFile(path, uint32(os.Getuid())); !errors.Is(err, ErrInvalidKeyFile) {
		t.Fatalf("sticky-mode key accepted: %v", err)
	}
}

func TestProvisionKeyCreateOnceAndAtomicFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "k2")
	rng := bytes.NewReader(bytes.Repeat([]byte{0x11}, KeySize))
	if err := ProvisionKey(path, rng, 0o600); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := ProvisionKey(path, bytes.NewReader(bytes.Repeat([]byte{0x22}, KeySize)), 0o600); err != nil {
		t.Fatal(err)
	}
	repeated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, repeated) {
		t.Fatal("repeat provisioning replaced existing key")
	}

	failed := filepath.Join(dir, "failed")
	if err := ProvisionKey(failed, failingReader{}, 0o600); err == nil {
		t.Fatal("RNG failure accepted")
	}
	if _, err := os.Stat(failed); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed provisioning left file: %v", err)
	}
	if err := ProvisionKey(filepath.Join(dir, "missing", "k2"), bytes.NewReader(bytes.Repeat([]byte{0x55}, KeySize)), 0o600); err == nil {
		t.Fatal("create failure accepted")
	}
}

func TestProvisionKeyFromOSCreatesValidKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "k2")
	if err := ProvisionKeyFromOS(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadKeyFile(path, uint32(os.Getuid())); err != nil {
		t.Fatal(err)
	}
}

func TestIdentityCommitmentGoldenVector(t *testing.T) {
	k2 := []byte("01234567890123456789012345678901")
	preimage := append([]byte("relay-station/control-asset-credential-key/v1\x00"), k2...)
	want := sha256.Sum256(preimage)
	if got := IdentityCommitment(k2); got != want {
		t.Fatal("commitment mismatch")
	}
}

func TestCommitmentStateDoesNotExposeValue(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, KeySize)
	commitment := IdentityCommitment(key)
	if got := CheckCommitment(nil, nil); got != CommitmentUnavailable {
		t.Fatalf("status=%d", got)
	}
	if got := CheckCommitment(key, nil); got != CommitmentAbsent {
		t.Fatalf("status=%d", got)
	}
	if got := CheckCommitment(key, &commitment); got != CommitmentMatch {
		t.Fatalf("status=%d", got)
	}
	other := bytes.Repeat([]byte{0x43}, KeySize)
	otherCommitment := IdentityCommitment(other)
	if got := CheckCommitment(key, &otherCommitment); got != CommitmentMismatch {
		t.Fatalf("status=%d", got)
	}
}

func TestCredentialCipherAADAndInjectedRNG(t *testing.T) {
	key := bytes.Repeat([]byte{0x33}, KeySize)
	nodeA, nodeB := uuid.New(), uuid.New()
	plain := []byte("management-key")
	rng := &sequenceReader{chunks: [][]byte{bytes.Repeat([]byte{1}, NonceSize), bytes.Repeat([]byte{2}, NonceSize)}}
	blobA, err := Seal(rng, key, NodeCredential, nodeA, plain)
	if err != nil {
		t.Fatal(err)
	}
	blobB, err := Seal(rng, key, NodeCredential, nodeA, plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(blobA[:NonceSize], blobB[:NonceSize]) {
		t.Fatal("two seals reused injected nonce")
	}
	if got, err := Open(key, NodeCredential, nodeA, blobA); err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("same Node open: %q %v", got, err)
	}
	for name, tc := range map[string]struct {
		kind CredentialKind
		id   uuid.UUID
		blob []byte
	}{
		"different node": {NodeCredential, nodeB, blobA},
		"gateway domain": {GatewayCredential, nodeA, blobA},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Open(key, tc.kind, tc.id, tc.blob); err == nil {
				t.Fatal("cross-boundary open succeeded")
			}
		})
	}
	gatewayA, gatewayB := uuid.New(), uuid.New()
	gatewayBlob, err := Seal(bytes.NewReader(bytes.Repeat([]byte{3}, NonceSize)), key, GatewayCredential, gatewayA, plain)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(key, GatewayCredential, gatewayB, gatewayBlob); err == nil {
		t.Fatal("cross-gateway open succeeded")
	}
	truncated := blobA[:NonceSize]
	if _, err := Open(key, NodeCredential, nodeA, truncated); err == nil {
		t.Fatal("truncated blob accepted")
	}
	tooShort := append([]byte(nil), blobA[:NonceSize+1]...)
	if _, err := Open(key, NodeCredential, nodeA, tooShort); err == nil {
		t.Fatal("too-short blob accepted")
	}
	wrongKey := bytes.Repeat([]byte{0x34}, KeySize)
	if _, err := Open(wrongKey, NodeCredential, nodeA, blobA); err == nil {
		t.Fatal("wrong key accepted")
	}
	tampered := append([]byte(nil), blobA...)
	tampered[len(tampered)-1] ^= 1
	if _, err := Open(key, NodeCredential, nodeA, tampered); err == nil {
		t.Fatal("tampered blob accepted")
	}
	if _, err := Seal(failingReader{}, key, NodeCredential, nodeA, plain); err == nil {
		t.Fatal("RNG failure accepted")
	}
}

func TestStartupWarningOnceAndSecretScannerRedactsCommitment(t *testing.T) {
	warnings := 0
	var warning StartupWarning
	var warningText string
	warning.Emit(func(value string) { warnings++; warningText = value })
	warning.Emit(func(string) { warnings++ })
	if warnings != 1 {
		t.Fatalf("warnings=%d want 1", warnings)
	}
	key := bytes.Repeat([]byte{7}, KeySize)
	commitment := IdentityCommitment(key)
	contents := []NamedContent{
		{Name: "safe", Value: []byte("PASS match=true")},
		{Name: "plaintext", Value: []byte("management-key")},
		{Name: "raw-k2", Value: key},
		{Name: "sealed-blob", Value: []byte("sealed-credential")},
		{Name: "commitment", Value: commitment[:]},
		{Name: "header", Value: []byte("X-Management-Key: management-key")},
		{Name: "native-body", Value: []byte(`{"secret":"management-key"}`)},
	}
	forbidden := [][]byte{[]byte("management-key"), key, []byte("sealed-credential"), commitment[:], []byte("X-Management-Key"), []byte(`{"secret":"management-key"}`)}
	if leaks := ScanSecretEvidence(contents, forbidden); len(leaks) != 6 {
		t.Fatalf("leaks=%v", leaks)
	}
	if bytes.Contains([]byte(warningText), key) || bytes.Contains([]byte(warningText), commitment[:]) {
		t.Fatalf("warning leaked secret material: %q", warningText)
	}
	if bytes.Contains([]byte(warningText), []byte("management-key")) {
		t.Fatalf("warning leaked credential: %q", warningText)
	}
	if leaks := ScanSecretEvidence([]NamedContent{{Name: "safe", Value: []byte("PASS match=true")}}, forbidden); len(leaks) != 0 {
		t.Fatalf("leaks=%v", leaks)
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

type sequenceReader struct {
	chunks [][]byte
	index  int
}

type NamedContent struct {
	Name  string
	Value []byte
}

// ScanSecretEvidence is intentionally test-only. It reports only names of
// evidence containing a forbidden value and never returns the value itself.
func ScanSecretEvidence(contents []NamedContent, forbidden [][]byte) []string {
	var leaks []string
	for _, content := range contents {
		for _, secret := range forbidden {
			if len(secret) > 0 && bytes.Contains(content.Value, secret) {
				leaks = append(leaks, content.Name)
				break
			}
		}
	}
	return leaks
}

func (r *sequenceReader) Read(p []byte) (int, error) {
	if r.index >= len(r.chunks) {
		return 0, io.EOF
	}
	b := r.chunks[r.index]
	r.index++
	copy(p, b)
	return len(b), nil
}
