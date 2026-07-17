package launch

import (
	"errors"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	launchShell32          = windows.NewLazySystemDLL("shell32.dll")
	launchKernel32         = windows.NewLazySystemDLL("kernel32.dll")
	procCommandLineToArgvW = launchShell32.NewProc("CommandLineToArgvW")
	procLocalFree          = launchKernel32.NewProc("LocalFree")
)

// ParseCustomArguments uses the same Windows parser used for shell command
// lines. A dummy argv[0] avoids the special parsing rules for the first token.
func ParseCustomArguments(value string) ([]string, error) {
	if value == "" {
		return nil, nil
	}
	command, err := syscall.UTF16PtrFromString(`GenshinToolsDummy.exe ` + value)
	if err != nil {
		return nil, err
	}
	var count int32
	pointer, _, callErr := procCommandLineToArgvW.Call(uintptr(unsafe.Pointer(command)), uintptr(unsafe.Pointer(&count)))
	if pointer == 0 {
		if errno, ok := callErr.(syscall.Errno); ok && errno != 0 {
			return nil, errno
		}
		return nil, errors.New("CommandLineToArgvW failed")
	}
	defer procLocalFree.Call(pointer)
	if count <= 1 {
		return nil, nil
	}
	items := unsafe.Slice((**uint16)(unsafe.Pointer(pointer)), int(count))
	result := make([]string, 0, count-1)
	for _, item := range items[1:] {
		result = append(result, windows.UTF16PtrToString(item))
	}
	return result, nil
}
