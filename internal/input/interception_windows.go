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

	interceptionKeyUp          = uint16(0x01)
	interceptionKeyE0          = uint16(0x02)
	interceptionKeyE1          = uint16(0x04)
	interceptionSetFilterIOCTL = uint32((0x22 << 16) | (0x804 << 2))
	interceptionSetEventIOCTL  = uint32((0x22 << 16) | (0x810 << 2))
	interceptionWriteIOCTL     = uint32((0x22 << 16) | (0x820 << 2))
	interceptionReadIOCTL      = uint32((0x22 << 16) | (0x840 << 2))
	interceptionMarker         = uintptr(0x51485844)
	interceptionDeviceCount    = 20
	interceptionKeyboardCount  = 10
	interceptionFilterKeyAll   = uint16(0xffff)
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
	return backend.PressDevice(1, key, interval)
}

func (backend *interceptionKeyboardBackend) PressDevice(device uint32, key uint32, interval time.Duration) error {
	if backend == nil {
		return errors.New("Interception keyboard backend is unavailable")
	}
	if device < 1 || device > interceptionKeyboardCount {
		return fmt.Errorf("Interception keyboard device %d is invalid", device)
	}
	index := int(device - 1)
	down, err := interceptionKeyboardData(key, false)
	if err != nil {
		return err
	}
	up, err := interceptionKeyboardData(key, true)
	if err != nil {
		return err
	}

	backend.mu.Lock()
	if backend.devices[index] == 0 || backend.devices[index] == windows.InvalidHandle {
		backend.mu.Unlock()
		return errors.New("Interception keyboard backend is closed")
	}
	if err := backend.writeDeviceLocked(index, &down); err != nil {
		backend.mu.Unlock()
		return err
	}
	backend.mu.Unlock()
	if hold := interceptionHoldDuration(interval); hold > 0 {
		time.Sleep(hold)
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if err := backend.writeDeviceLocked(index, &up); err != nil {
		// A down edge may already be inside the device stack. Retry the release
		// once before surfacing the fault so a transient error is less likely
		// to leave a key logically held.
		_ = backend.writeDeviceLocked(index, &up)
		return err
	}
	return nil
}

func (backend *interceptionKeyboardBackend) writeDeviceLocked(index int, data *interceptionKeyboardInput) error {
	if index < 0 || index >= interceptionKeyboardCount ||
		backend.devices[index] == 0 || backend.devices[index] == windows.InvalidHandle {
		return errors.New("Interception keyboard device is unavailable")
	}
	var written uint32
	err := windows.DeviceIoControl(
		backend.devices[index],
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

// CaptureKeyboard makes the driver's pre-user-mode keyboard strokes the sole
// physical truth for repeat held state. Every captured stroke is written back
// to the same logical device before it is observed by the worker, so unrelated
// keys and the user's original trigger edges remain transparent to Windows.
func (backend *interceptionKeyboardBackend) CaptureKeyboard(done <-chan struct{}, observe func(uint32, interceptionKeyboardInput)) error {
	if backend == nil {
		return errors.New("Interception keyboard backend is unavailable")
	}
	if err := backend.setKeyboardFilter(interceptionFilterKeyAll); err != nil {
		return err
	}
	defer func() { _ = backend.setKeyboardFilter(0) }()

	handles := make([]windows.Handle, interceptionKeyboardCount)
	backend.mu.Lock()
	for index := range interceptionKeyboardCount {
		handles[index] = backend.events[index]
	}
	backend.mu.Unlock()

	for {
		select {
		case <-done:
			return nil
		default:
		}
		wait, err := windows.WaitForMultipleObjects(handles, false, 50)
		if err != nil {
			return fmt.Errorf("wait for Interception keyboard stroke: %w", err)
		}
		if wait == uint32(windows.WAIT_TIMEOUT) {
			continue
		}
		index := int(wait - uint32(windows.WAIT_OBJECT_0))
		if index < 0 || index >= interceptionKeyboardCount {
			return fmt.Errorf("Interception keyboard wait returned unexpected index %#x", wait)
		}

		backend.mu.Lock()
		var data interceptionKeyboardInput
		var read uint32
		err = windows.DeviceIoControl(
			backend.devices[index],
			interceptionReadIOCTL,
			nil,
			0,
			(*byte)(unsafe.Pointer(&data)),
			uint32(unsafe.Sizeof(data)),
			&read,
			nil,
		)
		if err == nil && read == uint32(unsafe.Sizeof(data)) {
			err = backend.writeDeviceLocked(index, &data)
		}
		backend.mu.Unlock()
		if err != nil {
			return fmt.Errorf("receive/forward Interception keyboard device %d: %w", index+1, err)
		}
		if read == 0 {
			continue
		}
		if read != uint32(unsafe.Sizeof(data)) {
			return fmt.Errorf("Interception keyboard read returned %d of %d bytes", read, unsafe.Sizeof(data))
		}
		if observe != nil {
			observe(uint32(index+1), data)
		}
	}
}

func (backend *interceptionKeyboardBackend) setKeyboardFilter(filter uint16) error {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	for index := range interceptionKeyboardCount {
		if backend.devices[index] == 0 || backend.devices[index] == windows.InvalidHandle {
			return fmt.Errorf("Interception keyboard device %d is unavailable", index+1)
		}
		var returned uint32
		value := filter
		if err := windows.DeviceIoControl(
			backend.devices[index],
			interceptionSetFilterIOCTL,
			(*byte)(unsafe.Pointer(&value)),
			uint32(unsafe.Sizeof(value)),
			nil,
			0,
			&returned,
			nil,
		); err != nil {
			if filter != 0 {
				// A partially installed capture would block some keyboards
				// without a receiver. Roll back every device already changed
				// before returning the initialization failure.
				for rollback := 0; rollback < index; rollback++ {
					none := uint16(0)
					var ignored uint32
					_ = windows.DeviceIoControl(
						backend.devices[rollback],
						interceptionSetFilterIOCTL,
						(*byte)(unsafe.Pointer(&none)),
						uint32(unsafe.Sizeof(none)),
						nil,
						0,
						&ignored,
						nil,
					)
				}
			}
			return fmt.Errorf("set Interception keyboard device %d filter 0x%X: %w", index+1, filter, err)
		}
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
