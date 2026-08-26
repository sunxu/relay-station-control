package protectedfile

import (
	"os"
	"testing"
)

func TestDirectoryMetadataPolicy(t *testing.T) {
	current := uint32(os.Geteuid())
	if !directoryMetadataSafe(0, 0o755) || !directoryMetadataSafe(current, 0o700) ||
		!directoryMetadataSafe(0, 0o1777) {
		t.Fatal("safe root/current/sticky directory rejected")
	}
	other := current + 1
	if other == 0 || other == current {
		other = current - 1
	}
	if directoryMetadataSafe(other, 0o700) || directoryMetadataSafe(current, 0o1777) ||
		directoryMetadataSafe(0, 0o0777) {
		t.Fatal("unsafe owner or writable directory accepted")
	}
}
