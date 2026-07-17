// Package paths defines the portable, executable-local runtime layout.
package paths

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Layout contains all writable runtime locations. Everything is rooted under
// data next to the executable so a portable installation stays self-contained.
type Layout struct {
	Executable string
	Root       string
	Data       string
	Logs       string
	Cache      string
	Staging    string
	Injection  string
	Modules    string
	Config     string
}

// ForExecutable derives a portable layout from an executable path.
func ForExecutable(executable string) (Layout, error) {
	if executable == "" {
		return Layout{}, errors.New("executable path is empty")
	}

	absolute, err := filepath.Abs(executable)
	if err != nil {
		return Layout{}, fmt.Errorf("make executable path absolute: %w", err)
	}
	absolute = filepath.Clean(absolute)
	root := filepath.Dir(absolute)
	data := filepath.Join(root, "data")

	return Layout{
		Executable: absolute,
		Root:       root,
		Data:       data,
		Logs:       filepath.Join(data, "logs"),
		Cache:      filepath.Join(data, "cache"),
		Staging:    filepath.Join(data, "staging"),
		Injection:  filepath.Join(data, "injection"),
		Modules:    filepath.Join(data, "injection", "modules"),
		Config:     filepath.Join(data, "config.json"),
	}, nil
}

// Directories returns writable directories from parent to child.
func (l Layout) Directories() []string {
	return []string{l.Data, l.Logs, l.Cache, l.Staging, l.Injection, l.Modules}
}

// Ensure creates the writable runtime directories without creating a config file.
func (l Layout) Ensure() error {
	for _, directory := range l.Directories() {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", directory, err)
		}
	}
	return nil
}
