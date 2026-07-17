package game

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const maxConfigBytes = 1 << 20

var commonGameSubdirectories = []string{"", "Genshin Impact Game", "Genshin Impact game"}

// InspectRoot validates a user or discovery supplied directory without writing to it.
// More than one matching executable is returned as AmbiguousError instead of guessing.
func InspectRoot(root, customExecutable string) (Candidate, error) {
	root = strings.Trim(strings.TrimSpace(root), `"`)
	if root == "" {
		return Candidate{}, errors.New("game path is empty")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return Candidate{}, fmt.Errorf("make game path absolute: %w", err)
	}
	absolute = filepath.Clean(absolute)
	if info, statErr := os.Stat(absolute); statErr == nil && !info.IsDir() {
		if !strings.EqualFold(filepath.Ext(absolute), ".exe") {
			return Candidate{}, fmt.Errorf("selected file is not an executable: %s", absolute)
		}
		customExecutable = filepath.Base(absolute)
		absolute = filepath.Dir(absolute)
	}

	names, err := executableNames(customExecutable)
	if err != nil {
		return Candidate{}, err
	}
	type match struct{ root, path, name string }
	var matches []match
	seen := make(map[string]struct{})
	for _, subdirectory := range commonGameSubdirectories {
		candidateRoot := filepath.Join(absolute, subdirectory)
		for _, name := range names {
			path := filepath.Join(candidateRoot, name)
			info, statErr := os.Stat(path)
			if statErr != nil || info.IsDir() {
				continue
			}
			key := strings.ToLower(filepath.Clean(path))
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			matches = append(matches, match{root: candidateRoot, path: path, name: name})
		}
	}
	if len(matches) == 0 {
		return Candidate{}, fmt.Errorf("no supported game executable found below %q", absolute)
	}
	if len(matches) > 1 {
		return Candidate{}, &AmbiguousError{Root: absolute, Count: len(matches)}
	}
	selected := matches[0]
	values, configPath, configErr := readConfig(selected.root)
	if configErr != nil && !errors.Is(configErr, fs.ErrNotExist) {
		return Candidate{}, configErr
	}
	version := strings.TrimSpace(values["game_version"])
	server := detectServer(values, selected.name)
	return Candidate{Root: selected.root, Executable: selected.path, ExeName: selected.name, ConfigPath: configPath, Version: version, Server: server}, nil
}

func executableNames(custom string) ([]string, error) {
	custom = strings.Trim(strings.TrimSpace(custom), `"`)
	if custom == "" {
		return append([]string(nil), DefaultExecutableNames...), nil
	}
	if filepath.Base(custom) != custom || custom == "." || custom == ".." || !strings.EqualFold(filepath.Ext(custom), ".exe") {
		return nil, errors.New("custom executable must be a file name ending in .exe, not a path")
	}
	return []string{custom}, nil
}

func readConfig(root string) (map[string]string, string, error) {
	path := filepath.Join(root, "config.ini")
	file, err := os.Open(path)
	if err != nil {
		return map[string]string{}, "", err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("read config.ini: %w", err)
	}
	if len(data) > maxConfigBytes {
		return nil, "", errors.New("config.ini exceeds 1 MiB safety limit")
	}
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	values := make(map[string]string)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if ok {
			values[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, "", fmt.Errorf("parse config.ini: %w", err)
	}
	return values, path, nil
}

func detectServer(values map[string]string, executable string) Server {
	channel := strings.ToLower(values["channel"])
	cps := strings.ToLower(values["cps"])
	subChannel := strings.ToLower(values["sub_channel"])
	all := strings.Join([]string{channel, cps, subChannel}, " ")
	if channel == "14" || strings.Contains(all, "bilibili") {
		return ServerCNBilibili
	}
	if channel == "1" || strings.Contains(all, "pcadbdpz") {
		return ServerCNOfficial
	}
	if channel == "0" || strings.Contains(all, "os_usa") || strings.Contains(all, "os_euro") || strings.Contains(all, "os_asia") {
		return ServerGlobal
	}
	if strings.EqualFold(executable, "YuanShen.exe") {
		return ServerCNOfficial
	}
	if strings.EqualFold(executable, "GenshinImpact.exe") {
		return ServerGlobal
	}
	return ServerUnknown
}
