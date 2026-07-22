package config

import (
	"genshintools/internal/platform/winfile"

	"golang.org/x/sys/windows"
)

func replaceFile(source, destination string) error {
	sourceUTF16, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	destinationUTF16, err := windows.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	return winfile.Replace(sourceUTF16, destinationUTF16, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
