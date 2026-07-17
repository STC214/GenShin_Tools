package resources

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type PlanAction string

const (
	ActionKeep    PlanAction = "keep"
	ActionInstall PlanAction = "install"
	ActionRepair  PlanAction = "repair"
)

type PlanItem struct {
	File   ManifestFile
	Action PlanAction
	Reason string
}

type RepairPlan struct {
	Items         []PlanItem
	DownloadBytes int64
}

func BuildRepairPlan(gameRoot string, manifest Manifest) (RepairPlan, error) {
	if err := manifest.Validate(); err != nil {
		return RepairPlan{}, err
	}
	var plan RepairPlan
	for _, file := range manifest.Files {
		target := filepath.Join(gameRoot, file.Path)
		if err := ensureContained(gameRoot, target); err != nil {
			return RepairPlan{}, err
		}
		err := VerifyFile(target, file.Size, file.Hash)
		item := PlanItem{File: file, Action: ActionKeep, Reason: "verified"}
		if err != nil {
			item.Action = ActionRepair
			item.Reason = err.Error()
			if errors.Is(findRootError(err), os.ErrNotExist) {
				item.Action = ActionInstall
			}
			plan.DownloadBytes += file.Size
		}
		plan.Items = append(plan.Items, item)
	}
	return plan, nil
}

func findRootError(err error) error {
	for {
		next := errors.Unwrap(err)
		if next == nil {
			return err
		}
		err = next
	}
}

func (p RepairPlan) Changes() []ManifestFile {
	files := make([]ManifestFile, 0, len(p.Items))
	for _, item := range p.Items {
		if item.Action != ActionKeep {
			files = append(files, item.File)
		}
	}
	return files
}

func (p RepairPlan) RequiredCommitBytes() uint64 {
	var total uint64
	for _, item := range p.Items {
		if item.Action != ActionKeep && item.File.Size > 0 {
			total += uint64(item.File.Size)
		}
	}
	return total
}

func (p RepairPlan) ValidateStaging(stagingRoot string) error {
	for _, item := range p.Items {
		if item.Action == ActionKeep {
			continue
		}
		path := filepath.Join(stagingRoot, item.File.Path)
		if err := ensureContained(stagingRoot, path); err != nil {
			return err
		}
		if err := VerifyFile(path, item.File.Size, item.File.Hash); err != nil {
			return fmt.Errorf("staged file %q is not verified: %w", item.File.Path, err)
		}
	}
	return nil
}
