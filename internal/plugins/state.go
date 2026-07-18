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
	"strings"
	"time"
)

type State struct {
	SchemaVersion int                       `json:"schemaVersion"`
	Enabled       []string                  `json:"enabled"`
	Order         []string                  `json:"order"`
	Aliases       map[string]string         `json:"aliases,omitempty"`
	Installed     map[string]InstalledState `json:"installed,omitempty"`
}

type InstalledState struct {
	ActiveVersion    string   `json:"activeVersion"`
	RollbackVersions []string `json:"rollbackVersions,omitempty"`
}

type StateLoadResult struct {
	State         State
	RecoveredFrom string
}

func DefaultState() State {
	return State{SchemaVersion: StateSchemaVersion, Aliases: map[string]string{}, Installed: map[string]InstalledState{}}
}

func CloneState(state State) State {
	clone := State{SchemaVersion: state.SchemaVersion, Enabled: append([]string(nil), state.Enabled...), Order: append([]string(nil), state.Order...), Aliases: make(map[string]string, len(state.Aliases)), Installed: make(map[string]InstalledState, len(state.Installed))}
	for id, alias := range state.Aliases {
		clone.Aliases[id] = alias
	}
	for id, installed := range state.Installed {
		installed.RollbackVersions = append([]string(nil), installed.RollbackVersions...)
		clone.Installed[id] = installed
	}
	return clone
}

func LoadState(path string) (StateLoadResult, error) {
	data, err := readFileBounded(path, 4<<20)
	if errors.Is(err, os.ErrNotExist) {
		return StateLoadResult{State: DefaultState()}, nil
	}
	if errors.Is(err, errFileTooLarge) {
		return recoverState(path, err)
	}
	if err != nil {
		return StateLoadResult{}, err
	}
	var state State
	decoder := json.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return recoverState(path, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return recoverState(path, errors.New("plugin state contains trailing JSON data"))
	}
	if err := normalizeState(&state); err != nil {
		return recoverState(path, err)
	}
	return StateLoadResult{State: state}, nil
}

func SaveState(path string, state State) error {
	if err := normalizeState(&state); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".plugin-state-*.tmp")
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

func SetEnabled(state *State, id string, enabled bool) error {
	if state == nil || !idPattern.MatchString(id) {
		return errors.New("invalid plugin state or id")
	}
	state.Enabled = removeString(state.Enabled, id)
	if enabled {
		state.Enabled = append(state.Enabled, id)
		if !containsExact(state.Order, id) {
			state.Order = append(state.Order, id)
		}
	}
	return normalizeState(state)
}

func SetAlias(state *State, id, alias string) error {
	if state == nil || !idPattern.MatchString(id) {
		return errors.New("invalid plugin state or id")
	}
	alias = strings.TrimSpace(alias)
	if len([]rune(alias)) > 64 || strings.ContainsAny(alias, "\x00\r\n") {
		return errors.New("plugin alias is limited to 64 single-line characters")
	}
	if state.Aliases == nil {
		state.Aliases = map[string]string{}
	}
	if alias == "" {
		delete(state.Aliases, id)
	} else {
		state.Aliases[id] = alias
	}
	return nil
}

func Move(state *State, id string, delta int) error {
	if state == nil || (delta != -1 && delta != 1) {
		return errors.New("plugin move delta must be -1 or 1")
	}
	index := -1
	for i, value := range state.Order {
		if value == id {
			index = i
			break
		}
	}
	target := index + delta
	if index < 0 || target < 0 || target >= len(state.Order) {
		return errors.New("plugin cannot move in that direction")
	}
	state.Order[index], state.Order[target] = state.Order[target], state.Order[index]
	return nil
}

func EnabledInOrder(state State, available map[string]bool) []string {
	result := make([]string, 0, len(state.Enabled))
	enabled := make(map[string]bool, len(state.Enabled))
	for _, id := range state.Enabled {
		enabled[id] = true
	}
	for _, id := range state.Order {
		if enabled[id] && available[id] {
			result = append(result, id)
			delete(enabled, id)
		}
	}
	remaining := make([]string, 0, len(enabled))
	for id := range enabled {
		if available[id] {
			remaining = append(remaining, id)
		}
	}
	sort.Strings(remaining)
	return append(result, remaining...)
}

func normalizeState(state *State) error {
	if state.SchemaVersion != StateSchemaVersion {
		return fmt.Errorf("unsupported plugin state schema %d", state.SchemaVersion)
	}
	state.Enabled = uniqueIDs(state.Enabled)
	state.Order = uniqueIDs(state.Order)
	for _, id := range state.Enabled {
		if !containsExact(state.Order, id) {
			state.Order = append(state.Order, id)
		}
	}
	if state.Aliases == nil {
		state.Aliases = map[string]string{}
	}
	for id, alias := range state.Aliases {
		if !idPattern.MatchString(id) || len([]rune(alias)) > 64 || strings.ContainsAny(alias, "\x00\r\n") {
			return fmt.Errorf("invalid alias for plugin %q", id)
		}
	}
	if state.Installed == nil {
		state.Installed = map[string]InstalledState{}
	}
	for id, installed := range state.Installed {
		if !idPattern.MatchString(id) || !versionPattern.MatchString(installed.ActiveVersion) {
			return fmt.Errorf("invalid installed state for plugin %q", id)
		}
		for _, version := range installed.RollbackVersions {
			if !versionPattern.MatchString(version) {
				return fmt.Errorf("invalid rollback version for plugin %q", id)
			}
		}
		installed.RollbackVersions = uniqueStrings(installed.RollbackVersions)
		state.Installed[id] = installed
	}
	return nil
}

func recoverState(path string, cause error) (StateLoadResult, error) {
	target := path + ".corrupt-" + time.Now().UTC().Format("20060102T150405.000000000Z")
	if err := os.Rename(path, target); err != nil {
		return StateLoadResult{}, fmt.Errorf("plugin state invalid: %v; quarantine: %w", cause, err)
	}
	return StateLoadResult{State: DefaultState(), RecoveredFrom: target}, nil
}

func uniqueIDs(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		if idPattern.MatchString(value) && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func uniqueStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}

func removeString(values []string, target string) []string {
	result := values[:0]
	for _, value := range values {
		if value != target {
			result = append(result, value)
		}
	}
	return result
}
