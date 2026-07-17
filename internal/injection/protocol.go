package injection

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"genshintools/internal/game"

	"golang.org/x/sys/windows"
)

const ProtocolVersion = 2

type HelperRequest struct {
	ProtocolVersion int            `json:"protocolVersion"`
	RequestID       string         `json:"requestId"`
	ModulesRoot     string         `json:"modulesRoot"`
	ModuleIDs       []string       `json:"moduleIds"`
	Candidate       game.Candidate `json:"candidate"`
	Arguments       []string       `json:"arguments"`
	RemoteTimeoutMS int            `json:"remoteTimeoutMs"`
}

type HelperResult struct {
	ProtocolVersion int    `json:"protocolVersion"`
	RequestID       string `json:"requestId"`
	Success         bool   `json:"success"`
	PID             int    `json:"pid,omitempty"`
	Code            string `json:"code"`
	Error           string `json:"error,omitempty"`
	CompletedUTC    string `json:"completedUtc"`
}

func LoadHelperRequest(path string) (HelperRequest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return HelperRequest{}, err
	}
	if len(data) > 1<<20 {
		return HelperRequest{}, errors.New("helper request exceeds 1 MiB")
	}
	var request HelperRequest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return HelperRequest{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return HelperRequest{}, errors.New("helper request contains trailing data")
	}
	if request.ProtocolVersion != ProtocolVersion || !moduleIDPattern.MatchString(request.RequestID) || len(request.ModuleIDs) == 0 || len(request.ModuleIDs) > 32 {
		return HelperRequest{}, errors.New("unsupported helper protocol or invalid request/module count")
	}
	seenModules := map[string]bool{}
	for _, id := range request.ModuleIDs {
		if !moduleIDPattern.MatchString(id) || seenModules[id] {
			return HelperRequest{}, errors.New("helper module ids must be valid and unique")
		}
		seenModules[id] = true
	}
	if request.RemoteTimeoutMS < 1000 || request.RemoteTimeoutMS > 30_000 {
		return HelperRequest{}, errors.New("remote timeout must be within 1000..30000 ms")
	}
	if len(request.Arguments) > 256 {
		return HelperRequest{}, errors.New("too many game arguments")
	}
	for _, argument := range request.Arguments {
		if len(argument) > 8192 {
			return HelperRequest{}, errors.New("game argument exceeds 8192 bytes")
		}
	}
	return request, nil
}

func ExecuteHelper(request HelperRequest) HelperResult {
	result := HelperResult{ProtocolVersion: ProtocolVersion, RequestID: request.RequestID, Code: "preflight_failed", CompletedUTC: time.Now().UTC().Format(time.RFC3339Nano)}
	verified, err := game.InspectRoot(request.Candidate.Root, request.Candidate.ExeName)
	if err != nil {
		result.Error = "re-inspect game candidate: " + err.Error()
		return result
	}
	if !equalPath(verified.Executable, request.Candidate.Executable) || verified.ExeName != request.Candidate.ExeName || verified.Version != request.Candidate.Version || verified.Server != request.Candidate.Server {
		result.Error = "helper game inspection does not match the requested candidate"
		return result
	}
	dllPaths := make([]string, 0, len(request.ModuleIDs))
	locks := make([]windows.Handle, 0, len(request.ModuleIDs))
	defer func() {
		for _, handle := range locks {
			windows.CloseHandle(handle)
		}
	}()
	for _, moduleID := range request.ModuleIDs {
		audit, err := AuditModule(request.ModulesRoot, moduleID, verified)
		if err != nil {
			result.Error = moduleID + ": " + err.Error()
			return result
		}
		moduleLock, err := lockFileReadOnly(audit.DLLPath)
		if err != nil {
			result.Error = moduleID + ": lock audited module: " + err.Error()
			return result
		}
		locks = append(locks, moduleLock)
		lockedHash, err := fileSHA256(audit.DLLPath)
		if err != nil || !strings.EqualFold(lockedHash, audit.SHA256) {
			result.Error = moduleID + ": module changed after audit"
			return result
		}
		dllPaths = append(dllPaths, audit.DLLPath)
	}
	pid, err := launchSuspendedAndInject(request.Candidate.Executable, request.Candidate.Root, request.Arguments, dllPaths, time.Duration(request.RemoteTimeoutMS)*time.Millisecond)
	if err != nil {
		result.Code, result.Error = "injection_failed", err.Error()
		return result
	}
	result.Success, result.PID, result.Code = true, pid, "ok"
	return result
}

func ValidateHelperRequestScope(requestPath, helperPath string, request *HelperRequest) error {
	helper, err := filepath.Abs(helperPath)
	if err != nil {
		return err
	}
	root := filepath.Dir(helper)
	wantStaging := filepath.Join(root, "data", "staging", "injection")
	path, err := filepath.Abs(requestPath)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(wantStaging, path)
	if err != nil {
		return err
	}
	parts := strings.Split(filepath.Clean(relative), string(filepath.Separator))
	if len(parts) != 2 || !moduleIDPattern.MatchString(parts[0]) || parts[1] != "request.json" {
		return errors.New("helper request is outside the portable injection staging layout")
	}
	if err := rejectReparse(filepath.Dir(path)); err != nil {
		return fmt.Errorf("request directory: %w", err)
	}
	if err := rejectReparse(path); err != nil {
		return fmt.Errorf("request file: %w", err)
	}
	if err := rejectReparseTree(root, path); err != nil {
		return fmt.Errorf("portable request path: %w", err)
	}
	if request != nil {
		if request.RequestID != parts[0] {
			return errors.New("request id does not match its staging directory")
		}
		wantModules := filepath.Join(root, "data", "injection", "modules")
		modules, err := filepath.Abs(request.ModulesRoot)
		if err != nil || !equalPath(modules, wantModules) {
			return errors.New("module root is outside the helper portable layout")
		}
		if err := rejectReparseTree(root, wantModules); err != nil {
			return fmt.Errorf("portable module path: %w", err)
		}
	}
	return nil
}

func WriteHelperResult(requestPath string, result HelperResult) error {
	directory := filepath.Dir(requestPath)
	path := filepath.Join(directory, "result.json")
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temporary, err := os.CreateTemp(directory, ".result-*.tmp")
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
	if _, err := temporary.Write(data); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("commit helper result: %w", err)
	}
	committed = true
	return nil
}
