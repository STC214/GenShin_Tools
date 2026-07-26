//go:build windows && !386

package main

import (
	"fmt"
	"time"
)

type keybdDebugger struct{}

func startKeybdDebugger(int, time.Time) (*keybdDebugger, error) {
	return nil, fmt.Errorf("keybd_event debugger requires the x86 diagnostic build")
}

func (*keybdDebugger) Stop() ([]apiCallRecord, error) {
	return nil, nil
}
