package plugins

import (
	"errors"

	"golang.org/x/sys/windows"
)

const fileAttributeReparsePoint = 0x00000400

func rejectReparse(path string) error {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	attributes, err := windows.GetFileAttributes(pointer)
	if err != nil {
		return err
	}
	if attributes&fileAttributeReparsePoint != 0 {
		return errors.New("reparse points are not allowed")
	}
	return nil
}
