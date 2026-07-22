package selfupdate

import (
	"genshintools/internal/platform/winfile"

	"golang.org/x/sys/windows"
)

func replaceFile(source, destination string) error {
	sourcePointer, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	destinationPointer, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	return winfile.Replace(sourcePointer, destinationPointer, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
