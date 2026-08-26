package drivers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/sunxu/relay-station-control/internal/drivers/internal/protectedfile"
	"golang.org/x/sys/unix"
)

const FileSecretProvider = "file"

var (
	ErrSecretUnavailable      = errors.New("node driver secret: unavailable")
	ErrSecretReferenceUnknown = errors.New("node driver secret: reference unknown")
	ErrSecretProviderUnknown  = errors.New("node driver secret: provider unknown")
	ErrSecretFileUnsafe       = errors.New("node driver secret: file unsafe")
	ErrInvalidSecretConfig    = errors.New("node driver secret: invalid configuration")

	secretReferencePattern = regexp.MustCompile(`^[a-z][a-z0-9+.-]{1,31}://[A-Za-z0-9][A-Za-z0-9._~:/+-]*$`)
)

// Secret owns a short-lived credential buffer. It always formats as redacted.
// Callers should defer Destroy immediately after Resolve and only expose the
// value inside the callback that constructs the one authorized request header.
type Secret struct {
	mu        sync.Mutex
	value     []byte
	destroyed bool
}

func newSecret(value []byte) *Secret {
	return &Secret{value: append([]byte(nil), value...)}
}

func (secret *Secret) Use(callback func(string) error) error {
	if secret == nil || callback == nil {
		return ErrSecretUnavailable
	}
	secret.mu.Lock()
	defer secret.mu.Unlock()
	if secret.destroyed {
		return ErrSecretUnavailable
	}
	return callback(string(secret.value))
}

func (secret *Secret) Destroy() {
	if secret == nil {
		return
	}
	secret.mu.Lock()
	defer secret.mu.Unlock()
	for index := range secret.value {
		secret.value[index] = 0
	}
	secret.value = nil
	secret.destroyed = true
}

func (*Secret) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED]"))
}

type SecretResolver interface {
	Resolve(context.Context, SecretReference) (*Secret, error)
}

// FileSecretResolverConfig points to a protected JSON mapping file. The file
// contains opaque references and absolute secret paths; a database reference is
// never interpreted as a path.
type FileSecretResolverConfig struct {
	MappingFile     string
	Provider        string
	MaxMappingBytes int64
	MaxSecretBytes  int64
}

type secretMappingDocument struct {
	Provider   string               `json:"provider"`
	References []secretMappingEntry `json:"references"`
}

type secretMappingEntry struct {
	Reference string `json:"reference"`
	Path      string `json:"path"`
}

type FileSecretResolver struct {
	provider       string
	references     map[string]string
	maxSecretBytes int64
}

func (*FileSecretResolver) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("[REDACTED FileSecretResolver]"))
}

func NewFileSecretResolver(configuration FileSecretResolverConfig) (*FileSecretResolver, error) {
	if configuration.Provider == "" {
		configuration.Provider = FileSecretProvider
	}
	if configuration.MaxMappingBytes == 0 {
		configuration.MaxMappingBytes = DefaultSecretMappingBytes
	}
	if configuration.MaxSecretBytes == 0 {
		configuration.MaxSecretBytes = DefaultSecretBytes
	}
	if configuration.Provider != FileSecretProvider || configuration.MappingFile == "" || !filepath.IsAbs(configuration.MappingFile) ||
		configuration.MaxMappingBytes < 1 || configuration.MaxMappingBytes > maximumSecretMappingBytes ||
		configuration.MaxSecretBytes < minimumSecretBytes || configuration.MaxSecretBytes > maximumSecretBytes {
		return nil, ErrInvalidSecretConfig
	}

	encoded, err := readProtectedRegularFile(context.Background(), configuration.MappingFile, configuration.MaxMappingBytes)
	if err != nil {
		return nil, ErrInvalidSecretConfig
	}
	defer clearBytes(encoded)
	var document secretMappingDocument
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil || document.Provider != configuration.Provider || len(document.References) == 0 {
		return nil, ErrInvalidSecretConfig
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, ErrInvalidSecretConfig
	}
	references := make(map[string]string, len(document.References))
	for _, entry := range document.References {
		provider, valid := referenceProvider(entry.Reference)
		if !valid || provider != configuration.Provider || entry.Path == "" || !filepath.IsAbs(entry.Path) || filepath.Clean(entry.Path) != entry.Path {
			return nil, ErrInvalidSecretConfig
		}
		if _, duplicate := references[entry.Reference]; duplicate {
			return nil, ErrInvalidSecretConfig
		}
		references[entry.Reference] = entry.Path
	}
	return &FileSecretResolver{provider: configuration.Provider, references: references, maxSecretBytes: configuration.MaxSecretBytes}, nil
}

func (resolver *FileSecretResolver) Resolve(ctx context.Context, reference SecretReference) (*Secret, error) {
	if err := ctx.Err(); err != nil {
		return nil, ErrSecretUnavailable
	}
	if resolver == nil {
		return nil, ErrSecretUnavailable
	}
	provider, valid := referenceProvider(reference.value)
	if !valid {
		return nil, ErrSecretReferenceUnknown
	}
	if provider != resolver.provider {
		return nil, ErrSecretProviderUnknown
	}
	path, known := resolver.references[reference.value]
	if !known {
		return nil, ErrSecretReferenceUnknown
	}
	value, err := readProtectedRegularFile(ctx, path, resolver.maxSecretBytes)
	if err != nil {
		if errors.Is(err, errUnsafeProtectedFile) {
			return nil, ErrSecretFileUnsafe
		}
		return nil, ErrSecretUnavailable
	}
	defer clearBytes(value)
	value = removeOneTrailingLineEnding(value)
	if len(value) == 0 || bytes.IndexAny(value, "\r\n") >= 0 {
		return nil, ErrSecretFileUnsafe
	}
	return newSecret(value), nil
}

func referenceProvider(reference string) (string, bool) {
	if len(reference) < 6 || len(reference) > 512 || strings.TrimSpace(reference) != reference || !secretReferencePattern.MatchString(reference) {
		return "", false
	}
	separator := strings.Index(reference, "://")
	if separator < 0 {
		return "", false
	}
	return reference[:separator], true
}

var errUnsafeProtectedFile = errors.New("unsafe protected file")

func readProtectedRegularFile(ctx context.Context, path string, maximum int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := protectedfile.Open(path)
	if err != nil {
		if errors.Is(err, protectedfile.ErrUnsafePath) {
			return nil, errUnsafeProtectedFile
		}
		return nil, err
	}
	defer file.Close()
	fileDescriptor := int(file.Fd())
	var fileStatus unix.Stat_t
	if err := unix.Fstat(fileDescriptor, &fileStatus); err != nil ||
		(fileStatus.Uid != 0 && fileStatus.Uid != uint32(os.Geteuid())) {
		return nil, errUnsafeProtectedFile
	}
	information, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !information.Mode().IsRegular() || information.Mode().Perm()&0o077 != 0 || information.Size() < 1 || information.Size() > maximum {
		return nil, errUnsafeProtectedFile
	}

	buffer := make([]byte, 0, information.Size())
	chunk := make([]byte, 4096)
	remaining := maximum + 1
	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			clearBytes(buffer)
			return nil, err
		}
		readSize := int64(len(chunk))
		if readSize > remaining {
			readSize = remaining
		}
		count, readErr := file.Read(chunk[:readSize])
		if count > 0 {
			buffer = append(buffer, chunk[:count]...)
			remaining -= int64(count)
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			clearBytes(buffer)
			return nil, readErr
		}
	}
	clearBytes(chunk)
	if int64(len(buffer)) > maximum {
		clearBytes(buffer)
		return nil, errUnsafeProtectedFile
	}
	return buffer, nil
}

func removeOneTrailingLineEnding(value []byte) []byte {
	if bytes.HasSuffix(value, []byte("\r\n")) {
		return value[:len(value)-2]
	}
	if bytes.HasSuffix(value, []byte("\n")) {
		return value[:len(value)-1]
	}
	return value
}

func clearBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return ErrInvalidSecretConfig
	}
	return nil
}
