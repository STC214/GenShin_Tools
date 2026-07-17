package input

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

type IntegrityReport struct {
	SelfRID    uint32
	SelfName   string
	TargetPID  uint32
	TargetRID  uint32
	TargetName string
	Blocked    bool
	Error      string
}

func checkForegroundIntegrity() IntegrityReport {
	self, err := currentIntegrityLevel()
	if err != nil {
		report := IntegrityReport{}
		report.Error = err.Error()
		return report
	}
	return checkForegroundIntegrityFromSelf(self)
}

func checkForegroundIntegrityFromSelf(self uint32) IntegrityReport {
	report := IntegrityReport{}
	report.SelfRID, report.SelfName = self, integrityName(self)
	hwnd := windows.GetForegroundWindow()
	if hwnd == 0 {
		return report
	}
	var pid uint32
	if _, err := windows.GetWindowThreadProcessId(hwnd, &pid); err != nil {
		report.Error = fmt.Sprintf("GetWindowThreadProcessId: %v", err)
		return report
	}
	report.TargetPID = pid
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		report.Error = fmt.Sprintf("OpenProcess(%d): %v", pid, err)
		return report
	}
	defer windows.CloseHandle(process)
	target, err := processIntegrityLevel(process)
	if err != nil {
		report.Error = err.Error()
		return report
	}
	report.TargetRID, report.TargetName = target, integrityName(target)
	report.Blocked = target > self
	return report
}

func currentIntegrityLevel() (uint32, error) {
	return processIntegrityLevel(windows.CurrentProcess())
}

func processIntegrityLevel(process windows.Handle) (uint32, error) {
	var token windows.Token
	if err := windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token); err != nil {
		return 0, fmt.Errorf("OpenProcessToken: %w", err)
	}
	defer token.Close()
	var size uint32
	err := windows.GetTokenInformation(token, windows.TokenIntegrityLevel, nil, 0, &size)
	if err != nil && err != windows.ERROR_INSUFFICIENT_BUFFER {
		return 0, fmt.Errorf("GetTokenInformation(size): %w", err)
	}
	if size < uint32(unsafe.Sizeof(windows.Tokenmandatorylabel{})) {
		return 0, fmt.Errorf("GetTokenInformation returned invalid size %d", size)
	}
	buffer := make([]byte, size)
	if err := windows.GetTokenInformation(token, windows.TokenIntegrityLevel, &buffer[0], size, &size); err != nil {
		return 0, fmt.Errorf("GetTokenInformation(data): %w", err)
	}
	label := (*windows.Tokenmandatorylabel)(unsafe.Pointer(&buffer[0]))
	if label.Label.Sid == nil {
		return 0, fmt.Errorf("integrity SID has no sub-authority")
	}
	// Read the self-relative SID directly from the caller-owned token buffer.
	// Calling the Win32 GetSidSubAuthority helpers through x/sys trips Go's
	// checkptr instrumentation because the SID pointer is written by the kernel.
	type sidHeader struct {
		Revision            byte
		SubAuthorityCount   byte
		IdentifierAuthority [6]byte
		FirstSubAuthority   uint32
	}
	header := (*sidHeader)(unsafe.Pointer(label.Label.Sid))
	if header.SubAuthorityCount == 0 {
		return 0, fmt.Errorf("integrity SID has no sub-authority")
	}
	subAuthorities := unsafe.Slice(&header.FirstSubAuthority, int(header.SubAuthorityCount))
	return subAuthorities[len(subAuthorities)-1], nil
}

func integrityName(rid uint32) string {
	switch {
	case rid < 0x1000:
		return "untrusted"
	case rid < 0x2000:
		return "low"
	case rid < 0x3000:
		return "medium"
	case rid < 0x4000:
		return "high"
	case rid < 0x5000:
		return "system"
	default:
		return "protected"
	}
}
