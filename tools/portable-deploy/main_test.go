package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestMirrorReleasePreservesDataAndRemovesRetiredFiles(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "staged")
	target := filepath.Join(root, "installed")
	mustWrite(t, filepath.Join(source, "GenshinTools.exe"), "new")
	mustWrite(t, filepath.Join(source, "LICENSES", "current.txt"), "current")
	mustWrite(t, filepath.Join(target, "GenshinTools.exe"), "old")
	mustWrite(t, filepath.Join(target, "retired.exe"), "retired")
	mustWrite(t, filepath.Join(target, "LICENSES", "retired.txt"), "retired")
	mustWrite(t, filepath.Join(target, "data", "config.json"), "user")

	if err := mirrorRelease(source, target); err != nil {
		t.Fatal(err)
	}
	assertContent(t, filepath.Join(target, "GenshinTools.exe"), "new")
	assertContent(t, filepath.Join(target, "LICENSES", "current.txt"), "current")
	assertContent(t, filepath.Join(target, "data", "config.json"), "user")
	for _, path := range []string{
		filepath.Join(target, "retired.exe"),
		filepath.Join(target, "LICENSES", "retired.txt"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("retired path still exists: %s", path)
		}
	}
}

func TestMirrorReleaseRejectsPackagedData(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "staged")
	target := filepath.Join(root, "installed")
	mustWrite(t, filepath.Join(source, "data", "config.json"), "packaged")
	mustWrite(t, filepath.Join(target, "data", "config.json"), "user")
	if err := mirrorRelease(source, target); err == nil {
		t.Fatal("expected packaged data rejection")
	}
	assertContent(t, filepath.Join(target, "data", "config.json"), "user")
}

func TestMirrorReleaseRollsBackPartialInstall(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "staged")
	target := filepath.Join(root, "installed")
	mustWrite(t, filepath.Join(source, "a.exe"), "new-a")
	mustWrite(t, filepath.Join(source, "b.exe"), "new-b")
	mustWrite(t, filepath.Join(target, "a.exe"), "old-a")
	mustWrite(t, filepath.Join(target, "retired.exe"), "old-retired")
	mustWrite(t, filepath.Join(target, "data", "config.json"), "user")

	originalMove := movePath
	t.Cleanup(func() { movePath = originalMove })
	movePath = func(oldPath, newPath string) error {
		if filepath.Base(oldPath) == "b.exe" && filepath.Dir(oldPath) == source {
			return errors.New("injected install failure")
		}
		return originalMove(oldPath, newPath)
	}
	if err := mirrorRelease(source, target); err == nil {
		t.Fatal("expected injected install failure")
	}
	assertContent(t, filepath.Join(target, "a.exe"), "old-a")
	assertContent(t, filepath.Join(target, "retired.exe"), "old-retired")
	assertContent(t, filepath.Join(target, "data", "config.json"), "user")
}

func mustWrite(t *testing.T, path, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("%s = %q, want %q", path, data, want)
	}
}
