package resources

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const maxGameConfigBytes = 1 << 20

func StageVersionMetadata(gameRoot, stagingRoot, version string) ([]PlanItem, error) {
	version = strings.TrimSpace(version)
	if version == "" || strings.ContainsAny(version, "\r\n\x00") {
		return nil, errors.New("invalid game version metadata")
	}
	config, err := updatedGameConfig(filepath.Join(gameRoot, "config.ini"), version)
	if err != nil {
		return nil, err
	}
	generated := []struct {
		path string
		data []byte
	}{
		{path: "gid_ver", data: []byte(version)},
		{path: "config.ini", data: config},
	}
	items := make([]PlanItem, 0, len(generated))
	for _, value := range generated {
		digest := sha256.Sum256(value.data)
		file := ManifestFile{Path: value.path, Size: int64(len(value.data)), Hash: Hash{Algorithm: "sha256", Digest: hex.EncodeToString(digest[:])}}
		staged := filepath.Join(stagingRoot, value.path)
		if err := ensureContained(stagingRoot, staged); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Dir(staged), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(staged, value.data, 0o644); err != nil {
			return nil, fmt.Errorf("stage %s: %w", value.path, err)
		}
		target := filepath.Join(gameRoot, value.path)
		if err := VerifyFile(target, file.Size, file.Hash); err == nil {
			continue
		}
		action := ActionRepair
		reason := "version metadata differs"
		if _, err := os.Stat(target); errors.Is(err, os.ErrNotExist) {
			action, reason = ActionInstall, "version metadata is missing"
		}
		items = append(items, PlanItem{File: file, Action: action, Reason: reason})
	}
	return items, nil
}

func updatedGameConfig(path, version string) ([]byte, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return []byte("[General]\r\ngame_version=" + version + "\r\nchannel=1\r\nsub_channel=1\r\ncps=mihoyo\r\n"), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config.ini: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxGameConfigBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read config.ini: %w", err)
	}
	if len(data) > maxGameConfigBytes {
		return nil, errors.New("config.ini exceeds safety limit")
	}
	text := strings.TrimPrefix(string(data), "\ufeff")
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	inGeneral := false
	foundGeneral := false
	replaced := false
	insertAt := -1
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if inGeneral && !replaced && insertAt < 0 {
				insertAt = index
			}
			inGeneral = strings.EqualFold(trimmed, "[General]")
			foundGeneral = foundGeneral || inGeneral
			continue
		}
		if inGeneral {
			key, _, exists := strings.Cut(trimmed, "=")
			if exists && strings.EqualFold(strings.TrimSpace(key), "game_version") {
				lines[index] = "game_version=" + version
				replaced = true
			}
		}
	}
	if foundGeneral && !replaced {
		if insertAt < 0 {
			insertAt = len(lines)
		}
		lines = append(lines, "")
		copy(lines[insertAt+1:], lines[insertAt:])
		lines[insertAt] = "game_version=" + version
	} else if !foundGeneral {
		if len(lines) > 0 && lines[len(lines)-1] != "" {
			lines = append(lines, "")
		}
		lines = append(lines, "[General]", "game_version="+version)
	}
	return []byte(strings.Join(lines, "\r\n")), nil
}
