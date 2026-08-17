//go:build windows

package backups

import (
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// openStableFile keeps a Windows file ID alive without preventing the planned
// delete itself. os.Open does not provide delete sharing on every supported
// Windows filesystem/runtime combination, which would make confirmed backup
// deletion unusable while the identity handle is held.
func openStableFile(path string) (*os.File, error) {
	return openStableFileWithAccess(path, windows.GENERIC_READ)
}

func openStableDeleteFile(path string) (*os.File, error) {
	return openStableFileWithAccess(path, windows.GENERIC_READ|windows.DELETE)
}

func openStableFileWithAccess(path string, access uint32) (*os.File, error) {
	widePath, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		widePath,
		access,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, os.ErrInvalid
	}
	return file, nil
}

type stableFileDispositionInfoEx struct {
	Flags uint32
}

func removeStableFile(_ string, file *os.File) error {
	return removeStableFileWithDisposition(file, windows.SetFileInformationByHandle)
}

func removeStableFileWithDisposition(
	file *os.File,
	setDisposition func(windows.Handle, uint32, *byte, uint32) error,
) error {
	if file == nil {
		return errors.New("stable file handle is unavailable")
	}
	handle := windows.Handle(file.Fd())
	exInfo := stableFileDispositionInfoEx{
		Flags: windows.FILE_DISPOSITION_DELETE | windows.FILE_DISPOSITION_POSIX_SEMANTICS,
	}
	exErr := setDisposition(
		handle,
		windows.FileDispositionInfoEx,
		(*byte)(unsafe.Pointer(&exInfo)),
		uint32(unsafe.Sizeof(exInfo)),
	)
	if exErr == nil {
		if err := file.Close(); err != nil {
			return errors.Join(errors.New("finalize verified backup file deletion failed"), err)
		}
		return nil
	}
	// Older filesystems may not support FileDispositionInfoEx. The basic
	// disposition class still marks this exact handle's object for deletion;
	// never fall back to deleting the current pathname.
	basicInfo := [1]byte{1}
	basicErr := setDisposition(
		handle,
		windows.FileDispositionInfo,
		&basicInfo[0],
		uint32(len(basicInfo)),
	)
	if basicErr == nil {
		// Basic disposition removes the link only after the last owned handle is
		// closed. Close here so the caller can safely verify the pathname.
		if err := file.Close(); err != nil {
			return errors.Join(errors.New("finalize verified backup file deletion failed"), err)
		}
		return nil
	}
	return errors.Join(errors.New("delete verified backup file by handle failed"), exErr, basicErr)
}
