package plugins

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Layout struct {
	Root        string
	Modules     string
	Versions    string
	Staging     string
	State       string
	Catalog     string
	Transaction string
}

func NewLayout(dataRoot, modulesRoot string) (Layout, error) {
	dataRoot, err := filepath.Abs(dataRoot)
	if err != nil {
		return Layout{}, err
	}
	modulesRoot, err = filepath.Abs(modulesRoot)
	if err != nil {
		return Layout{}, err
	}
	root := filepath.Join(dataRoot, "plugins")
	return Layout{Root: root, Modules: modulesRoot, Versions: filepath.Join(root, "versions"), Staging: filepath.Join(root, "staging"), State: filepath.Join(root, "state.json"), Catalog: filepath.Join(root, "catalog.json"), Transaction: filepath.Join(root, "transaction.json")}, nil
}

func (layout Layout) Ensure() error {
	for _, directory := range []string{layout.Root, layout.Modules, layout.Versions, layout.Staging} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return err
		}
		if err := rejectReparse(directory); err != nil {
			return fmt.Errorf("plugin directory %s: %w", directory, err)
		}
	}
	return nil
}

func safeJoin(root, relative string) (string, error) {
	if relative == "" || filepath.IsAbs(relative) || strings.Contains(relative, ":") {
		return "", errors.New("relative plugin path is invalid")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	target, err := filepath.Abs(filepath.Join(root, relative))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("plugin path escapes its root")
	}
	return target, nil
}

func safeRemoveAll(root, target string) error {
	root, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("refusing to remove path outside plugin root")
	}
	if _, err := os.Lstat(target); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if err := filepath.WalkDir(target, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to remove symlink %s", path)
		}
		return rejectReparse(path)
	}); err != nil {
		return err
	}
	return os.RemoveAll(target)
}
