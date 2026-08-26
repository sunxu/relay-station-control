package drivers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeProtectedFile(t *testing.T, path string, value []byte) {
	t.Helper()
	if err := os.WriteFile(path, value, 0o600); err != nil {
		t.Fatalf("write protected file: %v", err)
	}
}

func writeMapping(t *testing.T, directory string, entries []secretMappingEntry) string {
	t.Helper()
	encoded, err := json.Marshal(secretMappingDocument{Provider: FileSecretProvider, References: entries})
	if err != nil {
		t.Fatalf("marshal mapping: %v", err)
	}
	path := filepath.Join(directory, "mapping.json")
	writeProtectedFile(t, path, encoded)
	return path
}

func TestFileSecretResolverResolvesOpaqueReferenceAndRecovers(t *testing.T) {
	directory := t.TempDir()
	secretPath := filepath.Join(directory, "management-key")
	reference := NewSecretReference("file://node-a/management-key")
	mapping := writeMapping(t, directory, []secretMappingEntry{{Reference: "file://node-a/management-key", Path: secretPath}})
	resolver, err := NewFileSecretResolver(FileSecretResolverConfig{MappingFile: mapping})
	if err != nil {
		t.Fatalf("construct resolver: %v", err)
	}

	if _, err := resolver.Resolve(context.Background(), reference); !errors.Is(err, ErrSecretUnavailable) {
		t.Fatalf("missing file error = %v", err)
	}
	writeProtectedFile(t, secretPath, []byte("management-key-value\n"))
	secret, err := resolver.Resolve(context.Background(), reference)
	if err != nil {
		t.Fatalf("resolve after recovery: %v", err)
	}
	defer secret.Destroy()
	var observed string
	if err := secret.Use(func(value string) error { observed = value; return nil }); err != nil {
		t.Fatalf("use secret: %v", err)
	}
	if observed != "management-key-value" {
		t.Fatalf("value was not read or one trailing newline was not removed")
	}

	writeProtectedFile(t, secretPath, []byte("rotated-value\r\n"))
	rotated, err := resolver.Resolve(context.Background(), reference)
	if err != nil {
		t.Fatalf("resolve rotated secret: %v", err)
	}
	defer rotated.Destroy()
	if err := rotated.Use(func(value string) error {
		if value != "rotated-value" {
			t.Fatal("resolver retained stale secret contents")
		}
		return nil
	}); err != nil {
		t.Fatalf("use rotated secret: %v", err)
	}
}

func TestFileSecretResolverClassifiesUnsafeAndUnknownInputs(t *testing.T) {
	directory := t.TempDir()
	safePath := filepath.Join(directory, "safe")
	unsafePath := filepath.Join(directory, "unsafe")
	readablePath := filepath.Join(directory, "group-readable")
	directoryPath := filepath.Join(directory, "not-a-file")
	symlinkPath := filepath.Join(directory, "symlink")
	intermediateTarget := filepath.Join(directory, "intermediate-target")
	intermediateLink := filepath.Join(directory, "intermediate-link")
	unsafeParent := filepath.Join(directory, "unsafe-parent")
	emptyPath := filepath.Join(directory, "empty")
	overPath := filepath.Join(directory, "over")
	writeProtectedFile(t, safePath, []byte("safe-value"))
	writeProtectedFile(t, unsafePath, []byte("unsafe-value"))
	if err := os.Chmod(unsafePath, 0o620); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	writeProtectedFile(t, readablePath, []byte("read"))
	if err := os.Chmod(readablePath, 0o640); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if err := os.Mkdir(directoryPath, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(safePath, symlinkPath); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if err := os.Mkdir(intermediateTarget, 0o700); err != nil {
		t.Fatalf("mkdir intermediate target: %v", err)
	}
	writeProtectedFile(t, filepath.Join(intermediateTarget, "secret"), []byte("safe"))
	if err := os.Symlink(intermediateTarget, intermediateLink); err != nil {
		t.Fatalf("intermediate symlink: %v", err)
	}
	if err := os.Mkdir(unsafeParent, 0o700); err != nil {
		t.Fatalf("mkdir unsafe parent: %v", err)
	}
	writeProtectedFile(t, filepath.Join(unsafeParent, "secret"), []byte("safe"))
	if err := os.Chmod(unsafeParent, 0o770); err != nil {
		t.Fatalf("chmod unsafe parent: %v", err)
	}
	writeProtectedFile(t, emptyPath, nil)
	writeProtectedFile(t, overPath, []byte("123456789"))

	entries := []secretMappingEntry{
		{Reference: "file://safe", Path: safePath},
		{Reference: "file://unsafe", Path: unsafePath},
		{Reference: "file://group-readable", Path: readablePath},
		{Reference: "file://directory", Path: directoryPath},
		{Reference: "file://symlink", Path: symlinkPath},
		{Reference: "file://intermediate-symlink", Path: filepath.Join(intermediateLink, "secret")},
		{Reference: "file://unsafe-parent", Path: filepath.Join(unsafeParent, "secret")},
		{Reference: "file://empty", Path: emptyPath},
		{Reference: "file://over", Path: overPath},
	}
	resolver, err := NewFileSecretResolver(FileSecretResolverConfig{MappingFile: writeMapping(t, directory, entries), MaxSecretBytes: 8})
	if err != nil {
		t.Fatalf("construct resolver: %v", err)
	}
	tests := []struct {
		name      string
		reference string
		want      error
	}{
		{"unknown provider", "vault://safe", ErrSecretProviderUnknown},
		{"malformed reference", "not-a-reference", ErrSecretReferenceUnknown},
		{"unknown reference", "file://unknown", ErrSecretReferenceUnknown},
		{"group writable", "file://unsafe", ErrSecretFileUnsafe},
		{"group readable", "file://group-readable", ErrSecretFileUnsafe},
		{"directory", "file://directory", ErrSecretFileUnsafe},
		{"symlink", "file://symlink", ErrSecretFileUnsafe},
		{"intermediate symlink", "file://intermediate-symlink", ErrSecretFileUnsafe},
		{"unsafe parent", "file://unsafe-parent", ErrSecretFileUnsafe},
		{"empty", "file://empty", ErrSecretFileUnsafe},
		{"over limit", "file://over", ErrSecretFileUnsafe},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := resolver.Resolve(context.Background(), NewSecretReference(test.reference)); !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestFileSecretResolverCancellationAndHeaderSafety(t *testing.T) {
	directory := t.TempDir()
	secretPath := filepath.Join(directory, "secret")
	writeProtectedFile(t, secretPath, []byte("value"))
	resolver, err := NewFileSecretResolver(FileSecretResolverConfig{
		MappingFile: writeMapping(t, directory, []secretMappingEntry{{Reference: "file://safe", Path: secretPath}}),
	})
	if err != nil {
		t.Fatalf("construct resolver: %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := resolver.Resolve(cancelled, NewSecretReference("file://safe")); !errors.Is(err, ErrSecretUnavailable) {
		t.Fatalf("cancelled error = %v", err)
	}

	writeProtectedFile(t, secretPath, []byte("value\nsecond-line"))
	if _, err := resolver.Resolve(context.Background(), NewSecretReference("file://safe")); !errors.Is(err, ErrSecretFileUnsafe) {
		t.Fatalf("embedded newline error = %v", err)
	}
}

func TestSecretAndResolverFormattingNeverLeaksCanaries(t *testing.T) {
	directory := t.TempDir()
	pathCanary := filepath.Join(directory, "path-canary-32d349")
	referenceCanary := "file://reference-canary-32d349"
	keyCanary := "key-canary-32d349"
	writeProtectedFile(t, pathCanary, []byte(keyCanary))
	resolver, err := NewFileSecretResolver(FileSecretResolverConfig{
		MappingFile: writeMapping(t, directory, []secretMappingEntry{{Reference: referenceCanary, Path: pathCanary}}),
	})
	if err != nil {
		t.Fatalf("construct resolver: %v", err)
	}
	secret, err := resolver.Resolve(context.Background(), NewSecretReference(referenceCanary))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	for _, object := range []any{resolver, secret, NewSecretReference(referenceCanary)} {
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
			value := fmt.Sprintf(format, object)
			for _, canary := range []string{pathCanary, referenceCanary, keyCanary} {
				if strings.Contains(value, canary) {
					t.Fatalf("%T format %q leaked canary", object, format)
				}
			}
		}
	}
	secret.Destroy()
	if err := secret.Use(func(string) error { return nil }); !errors.Is(err, ErrSecretUnavailable) {
		t.Fatalf("destroyed secret use = %v", err)
	}

	var reference any = NewSecretReference(referenceCanary)
	if _, implements := reference.(fmt.Stringer); implements {
		t.Fatal("SecretReference must not expose String")
	}
	if _, implements := reference.(fmt.GoStringer); implements {
		t.Fatal("SecretReference must not expose GoString")
	}
}

func TestFileSecretResolverRejectsUnsafeMapping(t *testing.T) {
	directory := t.TempDir()
	secretPath := filepath.Join(directory, "secret")
	writeProtectedFile(t, secretPath, []byte("secret"))
	validMapping := writeMapping(t, directory, []secretMappingEntry{{Reference: "file://safe", Path: secretPath}})

	unsafeMapping := filepath.Join(directory, "unsafe-mapping.json")
	encoded, err := os.ReadFile(validMapping)
	if err != nil {
		t.Fatalf("read mapping: %v", err)
	}
	writeProtectedFile(t, unsafeMapping, encoded)
	if err := os.Chmod(unsafeMapping, 0o606); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if _, err := NewFileSecretResolver(FileSecretResolverConfig{MappingFile: unsafeMapping}); !errors.Is(err, ErrInvalidSecretConfig) {
		t.Fatalf("unsafe mapping error = %v", err)
	}

	symlinkMapping := filepath.Join(directory, "mapping-symlink")
	if err := os.Symlink(validMapping, symlinkMapping); err != nil {
		t.Fatalf("symlink mapping: %v", err)
	}
	if _, err := NewFileSecretResolver(FileSecretResolverConfig{MappingFile: symlinkMapping}); !errors.Is(err, ErrInvalidSecretConfig) {
		t.Fatalf("symlink mapping error = %v", err)
	}
	intermediateTarget := filepath.Join(directory, "mapping-target")
	if err := os.Mkdir(intermediateTarget, 0o700); err != nil {
		t.Fatalf("mkdir mapping target: %v", err)
	}
	writeMapping(t, intermediateTarget, []secretMappingEntry{{Reference: "file://safe", Path: secretPath}})
	intermediateLink := filepath.Join(directory, "mapping-parent-link")
	if err := os.Symlink(intermediateTarget, intermediateLink); err != nil {
		t.Fatalf("symlink mapping parent: %v", err)
	}
	if _, err := NewFileSecretResolver(FileSecretResolverConfig{MappingFile: filepath.Join(intermediateLink, "mapping.json")}); !errors.Is(err, ErrInvalidSecretConfig) {
		t.Fatalf("intermediate symlink mapping error = %v", err)
	}

	unsafeParent := filepath.Join(directory, "unsafe-mapping-parent")
	if err := os.Mkdir(unsafeParent, 0o700); err != nil {
		t.Fatalf("mkdir unsafe mapping parent: %v", err)
	}
	unsafeParentMapping := writeMapping(t, unsafeParent, []secretMappingEntry{{Reference: "file://safe", Path: secretPath}})
	if err := os.Chmod(unsafeParent, 0o770); err != nil {
		t.Fatalf("chmod unsafe mapping parent: %v", err)
	}
	if _, err := NewFileSecretResolver(FileSecretResolverConfig{MappingFile: unsafeParentMapping}); !errors.Is(err, ErrInvalidSecretConfig) {
		t.Fatalf("unsafe mapping parent error = %v", err)
	}

	duplicateMapping := filepath.Join(directory, "duplicate.json")
	writeProtectedFile(t, duplicateMapping, []byte(`{"provider":"file","references":[{"reference":"file://safe","path":"`+secretPath+`"},{"reference":"file://safe","path":"`+secretPath+`"}]}`))
	if _, err := NewFileSecretResolver(FileSecretResolverConfig{MappingFile: duplicateMapping}); !errors.Is(err, ErrInvalidSecretConfig) {
		t.Fatalf("duplicate mapping error = %v", err)
	}
}
