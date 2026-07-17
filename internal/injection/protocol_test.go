package injection

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestValidateHelperRequestScope(t *testing.T) {
	root := t.TempDir()
	helper := filepath.Join(root, "GenshinTools-injector.exe")
	requestID := "0123456789abcdef"
	directory := filepath.Join(root, "data", "staging", "injection", requestID)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "data", "injection", "modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	requestPath := filepath.Join(directory, "request.json")
	if err := os.WriteFile(requestPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	request := HelperRequest{RequestID: requestID, ModulesRoot: filepath.Join(root, "data", "injection", "modules")}
	if err := ValidateHelperRequestScope(requestPath, helper, &request); err != nil {
		t.Fatal(err)
	}
	request.ModulesRoot = filepath.Join(root, "other-modules")
	if err := ValidateHelperRequestScope(requestPath, helper, &request); err == nil {
		t.Fatal("out-of-layout module root was accepted")
	}
	outside := filepath.Join(root, "request.json")
	if err := os.WriteFile(outside, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateHelperRequestScope(outside, helper, nil); err == nil {
		t.Fatal("out-of-layout request was accepted")
	}
}

func TestExecuteHelperRepeatsAuditAndInjectsOwnedFixture(t *testing.T) {
	if os.Getenv("GENSHINTOOLS_S09_CHILD") == "1" {
		return
	}
	fixture := newModuleFixture(t)
	t.Setenv("GENSHINTOOLS_S09_CHILD", "1")
	request := HelperRequest{ProtocolVersion: ProtocolVersion, RequestID: "abcdef0123456789", ModulesRoot: fixture.root, ModuleIDs: []string{"fixture"}, Candidate: fixture.candidate, Arguments: []string{"-test.run=^TestInjectionFixtureChild$"}, RemoteTimeoutMS: 5000}
	result := ExecuteHelper(request)
	if !result.Success || result.PID <= 0 {
		t.Fatalf("helper result = %+v", result)
	}
	process, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(result.PID))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(process)
	status, err := windows.WaitForSingleObject(process, 10_000)
	if err != nil || status != waitObject0 {
		t.Fatalf("fixture child wait status=0x%X err=%v", status, err)
	}
}
