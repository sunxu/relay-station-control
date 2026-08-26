// Package protectedfile opens configured files without following symlinks in
// their directory chain. It is internal to the Node Driver implementation.
package protectedfile

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"golang.org/x/sys/unix"
)

var ErrUnsafePath = errors.New("protected file path is unsafe")

// Open traverses an absolute, clean path from the filesystem root. Every
// parent component must be a real directory owned by root or the current UID
// and must not be group/other writable. A root-owned sticky directory is the
// sole write-permission exception, for standard temporary roots such as
// /private/tmp and /tmp.
func Open(path string) (*os.File, error) {
	path = canonicalDarwinRootAlias(path)
	if path == string(filepath.Separator) || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, ErrUnsafePath
	}
	components := strings.Split(strings.TrimPrefix(path, string(filepath.Separator)), string(filepath.Separator))
	if len(components) == 0 || components[len(components)-1] == "" {
		return nil, ErrUnsafePath
	}

	directoryFD, err := unix.Open(string(filepath.Separator), unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = unix.Close(directoryFD) }()

	for _, component := range components[:len(components)-1] {
		if err := validateDirectory(directoryFD); err != nil {
			return nil, err
		}
		nextFD, openErr := unix.Openat(directoryFD, component, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
		if openErr != nil {
			if errors.Is(openErr, unix.ELOOP) || errors.Is(openErr, unix.ENOTDIR) {
				return nil, ErrUnsafePath
			}
			return nil, openErr
		}
		_ = unix.Close(directoryFD)
		directoryFD = nextFD
	}
	if err := validateDirectory(directoryFD); err != nil {
		return nil, err
	}

	fileFD, err := unix.Openat(directoryFD, components[len(components)-1], unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		if errors.Is(err, unix.ELOOP) {
			return nil, ErrUnsafePath
		}
		return nil, err
	}
	file := os.NewFile(uintptr(fileFD), "protected-file")
	if file == nil {
		_ = unix.Close(fileFD)
		return nil, ErrUnsafePath
	}
	return file, nil
}

func validateDirectory(fileDescriptor int) error {
	var status unix.Stat_t
	if err := unix.Fstat(fileDescriptor, &status); err != nil {
		return err
	}
	if !directoryMetadataSafe(status.Uid, uint32(status.Mode)) {
		return ErrUnsafePath
	}
	return nil
}

func directoryMetadataSafe(owner, mode uint32) bool {
	current := uint32(os.Geteuid())
	if owner != 0 && owner != current {
		return false
	}
	if mode&0o022 == 0 {
		return true
	}
	return owner == 0 && mode&0o1000 != 0
}

// Darwin exposes /tmp and /var as immutable root-owned aliases into /private.
// Normalize only these platform aliases so os.TempDir/t.TempDir paths remain
// usable; all later components still undergo strict no-symlink traversal.
func canonicalDarwinRootAlias(path string) string {
	if runtime.GOOS != "darwin" {
		return path
	}
	for alias, canonical := range map[string]string{"/tmp": "/private/tmp", "/var": "/private/var"} {
		if path == alias {
			return canonical
		}
		if strings.HasPrefix(path, alias+string(filepath.Separator)) {
			return canonical + strings.TrimPrefix(path, alias)
		}
	}
	return path
}
