//go:build darwin

package indexer

import "golang.org/x/sys/unix"

// cloneFile makes dst an APFS copy-on-write clone of src. It fails on file
// systems without clonefile support; callers then fall back to a plain copy.
func cloneFile(src, dst string) error {
	return unix.Clonefile(src, dst, unix.CLONE_NOFOLLOW)
}
