package diagnostics

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"strconv"
	"time"
)

type SessionMarker struct {
	PID       int    `json:"pid"`
	StartedAt string `json:"startedAtUtc"`
	Version   string `json:"version"`
}

// BeginSession reports whether a marker from an unclean previous exit existed,
// then replaces it with the current session marker.
func BeginSession(path, version string) (bool, error) {
	_, statErr := os.Stat(path)
	previousUnclean := statErr == nil
	if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
		return false, statErr
	}
	marker := SessionMarker{PID: os.Getpid(), StartedAt: time.Now().UTC().Format(time.RFC3339Nano), Version: version}
	data, err := json.Marshal(marker)
	if err != nil {
		return false, err
	}
	data = append(data, '\n')
	temporary := path + "." + strconv.Itoa(os.Getpid()) + ".tmp"
	if err := os.WriteFile(temporary, data, 0o644); err != nil {
		return false, err
	}
	if err := replaceMarker(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return false, err
	}
	return previousUnclean, nil
}

func EndSession(path string) error {
	err := os.Remove(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
