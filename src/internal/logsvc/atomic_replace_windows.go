//go:build windows

package logsvc

import "golang.org/x/sys/windows"

func publishFileAtomic(source string, destination string) (bool, error) {
	sourcePath, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return false, err
	}
	destinationPath, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return false, err
	}
	if err := windows.MoveFileEx(sourcePath, destinationPath, windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return false, err
	}
	return true, nil
}
