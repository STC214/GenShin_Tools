package plugins

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const installTransactionSchema = 1

type installJournal struct {
	SchemaVersion int    `json:"schemaVersion"`
	Operation     string `json:"operation,omitempty"`
	Phase         string `json:"phase"`
	PluginID      string `json:"pluginId"`
	NewVersion    string `json:"newVersion"`
	OldVersion    string `json:"oldVersion,omitempty"`
	StageName     string `json:"stageName"`
	Backup        string `json:"backup,omitempty"`
}

func commitInstall(layout Layout, state *State, manifest Manifest, stageName, candidateDirectory string) (InstallResult, error) {
	active := filepath.Join(layout.Modules, manifest.ID)
	newRevision := manifestRevision(manifest)
	journal := installJournal{SchemaVersion: installTransactionSchema, Operation: "install", Phase: "prepared", PluginID: manifest.ID, NewVersion: newRevision, StageName: stageName}
	result := InstallResult{Manifest: manifest}

	if info, err := os.Lstat(active); err == nil {
		if !info.IsDir() {
			return InstallResult{}, errors.New("active plugin path is not a directory")
		}
		if err := rejectReparse(active); err != nil {
			return InstallResult{}, fmt.Errorf("active plugin directory: %w", err)
		}
		oldManifest, err := loadManifest(filepath.Join(active, "plugin.json"), manifest.ID)
		if err != nil {
			return InstallResult{}, fmt.Errorf("refuse to replace unauditable active plugin: %w", err)
		}
		if installed, ok := state.Installed[manifest.ID]; ok {
			if installed.ActiveVersion != oldManifest.Version || installed.ActiveRevision != "" && installed.ActiveRevision != manifestRevision(oldManifest) {
				return InstallResult{}, errors.New("plugin state active revision does not match the active directory")
			}
		}
		journal.OldVersion = oldManifest.Version
		if oldManifest.Version == manifest.Version {
			journal.OldVersion = manifestRevision(oldManifest)
		}
		result.PreviousVersion = oldManifest.Version
		if journal.OldVersion == newRevision {
			journal.Backup = filepath.Join("staging", stageName, "previous")
		} else {
			versionDirectory := filepath.Join(layout.Versions, manifest.ID, journal.OldVersion)
			if _, err := os.Lstat(versionDirectory); err == nil && journal.OldVersion == oldManifest.Version {
				journal.OldVersion = manifestRevision(oldManifest)
				versionDirectory = filepath.Join(layout.Versions, manifest.ID, journal.OldVersion)
			} else if err != nil && !errors.Is(err, os.ErrNotExist) {
				return InstallResult{}, err
			}
			if _, err := os.Lstat(versionDirectory); errors.Is(err, os.ErrNotExist) {
				if err := os.MkdirAll(filepath.Dir(versionDirectory), 0o755); err != nil {
					return InstallResult{}, err
				}
				journal.Backup, err = filepath.Rel(layout.Root, versionDirectory)
				if err != nil {
					return InstallResult{}, err
				}
				result.RollbackReady = true
			} else if err != nil {
				return InstallResult{}, err
			} else {
				journal.Backup = filepath.Join("staging", stageName, "previous")
				result.RollbackReady = true // An older copy already exists in versions.
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return InstallResult{}, err
	}

	if err := saveJournal(layout.Transaction, journal); err != nil {
		return InstallResult{}, err
	}
	failed := true
	defer func() {
		if failed {
			_ = RecoverTransaction(layout, state)
		}
	}()

	if journal.Backup != "" {
		backup, err := safeJoin(layout.Root, journal.Backup)
		if err != nil {
			return InstallResult{}, err
		}
		if err := os.Rename(active, backup); err != nil {
			return InstallResult{}, fmt.Errorf("archive active plugin: %w", err)
		}
		journal.Phase = "old_moved"
		if err := saveJournal(layout.Transaction, journal); err != nil {
			return InstallResult{}, err
		}
	}
	if err := os.Rename(candidateDirectory, active); err != nil {
		return InstallResult{}, fmt.Errorf("activate plugin candidate: %w", err)
	}
	journal.Phase = "new_moved"
	if err := saveJournal(layout.Transaction, journal); err != nil {
		return InstallResult{}, err
	}

	previousInstalled, hadPreviousInstalled := state.Installed[manifest.ID]
	installed := InstalledState{ActiveVersion: manifest.Version, ActiveRevision: newRevision}
	if hadPreviousInstalled {
		previous := previousInstalled
		installed.RollbackVersions = append(installed.RollbackVersions, previous.RollbackVersions...)
	}
	if journal.OldVersion != "" && journal.OldVersion != newRevision {
		installed.RollbackVersions = append(installed.RollbackVersions, journal.OldVersion)
	}
	installed.RollbackVersions = uniqueStrings(installed.RollbackVersions)
	installed.RollbackVersions = removeString(installed.RollbackVersions, newRevision)
	state.Installed[manifest.ID] = installed
	if err := SaveState(layout.State, *state); err != nil {
		if hadPreviousInstalled {
			state.Installed[manifest.ID] = previousInstalled
		} else {
			delete(state.Installed, manifest.ID)
		}
		return InstallResult{}, fmt.Errorf("commit plugin state: %w", err)
	}
	journal.Phase = "state_committed"
	if err := saveJournal(layout.Transaction, journal); err != nil {
		return InstallResult{}, err
	}
	failed = false
	if err := cleanupCommittedTransaction(layout, journal); err != nil {
		return InstallResult{}, err
	}
	return result, nil
}

// RecoverTransaction deterministically finishes cleanup or restores the old
// active directory after a crash between directory renames and state commit.
func RecoverTransaction(layout Layout, state *State) error {
	journal, err := loadJournal(layout.Transaction)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load plugin transaction: %w", err)
	}
	if err := validateJournal(layout, journal); err != nil {
		return err
	}
	if journal.Operation == "uninstall" {
		return recoverUninstallTransaction(layout, state, journal)
	}
	installed, stateCommitted := state.Installed[journal.PluginID]
	stateCommitted = stateCommitted && installedRevision(installed) == journal.NewVersion
	if journal.Phase == "state_committed" || (journal.Phase == "new_moved" && stateCommitted) {
		return cleanupCommittedTransaction(layout, journal)
	}
	active := filepath.Join(layout.Modules, journal.PluginID)
	if journal.Phase == "new_moved" {
		if err := safeRemoveAll(layout.Modules, active); err != nil {
			return fmt.Errorf("remove uncommitted active plugin: %w", err)
		}
	}
	if (journal.Phase == "old_moved" || journal.Phase == "new_moved") && journal.Backup != "" {
		backup, err := safeJoin(layout.Root, journal.Backup)
		if err != nil {
			return err
		}
		if _, err := os.Lstat(active); !errors.Is(err, os.ErrNotExist) {
			if err == nil {
				return errors.New("cannot restore plugin backup over an existing active directory")
			}
			return err
		}
		if err := os.Rename(backup, active); err != nil {
			return fmt.Errorf("restore plugin backup: %w", err)
		}
	}
	stageRoot := filepath.Join(layout.Staging, journal.StageName)
	if err := safeRemoveAll(layout.Staging, stageRoot); err != nil {
		return err
	}
	return os.Remove(layout.Transaction)
}

func manifestRevision(manifest Manifest) string {
	data, _ := json.Marshal(manifest)
	sum := sha256.Sum256(data)
	version := strings.SplitN(manifest.Version, "+", 2)[0]
	return version + "-rev." + hex.EncodeToString(sum[:8])
}

func installedRevision(installed InstalledState) string {
	if installed.ActiveRevision != "" {
		return installed.ActiveRevision
	}
	return installed.ActiveVersion
}

func cleanupCommittedTransaction(layout Layout, journal installJournal) error {
	stageRoot := filepath.Join(layout.Staging, journal.StageName)
	if err := safeRemoveAll(layout.Staging, stageRoot); err != nil {
		return err
	}
	if err := os.Remove(layout.Transaction); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func recoverUninstallTransaction(layout Layout, state *State, journal installJournal) error {
	_, stillInstalled := state.Installed[journal.PluginID]
	if journal.Phase == "state_committed" || (journal.Phase == "old_moved" && !stillInstalled) {
		return cleanupUninstallTransaction(layout, journal)
	}
	active := filepath.Join(layout.Modules, journal.PluginID)
	if journal.Phase == "old_moved" {
		backup, err := safeJoin(layout.Root, journal.Backup)
		if err != nil {
			return err
		}
		if _, err := os.Lstat(active); !errors.Is(err, os.ErrNotExist) {
			if err == nil {
				return errors.New("cannot restore uninstalled plugin over an existing active directory")
			}
			return err
		}
		if err := os.Rename(backup, active); err != nil {
			return fmt.Errorf("restore isolated plugin: %w", err)
		}
	}
	stageRoot := filepath.Join(layout.Staging, journal.StageName)
	if err := safeRemoveAll(layout.Staging, stageRoot); err != nil {
		return err
	}
	return os.Remove(layout.Transaction)
}

func cleanupUninstallTransaction(layout Layout, journal installJournal) error {
	stageRoot := filepath.Join(layout.Staging, journal.StageName)
	if err := safeRemoveAll(layout.Staging, stageRoot); err != nil {
		return err
	}
	versions := filepath.Join(layout.Versions, journal.PluginID)
	if err := safeRemoveAll(layout.Versions, versions); err != nil {
		return err
	}
	if err := os.Remove(layout.Transaction); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func saveJournal(path string, journal installJournal) error {
	if err := validateJournalShape(journal); err != nil {
		return err
	}
	data, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, append(data, '\n'))
}

func loadJournal(path string) (installJournal, error) {
	data, err := readFileBounded(path, 1<<20)
	if err != nil {
		return installJournal{}, err
	}
	var journal installJournal
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&journal); err != nil {
		return installJournal{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return installJournal{}, errors.New("plugin transaction contains trailing data")
	}
	if err := validateJournalShape(journal); err != nil {
		return installJournal{}, err
	}
	return journal, nil
}

func validateJournal(layout Layout, journal installJournal) error {
	if err := validateJournalShape(journal); err != nil {
		return err
	}
	stageRoot, err := safeJoin(layout.Staging, journal.StageName)
	if err != nil || filepath.Dir(stageRoot) != filepath.Clean(layout.Staging) {
		return errors.New("plugin transaction stage path is invalid")
	}
	operation := journal.Operation
	if operation == "" {
		operation = "install"
	}
	if operation == "uninstall" {
		if !containsExact([]string{"prepared", "old_moved", "state_committed"}, journal.Phase) || journal.OldVersion != journal.NewVersion {
			return errors.New("plugin uninstall transaction phase/version is invalid")
		}
		backup, err := safeJoin(layout.Root, journal.Backup)
		expected := filepath.Join(stageRoot, "removed")
		if err != nil || !strings.EqualFold(filepath.Clean(backup), filepath.Clean(expected)) {
			return errors.New("plugin uninstall backup path is invalid")
		}
		return nil
	}
	if !containsExact([]string{"prepared", "old_moved", "new_moved", "state_committed"}, journal.Phase) {
		return errors.New("plugin install transaction phase is invalid")
	}
	if journal.Phase == "old_moved" && journal.Backup == "" || journal.OldVersion != "" && journal.Phase != "prepared" && journal.Backup == "" {
		return errors.New("plugin install transaction lost its backup")
	}
	if journal.Backup != "" {
		backup, err := safeJoin(layout.Root, journal.Backup)
		if err != nil || journal.OldVersion == "" {
			return errors.New("plugin install backup path is invalid")
		}
		versionBackup := filepath.Join(layout.Versions, journal.PluginID, journal.OldVersion)
		stagingBackup := filepath.Join(stageRoot, "previous")
		if !strings.EqualFold(filepath.Clean(backup), filepath.Clean(versionBackup)) && !strings.EqualFold(filepath.Clean(backup), filepath.Clean(stagingBackup)) {
			return errors.New("plugin install backup path is invalid")
		}
	}
	return nil
}

func validateJournalShape(journal installJournal) error {
	if journal.SchemaVersion != installTransactionSchema || !idPattern.MatchString(journal.PluginID) || !versionPattern.MatchString(journal.NewVersion) || !stagePattern.MatchString(journal.StageName) {
		return errors.New("plugin transaction identity is invalid")
	}
	if journal.Operation != "" && journal.Operation != "install" && journal.Operation != "uninstall" {
		return errors.New("plugin transaction operation is invalid")
	}
	if journal.OldVersion != "" && !versionPattern.MatchString(journal.OldVersion) {
		return errors.New("plugin transaction old version is invalid")
	}
	if !containsExact([]string{"prepared", "old_moved", "new_moved", "state_committed"}, journal.Phase) {
		return errors.New("plugin transaction phase is invalid")
	}
	return nil
}
