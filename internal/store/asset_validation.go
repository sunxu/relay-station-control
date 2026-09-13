package store

import (
	"regexp"
	"strings"
)

// assetSecretReferencePattern mirrors public.control_valid_secret_reference.
// Keep this pre-transaction validator and the database constraint covered by
// the same acceptance matrix whenever either contract changes.
var assetSecretReferencePattern = regexp.MustCompile(`^[a-z][a-z0-9+.-]{1,31}://[A-Za-z0-9][A-Za-z0-9._~:/+-]*$`)

// ValidAssetSecretReference accepts exactly the opaque reference language
// enforced by public.control_valid_secret_reference. HTTP(S) values are
// endpoints, not Secret references.
func ValidAssetSecretReference(value string) bool {
	return len(value) >= 6 && len(value) <= 512 &&
		strings.TrimSpace(value) == value &&
		!strings.HasPrefix(value, "http://") &&
		!strings.HasPrefix(value, "https://") &&
		assetSecretReferencePattern.MatchString(value)
}
