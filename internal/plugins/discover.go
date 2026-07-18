package plugins

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

func Discover(modulesRoot string, state State) (items []Item, warnings []string, err error) {
	entries, err := os.ReadDir(modulesRoot)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if len(entries) > 1000 {
		return nil, nil, fmt.Errorf("plugin module root contains %d entries, limit is 1000", len(entries))
	}
	order := map[string]int{}
	for index, id := range state.Order {
		order[id] = index
	}
	enabled := map[string]bool{}
	for _, id := range state.Enabled {
		enabled[id] = true
	}
	for _, entry := range entries {
		if !entry.IsDir() || !idPattern.MatchString(entry.Name()) {
			continue
		}
		directory := filepath.Join(modulesRoot, entry.Name())
		if err := rejectReparse(directory); err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: unsafe directory: %v", entry.Name(), err))
			continue
		}
		manifest, manifestErr := loadManifest(filepath.Join(directory, "plugin.json"), entry.Name())
		if manifestErr != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", entry.Name(), manifestErr))
			continue
		}
		modulePath := filepath.Join(directory, manifest.ModuleFile)
		if err := regularFile(modulePath); err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: module manifest: %v", entry.Name(), err))
			continue
		}
		item := Item{Manifest: manifest, Directory: directory, ModulePath: modulePath, Enabled: enabled[manifest.ID], Alias: state.Aliases[manifest.ID]}
		if value, ok := order[manifest.ID]; ok {
			item.Order = value
		} else {
			item.Order = len(order) + len(items)
		}
		if manifest.ConfigSchema != "" {
			item.SchemaPath = filepath.Join(directory, manifest.ConfigSchema)
			item.ConfigPath = filepath.Join(directory, "config.ini")
			if err := regularFile(item.SchemaPath); err != nil {
				item.AuditWarning = "config schema unavailable: " + err.Error()
			}
		}
		items = append(items, item)
	}
	sort.SliceStable(items, func(left, right int) bool {
		if items[left].Order != items[right].Order {
			return items[left].Order < items[right].Order
		}
		return items[left].Manifest.ID < items[right].Manifest.ID
	})
	sort.Strings(warnings)
	return items, warnings, nil
}

func loadManifest(path, directoryID string) (Manifest, error) {
	if err := regularFile(path); err != nil {
		return Manifest{}, err
	}
	data, err := readFileBounded(path, 1<<20)
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Manifest{}, errors.New("plugin manifest contains trailing JSON data")
	}
	if err := validateManifest(manifest, directoryID); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func regularFile(path string) error {
	if err := rejectReparse(path); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("path is not a regular file")
	}
	return nil
}
