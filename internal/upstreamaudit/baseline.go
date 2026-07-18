package upstreamaudit

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

func UpdateBaseline(path string, original []byte, lock Lock, report Report, disposition Disposition) error {
	if report.Base == report.Head {
		return errors.New("upstream baseline is already current")
	}
	if err := ValidateDisposition(report, disposition); err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	current, readErr := io.ReadAll(io.LimitReader(file, maxLockBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return errors.Join(readErr, closeErr)
	}
	if len(current) > maxLockBytes {
		return errors.New("upstream lock exceeds 64 KiB")
	}
	if !bytes.Equal(current, original) {
		return errors.New("upstream lock changed after it was read")
	}
	if err := lock.Validate(); err != nil || lock.Commit != report.Base {
		return errors.New("upstream lock no longer matches the report base")
	}
	lock.Commit = report.Head
	lock.CommitTimeUTC = report.HeadCommitUTC
	lock.CheckedAtUTC = disposition.ReviewedUTC
	data, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return err
	}
	return atomicReplace(path, append(data, '\n'))
}

func atomicReplace(path string, data []byte) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".upstream-lock-*.tmp")
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
