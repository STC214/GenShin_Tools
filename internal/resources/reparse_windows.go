package resources

import (
	"fmt"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func rejectReparseAncestors(root, directory string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	directory, err = filepath.Abs(directory)
	if err != nil {
		return err
	}
	if err := ensureContained(root, directory); err != nil {
		return err
	}
	current := root
	relative, _ := filepath.Rel(root, directory)
	parts := []string{"."}
	if relative != "." {
		parts = append(parts, splitPath(relative)...)
	}
	for _, part := range parts {
		if part != "." {
			current = filepath.Join(current, part)
		}
		pathUTF16, err := windows.UTF16PtrFromString(current)
		if err != nil {
			return err
		}
		attributes, err := windows.GetFileAttributes(pathUTF16)
		if err == windows.ERROR_FILE_NOT_FOUND || err == windows.ERROR_PATH_NOT_FOUND {
			continue
		}
		if err != nil {
			return err
		}
		if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return fmt.Errorf("reparse point found at %s", current)
		}
	}
	return nil
}

func splitPath(value string) []string {
	var parts []string
	for value != "." && value != "" {
		directory, file := filepath.Split(value)
		if file != "" {
			parts = append([]string{file}, parts...)
		}
		value = filepath.Clean(directory)
	}
	return parts
}
