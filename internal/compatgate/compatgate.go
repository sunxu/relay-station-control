// Package compatgate validates a release artifact and the database
// compatibility floor before a Control process is started.
package compatgate

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"
	"golang.org/x/sys/unix"
)

const (
	ManifestVersion         = 1
	SupportedClass          = 1
	FloorSchemaVersion      = 1
	Stage1MigrationVersion  = 33
	ExitOK                  = 0
	ExitDatabaseUnavailable = 75
	ExitIncompatible        = 78
	ExitExecFailure         = 126
	DefaultDatabaseEnv      = "DATABASE_URL"
	DefaultManifestPath     = "/etc/relay-station/control-compat/manifest.v1.json"
	DefaultPublicKeyPath    = "/etc/relay-station/control-compat/manifest.ed25519.pub"
	DefaultArtifactPath     = "/usr/local/bin/control"
)

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Failure is a sanitized gate failure. The underlying cause is deliberately
// private so credentials and driver details cannot reach wrapper logs.
type Failure struct {
	Code   int
	Reason string
	cause  error
}

func (f *Failure) Error() string { return f.Reason }
func (f *Failure) Unwrap() error { return f.cause }

func incompatible(reason string, cause error) *Failure {
	return &Failure{Code: ExitIncompatible, Reason: reason, cause: cause}
}

func unavailable(reason string, cause error) *Failure {
	return &Failure{Code: ExitDatabaseUnavailable, Reason: reason, cause: cause}
}

// SignedManifest is the release-authority signed manifest envelope. The
// signature covers canonicalPayload, not the envelope's JSON formatting.
type SignedManifest struct {
	Version               int    `json:"version"`
	ControlArtifactDigest string `json:"control_artifact_digest"`
	CompatibilityClass    int    `json:"compatibility_class"`
	Signature             string `json:"signature"`
}

// CanonicalPayload returns the v1 payload signed by the release authority:
// [1,"sha256:<lowercase hex>",<class>].
func CanonicalPayload(version int, digest string, compatibilityClass int) ([]byte, error) {
	return json.Marshal([]any{version, digest, compatibilityClass})
}

func decodeManifest(data []byte) (SignedManifest, error) {
	var fields map[string]json.RawMessage
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	if err := decoder.Decode(&fields); err != nil {
		return SignedManifest{}, err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return SignedManifest{}, errors.New("trailing manifest value")
	}
	for _, name := range []string{"version", "control_artifact_digest", "compatibility_class", "signature"} {
		if _, ok := fields[name]; !ok {
			return SignedManifest{}, fmt.Errorf("missing manifest field %q", name)
		}
	}
	if len(fields) != 4 {
		return SignedManifest{}, errors.New("unknown manifest field")
	}
	var result SignedManifest
	for name, target := range map[string]any{
		"version":                 &result.Version,
		"control_artifact_digest": &result.ControlArtifactDigest,
		"compatibility_class":     &result.CompatibilityClass,
		"signature":               &result.Signature,
	} {
		if err := json.Unmarshal(fields[name], target); err != nil {
			return SignedManifest{}, fmt.Errorf("invalid manifest field %q", name)
		}
	}
	return result, nil
}

// VerifyManifest verifies the envelope, signature and supported class. It
// does not inspect the selected artifact or the database.
func VerifyManifest(manifestBytes, publicKeyBytes []byte) (SignedManifest, error) {
	manifest, err := decodeManifest(manifestBytes)
	if err != nil {
		return SignedManifest{}, incompatible("manifest_invalid", err)
	}
	if manifest.Version != ManifestVersion || manifest.CompatibilityClass < 0 || manifest.CompatibilityClass > SupportedClass {
		return SignedManifest{}, incompatible("manifest_class_unsupported", nil)
	}
	if !digestPattern.MatchString(manifest.ControlArtifactDigest) {
		return SignedManifest{}, incompatible("manifest_digest_invalid", nil)
	}
	if len(publicKeyBytes) != ed25519.PublicKeySize {
		return SignedManifest{}, incompatible("manifest_public_key_invalid", nil)
	}
	signature, err := base64.StdEncoding.DecodeString(manifest.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return SignedManifest{}, incompatible("manifest_signature_invalid", err)
	}
	payload, err := CanonicalPayload(manifest.Version, manifest.ControlArtifactDigest, manifest.CompatibilityClass)
	if err != nil || !ed25519.Verify(ed25519.PublicKey(publicKeyBytes), payload, signature) {
		return SignedManifest{}, incompatible("manifest_signature_invalid", err)
	}
	return manifest, nil
}

func readPublicKey(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 {
		return nil, errors.New("public key file is not a protected regular file")
	}
	return os.ReadFile(path)
}

func readManifest(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 64*1024 {
		return nil, errors.New("manifest file is invalid")
	}
	return os.ReadFile(path)
}

func openArtifact(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, errors.New("artifact file descriptor is invalid")
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Mode().Perm()&0o111 == 0 || info.Mode().Perm()&0o022 != 0 {
		file.Close()
		return nil, errors.New("artifact is not a protected regular file")
	}
	return file, nil
}

func digestOpenedArtifact(file *os.File) (string, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func digestArtifact(path string) (string, error) {
	file, err := openArtifact(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	return digestOpenedArtifact(file)
}

// ValidateArtifact verifies the signed manifest and selected regular-file
// artifact, without consulting PostgreSQL.
func ValidateArtifact(manifestPath, publicKeyPath, artifactPath string) (SignedManifest, error) {
	file, err := openArtifact(artifactPath)
	if err != nil {
		return SignedManifest{}, incompatible("artifact_unavailable", err)
	}
	defer file.Close()
	return validateOpenedArtifact(manifestPath, publicKeyPath, file)
}

// ValidateOCIDigest verifies that a selected immutable OCI manifest digest is
// exactly the artifact identity authorized by the signed release manifest.
// The supported Compose wrapper invokes this before creating the container.
func ValidateOCIDigest(ctx context.Context, manifestPath, publicKeyPath, selectedDigest, databaseURL string) (SignedManifest, error) {
	if !digestPattern.MatchString(selectedDigest) {
		return SignedManifest{}, incompatible("artifact_digest_invalid", nil)
	}
	manifestBytes, err := readManifest(manifestPath)
	if err != nil {
		return SignedManifest{}, incompatible("manifest_unavailable", err)
	}
	publicKey, err := readPublicKey(publicKeyPath)
	if err != nil {
		return SignedManifest{}, incompatible("public_key_unavailable", err)
	}
	manifest, err := VerifyManifest(manifestBytes, publicKey)
	if err != nil {
		return SignedManifest{}, err
	}
	if manifest.ControlArtifactDigest != selectedDigest {
		return SignedManifest{}, incompatible("artifact_digest_mismatch", nil)
	}
	floor, err := ReadFloorFromDatabase(ctx, databaseURL)
	if err != nil {
		return SignedManifest{}, err
	}
	if err := ValidateClass(manifest.CompatibilityClass, floor); err != nil {
		return SignedManifest{}, err
	}
	return manifest, nil
}

func validateOpenedArtifact(manifestPath, publicKeyPath string, file *os.File) (SignedManifest, error) {
	manifestBytes, err := readManifest(manifestPath)
	if err != nil {
		return SignedManifest{}, incompatible("manifest_unavailable", err)
	}
	publicKey, err := readPublicKey(publicKeyPath)
	if err != nil {
		return SignedManifest{}, incompatible("public_key_unavailable", err)
	}
	manifest, err := VerifyManifest(manifestBytes, publicKey)
	if err != nil {
		return SignedManifest{}, err
	}
	actualDigest, err := digestOpenedArtifact(file)
	if err != nil {
		return SignedManifest{}, incompatible("artifact_unavailable", err)
	}
	if actualDigest != manifest.ControlArtifactDigest {
		return SignedManifest{}, incompatible("artifact_digest_mismatch", nil)
	}
	return manifest, nil
}

type rowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// ReadFloor jointly validates the Goose migration identity and singleton
// marker. Only a pre-migration-33 database without the marker is floor zero.
func ReadFloor(ctx context.Context, db rowQuerier) (int, error) {
	var maximumVersion int
	var stage1Applied bool
	if err := db.QueryRow(ctx, `SELECT
		COALESCE(max(version_id) FILTER (WHERE is_applied), 0)::integer,
		COALESCE(bool_or(version_id = 33 AND is_applied), false)
		FROM public.goose_db_version`).Scan(&maximumVersion, &stage1Applied); err != nil {
		return 0, unavailable("database_unavailable", err)
	}
	var exists bool
	if err := db.QueryRow(ctx, `SELECT to_regclass('public.control_runtime_compatibility') IS NOT NULL`).Scan(&exists); err != nil {
		return 0, unavailable("database_unavailable", err)
	}
	if !exists {
		if maximumVersion <= 32 && !stage1Applied {
			return 0, nil
		}
		return 0, incompatible("compatibility_marker_invalid", nil)
	}
	if maximumVersion < Stage1MigrationVersion || !stage1Applied {
		return 0, incompatible("compatibility_marker_invalid", nil)
	}
	var schemaVersion, floor int
	err := db.QueryRow(ctx, `SELECT schema_version, phase6_evidence_floor FROM public.control_runtime_compatibility WHERE singleton_id = 1`).Scan(&schemaVersion, &floor)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, unavailable("database_unavailable", err)
	}
	if errors.Is(err, pgx.ErrNoRows) || schemaVersion != FloorSchemaVersion || floor < 0 || floor > SupportedClass {
		return 0, incompatible("compatibility_marker_invalid", err)
	}
	return floor, nil
}

// ReadFloorFromDatabase opens a read-only session and reads the marker. The
// caller resolves the protected database environment variable; credentials
// never appear in the gate's command line or sanitized error output.
func ReadFloorFromDatabase(ctx context.Context, databaseURL string) (int, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return 0, unavailable("database_unavailable", errors.New("database URL is empty"))
	}
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return 0, unavailable("database_unavailable", err)
	}
	connection, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return 0, unavailable("database_unavailable", err)
	}
	defer connection.Close(context.Background())
	if _, err := connection.Exec(ctx, `SET SESSION CHARACTERISTICS AS TRANSACTION READ ONLY`); err != nil {
		return 0, unavailable("database_unavailable", err)
	}
	return ReadFloor(ctx, connection)
}

// Validate performs the complete preflight without starting the artifact.
func Validate(ctx context.Context, manifestPath, publicKeyPath, artifactPath, databaseURL string) (SignedManifest, error) {
	manifest, err := ValidateArtifact(manifestPath, publicKeyPath, artifactPath)
	if err != nil {
		return SignedManifest{}, err
	}
	floor, err := ReadFloorFromDatabase(ctx, databaseURL)
	if err != nil {
		return SignedManifest{}, err
	}
	if err := ValidateClass(manifest.CompatibilityClass, floor); err != nil {
		return SignedManifest{}, err
	}
	return manifest, nil
}

// ValidateClass applies the database floor to a manifest class.
func ValidateClass(compatibilityClass, floor int) error {
	if floor < 0 || floor > SupportedClass || compatibilityClass < 0 || compatibilityClass > SupportedClass {
		return incompatible("compatibility_class_unsupported", nil)
	}
	if compatibilityClass < floor {
		return incompatible("compatibility_floor_rejected", nil)
	}
	return nil
}

// execVerifiedFile replaces the gate with the same opened file object whose
// digest was checked. The original pathname is used only as argv[0].
func execVerifiedFile(file *os.File, artifactPath string, args []string) error {
	if len(args) == 0 {
		args = []string{artifactPath}
	}
	if args[0] != artifactPath {
		return incompatible("exec_artifact_mismatch", nil)
	}
	if _, err := unix.FcntlInt(file.Fd(), unix.F_SETFD, 0); err != nil {
		return &Failure{Code: ExitExecFailure, Reason: "artifact_exec_failed", cause: err}
	}
	if err := descriptorExec(file.Fd(), args, os.Environ()); err != nil {
		return &Failure{Code: ExitExecFailure, Reason: "artifact_exec_failed", cause: err}
	}
	return nil
}

// Run validates and, unless checkOnly is true, starts the selected artifact.
func Run(ctx context.Context, manifestPath, publicKeyPath, artifactPath, databaseURL string, args []string, checkOnly bool) error {
	file, err := openArtifact(artifactPath)
	if err != nil {
		return incompatible("artifact_unavailable", err)
	}
	defer file.Close()
	manifest, err := validateOpenedArtifact(manifestPath, publicKeyPath, file)
	if err != nil {
		return err
	}
	floor, err := ReadFloorFromDatabase(ctx, databaseURL)
	if err != nil {
		return err
	}
	if err := ValidateClass(manifest.CompatibilityClass, floor); err != nil {
		return err
	}
	if checkOnly {
		return nil
	}
	return execVerifiedFile(file, artifactPath, args)
}

// EnsureCommandPath rejects relative paths in the supported deployment path.
func EnsureCommandPath(path string) error {
	if path == "" || !filepath.IsAbs(path) {
		return incompatible("artifact_path_invalid", nil)
	}
	return nil
}
