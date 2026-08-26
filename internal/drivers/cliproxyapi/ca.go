package cliproxyapi

import (
	"crypto/x509"
	"errors"
	"io"
	"os"

	"github.com/sunxu/relay-station-control/internal/drivers/internal/protectedfile"
	"golang.org/x/sys/unix"
)

const maximumCABytes int64 = 1 << 20

// LoadRootCAs reads an optional protected PEM bundle without following any
// symlink in its path. It returns fixed errors that never contain that path.
func LoadRootCAs(path string) (*x509.CertPool, error) {
	if path == "" {
		return nil, nil
	}
	file, err := protectedfile.Open(path)
	if err != nil {
		return nil, errors.New("cliproxyapi CA file is unavailable")
	}
	defer file.Close()
	fileDescriptor := int(file.Fd())
	var fileStatus unix.Stat_t
	if err := unix.Fstat(fileDescriptor, &fileStatus); err != nil || !caFileOwnerAllowed(fileStatus.Uid) {
		return nil, errors.New("cliproxyapi CA file is unsafe")
	}
	information, err := file.Stat()
	if err != nil || !information.Mode().IsRegular() || information.Mode().Perm()&0o022 != 0 ||
		information.Size() < 1 || information.Size() > maximumCABytes {
		return nil, errors.New("cliproxyapi CA file is unsafe")
	}
	encoded, err := io.ReadAll(io.LimitReader(file, maximumCABytes+1))
	if err != nil || int64(len(encoded)) > maximumCABytes {
		return nil, errors.New("cliproxyapi CA file is unavailable")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(encoded) {
		return nil, errors.New("cliproxyapi CA file is invalid")
	}
	return pool, nil
}

func caFileOwnerAllowed(owner uint32) bool {
	return owner == 0 || owner == uint32(os.Geteuid())
}
