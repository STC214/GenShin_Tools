package localenhance

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	sndAsync     = 0x0001
	sndNoDefault = 0x0002
	sndFileName  = 0x00020000
)

var playSoundW = windows.NewLazySystemDLL("winmm.dll").NewProc("PlaySoundW")

func PlayStartupSound(path string) error {
	if !strings.EqualFold(filepath.Ext(path), ".wav") {
		return errors.New("startup sound must be a WAV file")
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() || info.Size() <= 0 || info.Size() > 32<<20 {
		return errors.New("startup WAV size must be within 1 byte..32 MiB")
	}
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	result, _, callErr := playSoundW.Call(uintptr(unsafe.Pointer(pointer)), 0, sndAsync|sndNoDefault|sndFileName)
	if result == 0 {
		return fmt.Errorf("PlaySoundW failed: %w", callErr)
	}
	return nil
}

func StopStartupSound() {
	playSoundW.Call(0, 0, 0)
}
