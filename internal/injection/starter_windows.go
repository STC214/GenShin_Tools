package injection

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"genshintools/internal/launch"

	"golang.org/x/sys/windows"
)

type Starter struct {
	Context     context.Context
	HelperPath  string
	ModulesRoot string
	StagingRoot string
	Config      Config
	ModuleIDs   []string
}

var procCompareStringOrdinal = windows.NewLazySystemDLL("kernel32.dll").NewProc("CompareStringOrdinal")

func (starter Starter) Start(request launch.Request) (launch.Process, error) {
	config, err := starter.Config.Normalized()
	if err != nil {
		return nil, err
	}
	moduleIDs, err := starter.normalizedModuleIDs(config.ModuleID)
	if err != nil {
		return nil, err
	}
	if !config.Enabled || !config.RiskAcknowledged || len(moduleIDs) == 0 {
		return nil, errors.New("injection requires enabled state, risk acknowledgement and a selected module")
	}
	if err := starter.validateLayout(); err != nil {
		return nil, err
	}
	for _, moduleID := range moduleIDs {
		if _, err := AuditModule(starter.ModulesRoot, moduleID, request.Candidate); err != nil {
			return nil, fmt.Errorf("main-process injection preflight %s: %w", moduleID, err)
		}
	}
	helperInfo, err := inspectPE(starter.HelperPath)
	if err != nil {
		return nil, fmt.Errorf("validate injection helper PE: %w", err)
	}
	if helperInfo.IsDLL || helperInfo.Architecture != "amd64" {
		return nil, fmt.Errorf("validate injection helper PE: architecture=%s dll=%t, want amd64 executable", helperInfo.Architecture, helperInfo.IsDLL)
	}
	requestID, err := randomID()
	if err != nil {
		return nil, err
	}
	directory := filepath.Join(starter.StagingRoot, "injection", requestID)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	requestPath := filepath.Join(directory, "request.json")
	helperRequest := HelperRequest{ProtocolVersion: ProtocolVersion, RequestID: requestID, ModulesRoot: starter.ModulesRoot, ModuleIDs: moduleIDs, Candidate: request.Candidate, Arguments: append([]string(nil), request.Arguments...), RemoteTimeoutMS: config.RemoteTimeoutMS}
	if err := writeRequest(requestPath, helperRequest); err != nil {
		return nil, err
	}
	ctx := starter.Context
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(config.HelperTimeoutMS)*time.Millisecond)
	defer cancel()
	var output bytes.Buffer
	var runErr error
	if config.ElevatedHelper {
		runErr = runElevatedHelper(ctx, starter.HelperPath, requestPath)
	} else {
		command := exec.CommandContext(ctx, starter.HelperPath, "--request", requestPath)
		command.Dir = filepath.Dir(starter.HelperPath)
		command.Stdout, command.Stderr = &output, &output
		runErr = command.Run()
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("injection helper timeout/cancel: %w", ctx.Err())
	}
	result, resultErr := loadResult(filepath.Join(directory, "result.json"), requestID)
	if resultErr != nil {
		return nil, fmt.Errorf("injection helper result: %w; process=%v; output=%s", resultErr, runErr, boundedOutput(output.String()))
	}
	if runErr != nil || !result.Success || result.PID <= 0 {
		return nil, fmt.Errorf("injection helper failed (%s): %s; process=%v; output=%s", result.Code, result.Error, runErr, boundedOutput(output.String()))
	}
	process, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(result.PID))
	if err != nil {
		return nil, fmt.Errorf("open injected game PID %d: %w", result.PID, err)
	}
	if err := validateProcessPath(process, request.Candidate.Executable); err != nil {
		windows.CloseHandle(process)
		return nil, err
	}
	return &injectedProcess{pid: result.PID, handle: process}, nil
}

type injectedProcess struct {
	pid    int
	handle windows.Handle
}

func (process *injectedProcess) PID() int { return process.pid }

func (process *injectedProcess) Wait() (int, error) {
	defer windows.CloseHandle(process.handle)
	status, err := windows.WaitForSingleObject(process.handle, windows.INFINITE)
	if err != nil || status != waitObject0 {
		return -1, fmt.Errorf("wait injected game status=0x%X: %w", status, err)
	}
	var exitCode uint32
	if err := windows.GetExitCodeProcess(process.handle, &exitCode); err != nil {
		return -1, err
	}
	return int(exitCode), nil
}

func randomID() (string, error) {
	buffer := make([]byte, 12)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

func (starter Starter) validateLayout() error {
	helper, err := filepath.Abs(starter.HelperPath)
	if err != nil || !strings.EqualFold(filepath.Base(helper), "GenshinTools-injector.exe") {
		return errors.New("injection helper must be the portable GenshinTools-injector.exe")
	}
	root := filepath.Dir(helper)
	modules, modulesErr := filepath.Abs(starter.ModulesRoot)
	staging, stagingErr := filepath.Abs(starter.StagingRoot)
	if modulesErr != nil || stagingErr != nil || !equalPath(modules, filepath.Join(root, "data", "injection", "modules")) || !equalPath(staging, filepath.Join(root, "data", "staging")) {
		return errors.New("injection modules or staging path is outside the portable helper layout")
	}
	if err := rejectReparseTree(root, helper); err != nil {
		return fmt.Errorf("portable helper path: %w", err)
	}
	if err := rejectReparseTree(root, modules); err != nil {
		return fmt.Errorf("portable module path: %w", err)
	}
	if err := rejectReparseTree(root, staging); err != nil {
		return fmt.Errorf("portable staging path: %w", err)
	}
	return nil
}

func (starter Starter) normalizedModuleIDs(fallback string) ([]string, error) {
	values := starter.ModuleIDs
	if len(values) == 0 && fallback != "" {
		values = []string{fallback}
	}
	if len(values) > 32 {
		return nil, errors.New("at most 32 injection modules may be loaded")
	}
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, id := range values {
		id = strings.TrimSpace(id)
		if !moduleIDPattern.MatchString(id) || seen[id] {
			return nil, errors.New("injection module ids must be valid and unique")
		}
		seen[id] = true
		result = append(result, id)
	}
	return result, nil
}

func writeRequest(path string, request HelperRequest) error {
	data, err := json.MarshalIndent(request, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func loadResult(path, requestID string) (HelperResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return HelperResult{}, err
	}
	if len(data) > 1<<20 {
		return HelperResult{}, errors.New("result exceeds 1 MiB")
	}
	var result HelperResult
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return HelperResult{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return HelperResult{}, errors.New("result contains trailing JSON data")
	}
	if result.ProtocolVersion != ProtocolVersion || result.RequestID != requestID {
		return HelperResult{}, errors.New("result protocol or request id mismatch")
	}
	return result, nil
}

func validateProcessPath(process windows.Handle, expected string) error {
	buffer := make([]uint16, 32768)
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(process, 0, &buffer[0], &size); err != nil {
		return err
	}
	actual := filepath.Clean(windows.UTF16ToString(buffer[:size]))
	want, err := filepath.Abs(expected)
	if err != nil {
		return err
	}
	if !equalPath(actual, filepath.Clean(want)) {
		return fmt.Errorf("helper PID path is %q, want %q", actual, want)
	}
	return nil
}

func equalPath(left, right string) bool {
	leftPointer, leftErr := windows.UTF16PtrFromString(left)
	rightPointer, rightErr := windows.UTF16PtrFromString(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	result, _, _ := procCompareStringOrdinal.Call(uintptr(unsafe.Pointer(leftPointer)), ^uintptr(0), uintptr(unsafe.Pointer(rightPointer)), ^uintptr(0), 1)
	return result == 2
}

func boundedOutput(value string) string {
	if len(value) > 4096 {
		return value[:4096] + "..."
	}
	return value
}
