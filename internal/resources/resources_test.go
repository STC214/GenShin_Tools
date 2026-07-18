package resources

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"golang.org/x/sys/windows"
	"google.golang.org/protobuf/encoding/protowire"
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

func TestDownloaderBoundsOversizedResponse(t *testing.T) {
	expected := []byte("small expected payload")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write(bytes.Repeat([]byte("x"), 4<<20))
	}))
	defer server.Close()
	manifest := testManifest(expected, server.URL+"/oversized")
	root := t.TempDir()
	downloader := NewDownloader()
	downloader.MaxAttempts = 1
	if err := downloader.Download(context.Background(), manifest, root); err == nil {
		t.Fatal("oversized response succeeded")
	}
	part := filepath.Join(root, manifest.Files[0].Path) + ".part"
	info, err := os.Stat(part)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > int64(len(expected)+1) {
		t.Fatalf("oversized response wrote %d bytes, want at most %d", info.Size(), len(expected)+1)
	}
}

func TestDownloaderOfflineNeverPublishesDestination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	rawURL := server.URL + "/offline"
	server.Close()
	manifest := testManifest([]byte("offline resource"), rawURL)
	root := t.TempDir()
	downloader := NewDownloader()
	downloader.MaxAttempts = 1
	if err := downloader.Download(context.Background(), manifest, root); err == nil {
		t.Fatal("offline download succeeded")
	}
	if _, err := os.Stat(filepath.Join(root, manifest.Files[0].Path)); !os.IsNotExist(err) {
		t.Fatalf("offline destination was published: %v", err)
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

func TestRecoveryRejectsUnsafeJournalFiles(t *testing.T) {
	for name, data := range map[string][]byte{
		"trailing JSON": []byte(`{"schema_version":1,"id":"fixture","game_root":"C:\\\\game","state":"prepared","entries":[]} {}`),
		"oversized":     make([]byte, maxTransactionJournalBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "fixture", "transaction.json")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := RecoverTransactions(root); err == nil {
				t.Fatal("unsafe recovery journal was accepted")
			}
		})
	}
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

func TestMultiFileFailureRollsBackEarlierReplacement(t *testing.T) {
	gameRoot := t.TempDir()
	stagingRoot := t.TempDir()
	originalA, originalB := []byte("original-a"), []byte("original-b")
	updatedA, updatedB := []byte("updated-a"), []byte("updated-b")
	pathA, pathB := filepath.Join(gameRoot, "data", "a.bin"), filepath.Join(gameRoot, "data", "b.bin")
	if err := os.MkdirAll(filepath.Dir(pathA), 0o755); err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string][]byte{pathA: originalA, pathB: originalB} {
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manifest := Manifest{SchemaVersion: 1, Version: "test", Kind: "game", Files: []ManifestFile{
		testManifestFile(`data\a.bin`, updatedA), testManifestFile(`data\b.bin`, updatedB),
	}}
	plan, err := BuildRepairPlan(gameRoot, manifest)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := NewTransaction(stagingRoot, gameRoot, "multi-lock")
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Prepare(); err != nil {
		t.Fatal(err)
	}
	stageFile(t, tx.StagingRoot, manifest.Files[0].Path, updatedA)
	stageFile(t, tx.StagingRoot, manifest.Files[1].Path, updatedB)
	pointer, _ := windows.UTF16PtrFromString(pathB)
	handle, err := windows.CreateFile(pointer, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	if err := tx.Commit(plan); err == nil {
		t.Fatal("multi-file commit unexpectedly succeeded")
	}
	assertFileContent(t, pathA, originalA)
	assertFileContent(t, pathB, originalB)
}

func TestDeleteActionRollsBackWhenLaterFileIsLocked(t *testing.T) {
	gameRoot := t.TempDir()
	stagingRoot := t.TempDir()
	deleteTarget := filepath.Join(gameRoot, "old-sdk.dll")
	lockedTarget := filepath.Join(gameRoot, "locked.bin")
	if err := os.WriteFile(deleteTarget, []byte("old sdk"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockedTarget, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	replacement := []byte("replacement")
	replacementFile := testManifestFile("locked.bin", replacement)
	plan := RepairPlan{Items: []PlanItem{
		{File: ManifestFile{Path: "old-sdk.dll"}, Action: ActionDelete},
		{File: replacementFile, Action: ActionRepair},
	}}
	tx, err := NewTransaction(stagingRoot, gameRoot, "delete-rollback")
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Prepare(); err != nil {
		t.Fatal(err)
	}
	stageFile(t, tx.StagingRoot, "locked.bin", replacement)
	pointer, _ := windows.UTF16PtrFromString(lockedTarget)
	handle, err := windows.CreateFile(pointer, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	if err := tx.Commit(plan); err == nil {
		t.Fatal("locked transaction unexpectedly succeeded")
	}
	assertFileContent(t, deleteTarget, []byte("old sdk"))
	assertFileContent(t, lockedTarget, []byte("original"))
}

func TestMoveActionCommitsDirectoryAndFile(t *testing.T) {
	gameRoot := t.TempDir()
	stagingRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(gameRoot, "YuanShen_Data", "Managed"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gameRoot, "YuanShen_Data", "Managed", "keep.bin"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gameRoot, "YuanShen.exe"), []byte("exe"), 0o644); err != nil {
		t.Fatal(err)
	}
	tx, err := NewTransaction(stagingRoot, gameRoot, "move-success")
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Prepare(); err != nil {
		t.Fatal(err)
	}
	plan := RepairPlan{Items: []PlanItem{
		{File: ManifestFile{Path: "GenshinImpact_Data"}, SourcePath: "YuanShen_Data", Action: ActionMove},
		{File: ManifestFile{Path: "GenshinImpact.exe"}, SourcePath: "YuanShen.exe", Action: ActionMove},
	}}
	if err := tx.Commit(plan); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, filepath.Join(gameRoot, "GenshinImpact_Data", "Managed", "keep.bin"), []byte("keep"))
	assertFileContent(t, filepath.Join(gameRoot, "GenshinImpact.exe"), []byte("exe"))
	if _, err := os.Stat(filepath.Join(gameRoot, "YuanShen_Data")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old data directory still exists: %v", err)
	}
}

func TestMoveActionRollsBackWhenLaterFileIsLocked(t *testing.T) {
	gameRoot := t.TempDir()
	stagingRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(gameRoot, "YuanShen_Data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gameRoot, "YuanShen_Data", "keep.bin"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	lockedTarget := filepath.Join(gameRoot, "locked.bin")
	if err := os.WriteFile(lockedTarget, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	replacement := []byte("replacement")
	tx, err := NewTransaction(stagingRoot, gameRoot, "move-rollback")
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Prepare(); err != nil {
		t.Fatal(err)
	}
	stageFile(t, tx.StagingRoot, "locked.bin", replacement)
	plan := RepairPlan{Items: []PlanItem{
		{File: ManifestFile{Path: "GenshinImpact_Data"}, SourcePath: "YuanShen_Data", Action: ActionMove},
		{File: testManifestFile("locked.bin", replacement), Action: ActionRepair},
	}}
	pointer, _ := windows.UTF16PtrFromString(lockedTarget)
	handle, err := windows.CreateFile(pointer, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)
	if err := tx.Commit(plan); err == nil {
		t.Fatal("locked transaction unexpectedly succeeded")
	}
	assertFileContent(t, filepath.Join(gameRoot, "YuanShen_Data", "keep.bin"), []byte("keep"))
	assertFileContent(t, lockedTarget, []byte("original"))
	if _, err := os.Stat(filepath.Join(gameRoot, "GenshinImpact_Data")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target data directory survived rollback: %v", err)
	}
}

func TestSophonProviderAndChunkResume(t *testing.T) {
	first := []byte("first verified chunk-")
	second := []byte("second verified chunk")
	whole := append(append([]byte(nil), first...), second...)
	compressedFirst := zstdEncode(t, first)
	compressedSecond := zstdEncode(t, second)
	proto := sophonProtoFile(`data\sophon.bin`, whole, []protoChunk{{"chunk-a", first, 0, compressedFirst}, {"chunk-b", second, int64(len(first)), compressedSecond}})
	compressedManifest := zstdEncode(t, proto)
	manifestDigest := md5.Sum(proto)
	var firstRequests atomic.Int32

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/build":
			response := map[string]any{"retcode": 0, "message": "OK", "data": map[string]any{"build_id": "fixture", "tag": "6.7.0", "manifests": []any{map[string]any{
				"category_id": "1", "category_name": "game", "matching_field": "game", "stats": map[string]any{}, "deduplicated_stats": map[string]any{},
				"manifest":          map[string]any{"id": "manifest-id", "checksum": hex.EncodeToString(manifestDigest[:]), "compressed_size": fmt.Sprint(len(compressedManifest)), "uncompressed_size": fmt.Sprint(len(proto))},
				"manifest_download": map[string]any{"encryption": 0, "password": "", "compression": 1, "url_prefix": server.URL + "/manifest", "url_suffix": ""},
				"chunk_download":    map[string]any{"encryption": 0, "password": "", "compression": 1, "url_prefix": server.URL + "/chunks", "url_suffix": ""},
			}}}}
			_ = json.NewEncoder(writer).Encode(response)
		case "/manifest/manifest-id":
			_, _ = writer.Write(compressedManifest)
		case "/chunks/chunk-a":
			firstRequests.Add(1)
			_, _ = writer.Write(compressedFirst)
		case "/chunks/chunk-b":
			_, _ = writer.Write(compressedSecond)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	provider := NewSophonProvider()
	provider.BuildURL = server.URL + "/build"
	provider.RetryDelay = time.Millisecond
	catalog, err := provider.FetchCatalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Version != "6.7.0" || len(catalog.Assets()) != 1 {
		t.Fatalf("catalog = %+v", catalog)
	}
	manifest, err := provider.LoadManifest(context.Background(), catalog, "game")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	part := filepath.Join(root, manifest.Files[0].Path) + ".part"
	if err := os.MkdirAll(filepath.Dir(part), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(part, first, 0o644); err != nil {
		t.Fatal(err)
	}
	downloader := NewDownloader()
	downloader.RetryDelay = time.Millisecond
	if err := downloader.Download(context.Background(), manifest, root); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, filepath.Join(root, manifest.Files[0].Path), whole)
	if firstRequests.Load() != 0 {
		t.Fatalf("already verified first chunk was downloaded %d times", firstRequests.Load())
	}
}

func TestSophonBuildUnknownFieldFailsClosed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(writer, `{"retcode":0,"message":"OK","unexpected":true,"data":{"build_id":"x","tag":"1","manifests":[]}}`)
	}))
	defer server.Close()
	provider := NewSophonProvider()
	provider.BuildURL = server.URL
	provider.MaxAttempts = 1
	if _, err := provider.FetchCatalog(context.Background()); err == nil {
		t.Fatal("unknown API field was accepted")
	}
}

func TestSophonBranchDiscoveryAndPreloadURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(writer, `{"retcode":0,"message":"OK","data":{"game_branches":[{"game":{"id":"1Z8W5NHUQb","biz":"hk4e_cn"},"main":{"package_id":"main-pkg","branch":"main","password":"main-pass","tag":"6.7.0","diff_tags":[],"categories":[{"category_id":"1","matching_field":"game","type":"CATEGORY_TYPE_RESOURCE","scenarios":["CATEGORY_SCENARIO_FULL"]}],"required_client_version":""},"pre_download":{"package_id":"pre-pkg","branch":"pre_download","password":"pre-pass","tag":"6.8.0","diff_tags":["6.7.0"],"categories":[{"category_id":"1","matching_field":"game","type":"CATEGORY_TYPE_RESOURCE","scenarios":["CATEGORY_SCENARIO_FULL"]}],"required_client_version":""},"enable_base_pkg_predownload":true}]}}`)
	}))
	defer server.Close()
	provider := NewSophonProvider()
	provider.BranchesURL = server.URL
	branches, err := provider.FetchBranches(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if branches.PreDownload == nil || branches.PreDownload.Tag != "6.8.0" {
		t.Fatalf("branches = %+v", branches)
	}
	rawURL, err := branches.PreDownload.BuildURL()
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(rawURL)
	if parsed.Query().Get("branch") != "pre_download" || parsed.Query().Get("tag") != "6.8.0" || parsed.Query().Get("package_id") != "pre-pkg" {
		t.Fatalf("pre-download URL = %s", rawURL)
	}
}

func TestSophonBranchesRejectTrailingJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(writer, `{"retcode":0,"message":"OK","data":{"game_branches":[]}} {}`)
	}))
	defer server.Close()
	provider := NewSophonProvider()
	provider.BranchesURL = server.URL
	provider.MaxAttempts = 1
	if _, err := provider.FetchBranches(context.Background()); err == nil {
		t.Fatal("Sophon branches trailing JSON was accepted")
	}
}

func TestVersionMetadataCommitsAndPreloadSurvivesRecovery(t *testing.T) {
	gameRoot := t.TempDir()
	stagingRoot := t.TempDir()
	configPath := filepath.Join(gameRoot, "config.ini")
	if err := os.WriteFile(configPath, []byte("[General]\r\ngame_version=1.0.0\r\nchannel=1\r\n[Other]\r\nkeep=yes\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tx, err := NewTransaction(stagingRoot, gameRoot, "preload-2.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Prepare(); err != nil {
		t.Fatal(err)
	}
	items, err := StageVersionMetadata(gameRoot, tx.StagingRoot, "2.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("metadata changes = %d, want 2", len(items))
	}
	if err := tx.Commit(RepairPlan{Items: items}); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, filepath.Join(gameRoot, "gid_ver"), []byte("2.0.0"))
	updated, _ := os.ReadFile(configPath)
	if !strings.Contains(string(updated), "game_version=2.0.0") || !strings.Contains(string(updated), "keep=yes") {
		t.Fatalf("updated config = %q", updated)
	}

	preload, err := NewTransaction(stagingRoot, gameRoot, "preload-3.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if err := preload.Prepare(); err != nil {
		t.Fatal(err)
	}
	stageFile(t, preload.StagingRoot, "cached.bin", []byte("verified preload cache"))
	if err := preload.MarkPreloaded(); err != nil {
		t.Fatal(err)
	}
	if err := RecoverTransactions(stagingRoot); err != nil {
		t.Fatal(err)
	}
	assertFileContent(t, filepath.Join(preload.StagingRoot, "cached.bin"), []byte("verified preload cache"))
}

type protoChunk struct {
	id         string
	data       []byte
	offset     int64
	compressed []byte
}

func sophonProtoFile(path string, whole []byte, chunks []protoChunk) []byte {
	var file []byte
	file = appendProtoString(file, 1, path)
	for _, value := range chunks {
		var chunk []byte
		chunk = appendProtoString(chunk, 1, value.id)
		digest := md5.Sum(value.data)
		chunk = appendProtoString(chunk, 2, hex.EncodeToString(digest[:]))
		chunk = protowire.AppendTag(chunk, 3, protowire.VarintType)
		chunk = protowire.AppendVarint(chunk, uint64(value.offset))
		chunk = protowire.AppendTag(chunk, 4, protowire.VarintType)
		chunk = protowire.AppendVarint(chunk, uint64(len(value.compressed)))
		chunk = protowire.AppendTag(chunk, 5, protowire.VarintType)
		chunk = protowire.AppendVarint(chunk, uint64(len(value.data)))
		chunk = protowire.AppendTag(chunk, 6, protowire.VarintType)
		chunk = protowire.AppendVarint(chunk, 123456789)
		chunk = protowire.AppendTag(chunk, 7, protowire.BytesType)
		chunk = protowire.AppendBytes(chunk, []byte("audited-extension"))
		file = protowire.AppendTag(file, 2, protowire.BytesType)
		file = protowire.AppendBytes(file, chunk)
	}
	file = protowire.AppendTag(file, 3, protowire.VarintType)
	file = protowire.AppendVarint(file, 0)
	file = protowire.AppendTag(file, 4, protowire.VarintType)
	file = protowire.AppendVarint(file, uint64(len(whole)))
	digest := md5.Sum(whole)
	file = appendProtoString(file, 5, hex.EncodeToString(digest[:]))
	var manifest []byte
	manifest = protowire.AppendTag(manifest, 1, protowire.BytesType)
	return protowire.AppendBytes(manifest, file)
}

func appendProtoString(destination []byte, number protowire.Number, value string) []byte {
	destination = protowire.AppendTag(destination, number, protowire.BytesType)
	return protowire.AppendString(destination, value)
}

func zstdEncode(t *testing.T, data []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	encoder, err := zstd.NewWriter(&output)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := encoder.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := encoder.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func testManifest(content []byte, rawURL string) Manifest {
	digest := sha256.Sum256(content)
	return Manifest{SchemaVersion: 1, Version: "test", Kind: "game", Files: []ManifestFile{{Path: `data\resource.bin`, Size: int64(len(content)), Hash: Hash{Algorithm: "sha256", Digest: hex.EncodeToString(digest[:])}, URL: rawURL}}}
}

func testManifestFile(path string, content []byte) ManifestFile {
	digest := sha256.Sum256(content)
	return ManifestFile{Path: path, Size: int64(len(content)), Hash: Hash{Algorithm: "sha256", Digest: hex.EncodeToString(digest[:])}, URL: "https://example.invalid/resource"}
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
