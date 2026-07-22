package plugins

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	FufuMainTargetID      = "fufuplugin"
	FufuMainTargetFolder  = "FuFuPlugin"
	FufuMainDLL           = "FufuLauncher.UnlockerIsland.dll"
	FufuMainOfficialURL   = "https://github.com/CodeCubist/FufuLauncher--Plugins/blob/main/FuFuPlugin.zip?raw=true"
	FufuMainSourceURL     = "https://github.com/FufuLauncher/FufuLauncher"
	FufuMainPackageSource = "https://github.com/CodeCubist/FufuLauncher--Plugins"
)

var fufuFieldIDPart = regexp.MustCompile(`[^a-z0-9._-]+`)

// FufuTargetConfig is the public, INI-driven configuration contract used by
// Fufu's configuration-target page. Unknown keys and comments remain owned by
// the upstream plugin and are preserved when a Value is changed.
type FufuTargetConfig struct {
	Name        string
	Description string
	Developer   string
	Version     string
	DLL         string
	Settings    []FufuSetting
	Schema      ConfigSchema
}

type FufuSetting struct {
	Field ConfigField
	Help  string
}

func LoadFufuTargetConfig(configPath string) (FufuTargetConfig, error) {
	data, err := readFileBounded(configPath, 1<<20)
	if err != nil {
		return FufuTargetConfig{}, err
	}
	lines, err := parseINILines(data)
	if err != nil {
		return FufuTargetConfig{}, err
	}
	values := iniValues(lines)
	get := func(section, key string) string { return values[iniLookupKey(section, key)] }
	target := FufuTargetConfig{
		Name: strings.TrimSpace(get("General", "Name")), Description: strings.TrimSpace(get("General", "Description")),
		Developer: strings.TrimSpace(get("General", "Developer")), Version: strings.TrimSpace(get("General", "Version")), DLL: strings.TrimSpace(get("General", "File")),
		Schema: ConfigSchema{SchemaVersion: ConfigSchemaVersion},
	}
	if target.Name == "" || target.Developer == "" || target.DLL == "" || filepath.Base(target.DLL) != target.DLL || !strings.EqualFold(filepath.Ext(target.DLL), ".dll") {
		return FufuTargetConfig{}, errors.New("Fufu target General metadata is incomplete or unsafe")
	}
	sections := orderedINISections(lines)
	used := map[string]bool{}
	for _, section := range sections {
		if strings.EqualFold(section, "General") {
			continue
		}
		typeName := strings.ToLower(strings.TrimSpace(get(section, "Type")))
		value := get(section, "Value")
		name := strings.TrimSpace(get(section, "Name"))
		if name == "" || !containsExact([]string{"bool", "int", "float", "string", "key"}, typeName) {
			continue
		}
		idPart := fufuFieldIDPart.ReplaceAllString(strings.ToLower(section), "-")
		idPart = strings.Trim(idPart, "-._")
		if idPart == "" {
			return FufuTargetConfig{}, fmt.Errorf("Fufu setting section %q cannot be represented safely", section)
		}
		id := "fufu." + idPart
		if used[id] {
			return FufuTargetConfig{}, fmt.Errorf("duplicate normalized Fufu setting id %q", id)
		}
		field := ConfigField{ID: id, Section: section, Key: "Value", Name: name, Type: typeName, Default: value}
		if err := validateFieldValue(field, value); err != nil {
			return FufuTargetConfig{}, fmt.Errorf("Fufu setting %s: %w", section, err)
		}
		used[id] = true
		target.Schema.Fields = append(target.Schema.Fields, field)
		target.Settings = append(target.Settings, FufuSetting{Field: field, Help: strings.TrimSpace(get(section, "help"))})
	}
	if len(target.Schema.Fields) > 0 {
		if err := validateConfigSchema(target.Schema); err != nil {
			return FufuTargetConfig{}, fmt.Errorf("Fufu target settings: %w", err)
		}
	}
	return target, nil
}

func orderedINISections(lines []string) []string {
	var result []string
	seen := map[string]bool{}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) < 3 || trimmed[0] != '[' || trimmed[len(trimmed)-1] != ']' {
			continue
		}
		section := strings.TrimSpace(trimmed[1 : len(trimmed)-1])
		key := strings.ToLower(section)
		if section != "" && !seen[key] {
			seen[key] = true
			result = append(result, section)
		}
	}
	return result
}

func FufuTargetEnabled(directory, dllName string) (enabled bool, installed bool, err error) {
	if filepath.Base(dllName) != dllName || !strings.EqualFold(filepath.Ext(dllName), ".dll") {
		return false, false, errors.New("Fufu target DLL name is unsafe")
	}
	enabledPath := filepath.Join(directory, dllName)
	disabledPath := strings.TrimSuffix(enabledPath, filepath.Ext(enabledPath)) + ".disabled"
	enabledExists, err := regularExists(enabledPath)
	if err != nil {
		return false, false, err
	}
	disabledExists, err := regularExists(disabledPath)
	if err != nil {
		return false, false, err
	}
	if enabledExists && disabledExists {
		return false, true, errors.New("both enabled and disabled Fufu target DLLs exist")
	}
	return enabledExists, enabledExists || disabledExists, nil
}

func SetFufuTargetEnabled(directory, dllName string, enable bool) error {
	enabled, installed, err := FufuTargetEnabled(directory, dllName)
	if err != nil {
		return err
	}
	if !installed {
		return errors.New("Fufu target is not installed")
	}
	if enabled == enable {
		return nil
	}
	enabledPath := filepath.Join(directory, dllName)
	disabledPath := strings.TrimSuffix(enabledPath, filepath.Ext(enabledPath)) + ".disabled"
	from, to := enabledPath, disabledPath
	if enable {
		from, to = disabledPath, enabledPath
	}
	if _, err := os.Lstat(to); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return errors.New("Fufu target destination already exists")
		}
		return err
	}
	return os.Rename(from, to)
}

func regularExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("Fufu target path is not a regular file: %s", filepath.Base(path))
	}
	return true, nil
}
