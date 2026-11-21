//go:build !darwin && !linux

package indexer

import "errors"

// cloneFile is unsupported here; callers fall back to a plain copy.
func cloneFile(src, dst string) error {
	return errors.New("copy-on-write clone not supported on this platform")
}
