package selfupdate

import (
	"genshintools/internal/platform/winfile"

	"golang.org/x/sys/windows"
)

func replaceFile(source, destination string) error {
	return movePath(source, destination, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

// Promote a new directory without overwriting an existing staged release.
// Reuse the bounded Windows sharing/access retry used for atomic file writes.
func renameNewDirectory(source, destination string) error {
	return movePath(source, destination, windows.MOVEFILE_WRITE_THROUGH)
}

func movePath(source, destination string, flags uint32) error {
	sourcePointer, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	destinationPointer, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	return winfile.Replace(sourcePointer, destinationPointer, flags)
}
