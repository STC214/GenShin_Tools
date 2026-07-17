package resources

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

type DiskSpace struct {
	Available uint64
	Total     uint64
	Free      uint64
}

func QueryDiskSpace(path string) (DiskSpace, error) {
	existing := path
	for {
		if _, err := os.Stat(existing); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return DiskSpace{}, err
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return DiskSpace{}, fmt.Errorf("no existing ancestor for %s", path)
		}
		existing = parent
	}
	pointer, err := windows.UTF16PtrFromString(existing)
	if err != nil {
		return DiskSpace{}, err
	}
	var result DiskSpace
	if err := windows.GetDiskFreeSpaceEx(pointer, &result.Available, &result.Total, &result.Free); err != nil {
		return DiskSpace{}, fmt.Errorf("query disk space for %s: %w", existing, err)
	}
	return result, nil
}

func RequireDiskSpace(path string, bytes uint64) error {
	space, err := QueryDiskSpace(path)
	if err != nil {
		return err
	}
	if space.Available < bytes {
		return fmt.Errorf("insufficient disk space at %s: available %d bytes, require %d bytes", path, space.Available, bytes)
	}
	return nil
}
