package resources

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestParseManifestStrictAndSafe(t *testing.T) {
	digest := strings.Repeat("a", 64)
	valid := fmt.Sprintf(`{"schema_version":1,"version":"5.0","kind":"game","files":[{"path":"GenshinImpact_Data/data.bin","size":12,"hash":{"algorithm":"sha256","digest":"%s"},"url":"https://example.invalid/data.bin"}]}`, digest)
	manifest, err := ParseManifest(strings.NewReader(valid))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Files[0].Path != `GenshinImpact_Data\data.bin` {
		t.Fatalf("normalized path = %q", manifest.Files[0].Path)
	}

	invalid := []string{
		strings.Replace(valid, `"kind":"game",`, `"kind":"game","new_api_field":true,`, 1),
		strings.Replace(valid, `GenshinImpact_Data/data.bin`, `../outside.bin`, 1),
		strings.Replace(valid, `GenshinImpact_Data/data.bin`, `CON.txt`, 1),
		strings.Replace(valid, `"files":[{`, `"files":[],"ignored":[{`, 1),
		strings.Replace(valid, `"schema_version":1`, `"schema_version":2`, 1),
	}
	for i, document := range invalid {
		if _, err := ParseManifest(strings.NewReader(document)); err == nil {
			t.Fatalf("invalid manifest %d was accepted", i)
		}
	}
}

func TestNormalizeRelativePathRejectsWindowsEscapes(t *testing.T) {
	for _, value := range []string{`C:\game.bin`, `\\server\share\x`, `..\x`, `dir\..\..\x`, `safe\file:stream`, `safe\name.`, `LPT1\x`} {
		if _, err := NormalizeRelativePath(value); err == nil {
			t.Fatalf("unsafe path %q accepted", value)
		}
	}
}

func TestDiskSpacePreflightRejectsImpossibleRequest(t *testing.T) {
	if err := RequireDiskSpace(t.TempDir(), ^uint64(0)); err == nil {
		t.Fatal("impossible disk-space request succeeded")
	}
}

func TestDownloaderResumeRetryAndVerify(t *testing.T) {
	content := []byte("verified game resource payload")
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		attempt := attempts.Add(1)
		if attempt == 1 {
			writer.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if got := request.Header.Get("Range"); got != "bytes=9-" {
			t.Errorf("Range = %q", got)
		}
		writer.Header().Set("Content-Range", fmt.Sprintf("bytes 9-%d/%d", len(content)-1, len(content)))
		writer.WriteHeader(http.StatusPartialContent)
		_, _ = writer.Write(content[9:])
	}))
	defer server.Close()

	manifest := testManifest(content, server.URL+"/resource")
	root := t.TempDir()
	destination := filepath.Join(root, manifest.Files[0].Path) + ".part"
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, content[:9], 0o644); err != nil {
		t.Fatal(err)
	}
	var latest Progress
	downloader := NewDownloader()
	downloader.RetryDelay = time.Millisecond
	downloader.OnProgress = func(progress Progress) { latest = progress }
	if err := downloader.Download(context.Background(), manifest, root); err != nil {
		t.Fatal(err)
	}
	if err := VerifyFile(filepath.Join(root, manifest.Files[0].Path), int64(len(content)), manifest.Files[0].Hash); err != nil {
		t.Fatal(err)
	}
	if latest.FilesDone != 1 || latest.BytesDone != int64(len(content)) {
		t.Fatalf("progress = %+v", latest)
	}
}

func TestDownloaderCancellationLeavesOnlyPartialFile(t *testing.T) {
	content := []byte(strings.Repeat("x", 256*1024))
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		flusher := writer.(http.Flusher)
		writer.Header().Set("Content-Length", fmt.Sprint(len(content)))
		close(started)
		for offset := 0; offset < len(content); offset += 1024 {
			_, _ = writer.Write(content[offset : offset+1024])
			flusher.Flush()
			time.Sleep(time.Millisecond)
		}
	}))
	defer server.Close()
	manifest := testManifest(content, server.URL+"/slow")
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	downloader := NewDownloader()
	downloader.MaxAttempts = 1
	done := make(chan error, 1)
	go func() { done <- downloader.Download(ctx, manifest, root) }()
	<-started
	cancel()
	if err := <-done; err == nil {
		t.Fatal("cancelled download succeeded")
	}
	if _, err := os.Stat(filepath.Join(root, manifest.Files[0].Path)); !os.IsNotExist(err) {
		t.Fatalf("verified destination exists after cancellation: %v", err)
	}
}

func TestDownloaderHashMismatchNeverPublishesDestination(t *testing.T) {
	expected := []byte("expected bytes")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte("corrupted data"))
	}))
	defer server.Close()
	manifest := testManifest(expected, server.URL+"/corrupt")
	root := t.TempDir()
	downloader := NewDownloader()
	downloader.MaxAttempts = 1
	if err := downloader.Download(context.Background(), manifest, root); err == nil {
		t.Fatal("hash-mismatched download succeeded")
	}
	destination := filepath.Join(root, manifest.Files[0].Path)
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("destination was published: %v", err)
	}
	if _, err := os.Stat(destination + ".part"); !os.IsNotExist(err) {
		t.Fatalf("invalid partial file remains: %v", err)
	}
}

func TestRepairPlanCommitAndRecovery(t *testing.T) {
	old := []byte("old valid resource")
	updated := []byte("new verified resource")
	gameRoot := t.TempDir()
	dataStaging := t.TempDir()
	target := filepath.Join(gameRoot, "data", "resource.bin")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, old, 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := testManifest(updated, "https://example.invalid/resource")
	plan, err := BuildRepairPlan(gameRoot, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Items[0].Action != ActionRepair {
		t.Fatalf("action = %s", plan.Items[0].Action)
	}
	transaction, err := NewTransaction(dataStaging, gameRoot, "test-commit")
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.Prepare(); err != nil {
		t.Fatal(err)
	}
	stageFile(t, transaction.StagingRoot, manifest.Files[0].Path, updated)
	if err := transaction.Commit(plan); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, target, updated)

	// Simulate a process dying after the original was renamed but before install.
	recoveryTarget := filepath.Join(gameRoot, "data", "recover.bin")
	backup := recoveryTarget + ".genshintools-crash.bak"
	if err := os.WriteFile(recoveryTarget, old, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(recoveryTarget, backup); err != nil {
		t.Fatal(err)
	}
	crashRoot := filepath.Join(dataStaging, "crash")
	if err := os.MkdirAll(crashRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	saved := journal{SchemaVersion: journalSchemaVersion, ID: "crash", GameRoot: gameRoot, State: "committing", Entries: []journalEntry{{RelativePath: `data\recover.bin`, Target: recoveryTarget, Backup: backup, Temporary: recoveryTarget + ".genshintools-crash.new", HadOriginal: true, State: "prepared"}}}
	data, _ := jsonMarshal(saved)
	if err := os.WriteFile(filepath.Join(crashRoot, "transaction.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RecoverTransactions(dataStaging); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, recoveryTarget, old)
}

func TestCommitFileLockRollsBackWithoutChangingOriginal(t *testing.T) {
	original := []byte("original remains available")
	updated := []byte("replacement")
	gameRoot := t.TempDir()
	target := filepath.Join(gameRoot, "data", "resource.bin")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, original, 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := testManifest(updated, "https://example.invalid/resource")
	plan, err := BuildRepairPlan(gameRoot, manifest)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := NewTransaction(t.TempDir(), gameRoot, "locked")
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Prepare(); err != nil {
		t.Fatal(err)
	}
	stageFile(t, tx.StagingRoot, manifest.Files[0].Path, updated)
	pointer, _ := windows.UTF16PtrFromString(target)
	handle, err := windows.CreateFile(pointer, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	if err := tx.Commit(plan); err == nil {
		t.Fatal("commit unexpectedly replaced a locked file")
	}
	assertFileContent(t, target, original)
	if _, err := os.Stat(target + ".genshintools-locked.new"); !os.IsNotExist(err) {
		t.Fatalf("temporary target remains: %v", err)
	}
}

func testManifest(content []byte, rawURL string) Manifest {
	digest := sha256.Sum256(content)
	return Manifest{SchemaVersion: 1, Version: "test", Kind: "game", Files: []ManifestFile{{Path: `data\resource.bin`, Size: int64(len(content)), Hash: Hash{Algorithm: "sha256", Digest: hex.EncodeToString(digest[:])}, URL: rawURL}}}
}

func stageFile(t *testing.T, root, relative string, content []byte) {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertFileContent(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

func jsonMarshal(value any) ([]byte, error) {
	return json.Marshal(value)
}
