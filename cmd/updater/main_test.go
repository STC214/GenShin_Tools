package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"genshintools/internal/selfupdate"
)

func TestRunLoadsFixedRequestAndExecutes(t *testing.T) {
	root := t.TempDir()
	helper := filepath.Join(root, "data", "updates", "runner", "GenshinTools-updater.exe")
	requestPath := filepath.Join(root, "data", "updates", "update-request.json")
	request := selfupdate.UpdaterRequest{
		ProtocolVersion: selfupdate.UpdaterProtocolVersion,
		Version:         "1.2.3", ManifestSHA256: strings.Repeat("a", 64),
		Parent:        selfupdate.ProcessIdentity{PID: 10, CreationTime: 20},
		WaitTimeoutMS: 5_000, Restart: true,
	}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string][]byte{helper: []byte("helper"), requestPath: data} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	called := false
	code := run([]string{"--request", requestPath}, &bytes.Buffer{}, helper, func(_ context.Context, layout selfupdate.UpdateLayout, got selfupdate.UpdaterRequest, _ *selfupdate.UpdaterHooks) error {
		called = strings.EqualFold(layout.InstallRoot, root) && got == request
		return nil
	})
	if code != 0 || !called {
		t.Fatalf("run code=%d called=%t", code, called)
	}
	if _, err := os.Lstat(requestPath); !os.IsNotExist(err) {
		t.Fatal("successful updater did not remove its request")
	}
}

func TestRunRejectsUnexpectedArguments(t *testing.T) {
	var stderr bytes.Buffer
	if code := run([]string{"--install-root", `C:\unsafe`}, &stderr, "ignored", nil); code != 2 {
		t.Fatalf("run code=%d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "usage:") {
		t.Fatalf("stderr=%q", stderr.String())
	}
}
