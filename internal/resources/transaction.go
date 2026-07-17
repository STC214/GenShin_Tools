package resources

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const journalSchemaVersion = 1

type journal struct {
	SchemaVersion int            `json:"schema_version"`
	ID            string         `json:"id"`
	GameRoot      string         `json:"game_root"`
	State         string         `json:"state"`
	Entries       []journalEntry `json:"entries"`
}

type journalEntry struct {
	RelativePath string `json:"relative_path"`
	SourcePath   string `json:"source_path,omitempty"`
	Source       string `json:"source,omitempty"`
	Target       string `json:"target"`
	Temporary    string `json:"temporary"`
	Backup       string `json:"backup"`
	HadOriginal  bool   `json:"had_original"`
	State        string `json:"state"`
}

type Transaction struct {
	ID          string
	GameRoot    string
	Root        string
	StagingRoot string
	journalPath string
	journal     journal
}

func NewTransaction(dataStagingRoot, gameRoot, id string) (*Transaction, error) {
	if strings.TrimSpace(id) == "" || strings.ContainsAny(id, `\/:`) || id == "." || id == ".." {
		return nil, errors.New("invalid transaction ID")
	}
	gameRoot, err := filepath.Abs(gameRoot)
	if err != nil {
		return nil, err
	}
	root, err := filepath.Abs(filepath.Join(dataStagingRoot, id))
	if err != nil {
		return nil, err
	}
	if err := ensureContained(dataStagingRoot, root); err != nil {
		return nil, err
	}
	t := &Transaction{
		ID: id, GameRoot: filepath.Clean(gameRoot), Root: root,
		StagingRoot: filepath.Join(root, "files"), journalPath: filepath.Join(root, "transaction.json"),
	}
	t.journal = journal{SchemaVersion: journalSchemaVersion, ID: id, GameRoot: t.GameRoot, State: "prepared"}
	return t, nil
}

func (t *Transaction) Prepare() error {
	if err := os.MkdirAll(t.StagingRoot, 0o755); err != nil {
		return fmt.Errorf("create transaction staging: %w", err)
	}
	return t.saveJournal()
}

func (t *Transaction) MarkPreloaded() error {
	t.journal.State = "preloaded"
	return t.saveJournal()
}

func (t *Transaction) Abort() error {
	if err := rollbackJournal(&t.journal); err != nil {
		return err
	}
	return os.RemoveAll(t.Root)
}

func (t *Transaction) Commit(plan RepairPlan) error {
	if err := plan.ValidateStaging(t.StagingRoot); err != nil {
		return err
	}
	if err := t.validateMoves(plan); err != nil {
		return err
	}
	if err := RequireDiskSpace(t.GameRoot, plan.RequiredCommitBytes()); err != nil {
		return err
	}
	t.journal.State = "committing"
	if err := t.saveJournal(); err != nil {
		return err
	}
	for _, item := range plan.Items {
		if item.Action == ActionKeep {
			continue
		}
		if err := t.install(item); err != nil {
			rollbackErr := rollbackJournal(&t.journal)
			t.journal.State = "rolled_back"
			_ = t.saveJournal()
			if rollbackErr == nil {
				_ = os.RemoveAll(t.Root)
			}
			if rollbackErr != nil {
				return errors.Join(err, fmt.Errorf("rollback: %w", rollbackErr))
			}
			return err
		}
	}
	t.journal.State = "complete"
	if err := t.saveJournal(); err != nil {
		return err
	}
	if err := cleanupJournal(&t.journal); err != nil {
		return fmt.Errorf("commit complete but cleanup failed: %w", err)
	}
	if err := os.RemoveAll(t.Root); err != nil {
		return fmt.Errorf("remove completed transaction: %w", err)
	}
	return nil
}

func (t *Transaction) install(item PlanItem) error {
	file, action := item.File, item.Action
	target := filepath.Join(t.GameRoot, file.Path)
	if err := ensureContained(t.GameRoot, target); err != nil {
		return err
	}
	if err := rejectReparseAncestors(t.GameRoot, filepath.Dir(target)); err != nil {
		return fmt.Errorf("unsafe target %q: %w", file.Path, err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	entry := journalEntry{
		RelativePath: file.Path,
		Target:       target,
		Temporary:    target + ".genshintools-" + t.ID + ".new",
		Backup:       target + ".genshintools-" + t.ID + ".bak",
		State:        "prepared",
	}
	if action == ActionMove {
		entry.SourcePath = item.SourcePath
		entry.Source = filepath.Join(t.GameRoot, item.SourcePath)
	}
	_, err := os.Stat(target)
	entry.HadOriginal = err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	t.journal.Entries = append(t.journal.Entries, entry)
	index := len(t.journal.Entries) - 1
	if err := t.saveJournal(); err != nil {
		return err
	}
	if action == ActionMove {
		if err := os.Rename(entry.Source, entry.Target); err != nil {
			return fmt.Errorf("move %q to %q: %w", item.SourcePath, file.Path, err)
		}
		t.journal.Entries[index].State = "installed"
		return t.saveJournal()
	}
	if action == ActionDelete {
		if !entry.HadOriginal {
			t.journal.Entries[index].State = "installed"
			return t.saveJournal()
		}
		if err := os.Rename(entry.Target, entry.Backup); err != nil {
			return fmt.Errorf("back up deleted target %q: %w", file.Path, err)
		}
		t.journal.Entries[index].State = "installed"
		return t.saveJournal()
	}
	staged := filepath.Join(t.StagingRoot, file.Path)
	if err := copyVerified(staged, entry.Temporary, file); err != nil {
		return fmt.Errorf("prepare target file %q: %w", file.Path, err)
	}
	if entry.HadOriginal {
		if err := os.Rename(entry.Target, entry.Backup); err != nil {
			return fmt.Errorf("back up target %q: %w", file.Path, err)
		}
		t.journal.Entries[index].State = "backed_up"
		if err := t.saveJournal(); err != nil {
			return err
		}
	}
	t.journal.Entries[index].State = "installing"
	if err := t.saveJournal(); err != nil {
		return err
	}
	if err := os.Rename(entry.Temporary, entry.Target); err != nil {
		return fmt.Errorf("install target %q: %w", file.Path, err)
	}
	t.journal.Entries[index].State = "installed"
	if err := t.saveJournal(); err != nil {
		return err
	}
	return nil
}

func (t *Transaction) validateMoves(plan RepairPlan) error {
	seenTargets := make(map[string]struct{})
	for _, item := range plan.Items {
		if item.Action != ActionMove {
			continue
		}
		sourceRelative, err := NormalizeRelativePath(item.SourcePath)
		if err != nil {
			return fmt.Errorf("invalid move source: %w", err)
		}
		targetRelative, err := NormalizeRelativePath(item.File.Path)
		if err != nil {
			return fmt.Errorf("invalid move target: %w", err)
		}
		if strings.EqualFold(sourceRelative, targetRelative) {
			return errors.New("move source and target are identical")
		}
		key := strings.ToLower(targetRelative)
		if _, duplicate := seenTargets[key]; duplicate {
			return fmt.Errorf("duplicate move target %q", targetRelative)
		}
		seenTargets[key] = struct{}{}
		source, target := filepath.Join(t.GameRoot, sourceRelative), filepath.Join(t.GameRoot, targetRelative)
		if err := ensureContained(t.GameRoot, source); err != nil {
			return err
		}
		if err := ensureContained(t.GameRoot, target); err != nil {
			return err
		}
		if err := rejectReparseAncestors(t.GameRoot, source); err != nil {
			return fmt.Errorf("unsafe move source %q: %w", sourceRelative, err)
		}
		if _, err := os.Stat(source); err != nil {
			return fmt.Errorf("move source %q: %w", sourceRelative, err)
		}
		if _, err := os.Stat(target); err == nil {
			return fmt.Errorf("move target %q already exists", targetRelative)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func copyVerified(source, destination string, file ManifestFile) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	syncErr := output.Sync()
	closeErr := output.Close()
	if copyErr != nil || syncErr != nil || closeErr != nil {
		_ = os.Remove(destination)
		return errors.Join(copyErr, syncErr, closeErr)
	}
	if err := VerifyFile(destination, file.Size, file.Hash); err != nil {
		_ = os.Remove(destination)
		return err
	}
	return nil
}

func RecoverTransactions(dataStagingRoot string) error {
	entries, err := os.ReadDir(dataStagingRoot)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var result error
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(dataStagingRoot, entry.Name(), "transaction.json")
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			result = errors.Join(result, err)
			continue
		}
		var saved journal
		decoder := json.NewDecoder(strings.NewReader(string(data)))
		decoder.DisallowUnknownFields()
		decoderErr := decoder.Decode(&saved)
		if decoderErr != nil || saved.SchemaVersion != journalSchemaVersion || validateJournal(&saved) != nil {
			result = errors.Join(result, fmt.Errorf("cannot recover %s: invalid journal", path))
			continue
		}
		if saved.State == "preloaded" {
			continue
		}
		if saved.State == "complete" {
			err = cleanupJournal(&saved)
		} else {
			err = rollbackJournal(&saved)
		}
		if err == nil {
			err = os.RemoveAll(filepath.Dir(path))
		}
		result = errors.Join(result, err)
	}
	return result
}

func rollbackJournal(saved *journal) error {
	var result error
	for i := len(saved.Entries) - 1; i >= 0; i-- {
		entry := saved.Entries[i]
		if entry.Source != "" {
			if _, err := os.Stat(entry.Target); err == nil {
				if _, sourceErr := os.Stat(entry.Source); sourceErr == nil {
					result = errors.Join(result, fmt.Errorf("cannot restore move %s: source already exists", entry.SourcePath))
				} else if !errors.Is(sourceErr, os.ErrNotExist) {
					result = errors.Join(result, sourceErr)
				} else if err := os.Rename(entry.Target, entry.Source); err != nil {
					result = errors.Join(result, err)
				}
			} else if !errors.Is(err, os.ErrNotExist) {
				result = errors.Join(result, err)
			}
			result = errors.Join(result, removeIfExists(entry.Temporary))
			continue
		}
		if entry.HadOriginal {
			if _, err := os.Stat(entry.Backup); err == nil {
				_ = os.Remove(entry.Target)
				if err := os.Rename(entry.Backup, entry.Target); err != nil {
					result = errors.Join(result, err)
				}
			}
		} else if entry.State == "installing" || entry.State == "installed" {
			result = errors.Join(result, removeIfExists(entry.Target))
		}
		result = errors.Join(result, removeIfExists(entry.Temporary))
	}
	return result
}

func validateJournal(saved *journal) error {
	if saved.ID == "" || strings.ContainsAny(saved.ID, `\/:`) || saved.GameRoot == "" {
		return errors.New("invalid journal identity")
	}
	for _, entry := range saved.Entries {
		relative, err := NormalizeRelativePath(entry.RelativePath)
		if err != nil {
			return err
		}
		target := filepath.Join(saved.GameRoot, relative)
		if err := ensureContained(saved.GameRoot, target); err != nil {
			return err
		}
		if filepath.Clean(entry.Target) != filepath.Clean(target) || filepath.Clean(entry.Temporary) != filepath.Clean(target+".genshintools-"+saved.ID+".new") || filepath.Clean(entry.Backup) != filepath.Clean(target+".genshintools-"+saved.ID+".bak") {
			return errors.New("journal paths do not match transaction identity")
		}
		if entry.SourcePath != "" {
			sourceRelative, err := NormalizeRelativePath(entry.SourcePath)
			if err != nil {
				return err
			}
			source := filepath.Join(saved.GameRoot, sourceRelative)
			if err := ensureContained(saved.GameRoot, source); err != nil || filepath.Clean(entry.Source) != filepath.Clean(source) {
				return errors.New("journal move source does not match transaction identity")
			}
		} else if entry.Source != "" {
			return errors.New("journal move source is incomplete")
		}
	}
	return nil
}

func cleanupJournal(saved *journal) error {
	var result error
	for _, entry := range saved.Entries {
		result = errors.Join(result, removeIfExists(entry.Backup), removeIfExists(entry.Temporary))
	}
	return result
}

func removeIfExists(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (t *Transaction) saveJournal() error {
	data, err := json.MarshalIndent(t.journal, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temporary := t.journalPath + fmt.Sprintf(".%d.tmp", time.Now().UnixNano())
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		_ = os.Remove(temporary)
		return errors.Join(writeErr, syncErr, closeErr)
	}
	if err := replaceFile(temporary, t.journalPath); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}
