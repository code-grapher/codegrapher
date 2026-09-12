//go:build windows

package daemon

import (
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestStateUsesProtectedSingleUserDACL(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	if err := writeState(dir, diskState{Status: Status{Lifecycle: LifecycleStarting}, Nonce: "nonce", Token: "token"}); err != nil {
		t.Fatal(err)
	}
	descriptor, err := windows.GetNamedSecurityInfo(filepath.Join(dir, stateFileName), windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if dacl == nil || dacl.AceCount != 1 {
		t.Fatalf("state DACL entries = %v, want exactly one current-user entry", dacl)
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil {
		t.Fatal(err)
	}
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Mask == 0 {
		t.Fatalf("state ACE = type %d mask %x, want current-user access", ace.Header.AceType, ace.Mask)
	}
}
