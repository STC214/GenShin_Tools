package injection

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const fileAttributeReparsePoint = 0x00000400

var (
	versionDLL                 = windows.NewLazySystemDLL("version.dll")
	procGetFileVersionInfoSize = versionDLL.NewProc("GetFileVersionInfoSizeW")
	procGetFileVersionInfo     = versionDLL.NewProc("GetFileVersionInfoW")
	procVerQueryValue          = versionDLL.NewProc("VerQueryValueW")
)

type fixedFileInfo struct {
	Signature, StructVersion           uint32
	FileVersionMS, FileVersionLS       uint32
	ProductVersionMS, ProductVersionLS uint32
	FileFlagsMask, FileFlags           uint32
	FileOS, FileType, FileSubtype      uint32
	FileDateMS, FileDateLS             uint32
}

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

func lockFileReadOnly(path string) (windows.Handle, error) {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	handle, err := windows.CreateFile(pointer, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return 0, err
	}
	return handle, nil
}

// rejectReparseTree checks every existing component from root through target.
// The elevated helper uses it to prevent a portable data subtree from being
// redirected after the unelevated process has validated its lexical path.
func rejectReparseTree(root, target string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("path is outside its trusted root")
	}
	current := root
	parts := []string{}
	if relative != "." {
		parts = strings.Split(relative, string(filepath.Separator))
	}
	for index := -1; index < len(parts); index++ {
		if index >= 0 {
			current = filepath.Join(current, parts[index])
		}
		if _, err := os.Lstat(current); err != nil {
			return err
		}
		if err := rejectReparse(current); err != nil {
			return fmt.Errorf("%s: %w", current, err)
		}
	}
	return nil
}

func fileVersion(path string) (string, error) {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	size, _, sizeErr := procGetFileVersionInfoSize.Call(uintptr(unsafe.Pointer(pointer)), 0)
	if size == 0 {
		if errno, ok := sizeErr.(syscall.Errno); ok && (errno == 0 || errno == windows.ERROR_RESOURCE_DATA_NOT_FOUND || errno == windows.ERROR_RESOURCE_TYPE_NOT_FOUND || errno == windows.ERROR_RESOURCE_NAME_NOT_FOUND) {
			return "", nil
		}
		return "", fmt.Errorf("GetFileVersionInfoSizeW: %w", sizeErr)
	}
	if size > 16<<20 {
		return "", errors.New("version resource exceeds 16 MiB")
	}
	buffer := make([]byte, size)
	ok, _, callErr := procGetFileVersionInfo.Call(uintptr(unsafe.Pointer(pointer)), 0, size, uintptr(unsafe.Pointer(&buffer[0])))
	if ok == 0 {
		return "", fmt.Errorf("GetFileVersionInfoW: %w", callErr)
	}
	root, _ := windows.UTF16PtrFromString(`\`)
	var value *fixedFileInfo
	var length uint32
	ok, _, callErr = procVerQueryValue.Call(uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(root)), uintptr(unsafe.Pointer(&value)), uintptr(unsafe.Pointer(&length)))
	if ok == 0 {
		return "", fmt.Errorf("VerQueryValueW fixed file info: %w", callErr)
	}
	if value == nil || length < uint32(unsafe.Sizeof(fixedFileInfo{})) || value.Signature != 0xFEEF04BD {
		return "", errors.New("version resource has invalid fixed file info")
	}
	return fmt.Sprintf("%d.%d.%d.%d", value.FileVersionMS>>16, value.FileVersionMS&0xffff, value.FileVersionLS>>16, value.FileVersionLS&0xffff), nil
}
