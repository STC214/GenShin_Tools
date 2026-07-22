package plugins

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"genshintools/internal/game"
	"genshintools/internal/injection"
)

func Rollback(ctx context.Context, layout Layout, state *State, id, version string, candidate game.Candidate) (InstallResult, error) {
	if state == nil || !idPattern.MatchString(id) || !versionPattern.MatchString(version) {
		return InstallResult{}, errors.New("invalid plugin rollback request")
	}
	installed, ok := state.Installed[id]
	if !ok || installedRevision(installed) == version || !containsExact(installed.RollbackVersions, version) {
		return InstallResult{}, errors.New("requested plugin rollback version is unavailable")
	}
	if err := layout.Ensure(); err != nil {
		return InstallResult{}, err
	}
	if err := RecoverTransaction(layout, state); err != nil {
		return InstallResult{}, err
	}
	source := filepath.Join(layout.Versions, id, version)
	if err := rejectReparse(source); err != nil {
		return InstallResult{}, fmt.Errorf("rollback version directory: %w", err)
	}
	stageRoot, err := os.MkdirTemp(layout.Staging, id+"-rollback-")
	if err != nil {
		return InstallResult{}, err
	}
	stageName := filepath.Base(stageRoot)
	candidateRoot := filepath.Join(stageRoot, "candidate")
	candidateDirectory := filepath.Join(candidateRoot, id)
	if err := copyPluginTree(ctx, source, candidateDirectory); err != nil {
		_ = safeRemoveAll(layout.Staging, stageRoot)
		return InstallResult{}, err
	}
	manifest, err := loadManifest(filepath.Join(candidateDirectory, "plugin.json"), id)
	if err != nil || manifest.Version != version && manifestRevision(manifest) != version {
		_ = safeRemoveAll(layout.Staging, stageRoot)
		return InstallResult{}, errors.New("rollback directory manifest/version is invalid")
	}
	if _, err := injection.AuditModule(candidateRoot, id, candidate); err != nil {
		_ = safeRemoveAll(layout.Staging, stageRoot)
		return InstallResult{}, fmt.Errorf("rollback S09 module audit: %w", err)
	}
	return commitInstall(layout, state, manifest, stageName, candidateDirectory)
}

// Uninstall removes one audited plugin without deleting its active directory
// until the new state has been durably committed. Recovery either restores the
// directory or completes cleanup after an interruption.
func Uninstall(layout Layout, state *State, id string) (Manifest, error) {
	if state == nil || !idPattern.MatchString(id) {
		return Manifest{}, errors.New("invalid plugin uninstall request")
	}
	if err := layout.Ensure(); err != nil {
		return Manifest{}, err
	}
	if err := RecoverTransaction(layout, state); err != nil {
		return Manifest{}, err
	}
	active := filepath.Join(layout.Modules, id)
	if err := rejectReparse(active); err != nil {
		return Manifest{}, fmt.Errorf("active plugin directory: %w", err)
	}
	manifest, err := loadManifest(filepath.Join(active, "plugin.json"), id)
	if err != nil {
		return Manifest{}, fmt.Errorf("refuse to uninstall unauditable plugin: %w", err)
	}
	installed, tracked := state.Installed[id]
	if !tracked {
		return Manifest{}, errors.New("uninstall requires a transactionally installed plugin")
	}
	if installed.ActiveVersion != manifest.Version || installed.ActiveRevision != "" && installed.ActiveRevision != manifestRevision(manifest) {
		return Manifest{}, errors.New("plugin state active revision does not match the active directory")
	}
	stageRoot, err := os.MkdirTemp(layout.Staging, id+"-uninstall-")
	if err != nil {
		return Manifest{}, err
	}
	stageName := filepath.Base(stageRoot)
	backup := filepath.Join(stageRoot, "removed")
	backupRelative, err := filepath.Rel(layout.Root, backup)
	if err != nil {
		_ = safeRemoveAll(layout.Staging, stageRoot)
		return Manifest{}, err
	}
	journal := installJournal{SchemaVersion: installTransactionSchema, Operation: "uninstall", Phase: "prepared", PluginID: id, NewVersion: manifest.Version, OldVersion: manifest.Version, StageName: stageName, Backup: backupRelative}
	if err := saveJournal(layout.Transaction, journal); err != nil {
		_ = safeRemoveAll(layout.Staging, stageRoot)
		return Manifest{}, err
	}
	failed := true
	defer func() {
		if failed {
			_ = RecoverTransaction(layout, state)
		}
	}()
	if err := os.Rename(active, backup); err != nil {
		return Manifest{}, fmt.Errorf("isolate active plugin: %w", err)
	}
	journal.Phase = "old_moved"
	if err := saveJournal(layout.Transaction, journal); err != nil {
		return Manifest{}, err
	}
	next := CloneState(*state)
	next.Enabled = removeString(next.Enabled, id)
	next.Order = removeString(next.Order, id)
	delete(next.Aliases, id)
	delete(next.Installed, id)
	if err := SaveState(layout.State, next); err != nil {
		return Manifest{}, fmt.Errorf("commit plugin uninstall state: %w", err)
	}
	*state = next
	journal.Phase = "state_committed"
	if err := saveJournal(layout.Transaction, journal); err != nil {
		return Manifest{}, err
	}
	failed = false
	if err := cleanupUninstallTransaction(layout, journal); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func copyPluginTree(ctx context.Context, source, destination string) error {
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return err
	}
	files := 0
	var total int64
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("rollback tree contains a symbolic link")
		}
		if err := rejectReparse(path); err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return errors.New("rollback tree contains a non-regular file")
		}
		files++
		total += info.Size()
		if files > maxPackageFiles || info.Size() > maxPackageFileSize || total > maxUncompressedBytes {
			return errors.New("rollback tree exceeds package limits")
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			input.Close()
			return err
		}
		written, copyErr := io.Copy(output, io.LimitReader(input, info.Size()+1))
		syncErr := output.Sync()
		closeOutputErr := output.Close()
		closeInputErr := input.Close()
		if copyErr != nil || syncErr != nil || closeOutputErr != nil || closeInputErr != nil || written != info.Size() {
			return fmt.Errorf("copy rollback file %s failed", relative)
		}
		return nil
	})
}
