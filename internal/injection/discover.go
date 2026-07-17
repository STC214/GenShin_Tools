package injection

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"genshintools/internal/game"
)

func DiscoverCompatible(modulesRoot string, candidate game.Candidate) (compatible []Audit, warnings []string, err error) {
	entries, err := os.ReadDir(modulesRoot)
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if len(entries) > 1000 {
		return nil, nil, fmt.Errorf("module directory contains %d entries, limit is 1000", len(entries))
	}
	for _, entry := range entries {
		if !entry.IsDir() || !moduleIDPattern.MatchString(entry.Name()) {
			continue
		}
		if info, err := entry.Info(); err != nil || info.Mode()&os.ModeSymlink != 0 {
			warnings = append(warnings, fmt.Sprintf("%s: unsafe directory entry", entry.Name()))
			continue
		}
		audit, auditErr := AuditModule(filepath.Clean(modulesRoot), entry.Name(), candidate)
		if auditErr != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", entry.Name(), auditErr))
			continue
		}
		compatible = append(compatible, audit)
	}
	sort.Slice(compatible, func(left, right int) bool { return compatible[left].Manifest.ID < compatible[right].Manifest.ID })
	sort.Strings(warnings)
	return compatible, warnings, nil
}
