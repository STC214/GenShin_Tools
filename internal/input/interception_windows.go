//go:build windows

package input

import (
	"errors"
	"fmt"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	// InterceptionReleaseURL is the pinned official driver release presented
	// to users. The driver remains a separately licensed, user-installed
	// system component and is not redistributed by this project.
	InterceptionReleaseURL = "https://github.com/oblitum/Interception/releases/tag/v1.0.1"

	interceptionKeyUp         = uint16(0x01)
	interceptionKeyE0         = uint16(0x02)
	interceptionKeyE1         = uint16(0x04)
	interceptionSetEventIOCTL = uint32((0x22 << 16) | (0x810 << 2))
	interceptionWriteIOCTL    = uint32((0x22 << 16) | (0x820 << 2))
	interceptionMarker        = uintptr(0x51485844)
	interceptionDeviceCount   = 20
)

// InterceptionDriverStatus distinguishes a missing driver from an installed
// driver that this process cannot open. Interception requires an elevated
// client even after installation.
type InterceptionDriverStatus struct {
	Installed  bool
	Accessible bool
	Busy       bool
	Error      string
}

// interceptionKeyboardInput mirrors the documented Windows
// KEYBOARD_INPUT_DATA ABI consumed by the Interception keyboard device.
// Keep this definition private: it is a protocol boundary, not a copied
// implementation of the upstream user-mode library.
type interceptionKeyboardInput struct {
	UnitID           uint16
	MakeCode         uint16
	Flags            uint16
	Reserved         uint16
	ExtraInformation uint32
}

type interceptionKeyboardBackend struct {
	devices [interceptionDeviceCount]windows.Handle
	events  [interceptionDeviceCount]windows.Handle
	mu      sync.Mutex
}

func ProbeInterceptionDriver() InterceptionDriverStatus {
	backend, err := newInterceptionContext()
	if err == nil {
		backend.Close()
		level, levelErr := currentIntegrityLevel()
		if levelErr != nil {
			return InterceptionDriverStatus{Installed: true, Error: levelErr.Error()}
		}
		if level < 0x3000 {
			return InterceptionDriverStatus{
				Installed: true,
				Error:     "Interception requires an administrator (High integrity) process",
			}
		}
		return InterceptionDriverStatus{Installed: true, Accessible: true}
	}
	status := InterceptionDriverStatus{Error: err.Error()}
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		status.Installed = true
	} else if errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_BUSY) {
		status.Installed = true
		status.Busy = true
	}
	return status
}

func newInterceptionKeyboardBackend() (*interceptionKeyboardBackend, error) {
	level, err := currentIntegrityLevel()
	if err != nil {
		return nil, fmt.Errorf("query integrity before opening Interception: %w", err)
	}
	if level < 0x3000 {
		return nil, errors.New("Interception requires Genshin Tools to run as administrator (High integrity)")
	}
	backend, err := newInterceptionContext()
	if err != nil {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return nil, fmt.Errorf("Interception driver is installed but inaccessible; run Genshin Tools as administrator: %w", err)
		}
		if errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_BUSY) {
			return nil, fmt.Errorf("Interception driver is busy; close other applications using Interception and retry: %w", err)
		}
		return nil, fmt.Errorf("Interception keyboard driver is unavailable; install v1.0.1 and restart Windows: %w", err)
	}
	return backend, nil
}

func newInterceptionContext() (*interceptionKeyboardBackend, error) {
	backend := &interceptionKeyboardBackend{}
	for index := range backend.devices {
		backend.devices[index] = windows.InvalidHandle
	}
	for index := 0; index < interceptionDeviceCount; index++ {
		path, err := windows.UTF16PtrFromString(fmt.Sprintf(`\\.\interception%02d`, index))
		if err != nil {
			backend.Close()
			return nil, err
		}
		device, err := windows.CreateFile(
			path,
			windows.GENERIC_READ,
			0,
			nil,
			windows.OPEN_EXISTING,
			0,
			0,
		)
		if err != nil {
			backend.Close()
			return nil, fmt.Errorf("open Interception device %02d: %w", index, err)
		}
		backend.devices[index] = device
		event, err := windows.CreateEvent(nil, 1, 0, nil)
		if err != nil {
			backend.Close()
			return nil, fmt.Errorf("create Interception device event %02d: %w", index, err)
		}
		backend.events[index] = event
		eventHandles := [2]windows.Handle{event, 0}
		var returned uint32
		err = windows.DeviceIoControl(
			device,
			interceptionSetEventIOCTL,
			(*byte)(unsafe.Pointer(&eventHandles[0])),
			uint32(unsafe.Sizeof(eventHandles)),
			nil,
			0,
			&returned,
			nil,
		)
		if err != nil {
			backend.Close()
			return nil, fmt.Errorf("initialize Interception device %02d: %w", index, err)
		}
	}
	return backend, nil
}

func (backend *interceptionKeyboardBackend) Close() error {
	if backend == nil {
		return nil
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	var result error
	for index := range backend.devices {
		if backend.devices[index] != 0 && backend.devices[index] != windows.InvalidHandle {
			result = errors.Join(result, windows.CloseHandle(backend.devices[index]))
			backend.devices[index] = windows.InvalidHandle
		}
		if backend.events[index] != 0 && backend.events[index] != windows.InvalidHandle {
			result = errors.Join(result, windows.CloseHandle(backend.events[index]))
			backend.events[index] = 0
		}
	}
	return result
}

func (backend *interceptionKeyboardBackend) Press(key uint32, interval time.Duration) error {
	if backend == nil {
		return errors.New("Interception keyboard backend is unavailable")
	}
	down, err := interceptionKeyboardData(key, false)
	if err != nil {
		return err
	}
	up, err := interceptionKeyboardData(key, true)
	if err != nil {
		return err
	}

	backend.mu.Lock()
	defer backend.mu.Unlock()
	if backend.devices[0] == 0 || backend.devices[0] == windows.InvalidHandle {
		return errors.New("Interception keyboard backend is closed")
	}
	if err := backend.writeLocked(&down); err != nil {
		return err
	}
	if hold := interceptionHoldDuration(interval); hold > 0 {
		time.Sleep(hold)
	}
	if err := backend.writeLocked(&up); err != nil {
		// A down edge may already be inside the device stack. Retry the release
		// once before surfacing the fault so a transient error is less likely
		// to leave a key logically held.
		_ = backend.writeLocked(&up)
		return err
	}
	return nil
}

func (backend *interceptionKeyboardBackend) writeLocked(data *interceptionKeyboardInput) error {
	var written uint32
	err := windows.DeviceIoControl(
		backend.devices[0],
		interceptionWriteIOCTL,
		(*byte)(unsafe.Pointer(data)),
		uint32(unsafe.Sizeof(*data)),
		nil,
		0,
		&written,
		nil,
	)
	if err != nil {
		return fmt.Errorf("Interception keyboard write: %w", err)
	}
	if written != uint32(unsafe.Sizeof(*data)) {
		return fmt.Errorf("Interception keyboard write accepted %d of %d bytes", written, unsafe.Sizeof(*data))
	}
	return nil
}

func interceptionKeyboardData(key uint32, up bool) (interceptionKeyboardInput, error) {
	key = NormalizeKeyCode(key)
	virtualKey := VirtualKey(key)
	foreground := windows.GetForegroundWindow()
	threadID, _, _ := procGetWindowThreadProcessID.Call(uintptr(foreground), 0)
	layout, _, _ := procGetKeyboardLayout.Call(threadID)
	scan, _, callErr := procMapVirtualKeyExW.Call(uintptr(virtualKey), mapvkVKToVSCEx, layout)
	if scan == 0 {
		return interceptionKeyboardInput{}, fmt.Errorf(
			"MapVirtualKeyExW returned no scan code for virtual key 0x%02X: %w",
			virtualKey,
			normalizeCallError(callErr),
		)
	}
	flags := uint16(0)
	prefix := uint16((scan >> 8) & 0xff)
	switch {
	case prefix == 0xe1:
		flags |= interceptionKeyE1
	case KeyIsExtended(key) || prefix == 0xe0:
		flags |= interceptionKeyE0
	}
	if up {
		flags |= interceptionKeyUp
	}
	return interceptionKeyboardInput{
		MakeCode:         uint16(scan & 0xff),
		Flags:            flags,
		ExtraInformation: uint32(interceptionMarker),
	}, nil
}

func interceptionHoldDuration(interval time.Duration) time.Duration {
	if interval <= time.Millisecond {
		return 0
	}
	hold := interval / 3
	if hold < time.Millisecond {
		return time.Millisecond
	}
	if hold > 30*time.Millisecond {
		return 30 * time.Millisecond
	}
	return hold
}
