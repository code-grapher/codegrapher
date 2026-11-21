//go:build linux

package indexer

import (
	"os"

	"golang.org/x/sys/unix"
)

// cloneFile makes dst a reflink (FICLONE) of src on file systems that share
// extents (btrfs, XFS, bcachefs). It fails elsewhere; callers then fall back
// to a plain copy.
func cloneFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if err := unix.IoctlFileClone(int(out.Fd()), int(in.Fd())); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return err
	}
	return out.Close()
}
