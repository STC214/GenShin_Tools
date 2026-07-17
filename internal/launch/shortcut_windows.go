package launch

import (
	"fmt"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"

	"genshintools/internal/game"
	"golang.org/x/sys/windows"
)

var (
	shortcutOle32                = windows.NewLazySystemDLL("ole32.dll")
	shortcutShell32              = windows.NewLazySystemDLL("shell32.dll")
	procShortcutCoInitializeEx   = shortcutOle32.NewProc("CoInitializeEx")
	procShortcutCoUninitialize   = shortcutOle32.NewProc("CoUninitialize")
	procShortcutCoCreateInstance = shortcutOle32.NewProc("CoCreateInstance")
	procShortcutCoTaskMemFree    = shortcutOle32.NewProc("CoTaskMemFree")
	procSHGetKnownFolderPath     = shortcutShell32.NewProc("SHGetKnownFolderPath")
)

const (
	coinitApartmentThreaded = 0x2
	clsctxInprocServer      = 0x1
	rpcEChangedMode         = 0x80010106
)

var (
	clsidShellLink = windows.GUID{Data1: 0x00021401, Data4: [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidShellLinkW  = windows.GUID{Data1: 0x000214F9, Data4: [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidPersistFile = windows.GUID{Data1: 0x0000010B, Data4: [8]byte{0xC0, 0, 0, 0, 0, 0, 0, 0x46}}
	folderDesktop  = windows.GUID{Data1: 0xB4BFCC3A, Data2: 0xDB2C, Data3: 0x424C, Data4: [8]byte{0xB0, 0x29, 0x7F, 0xE9, 0x9A, 0x87, 0xC6, 0x41}}
)

type comObject struct{ vtable *[32]uintptr }

func (o *comObject) call(index int, arguments ...uintptr) uintptr {
	callArguments := append([]uintptr{uintptr(unsafe.Pointer(o))}, arguments...)
	result, _, _ := syscall.SyscallN(o.vtable[index], callArguments...)
	return result
}

func (o *comObject) release() { o.call(2) }

func hresultError(operation string, result uintptr) error {
	if int32(result) < 0 {
		return fmt.Errorf("%s failed: HRESULT 0x%08X", operation, uint32(result))
	}
	return nil
}

func DesktopDirectory() (string, error) {
	var path *uint16
	result, _, _ := procSHGetKnownFolderPath.Call(uintptr(unsafe.Pointer(&folderDesktop)), 0, 0, uintptr(unsafe.Pointer(&path)))
	if err := hresultError("SHGetKnownFolderPath", result); err != nil {
		return "", err
	}
	defer procShortcutCoTaskMemFree.Call(uintptr(unsafe.Pointer(path)))
	return windows.UTF16PtrToString(path), nil
}

func CreateDesktopShortcut(name string, candidate game.Candidate, config Config) (string, error) {
	arguments, err := BuildArguments(config)
	if err != nil {
		return "", err
	}
	desktop, err := DesktopDirectory()
	if err != nil {
		return "", err
	}
	if name == "" {
		name = "原神 - Genshin Tools"
	}
	return filepath.Join(desktop, name+".lnk"), CreateShortcut(filepath.Join(desktop, name+".lnk"), candidate.Executable, candidate.Root, arguments, candidate.Executable)
}

func CreateShortcut(shortcutPath, target, workingDirectory string, arguments []string, iconPath string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	initialized := false
	result, _, _ := procShortcutCoInitializeEx.Call(0, coinitApartmentThreaded)
	if uint32(result) != rpcEChangedMode {
		if err := hresultError("CoInitializeEx", result); err != nil {
			return err
		}
		initialized = true
	}
	if initialized {
		defer procShortcutCoUninitialize.Call()
	}

	var shellLink *comObject
	result, _, _ = procShortcutCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidShellLink)), 0, clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidShellLinkW)), uintptr(unsafe.Pointer(&shellLink)),
	)
	if err := hresultError("CoCreateInstance(CLSID_ShellLink)", result); err != nil {
		return err
	}
	defer shellLink.release()

	setString := func(index int, operation, value string) error {
		pointer, err := windows.UTF16PtrFromString(value)
		if err != nil {
			return err
		}
		result := shellLink.call(index, uintptr(unsafe.Pointer(pointer)))
		runtime.KeepAlive(pointer)
		return hresultError(operation, result)
	}
	if err := setString(20, "IShellLinkW.SetPath", target); err != nil {
		return err
	}
	if err := setString(9, "IShellLinkW.SetWorkingDirectory", workingDirectory); err != nil {
		return err
	}
	if err := setString(11, "IShellLinkW.SetArguments", windows.ComposeCommandLine(arguments)); err != nil {
		return err
	}
	if iconPath != "" {
		pointer, err := windows.UTF16PtrFromString(iconPath)
		if err != nil {
			return err
		}
		result := shellLink.call(17, uintptr(unsafe.Pointer(pointer)), 0)
		runtime.KeepAlive(pointer)
		if err := hresultError("IShellLinkW.SetIconLocation", result); err != nil {
			return err
		}
	}
	description, _ := windows.UTF16PtrFromString("通过 Genshin Tools 纯净启动配置启动游戏")
	if err := hresultError("IShellLinkW.SetDescription", shellLink.call(7, uintptr(unsafe.Pointer(description)))); err != nil {
		return err
	}

	var persistFile *comObject
	result = shellLink.call(0, uintptr(unsafe.Pointer(&iidPersistFile)), uintptr(unsafe.Pointer(&persistFile)))
	if err := hresultError("QueryInterface(IPersistFile)", result); err != nil {
		return err
	}
	defer persistFile.release()
	pathPointer, err := windows.UTF16PtrFromString(shortcutPath)
	if err != nil {
		return err
	}
	result = persistFile.call(6, uintptr(unsafe.Pointer(pathPointer)), 1)
	runtime.KeepAlive(pathPointer)
	return hresultError("IPersistFile.Save", result)
}
