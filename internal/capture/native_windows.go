package capture

import (
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"unsafe"

	"genshintools/internal/gamewindow"
	"genshintools/internal/platform/winfile"

	"golang.org/x/sys/windows"
)

type NativeCapturer struct{}

type nativeRect struct{ Left, Top, Right, Bottom int32 }
type bitmapInfoHeader struct {
	Size                         uint32
	Width, Height                int32
	Planes, BitCount             uint16
	Compression                  uint32
	SizeImage                    uint32
	XPelsPerMeter, YPelsPerMeter int32
	ClrUsed, ClrImportant        uint32
}
type bitmapInfo struct{ Header bitmapInfoHeader }

const (
	wmPrint              = 0x0317
	prfAll               = 0x0000003F
	smtoBlockAbortIfHung = 0x0003
	srccopy              = 0x00CC0020
	dibRGBColors         = 0
	biRGB                = 0
	maxCapturePixels     = 100_000_000
)

var (
	user32Capture                 = windows.NewLazySystemDLL("user32.dll")
	gdi32Capture                  = windows.NewLazySystemDLL("gdi32.dll")
	procGetWindowDCCapture        = user32Capture.NewProc("GetWindowDC")
	procReleaseDCCapture          = user32Capture.NewProc("ReleaseDC")
	procSendMessageTimeoutCapture = user32Capture.NewProc("SendMessageTimeoutW")
	procCreateCompatibleDCCapture = gdi32Capture.NewProc("CreateCompatibleDC")
	procCreateDIBSectionCapture   = gdi32Capture.NewProc("CreateDIBSection")
	procSelectObjectCapture       = gdi32Capture.NewProc("SelectObject")
	procDeleteObjectCapture       = gdi32Capture.NewProc("DeleteObject")
	procDeleteDCCapture           = gdi32Capture.NewProc("DeleteDC")
	procBitBltCapture             = gdi32Capture.NewProc("BitBlt")
)

func (NativeCapturer) Capture(target Target, destination string) error {
	hwnd, err := gamewindow.Find(target)
	if err != nil {
		return err
	}
	if gamewindow.IsMinimized(hwnd) {
		return errors.New("game window is minimized")
	}
	windowRectangle, err := gamewindow.Bounds(hwnd)
	if err != nil {
		return fmt.Errorf("GetWindowRect failed: %w", err)
	}
	rectangle := nativeRect(windowRectangle)
	width, height := int(rectangle.Right-rectangle.Left), int(rectangle.Bottom-rectangle.Top)
	if width <= 0 || height <= 0 || int64(width)*int64(height) > maxCapturePixels {
		return fmt.Errorf("game window capture dimensions are unsafe: %dx%d", width, height)
	}
	imageData, err := capturePixels(uintptr(hwnd), width, height)
	if err != nil {
		return err
	}
	return writePNGAtomic(destination, imageData)
}

func capturePixels(hwnd uintptr, width, height int) (*image.RGBA, error) {
	windowDC, _, callErr := procGetWindowDCCapture.Call(hwnd)
	if windowDC == 0 {
		return nil, fmt.Errorf("GetWindowDC failed: %w", callErr)
	}
	defer procReleaseDCCapture.Call(hwnd, windowDC)
	memoryDC, _, callErr := procCreateCompatibleDCCapture.Call(windowDC)
	if memoryDC == 0 {
		return nil, fmt.Errorf("CreateCompatibleDC failed: %w", callErr)
	}
	defer procDeleteDCCapture.Call(memoryDC)
	info := bitmapInfo{Header: bitmapInfoHeader{Size: uint32(unsafe.Sizeof(bitmapInfoHeader{})), Width: int32(width), Height: -int32(height), Planes: 1, BitCount: 32, Compression: biRGB}}
	var bits unsafe.Pointer
	bitmap, _, callErr := procCreateDIBSectionCapture.Call(memoryDC, uintptr(unsafe.Pointer(&info)), dibRGBColors, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bitmap == 0 || bits == nil {
		return nil, fmt.Errorf("CreateDIBSection failed: %w", callErr)
	}
	defer procDeleteObjectCapture.Call(bitmap)
	old, _, _ := procSelectObjectCapture.Call(memoryDC, bitmap)
	defer procSelectObjectCapture.Call(memoryDC, old)
	var result uintptr
	ok, _, _ := procSendMessageTimeoutCapture.Call(hwnd, wmPrint, memoryDC, prfAll, smtoBlockAbortIfHung, 1500, uintptr(unsafe.Pointer(&result)))
	copyVisible := func() error {
		copied, _, bitErr := procBitBltCapture.Call(memoryDC, 0, 0, uintptr(width), uintptr(height), windowDC, 0, 0, srccopy)
		if copied == 0 {
			return fmt.Errorf("BitBlt failed: %w", bitErr)
		}
		return nil
	}
	if ok == 0 {
		if err := copyVisible(); err != nil {
			return nil, fmt.Errorf("bounded WM_PRINT and visible-window fallback failed: %w", err)
		}
	}
	raw := unsafe.Slice((*byte)(bits), width*height*4)
	readFrame := func() (*image.RGBA, bool) {
		output := image.NewRGBA(image.Rect(0, 0, width, height))
		nonzero := false
		for index := 0; index < width*height; index++ {
			source, target := index*4, index*4
			blue, green, red := raw[source], raw[source+1], raw[source+2]
			if blue != 0 || green != 0 || red != 0 {
				nonzero = true
			}
			output.Pix[target], output.Pix[target+1], output.Pix[target+2], output.Pix[target+3] = red, green, blue, 0xFF
		}
		return output, nonzero
	}
	output, nonzero := readFrame()
	if !nonzero && ok != 0 {
		if err := copyVisible(); err != nil {
			return nil, fmt.Errorf("WM_PRINT returned an empty frame and visible-window fallback failed: %w", err)
		}
		output, nonzero = readFrame()
	}
	if !nonzero {
		return nil, errors.New("capture returned an empty frame")
	}
	return output, nil
}

func writePNGAtomic(destination string, frame image.Image) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".screenshot-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := png.Encode(temporary, frame); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := winfile.Replace(windows.StringToUTF16Ptr(temporaryPath), windows.StringToUTF16Ptr(destination), windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return err
	}
	committed = true
	return nil
}
