//go:build windows

package store

// Building and reading owner-only access-control lists on Windows lives here.
// When they are applied and what a refusal says do not.

import (
	"errors"
	"fmt"
	"slices"
	"syscall"
	"unsafe"
)

var (
	advapi32                  = syscall.NewLazyDLL("advapi32.dll")
	procConvertStringSDToSD   = advapi32.NewProc("ConvertStringSecurityDescriptorToSecurityDescriptorW")
	procGetNamedSecurityInfoW = advapi32.NewProc("GetNamedSecurityInfoW")
	errNotOwnerOnly           = errors.New("access is granted to someone other than the owner")
)

const (
	sddlRevision1             = 1
	seFileObject              = 1
	daclSecurityInformation   = 0x4
	accessDeniedACEType       = 0x1
	accessDeniedObjectACEType = 0x6
	accessDeniedCallbackType  = 0xA
	accessDeniedCallbackObj   = 0xC
	accessAllowedACEType      = 0x0
)

// currentUserSID is the principal "owner-only" means: the user the process
// runs as, read from its token rather than from a name that could be spelled
// two ways.
func currentUserSID() (string, error) {
	token, err := syscall.OpenCurrentProcessToken()
	if err != nil {
		return "", err
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return "", err
	}
	return user.User.Sid.String()
}

// privateSecurityAttributes builds security attributes carrying a protected
// list (P: nothing inherited from the parent) with one entry, full access for
// the current user, inherited by files (OI) and directories (CI) created
// below. The handle a creation returns is not inheritable.
func privateSecurityAttributes() (*syscall.SecurityAttributes, func(), error) {
	sid, err := currentUserSID()
	if err != nil {
		return nil, nil, err
	}
	sddl, err := syscall.UTF16PtrFromString("D:P(A;OICI;FA;;;" + sid + ")")
	if err != nil {
		return nil, nil, err
	}
	var sd uintptr
	ok, _, callErr := procConvertStringSDToSD.Call(uintptr(unsafe.Pointer(sddl)), sddlRevision1, uintptr(unsafe.Pointer(&sd)), 0)
	if ok == 0 {
		return nil, nil, callErr
	}
	sa := &syscall.SecurityAttributes{SecurityDescriptor: sd}
	sa.Length = uint32(unsafe.Sizeof(*sa))
	return sa, func() { syscall.LocalFree(syscall.Handle(sd)) }, nil
}

type aclHeader struct {
	Revision byte
	Sbz1     byte
	Size     uint16
	Count    uint16
	Sbz2     uint16
}

type aceHeader struct {
	Type  byte
	Flags byte
	Size  uint16
}

// foreignGrants reads path's list and returns the current user's SID and every
// other SID that an entry grants anything to; owner-only means none. A missing
// list (a NULL DACL) grants everyone everything and is reported as such. Deny
// entries only narrow access and are allowed. A granting entry of another kind
// (object, callback, compound) is reported rather than interpreted: an entry
// this function cannot read is not evidence of owner-only access. Inherit-only
// entries are judged too, because they decide what every packet created below
// this directory will allow.
func foreignGrants(path string) (user string, foreign []string, err error) {
	user, err = currentUserSID()
	if err != nil {
		return "", nil, err
	}
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return "", nil, err
	}
	var dacl *aclHeader
	var sd uintptr
	status, _, _ := procGetNamedSecurityInfoW.Call(uintptr(unsafe.Pointer(p)), seFileObject, daclSecurityInformation,
		0, 0, uintptr(unsafe.Pointer(&dacl)), 0, uintptr(unsafe.Pointer(&sd)))
	if status != 0 {
		return "", nil, syscall.Errno(status)
	}
	defer syscall.LocalFree(syscall.Handle(sd))
	if dacl == nil {
		return user, nil, fmt.Errorf("%w: the list is missing, which grants everyone", errNotOwnerOnly)
	}
	offset := uintptr(unsafe.Sizeof(*dacl))
	for i := 0; i < int(dacl.Count); i++ {
		if offset+unsafe.Sizeof(aceHeader{}) > uintptr(dacl.Size) {
			return "", nil, fmt.Errorf("access list entry %d lies outside the list", i)
		}
		ace := (*aceHeader)(unsafe.Add(unsafe.Pointer(dacl), offset))
		if uintptr(ace.Size) < unsafe.Sizeof(aceHeader{}) || offset+uintptr(ace.Size) > uintptr(dacl.Size) {
			return "", nil, fmt.Errorf("access list entry %d has an impossible size", i)
		}
		switch ace.Type {
		case accessDeniedACEType, accessDeniedObjectACEType, accessDeniedCallbackType, accessDeniedCallbackObj:
		case accessAllowedACEType:
			// Header, then the 4-byte access mask, then the SID.
			trustee := (*syscall.SID)(unsafe.Add(unsafe.Pointer(ace), unsafe.Sizeof(*ace)+4))
			got, err := trustee.String()
			if err != nil {
				return "", nil, err
			}
			if got != user && !slices.Contains(foreign, got) {
				foreign = append(foreign, got)
			}
		default:
			return user, foreign, fmt.Errorf("%w: entry type %#x grants access this check does not interpret", errNotOwnerOnly, ace.Type)
		}
		offset += uintptr(ace.Size)
	}
	return user, foreign, nil
}
