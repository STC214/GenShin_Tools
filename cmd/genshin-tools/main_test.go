package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"genshintools/internal/buildinfo"
)

func TestRunVersion(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if code := run([]string{"--version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run returned %d; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), buildinfo.Current().Version) {
		t.Fatalf("version output %q does not contain %q", stdout.String(), buildinfo.Current().Version)
	}
}

func TestRunVersionJSON(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if code := run([]string{"--version-json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run returned %d; stderr=%q", code, stderr.String())
	}

	var got buildinfo.Info
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode JSON output: %v", err)
	}
	if got.Version == "" || got.GoVersion == "" || got.Platform == "" {
		t.Fatalf("incomplete version output: %+v", got)
	}
}

func TestRunRejectsUnknownArgument(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	if code := run([]string{"--not-a-real-option"}, &stdout, &stderr); code != 2 {
		t.Fatalf("run returned %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unknown argument") {
		t.Fatalf("stderr %q does not explain the error", stderr.String())
	}
}
