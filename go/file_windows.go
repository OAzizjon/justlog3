//go:build windows

package justlog3

import (
	"os"
	"syscall"
)

// Access rights os.OpenFile uses for O_APPEND: everything GENERIC_WRITE grants except FILE_WRITE_DATA.
const appendAccess = syscall.FILE_APPEND_DATA | syscall.FILE_WRITE_ATTRIBUTES | fileWriteEA |
	syscall.STANDARD_RIGHTS_WRITE | syscall.SYNCHRONIZE

const fileWriteEA = 0x00000010

// openAppend opens name like os.OpenFile with O_APPEND|O_CREATE|O_WRONLY, but
// also shares it for deletion, so other programs can rename or delete the log
// while it is open (log rotation). os.OpenFile does not set FILE_SHARE_DELETE.
func openAppend(name string) (*os.File, error) {
	p, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}
	h, err := syscall.CreateFile(p, appendAccess,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil, syscall.OPEN_ALWAYS, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}
	return os.NewFile(uintptr(h), name), nil
}
