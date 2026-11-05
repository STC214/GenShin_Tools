package injection

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVerifyModuleSignature(t *testing.T) {
	signed := filepath.Join(os.Getenv("SystemRoot"), "System32", "ntdll.dll")
	if err := VerifyModuleSignature(signed); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(signed)
	if err != nil {
		t.Fatal(err)
	}
	data[0x40] ^= 1 // Alter the signed DOS stub without destroying the PE layout.
	tampered := filepath.Join(t.TempDir(), "tampered.exe")
	if err := os.WriteFile(tampered, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := VerifyModuleSignature(tampered); err == nil {
		t.Fatal("tampered signed PE accepted")
	}
	unsigned, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyModuleSignature(unsigned); err == nil {
		t.Fatal("unsigned test executable accepted")
	}
	if err := VerifyModuleSignature(filepath.Join(t.TempDir(), "missing.dll")); err == nil {
		t.Fatal("missing signature target accepted")
	}
}
