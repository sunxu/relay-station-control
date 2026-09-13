//go:build linux

package compatgate

import (
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// descriptorExec is the Linux fexecve-equivalent. AT_EMPTY_PATH makes the
// kernel execute the already-open file description rather than resolve a
// pathname again.
func descriptorExec(fd uintptr, argv, envv []string) error {
	path, err := syscall.BytePtrFromString("")
	if err != nil {
		return err
	}
	argvp, err := syscall.SlicePtrFromStrings(argv)
	if err != nil {
		return err
	}
	envp, err := syscall.SlicePtrFromStrings(envv)
	if err != nil {
		return err
	}
	_, _, errno := unix.RawSyscall6(
		unix.SYS_EXECVEAT,
		fd,
		uintptr(unsafe.Pointer(path)),
		uintptr(unsafe.Pointer(&argvp[0])),
		uintptr(unsafe.Pointer(&envp[0])),
		unix.AT_EMPTY_PATH,
		0,
	)
	runtime.KeepAlive(path)
	runtime.KeepAlive(argvp)
	runtime.KeepAlive(envp)
	if errno != 0 {
		return errno
	}
	return nil
}
