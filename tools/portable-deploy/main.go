// Command portable-deploy installs a verified portable ZIP while preserving
// only the target's runtime data directory. All other target content is
// replaced transactionally so files retired by a release cannot accumulate.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"genshintools/internal/platform/win32"
	"genshintools/internal/platform/winfile"
	"genshintools/internal/selfupdate"
	"golang.org/x/sys/windows"
)

var movePath = os.Rename

const deployJournalSchema = 1

type deployJournal struct {
	SchemaVersion int    `json:"schemaVersion"`
	Target        string `json:"target"`
	Source        string `json:"source"`
	Backup        string `json:"backup"`
	Phase         string `json:"phase"`
}

func main() {
	var archive, target, version string
	flag.StringVar(&archive, "archive", "", "portable ZIP path")
	flag.StringVar(&target, "target", "", "deployment directory")
	flag.StringVar(&version, "version", "", "expected product SemVer")
	flag.Parse()
	if err := deploy(context.Background(), archive, target, version); err != nil {
		fmt.Fprintln(os.Stderr, "portable deployment failed:", err)
		os.Exit(1)
	}
	fmt.Printf("Deployed Genshin Tools %s to %s\n", version, target)
}

func deploy(ctx context.Context, archive, target, version string) error {
	archive, err := filepath.Abs(archive)
	if err != nil {
		return err
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return err
	}
	if filepath.Dir(target) == target {
		return errors.New("deployment target must not be a volume root")
	}
	mutex, err := acquireDeploymentMutex(target)
	if err != nil {
		return err
	}
	defer win32.CloseHandle(mutex)
	if info, statErr := os.Lstat(target); statErr == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("deployment target must be a real directory")
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	size, digest, err := hashFile(archive)
	if err != nil {
		return err
	}
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	if err := recoverDeployment(target); err != nil {
		return fmt.Errorf("recover interrupted deployment: %w", err)
	}
	stagingRoot, err := os.MkdirTemp(parent, ".genshintools-deploy-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stagingRoot)
	artifact := selfupdate.Artifact{
		OS: "windows", Arch: "amd64", URL: "https://deploy.invalid/" + filepath.Base(archive),
		Size: size, SHA256: digest,
	}
	staged, err := selfupdate.StagePackage(ctx, archive, stagingRoot, version, artifact)
	if err != nil {
		return fmt.Errorf("verify portable ZIP: %w", err)
	}
	if err := mirrorRelease(staged.Directory, target); err != nil {
		return err
	}
	return nil
}

func mirrorRelease(source, target string) error {
	if err := recoverDeployment(target); err != nil {
		return fmt.Errorf("recover interrupted deployment: %w", err)
	}
	incoming, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	for _, entry := range incoming {
		if strings.EqualFold(entry.Name(), "data") {
			return errors.New("portable release must not contain a data directory")
		}
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}
	existing, err := os.ReadDir(target)
	if err != nil {
		return err
	}
	for _, entry := range existing {
		if strings.EqualFold(entry.Name(), "data") {
			info, infoErr := entry.Info()
			if infoErr != nil {
				return fmt.Errorf("inspect data directory: %w", infoErr)
			}
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return errors.New("preserved data path must be a real directory")
			}
		}
	}
	backup, err := os.MkdirTemp(filepath.Dir(target), ".genshintools-backup-*")
	if err != nil {
		return err
	}
	journal := deployJournal{
		SchemaVersion: deployJournalSchema,
		Target:        target,
		Source:        source,
		Backup:        backup,
		Phase:         "backing-up",
	}
	journalFile := deploymentJournalPath(target)
	if err := createDeploymentJournal(journalFile, journal); err != nil {
		_ = os.RemoveAll(backup)
		return err
	}
	finished := false
	defer func() {
		if finished {
			_ = os.RemoveAll(backup)
		}
	}()

	rollback := func(cause error) error {
		if rollbackErr := rollbackDeployment(journal); rollbackErr != nil {
			return errors.Join(cause, fmt.Errorf("rollback remains journaled for the next run: %w", rollbackErr))
		}
		finished = true
		if removeErr := os.Remove(journalFile); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return errors.Join(cause, fmt.Errorf("remove completed rollback journal: %w", removeErr))
		}
		return cause
	}
	for _, entry := range existing {
		if strings.EqualFold(entry.Name(), "data") {
			continue
		}
		if err := movePath(filepath.Join(target, entry.Name()), filepath.Join(backup, entry.Name())); err != nil {
			return rollback(fmt.Errorf("stage old %s: %w", entry.Name(), err))
		}
	}
	journal.Phase = "installing"
	if err := replaceDeploymentJournal(journalFile, journal); err != nil {
		return rollback(fmt.Errorf("persist install phase: %w", err))
	}
	for _, entry := range incoming {
		if err := movePath(filepath.Join(source, entry.Name()), filepath.Join(target, entry.Name())); err != nil {
			return rollback(fmt.Errorf("install new %s: %w", entry.Name(), err))
		}
	}
	journal.Phase = "committed"
	if err := replaceDeploymentJournal(journalFile, journal); err != nil {
		return rollback(fmt.Errorf("persist commit phase: %w", err))
	}
	if err := os.RemoveAll(backup); err != nil {
		return fmt.Errorf("deployment committed; clean old backup on next run: %w", err)
	}
	if err := os.Remove(journalFile); err != nil {
		return fmt.Errorf("deployment committed; clean transaction journal on next run: %w", err)
	}
	finished = true
	return nil
}

func deploymentJournalPath(target string) string {
	return filepath.Join(filepath.Dir(target), "."+filepath.Base(target)+".deploy-journal.json")
}

func acquireDeploymentMutex(target string) (windows.Handle, error) {
	digest := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(target))))
	name := "Global\\GenshinTools.PortableDeploy." + hex.EncodeToString(digest[:16])
	handle, alreadyRunning, err := win32.CreateSingleInstanceMutex(name)
	if err != nil {
		return 0, err
	}
	if alreadyRunning {
		win32.CloseHandle(handle)
		return 0, errors.New("another deployment is already running for this target")
	}
	return handle, nil
}

func createDeploymentJournal(path string, journal deployJournal) error {
	data, err := encodeDeploymentJournal(journal)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return errors.New("another deployment owns the transaction journal")
		}
		return err
	}
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if result := errors.Join(writeErr, syncErr, closeErr); result != nil {
		_ = os.Remove(path)
		return result
	}
	return nil
}

func replaceDeploymentJournal(path string, journal deployJournal) error {
	data, err := encodeDeploymentJournal(journal)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".deploy-journal-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	_, writeErr := temporary.Write(data)
	syncErr := temporary.Sync()
	closeErr := temporary.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return err
	}
	source, err := windows.UTF16PtrFromString(temporaryPath)
	if err != nil {
		return err
	}
	destination, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return winfile.Replace(source, destination, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

func encodeDeploymentJournal(journal deployJournal) ([]byte, error) {
	data, err := json.Marshal(journal)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func recoverDeployment(target string) error {
	path := deploymentJournalPath(target)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(data) > 64<<10 {
		return errors.New("deployment journal exceeds 64 KiB")
	}
	var journal deployJournal
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&journal); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("deployment journal contains trailing JSON")
	}
	if err := validateDeploymentJournal(journal, target); err != nil {
		return err
	}
	if journal.Phase != "committed" && journal.Phase != "rolled-back" {
		if err := rollbackDeployment(journal); err != nil {
			return err
		}
	} else if err := os.RemoveAll(journal.Backup); err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Dir(journal.Source)); err != nil {
		return err
	}
	return os.Remove(path)
}

func validateDeploymentJournal(journal deployJournal, target string) error {
	target = filepath.Clean(target)
	parent := filepath.Dir(target)
	stagingRoot := filepath.Dir(journal.Source)
	if journal.SchemaVersion != deployJournalSchema || !strings.EqualFold(filepath.Clean(journal.Target), target) {
		return errors.New("deployment journal target or schema is invalid")
	}
	if filepath.Dir(stagingRoot) != parent || !strings.HasPrefix(filepath.Base(stagingRoot), ".genshintools-deploy-") ||
		filepath.Dir(journal.Backup) != parent || !strings.HasPrefix(filepath.Base(journal.Backup), ".genshintools-backup-") {
		return errors.New("deployment journal paths are outside the target parent")
	}
	switch journal.Phase {
	case "backing-up", "installing", "restoring", "rolled-back", "committed":
		return nil
	default:
		return errors.New("deployment journal phase is invalid")
	}
}

func rollbackDeployment(journal deployJournal) error {
	backupEntries, err := os.ReadDir(journal.Backup)
	if errors.Is(err, os.ErrNotExist) {
		return errors.New("deployment backup is missing")
	}
	if err != nil {
		return err
	}
	if journal.Phase == "installing" {
		entries, err := os.ReadDir(journal.Target)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if strings.EqualFold(entry.Name(), "data") {
				continue
			}
			if err := os.RemoveAll(filepath.Join(journal.Target, entry.Name())); err != nil {
				return err
			}
		}
		journal.Phase = "restoring"
		if err := replaceDeploymentJournal(deploymentJournalPath(journal.Target), journal); err != nil {
			return err
		}
	}
	for _, entry := range backupEntries {
		if err := movePath(filepath.Join(journal.Backup, entry.Name()), filepath.Join(journal.Target, entry.Name())); err != nil {
			return err
		}
	}
	journal.Phase = "rolled-back"
	if err := replaceDeploymentJournal(deploymentJournalPath(journal.Target), journal); err != nil {
		return err
	}
	return os.RemoveAll(journal.Backup)
}

func hashFile(path string) (int64, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return 0, "", err
	}
	return size, hex.EncodeToString(hash.Sum(nil)), nil
}
