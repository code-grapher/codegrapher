//go:build windows

package daemon

import (
	"errors"

	"golang.org/x/sys/windows"
)

func isTransientStateIOError(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
		errors.Is(err, windows.ERROR_LOCK_VIOLATION) ||
		errors.Is(err, windows.ERROR_ACCESS_DENIED)
}
