package localenhance

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const BetterGIURI = "bettergi://start"

type BetterGIInfo struct {
	Registered bool
	Executable string
	Command    string
	License    string
}

func AuditBetterGI() (BetterGIInfo, error) {
	info := BetterGIInfo{License: "GPL-3.0; external protocol integration only"}
	key, err := registry.OpenKey(registry.CLASSES_ROOT, `bettergi\shell\open\command`, registry.QUERY_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return info, nil
	}
	if err != nil {
		return info, err
	}
	defer key.Close()
	command, kind, err := key.GetStringValue("")
	if err != nil {
		return info, fmt.Errorf("read BetterGI URL handler: %w", err)
	}
	if kind != registry.SZ && kind != registry.EXPAND_SZ {
		return info, fmt.Errorf("read BetterGI URL handler: unexpected registry type %d", kind)
	}
	executable, err := executableFromCommand(command)
	if err != nil {
		return info, err
	}
	absolute, err := filepath.Abs(os.ExpandEnv(executable))
	if err != nil {
		return info, err
	}
	file, err := os.Stat(absolute)
	if err != nil || file.IsDir() || !strings.EqualFold(filepath.Ext(absolute), ".exe") {
		return info, fmt.Errorf("BetterGI handler target is unavailable: %s", absolute)
	}
	info.Registered, info.Executable, info.Command = true, absolute, command
	return info, nil
}

func executableFromCommand(command string) (string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return "", errors.New("BetterGI URL handler command is empty")
	}
	if command[0] == '"' {
		end := strings.Index(command[1:], `"`)
		if end < 0 {
			return "", errors.New("BetterGI URL handler has unmatched quote")
		}
		return command[1 : end+1], nil
	}
	if index := strings.IndexAny(command, " \t"); index >= 0 {
		return command[:index], nil
	}
	return command, nil
}

var shellExecuteW = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteW")

func StartBetterGI() error {
	info, err := AuditBetterGI()
	if err != nil {
		return err
	}
	if !info.Registered {
		return errors.New("BetterGI URL handler is not registered")
	}
	verb, _ := windows.UTF16PtrFromString("open")
	target, _ := windows.UTF16PtrFromString(BetterGIURI)
	result, _, callErr := shellExecuteW.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(target)), 0, 0, 1)
	if result <= 32 {
		return fmt.Errorf("ShellExecuteW BetterGI failed with code %d: %w", result, callErr)
	}
	return nil
}
