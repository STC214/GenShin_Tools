package plugins

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var iniNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

type ConfigSchema struct {
	SchemaVersion int            `json:"schemaVersion"`
	Fields        []ConfigField  `json:"fields"`
	Presets       []ConfigPreset `json:"presets,omitempty"`
}

type ConfigField struct {
	ID      string   `json:"id"`
	Section string   `json:"section"`
	Key     string   `json:"key"`
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	Default string   `json:"default"`
	Min     *float64 `json:"min,omitempty"`
	Max     *float64 `json:"max,omitempty"`
}

type ConfigPreset struct {
	ID     string            `json:"id"`
	Name   string            `json:"name"`
	Values map[string]string `json:"values"`
}

func LoadConfigSchema(path string) (ConfigSchema, error) {
	if err := regularFile(path); err != nil {
		return ConfigSchema{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ConfigSchema{}, err
	}
	if len(data) > 1<<20 {
		return ConfigSchema{}, errors.New("plugin config schema exceeds 1 MiB")
	}
	var schema ConfigSchema
	decoder := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&schema); err != nil {
		return ConfigSchema{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return ConfigSchema{}, errors.New("plugin config schema contains trailing JSON data")
	}
	if err := validateConfigSchema(schema); err != nil {
		return ConfigSchema{}, err
	}
	return schema, nil
}

func ReadConfig(configPath string, schema ConfigSchema) (map[string]string, error) {
	if err := validateConfigSchema(schema); err != nil {
		return nil, err
	}
	values := make(map[string]string, len(schema.Fields))
	for _, field := range schema.Fields {
		values[field.ID] = field.Default
	}
	data, err := os.ReadFile(configPath)
	if errors.Is(err, os.ErrNotExist) {
		return values, nil
	}
	if err != nil {
		return nil, err
	}
	lines, err := parseINILines(data)
	if err != nil {
		return nil, err
	}
	physical := iniValues(lines)
	for _, field := range schema.Fields {
		if value, ok := physical[iniLookupKey(field.Section, field.Key)]; ok {
			if err := validateFieldValue(field, value); err != nil {
				return nil, fmt.Errorf("config field %s: %w", field.ID, err)
			}
			values[field.ID] = value
		}
	}
	return values, nil
}

func UpdateConfig(configPath string, schema ConfigSchema, fieldID, value string) error {
	return applyConfigValues(configPath, schema, map[string]string{fieldID: value})
}

func ApplyPreset(configPath string, schema ConfigSchema, presetID string) error {
	for _, preset := range schema.Presets {
		if preset.ID == presetID {
			return applyConfigValues(configPath, schema, preset.Values)
		}
	}
	return fmt.Errorf("unknown plugin preset %q", presetID)
}

func applyConfigValues(configPath string, schema ConfigSchema, values map[string]string) error {
	if err := validateConfigSchema(schema); err != nil {
		return err
	}
	fields := make(map[string]ConfigField, len(schema.Fields))
	for _, field := range schema.Fields {
		fields[field.ID] = field
	}
	for id, value := range values {
		field, ok := fields[id]
		if !ok {
			return fmt.Errorf("unknown plugin config field %q", id)
		}
		if err := validateFieldValue(field, value); err != nil {
			return fmt.Errorf("config field %s: %w", id, err)
		}
	}
	data, err := os.ReadFile(configPath)
	if errors.Is(err, os.ErrNotExist) {
		data = nil
	} else if err != nil {
		return err
	}
	lines, err := parseINILines(data)
	if err != nil {
		return err
	}
	for id, value := range values {
		lines = setINIValue(lines, fields[id].Section, fields[id].Key, value)
	}
	return atomicWrite(configPath, []byte(strings.Join(lines, "\r\n")+"\r\n"))
}

func validateConfigSchema(schema ConfigSchema) error {
	if schema.SchemaVersion != ConfigSchemaVersion {
		return errors.New("unsupported plugin config schema")
	}
	if len(schema.Fields) == 0 || len(schema.Fields) > 128 || len(schema.Presets) > 32 {
		return errors.New("plugin config schema requires 1..128 fields and at most 32 presets")
	}
	fields := map[string]ConfigField{}
	physical := map[string]bool{}
	for _, field := range schema.Fields {
		if !idPattern.MatchString(field.ID) || !iniNamePattern.MatchString(field.Section) || !iniNamePattern.MatchString(field.Key) || strings.TrimSpace(field.Name) == "" {
			return fmt.Errorf("invalid plugin config field %q", field.ID)
		}
		if _, exists := fields[field.ID]; exists || physical[iniLookupKey(field.Section, field.Key)] {
			return fmt.Errorf("duplicate plugin config field %q", field.ID)
		}
		if !containsExact([]string{"bool", "int", "float", "string", "key"}, field.Type) {
			return fmt.Errorf("unsupported type %q for field %s", field.Type, field.ID)
		}
		if field.Min != nil && field.Max != nil && *field.Min > *field.Max {
			return fmt.Errorf("invalid range for field %s", field.ID)
		}
		if err := validateFieldValue(field, field.Default); err != nil {
			return fmt.Errorf("default for field %s: %w", field.ID, err)
		}
		fields[field.ID] = field
		physical[iniLookupKey(field.Section, field.Key)] = true
	}
	presets := map[string]bool{}
	for _, preset := range schema.Presets {
		if !idPattern.MatchString(preset.ID) || strings.TrimSpace(preset.Name) == "" || len(preset.Values) == 0 || presets[preset.ID] {
			return fmt.Errorf("invalid plugin preset %q", preset.ID)
		}
		presets[preset.ID] = true
		for id, value := range preset.Values {
			field, ok := fields[id]
			if !ok {
				return fmt.Errorf("preset %s references unknown field %s", preset.ID, id)
			}
			if err := validateFieldValue(field, value); err != nil {
				return fmt.Errorf("preset %s field %s: %w", preset.ID, id, err)
			}
		}
	}
	return nil
}

func validateFieldValue(field ConfigField, value string) error {
	if len(value) > 4096 || strings.ContainsAny(value, "\x00\r\n") {
		return errors.New("value is too long or contains a line break")
	}
	var number float64
	switch field.Type {
	case "bool":
		if !containsExact([]string{"0", "1", "true", "false"}, strings.ToLower(value)) {
			return errors.New("boolean must be 0, 1, true or false")
		}
		return nil
	case "int":
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return errors.New("value is not an integer")
		}
		number = float64(parsed)
	case "key":
		parsed, err := strconv.ParseInt(value, 10, 32)
		if err != nil || parsed < 1 || parsed > 255 {
			return errors.New("virtual key must be within 1..255")
		}
		number = float64(parsed)
	case "float":
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
			return errors.New("value is not a finite number")
		}
		number = parsed
	case "string":
		return nil
	default:
		return errors.New("unsupported config type")
	}
	if field.Min != nil && number < *field.Min || field.Max != nil && number > *field.Max {
		return errors.New("numeric value is outside its declared range")
	}
	return nil
}

func parseINILines(data []byte) ([]string, error) {
	if len(data) > 1<<20 {
		return nil, errors.New("plugin config exceeds 1 MiB")
	}
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, errors.New("plugin config contains NUL")
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		return nil, nil
	}
	lines := strings.Split(text, "\n")
	if len(lines) > 10_000 {
		return nil, errors.New("plugin config contains too many lines")
	}
	return lines, nil
}

func iniValues(lines []string) map[string]string {
	result := map[string]string{}
	section := ""
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			section = strings.TrimSpace(trimmed[1 : len(trimmed)-1])
			continue
		}
		if section == "" || strings.HasPrefix(trimmed, ";") || strings.HasPrefix(trimmed, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			result[iniLookupKey(section, strings.TrimSpace(parts[0]))] = strings.TrimSpace(parts[1])
		}
	}
	return result
}

func setINIValue(lines []string, section, key, value string) []string {
	sectionStart, sectionEnd := -1, len(lines)
	current := ""
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			current = strings.TrimSpace(trimmed[1 : len(trimmed)-1])
			if strings.EqualFold(current, section) {
				sectionStart = index
				sectionEnd = len(lines)
			} else if sectionStart >= 0 {
				sectionEnd = index
				break
			}
			continue
		}
		if sectionStart >= 0 && current != "" {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 && strings.EqualFold(strings.TrimSpace(parts[0]), key) {
				lines[index] = key + " = " + value
				return lines
			}
		}
	}
	if sectionStart < 0 {
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
			lines = append(lines, "")
		}
		return append(lines, "["+section+"]", key+" = "+value)
	}
	line := key + " = " + value
	lines = append(lines, "")
	copy(lines[sectionEnd+1:], lines[sectionEnd:])
	lines[sectionEnd] = line
	return lines
}

func iniLookupKey(section, key string) string {
	return strings.ToLower(section) + "\x00" + strings.ToLower(key)
}

func atomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".plugin-config-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if _, err := temporary.Write(data); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := replaceFile(temporaryPath, path); err != nil {
		return err
	}
	committed = true
	return nil
}
