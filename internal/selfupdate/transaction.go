package selfupdate

import (
	"bytes"
	"context"
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

const (
	transactionSchemaVersion = 1
	maxJournalBytes          = 1 << 20
)

type UpdateLayout struct {
	InstallRoot string
	Root        string
	Versions    string
	Backups     string
	Downloads   string
	Runner      string
	Journal     string
}

type transactionJournal struct {
	SchemaVersion  int               `json:"schemaVersion"`
	Version        string            `json:"version"`
	ManifestSHA256 string            `json:"manifestSha256"`
	Phase          string            `json:"phase"`
	Files          []transactionFile `json:"files"`
}

type transactionFile struct {
	Path        string `json:"path"`
	HadOriginal bool   `json:"hadOriginal"`
	OldSize     int64  `json:"oldSize,omitempty"`
	OldSHA256   string `json:"oldSha256,omitempty"`
}

type CommitHooks struct {
	BeforeReplace func(index int, relative string) error
}

func NewUpdateLayout(installRoot string) (UpdateLayout, error) {
	installRoot, err := filepath.Abs(installRoot)
	if err != nil {
		return UpdateLayout{}, err
	}
	root := filepath.Join(installRoot, "data", "updates")
	return UpdateLayout{InstallRoot: installRoot, Root: root, Versions: filepath.Join(root, "versions"), Backups: filepath.Join(root, "backups"), Downloads: filepath.Join(root, "downloads"), Runner: filepath.Join(root, "runner"), Journal: filepath.Join(root, "transaction.json")}, nil
}

func (layout UpdateLayout) Ensure() error {
	if err := validateUpdateLayout(layout); err != nil {
		return err
	}
	info, err := os.Stat(layout.InstallRoot)
	if err != nil || !info.IsDir() {
		return errors.New("update install root must be an existing directory")
	}
	if err := rejectReparse(layout.InstallRoot); err != nil {
		return fmt.Errorf("update install root: %w", err)
	}
	for _, directory := range []string{layout.Root, layout.Versions, layout.Backups, layout.Downloads, layout.Runner} {
		if err := rejectReparseWithin(layout.InstallRoot, directory); err != nil {
			return fmt.Errorf("update directory %s: %w", directory, err)
		}
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return err
		}
		if err := rejectReparseWithin(layout.InstallRoot, directory); err != nil {
			return fmt.Errorf("update directory %s: %w", directory, err)
		}
	}
	return nil
}

func validateUpdateLayout(layout UpdateLayout) error {
	want, err := NewUpdateLayout(layout.InstallRoot)
	if err != nil {
		return err
	}
	for _, pair := range [][2]string{{layout.InstallRoot, want.InstallRoot}, {layout.Root, want.Root}, {layout.Versions, want.Versions}, {layout.Backups, want.Backups}, {layout.Downloads, want.Downloads}, {layout.Runner, want.Runner}, {layout.Journal, want.Journal}} {
		if !strings.EqualFold(filepath.Clean(pair[0]), filepath.Clean(pair[1])) {
			return errors.New("update layout does not match the install root")
		}
	}
	return nil
}

type TransactionStatus struct {
	Version        string
	ManifestSHA256 string
	Phase          string
}

func PendingTransaction(layout UpdateLayout) (TransactionStatus, bool, error) {
	if err := validateUpdateLayout(layout); err != nil {
		return TransactionStatus{}, false, err
	}
	journal, err := loadJournal(layout.Journal)
	if errors.Is(err, os.ErrNotExist) {
		return TransactionStatus{}, false, nil
	}
	if err != nil {
		return TransactionStatus{}, false, err
	}
	return TransactionStatus{Version: journal.Version, ManifestSHA256: journal.ManifestSHA256, Phase: journal.Phase}, true, nil
}

func CommitStaged(ctx context.Context, layout UpdateLayout, version, manifestSHA256 string, hooks *CommitHooks) error {
	if err := layout.Ensure(); err != nil {
		return err
	}
	if _, err := os.Lstat(layout.Journal); err == nil {
		return errors.New("an update transaction already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	stagedDirectory := filepath.Join(layout.Versions, version)
	manifest, err := VerifyStaged(ctx, stagedDirectory, version, manifestSHA256)
	if err != nil {
		return err
	}
	journal := transactionJournal{SchemaVersion: transactionSchemaVersion, Version: version, ManifestSHA256: manifestSHA256, Phase: "prepared", Files: make([]transactionFile, len(manifest.Files))}
	for index, file := range manifest.Files {
		journal.Files[index].Path = file.Path
	}
	if err := saveJournal(layout.Journal, journal); err != nil {
		return err
	}
	backupDirectory := filepath.Join(layout.Backups, version)
	if err := os.Mkdir(backupDirectory, 0o755); err != nil {
		return abandonAfterFailure(layout, journal, err)
	}
	journal.Phase = "backing-up"
	if err := saveJournal(layout.Journal, journal); err != nil {
		return abandonAfterFailure(layout, journal, err)
	}
	for index, file := range manifest.Files {
		if err := ctx.Err(); err != nil {
			return abandonAfterFailure(layout, journal, err)
		}
		target, err := safeInstallPath(layout.InstallRoot, file.Path)
		if err != nil {
			return abandonAfterFailure(layout, journal, err)
		}
		info, err := os.Lstat(target)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() {
			return abandonAfterFailure(layout, journal, fmt.Errorf("installed update target %q is not a regular file", file.Path))
		}
		if err := rejectReparseWithin(layout.InstallRoot, target); err != nil {
			return abandonAfterFailure(layout, journal, err)
		}
		backup := filepath.Join(backupDirectory, filepath.FromSlash(file.Path))
		size, digest, err := copyVerified(target, backup, "", 0)
		if err != nil {
			return abandonAfterFailure(layout, journal, fmt.Errorf("back up %q: %w", file.Path, err))
		}
		journal.Files[index].HadOriginal, journal.Files[index].OldSize, journal.Files[index].OldSHA256 = true, size, digest
	}
	journal.Phase = "backed-up"
	if err := saveJournal(layout.Journal, journal); err != nil {
		return abandonAfterFailure(layout, journal, err)
	}
	journal.Phase = "committing"
	if err := saveJournal(layout.Journal, journal); err != nil {
		return abandonAfterFailure(layout, journal, err)
	}
	for index, file := range manifest.Files {
		if err := ctx.Err(); err != nil {
			return rollbackAfterFailure(layout, journal, err)
		}
		if hooks != nil && hooks.BeforeReplace != nil {
			if err := hooks.BeforeReplace(index, file.Path); err != nil {
				return rollbackAfterFailure(layout, journal, err)
			}
		}
		source := filepath.Join(stagedDirectory, filepath.FromSlash(file.Path))
		target, err := safeInstallPath(layout.InstallRoot, file.Path)
		if err != nil {
			return rollbackAfterFailure(layout, journal, err)
		}
		if err := rejectReparseWithin(layout.InstallRoot, filepath.Dir(target)); err != nil {
			return rollbackAfterFailure(layout, journal, err)
		}
		if err := replaceFromSource(source, target, file.Size, file.SHA256); err != nil {
			return rollbackAfterFailure(layout, journal, err)
		}
		if err := verifyPackageFile(target, file); err != nil {
			return rollbackAfterFailure(layout, journal, err)
		}
	}
	journal.Phase = "committed"
	return saveJournal(layout.Journal, journal)
}

func RecoverTransaction(ctx context.Context, layout UpdateLayout) error {
	if err := validateUpdateLayout(layout); err != nil {
		return err
	}
	journal, err := loadJournal(layout.Journal)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	switch journal.Phase {
	case "prepared", "backing-up":
		return abandonUncommitted(layout, journal)
	case "backed-up", "committing":
		return rollbackTransaction(ctx, layout, journal)
	case "committed":
		return nil
	default:
		return errors.New("update transaction has an invalid phase")
	}
}

func ConfirmTransaction(layout UpdateLayout, version, manifestSHA256 string) error {
	if err := validateUpdateLayout(layout); err != nil {
		return err
	}
	journal, err := loadJournal(layout.Journal)
	if err != nil {
		return err
	}
	if (journal.Phase != "committed" && journal.Phase != "restarting") || journal.Version != version || journal.ManifestSHA256 != manifestSHA256 {
		return errors.New("update confirmation does not match the committed transaction")
	}
	stagedDirectory := filepath.Join(layout.Versions, version)
	manifest, err := VerifyStaged(context.Background(), stagedDirectory, version, manifestSHA256)
	if err != nil {
		return fmt.Errorf("verify committed update staging: %w", err)
	}
	for _, file := range manifest.Files {
		target, err := safeInstallPath(layout.InstallRoot, file.Path)
		if err != nil {
			return err
		}
		if err := rejectReparseWithin(layout.InstallRoot, target); err != nil {
			return err
		}
		if err := verifyPackageFile(target, file); err != nil {
			return fmt.Errorf("verify installed update file %q: %w", file.Path, err)
		}
	}
	if err := safeRemoveTree(layout.Backups, filepath.Join(layout.Backups, version)); err != nil {
		return err
	}
	if err := safeRemoveTree(layout.Versions, filepath.Join(layout.Versions, version)); err != nil {
		return err
	}
	return os.Remove(layout.Journal)
}

func MarkTransactionRestarting(layout UpdateLayout, version, manifestSHA256 string) error {
	if err := validateUpdateLayout(layout); err != nil {
		return err
	}
	journal, err := loadJournal(layout.Journal)
	if err != nil {
		return err
	}
	if journal.Phase != "committed" || journal.Version != version || journal.ManifestSHA256 != manifestSHA256 {
		return errors.New("update restart marker does not match the committed transaction")
	}
	journal.Phase = "restarting"
	return saveJournal(layout.Journal, journal)
}

func RollbackCommitted(ctx context.Context, layout UpdateLayout, version, manifestSHA256 string) error {
	if err := validateUpdateLayout(layout); err != nil {
		return err
	}
	journal, err := loadJournal(layout.Journal)
	if err != nil {
		return err
	}
	if (journal.Phase != "committed" && journal.Phase != "restarting") || journal.Version != version || journal.ManifestSHA256 != manifestSHA256 {
		return errors.New("update rollback does not match the committed transaction")
	}
	return rollbackTransaction(ctx, layout, journal)
}

func rollbackAfterFailure(layout UpdateLayout, journal transactionJournal, cause error) error {
	if rollbackErr := rollbackTransaction(context.Background(), layout, journal); rollbackErr != nil {
		return errors.Join(cause, fmt.Errorf("update rollback: %w", rollbackErr))
	}
	return cause
}

func abandonAfterFailure(layout UpdateLayout, journal transactionJournal, cause error) error {
	if cleanupErr := abandonUncommitted(layout, journal); cleanupErr != nil {
		return errors.Join(cause, fmt.Errorf("abandon update transaction: %w", cleanupErr))
	}
	return cause
}

func rollbackTransaction(ctx context.Context, layout UpdateLayout, journal transactionJournal) error {
	backupDirectory := filepath.Join(layout.Backups, journal.Version)
	for index := len(journal.Files) - 1; index >= 0; index-- {
		if err := ctx.Err(); err != nil {
			return err
		}
		file := journal.Files[index]
		target, err := safeInstallPath(layout.InstallRoot, file.Path)
		if err != nil {
			return err
		}
		if file.HadOriginal {
			backup := filepath.Join(backupDirectory, filepath.FromSlash(file.Path))
			if err := rejectReparseWithin(layout.InstallRoot, filepath.Dir(target)); err != nil {
				return err
			}
			if err := replaceFromSource(backup, target, file.OldSize, file.OldSHA256); err != nil {
				return fmt.Errorf("restore %q: %w", file.Path, err)
			}
		} else if err := removeInstalledFile(layout.InstallRoot, target); err != nil {
			return fmt.Errorf("remove new file %q: %w", file.Path, err)
		}
	}
	return abandonUncommitted(layout, journal)
}

func abandonUncommitted(layout UpdateLayout, journal transactionJournal) error {
	if err := safeRemoveTree(layout.Backups, filepath.Join(layout.Backups, journal.Version)); err != nil {
		return err
	}
	return os.Remove(layout.Journal)
}

func saveJournal(path string, journal transactionJournal) error {
	if err := validateJournal(journal); err != nil {
		return err
	}
	data, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteJournal(path, append(data, '\n'))
}

func atomicWriteJournal(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".update-journal-*.tmp")
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

func loadJournal(path string) (transactionJournal, error) {
	input, err := os.Open(path)
	if err != nil {
		return transactionJournal{}, err
	}
	defer input.Close()
	data, err := io.ReadAll(io.LimitReader(input, maxJournalBytes+1))
	if err != nil {
		return transactionJournal{}, err
	}
	if len(data) > maxJournalBytes {
		return transactionJournal{}, errors.New("update transaction journal exceeds 1 MiB")
	}
	var journal transactionJournal
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&journal); err != nil {
		return transactionJournal{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return transactionJournal{}, errors.New("update transaction journal contains trailing JSON")
	}
	if err := validateJournal(journal); err != nil {
		return transactionJournal{}, err
	}
	return journal, nil
}

func validateJournal(journal transactionJournal) error {
	if journal.SchemaVersion != transactionSchemaVersion || !shaPattern.MatchString(journal.ManifestSHA256) {
		return errors.New("update transaction schema or manifest hash is invalid")
	}
	if _, ok := parseVersion(journal.Version); !ok {
		return errors.New("update transaction version is invalid")
	}
	if journal.Phase != "prepared" && journal.Phase != "backing-up" && journal.Phase != "backed-up" && journal.Phase != "committing" && journal.Phase != "committed" && journal.Phase != "restarting" {
		return errors.New("update transaction phase is invalid")
	}
	if len(journal.Files) == 0 || len(journal.Files) > maxPackageFiles {
		return errors.New("update transaction file count is invalid")
	}
	seen := make(map[string]bool, len(journal.Files))
	for _, file := range journal.Files {
		name, err := safeReleasePath(file.Path)
		key := strings.ToLower(name)
		if err != nil || !allowedReleasePath(name) || seen[key] {
			return errors.New("update transaction contains an invalid file path")
		}
		seen[key] = true
		if file.HadOriginal {
			if file.OldSize <= 0 || file.OldSize > maxPackageFileBytes || !shaPattern.MatchString(file.OldSHA256) {
				return errors.New("update transaction contains invalid backup metadata")
			}
		} else if file.OldSize != 0 || file.OldSHA256 != "" {
			return errors.New("update transaction contains unexpected backup metadata")
		}
	}
	for _, required := range []string{"build-info.json", "genshintools-injector.exe", "genshintools-updater.exe", "genshintools.exe", "license_policy.md", "third_party_notices.md"} {
		if !seen[required] {
			return fmt.Errorf("update transaction is missing required file %q", required)
		}
	}
	return nil
}

func safeInstallPath(root, relative string) (string, error) {
	name, err := safeReleasePath(relative)
	if err != nil || !allowedReleasePath(name) {
		return "", errors.New("update path is not installable")
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return "", err
	}
	target, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("update path escapes install root")
	}
	return target, nil
}

func replaceFromSource(source, target string, size int64, digest string) error {
	if err := rejectReparse(source); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), ".update-replace-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	_ = temporary.Close()
	if err := os.Remove(temporaryPath); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if _, _, err := copyVerified(source, temporaryPath, digest, size); err != nil {
		return err
	}
	if err := replaceFile(temporaryPath, target); err != nil {
		return err
	}
	committed = true
	return nil
}

func copyVerified(source, destination, expectedDigest string, expectedSize int64) (int64, string, error) {
	input, err := os.Open(source)
	if err != nil {
		return 0, "", err
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return 0, "", err
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, "", err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(output, hash), input)
	digest := hex.EncodeToString(hash.Sum(nil))
	if copyErr == nil && expectedSize > 0 && written != expectedSize {
		copyErr = errors.New("update file size changed during transaction")
	}
	if copyErr == nil && expectedDigest != "" && digest != expectedDigest {
		copyErr = errors.New("update file SHA-256 changed during transaction")
	}
	if copyErr == nil {
		copyErr = output.Sync()
	}
	if closeErr := output.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		_ = os.Remove(destination)
	}
	return written, digest, copyErr
}

func removeInstalledFile(root, target string) error {
	if err := rejectReparseWithin(root, target); err != nil {
		return err
	}
	info, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("refusing to remove non-regular update target")
	}
	return os.Remove(target)
}

func safeRemoveTree(root, target string) error {
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
		return errors.New("refusing to remove update path outside its root")
	}
	if _, err := os.Lstat(target); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if err := filepath.WalkDir(target, func(filePath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("refusing to remove update tree containing a symbolic link")
		}
		return rejectReparse(filePath)
	}); err != nil {
		return err
	}
	return os.RemoveAll(target)
}
