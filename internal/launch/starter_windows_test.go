package launch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"genshintools/internal/game"
)

func TestNativeStarterHelper(t *testing.T) {
	if os.Getenv("GENSHINTOOLS_LAUNCH_HELPER") != "1" {
		return
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		os.Exit(91)
	}
	result := struct {
		WorkingDirectory string
		Arguments        []string
	}{workingDirectory, os.Args}
	data, err := json.Marshal(result)
	if err != nil || os.WriteFile(os.Getenv("GENSHINTOOLS_LAUNCH_RESULT"), data, 0o644) != nil {
		os.Exit(92)
	}
	os.Exit(0)
}

func TestNativeStarterUnicodePathWorkingDirectoryAndArguments(t *testing.T) {
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "含 Unicode 空格")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "原神 测试.exe")
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, data, 0o755); err != nil {
		t.Fatal(err)
	}
	resultPath := filepath.Join(t.TempDir(), "result.json")
	t.Setenv("GENSHINTOOLS_LAUNCH_HELPER", "1")
	t.Setenv("GENSHINTOOLS_LAUNCH_RESULT", resultPath)
	arguments := []string{"-test.run=TestNativeStarterHelper", "--", "普通", "A B", `C:\路径 带空格\file.txt`, `quote"inside`}
	process, err := (NativeStarter{}).Start(Request{Candidate: game.Candidate{Root: root, Executable: target, ExeName: filepath.Base(target)}, Arguments: arguments})
	if err != nil {
		t.Fatal(err)
	}
	code, err := process.Wait()
	if err != nil || code != 0 {
		t.Fatalf("helper exit code=%d err=%v", code, err)
	}
	encoded, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		WorkingDirectory string
		Arguments        []string
	}
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(got.WorkingDirectory) || !filepath.IsAbs(root) || !reflect.DeepEqual(filepath.Clean(got.WorkingDirectory), filepath.Clean(root)) {
		t.Fatalf("working directory = %q, want %q", got.WorkingDirectory, root)
	}
	if !reflect.DeepEqual(got.Arguments[1:], arguments) {
		t.Fatalf("arguments = %#v, want %#v", got.Arguments[1:], arguments)
	}
}
