//go:build !linux

package compatgate

import "errors"

func descriptorExec(uintptr, []string, []string) error {
	return errors.New("descriptor execution unavailable")
}
