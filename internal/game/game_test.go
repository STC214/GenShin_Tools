package game

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestInspectRootOfficialAndBilibili(t *testing.T) {
	for _, test := range []struct {
		name, exe, config, version string
		server                     Server
	}{
		{"official", "YuanShen.exe", "\xEF\xBB\xBF[General]\ngame_version=5.7.0\nchannel=1\n", "5.7.0", ServerCNOfficial},
		{"bilibili", "YuanShen.exe", "game_version=5.7.1\nchannel=14\ncps=bilibili\n", "5.7.1", ServerCNBilibili},
		{"global", "GenshinImpact.exe", "game_version=5.7.2\nchannel=0\n", "5.7.2", ServerGlobal},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			mustWrite(t, filepath.Join(root, test.exe), "exe")
			mustWrite(t, filepath.Join(root, "config.ini"), test.config)
			before := snapshotTree(t, root)
			got, err := InspectRoot(root, "")
			if err != nil {
				t.Fatal(err)
			}
			if got.Version != test.version || got.Server != test.server || got.ExeName != test.exe {
				t.Fatalf("candidate = %+v", got)
			}
			after := snapshotTree(t, root)
			if before != after {
				t.Fatalf("inspection modified tree: before=%q after=%q", before, after)
			}
		})
	}
}

func TestInspectRootSubdirectoryCustomAndAmbiguous(t *testing.T) {
	root := filepath.Join(t.TempDir(), "含 Unicode 和空格")
	gameRoot := filepath.Join(root, "Genshin Impact Game")
	mustWrite(t, filepath.Join(gameRoot, "Custom Game.exe"), "exe")
	got, err := InspectRoot(root, "Custom Game.exe")
	if err != nil || got.Root != gameRoot {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	for _, invalid := range []string{"..\\evil.exe", "folder/game.exe", "game.com"} {
		if _, err := InspectRoot(root, invalid); err == nil {
			t.Fatalf("custom executable %q accepted", invalid)
		}
	}
	mustWrite(t, filepath.Join(gameRoot, "YuanShen.exe"), "exe")
	mustWrite(t, filepath.Join(gameRoot, "GenshinImpact.exe"), "exe")
	_, err = InspectRoot(root, "")
	var ambiguous *AmbiguousError
	if !errors.As(err, &ambiguous) || ambiguous.Count != 2 {
		t.Fatalf("expected ambiguity, got %v", err)
	}
}

func TestDiscoveryDeduplicatesAndRequiresSelection(t *testing.T) {
	one, two := t.TempDir(), t.TempDir()
	mustWrite(t, filepath.Join(one, "YuanShen.exe"), "1")
	mustWrite(t, filepath.Join(two, "GenshinImpact.exe"), "2")
	result, err := DiscoverRoots(context.Background(), []string{one, one, "missing", two}, "")
	if err != nil || len(result.Candidates) != 2 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, err := SelectSingle(result); err == nil {
		t.Fatal("multiple candidates were silently selected")
	}
}

func TestDirectorySizeAndCancellation(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "a.bin"), "1234")
	mustWrite(t, filepath.Join(root, "sub", "b.bin"), "123456")
	total, skipped, err := DirectorySize(context.Background(), root, nil)
	if err != nil || skipped != 0 || total.Bytes != 10 || total.Files != 2 {
		t.Fatalf("total=%+v skipped=%d err=%v", total, skipped, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = DirectorySize(ctx, root, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
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

func snapshotTree(t *testing.T, root string) string {
	t.Helper()
	var result string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		result += path + ":" + info.ModTime().UTC().String() + ";"
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
