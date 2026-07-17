package game

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const hypRegistryPath = `Software\miHoYo\HYP\1_1\hk4e_cn`

// AutoDiscover reads only documented/local installation hints, then validates
// every result through InspectRoot. It never writes registry or game files.
func AutoDiscover(ctx context.Context, savedPath, customExecutable string) (Discovery, error) {
	roots := []string{savedPath}
	if key, err := registry.OpenKey(registry.CURRENT_USER, hypRegistryPath, registry.QUERY_VALUE); err == nil {
		if value, _, valueErr := key.GetStringValue("GameInstallPath"); valueErr == nil {
			roots = append(roots, value)
		}
		key.Close()
	}
	for _, drive := range fixedDrives() {
		roots = append(roots,
			filepath.Join(drive, `Program Files`, `Genshin Impact`, `Genshin Impact Game`),
			filepath.Join(drive, `Genshin Impact`, `Genshin Impact Game`),
			filepath.Join(drive, `Program Files`, `HoYoPlay`, `games`, `Genshin Impact game`),
			filepath.Join(drive, `Program Files`, `HoYoPlay`, `games`, `Genshin Impact Game`),
		)
	}
	return DiscoverRoots(ctx, roots, customExecutable)
}

func fixedDrives() []string {
	buffer := make([]uint16, 512)
	count, err := windows.GetLogicalDriveStrings(uint32(len(buffer)), &buffer[0])
	if err != nil || count == 0 || int(count) > len(buffer) {
		return []string{`C:\`, `D:\`, `E:\`}
	}
	var drives []string
	for offset := 0; offset < int(count); {
		length := 0
		for offset+length < int(count) && buffer[offset+length] != 0 {
			length++
		}
		if length == 0 {
			break
		}
		drive := syscall.UTF16ToString(buffer[offset : offset+length])
		pointer, _ := windows.UTF16PtrFromString(drive)
		kind := windows.GetDriveType(pointer)
		if kind == windows.DRIVE_FIXED {
			drives = append(drives, drive)
		}
		offset += length + 1
	}
	return drives
}

type ProcessIdentity struct {
	PID          uint32
	CreationTime int64
	Executable   string
	VerifiedPath bool
}

// RunningProcesses snapshots matching processes and records creation time so a
// later refresh can distinguish PID reuse. Full path equality is required when
// Windows allows querying it; name-only results are returned as unverified.
func RunningProcesses(candidate Candidate) ([]ProcessIdentity, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return nil, err
	}
	wantedName := strings.ToLower(candidate.ExeName)
	wantedPath := strings.ToLower(filepath.Clean(candidate.Executable))
	var matches []ProcessIdentity
	for {
		name := strings.ToLower(windows.UTF16ToString(entry.ExeFile[:]))
		if name == wantedName {
			identity, ok := inspectProcess(entry.ProcessID, wantedPath)
			if ok {
				matches = append(matches, identity)
			}
		}
		err = windows.Process32Next(snapshot, &entry)
		if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	return matches, nil
}

func inspectProcess(pid uint32, wantedPath string) (ProcessIdentity, bool) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ProcessIdentity{PID: pid, VerifiedPath: false}, true
	}
	defer windows.CloseHandle(handle)
	identity := ProcessIdentity{PID: pid}
	var creation, exit, kernel, user windows.Filetime
	if windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user) == nil {
		identity.CreationTime = creation.Nanoseconds()
	}
	buffer := make([]uint16, 32768)
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size); err != nil {
		return identity, true
	}
	identity.Executable = windows.UTF16ToString(buffer[:size])
	identity.VerifiedPath = true
	return identity, strings.EqualFold(filepath.Clean(identity.Executable), wantedPath)
}
