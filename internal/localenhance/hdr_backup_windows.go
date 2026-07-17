package localenhance

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

type hdrBackupFile struct {
	SchemaVersion int         `json:"schemaVersion"`
	CreatedUTC    time.Time   `json:"createdUtc"`
	Snapshot      HDRSnapshot `json:"snapshot"`
}

func ApplyHDRWithBackup(store RegistryStore, config HDRConfig, backupPath string) error {
	config, err := config.Normalized()
	if err != nil {
		return err
	}
	_, snapshot, err := ReadHDR(store)
	if err != nil {
		return err
	}
	if err := saveHDRBackup(backupPath, snapshot); err != nil {
		return fmt.Errorf("save HDR backup before registry write: %w", err)
	}
	_, err = applyHDRSnapshot(store, config, snapshot)
	return err
}

func RestoreHDRBackup(store RegistryStore, backupPath string) error {
	data, err := os.ReadFile(backupPath)
	if err != nil {
		return err
	}
	var backup hdrBackupFile
	if err := json.Unmarshal(data, &backup); err != nil || backup.SchemaVersion != 1 {
		return errors.New("invalid HDR backup file")
	}
	return RestoreHDR(store, backup.Snapshot)
}

func saveHDRBackup(path string, snapshot HDRSnapshot) error {
	data, err := json.MarshalIndent(hdrBackupFile{SchemaVersion: 1, CreatedUTC: time.Now().UTC(), Snapshot: snapshot}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".hdr-backup-*.tmp")
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
	source, _ := windows.UTF16PtrFromString(temporaryPath)
	destination, _ := windows.UTF16PtrFromString(path)
	if err := windows.MoveFileEx(source, destination, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return err
	}
	committed = true
	return nil
}
