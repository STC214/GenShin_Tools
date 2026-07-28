//go:build windows

package input

import (
	"testing"
	"time"
	"unsafe"
)

func TestInterceptionKeyboardProtocolABI(t *testing.T) {
	if got := unsafe.Sizeof(interceptionKeyboardInput{}); got != 12 {
		t.Fatalf("KEYBOARD_INPUT_DATA size = %d, want 12", got)
	}
	if interceptionSetFilterIOCTL != 0x222010 {
		t.Fatalf("Interception IOCTL_SET_FILTER = 0x%X, want 0x222010", interceptionSetFilterIOCTL)
	}
	if interceptionWriteIOCTL != 0x222080 {
		t.Fatalf("Interception IOCTL_WRITE = 0x%X, want 0x222080", interceptionWriteIOCTL)
	}
	if interceptionReadIOCTL != 0x222100 {
		t.Fatalf("Interception IOCTL_READ = 0x%X, want 0x222100", interceptionReadIOCTL)
	}
	if interceptionSetEventIOCTL != 0x222040 {
		t.Fatalf("Interception IOCTL_SET_EVENT = 0x%X, want 0x222040", interceptionSetEventIOCTL)
	}
	if interceptionMarker != 0x51485844 {
		t.Fatalf("Interception recursion marker = 0x%X", interceptionMarker)
	}
}

func TestInterceptionProbeNeverClaimsMediumIntegrityIsAccessible(t *testing.T) {
	level, err := currentIntegrityLevel()
	if err != nil {
		t.Fatal(err)
	}
	status := ProbeInterceptionDriver()
	if level < 0x3000 && status.Accessible {
		t.Fatalf("driver probe reported accessible at %s integrity: %+v", integrityName(level), status)
	}
}

func TestInterceptionHoldDuration(t *testing.T) {
	tests := []struct {
		interval time.Duration
		want     time.Duration
	}{
		{time.Millisecond, 0},
		{2 * time.Millisecond, time.Millisecond},
		{9 * time.Millisecond, 3 * time.Millisecond},
		{300 * time.Millisecond, 30 * time.Millisecond},
	}
	for _, test := range tests {
		if got := interceptionHoldDuration(test.interval); got != test.want {
			t.Fatalf("interceptionHoldDuration(%v) = %v, want %v", test.interval, got, test.want)
		}
	}
}

func TestInterceptionKeyboardDataCarriesScanCodeStateAndMarker(t *testing.T) {
	down, err := interceptionKeyboardData(EncodeKeyCode('F', false), false)
	if err != nil {
		t.Fatal(err)
	}
	up, err := interceptionKeyboardData(EncodeKeyCode('F', false), true)
	if err != nil {
		t.Fatal(err)
	}
	if down.MakeCode == 0 || down.Flags != 0 {
		t.Fatalf("F down = %+v", down)
	}
	if up.MakeCode != down.MakeCode || up.Flags != interceptionKeyUp {
		t.Fatalf("F up = %+v, down = %+v", up, down)
	}
	if down.ExtraInformation != uint32(interceptionMarker) || up.ExtraInformation != uint32(interceptionMarker) {
		t.Fatalf("Interception marker missing: down=%+v up=%+v", down, up)
	}
}
