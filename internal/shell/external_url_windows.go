package shell

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var shellExecuteW = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteW")

func openExternalURL(target string) error {
	parsed, err := url.Parse(target)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || !strings.EqualFold(parsed.Hostname(), "fu1.fun") || (parsed.Port() != "" && parsed.Port() != "443") || parsed.User != nil {
		return errors.New("external URL is not an HTTPS Fufu official URL")
	}
	verb, err := windows.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	value, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	result, _, callErr := shellExecuteW.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(value)), 0, 0, 1)
	if result <= 32 {
		if callErr != nil && !errors.Is(callErr, windows.ERROR_SUCCESS) {
			return fmt.Errorf("ShellExecuteW: %w", callErr)
		}
		return fmt.Errorf("ShellExecuteW returned %d", result)
	}
	return nil
}
