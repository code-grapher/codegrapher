//go:build windows

package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"unsafe"

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
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Mask != userFileAccess {
		t.Fatalf("state ACE = type %d mask %x, want current-user access", ace.Header.AceType, ace.Mask)
	}
	currentUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	aceSID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	if !aceSID.Equals(currentUser.User.Sid) {
		t.Fatalf("state ACE SID = %v, want current user %v", aceSID, currentUser.User.Sid)
	}

	dirDescriptor, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	dirDACL, _, err := dirDescriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	var dirACE *windows.ACCESS_ALLOWED_ACE
	if dirDACL == nil || dirDACL.AceCount != 1 {
		t.Fatalf("state directory DACL entries = %v, want exactly one current-user entry", dirDACL)
	}
	if err := windows.GetAce(dirDACL, 0, &dirACE); err != nil {
		t.Fatal(err)
	}
	wantInheritance := uint8(windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE)
	if dirACE.Header.AceFlags&wantInheritance != wantInheritance {
		t.Fatalf("state directory ACE flags = %x, want object and container inheritance", dirACE.Header.AceFlags)
	}

	inheritedPath := filepath.Join(dir, "inherited.lock")
	if err := os.WriteFile(inheritedPath, []byte("lock"), 0o600); err != nil {
		t.Fatal("create inherited private file:", err)
	}
	if _, err := os.ReadFile(inheritedPath); err != nil {
		t.Fatal("read inherited private file:", err)
	}
	inheritedDescriptor, err := windows.GetNamedSecurityInfo(inheritedPath, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	inheritedDACL, _, err := inheritedDescriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	var inheritedACE *windows.ACCESS_ALLOWED_ACE
	if inheritedDACL == nil || inheritedDACL.AceCount != 1 {
		t.Fatalf("inherited file DACL entries = %v, want exactly one current-user entry", inheritedDACL)
	}
	if err := windows.GetAce(inheritedDACL, 0, &inheritedACE); err != nil {
		t.Fatal(err)
	}
	if inheritedACE.Header.AceFlags&windows.INHERITED_ACE == 0 {
		t.Fatalf("inherited file ACE flags = %x, want inherited ACE", inheritedACE.Header.AceFlags)
	}
}
