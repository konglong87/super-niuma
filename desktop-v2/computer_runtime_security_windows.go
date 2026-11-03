//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"runtime"

	"golang.org/x/sys/windows"
)

// Windows does not expose Unix mode bits or uid ownership through os.FileInfo.
// Protect the runtime directory with a non-inheriting DACL that grants full
// access only to the current process user. This keeps the same owner-only
// boundary as the Unix implementation without relying on syscall.Stat_t.
func validatePrivateRuntimeDirectory(path string, info os.FileInfo) error {
	if !info.IsDir() {
		return errors.New("runtime directory must be an owner-only private directory")
	}

	tokenUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("resolve current Windows user: %w", err)
	}
	if tokenUser == nil || tokenUser.User.Sid == nil || !tokenUser.User.Sid.IsValid() {
		return errors.New("resolve current Windows user: invalid user SID")
	}

	userSID := tokenUser.User.Sid
	var pinner runtime.Pinner
	pinner.Pin(userSID)
	defer pinner.Unpin()

	acl, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.SET_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(userSID),
		},
	}}, nil)
	if err != nil {
		return fmt.Errorf("build runtime directory ACL: %w", err)
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		acl,
		nil,
	); err != nil {
		return fmt.Errorf("protect runtime directory ACL: %w", err)
	}
	return nil
}
