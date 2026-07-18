package selfupdate

import (
	"context"
	"errors"
	"os"
)

type StartupUpdateStatus string

const (
	StartupUpdateNone      StartupUpdateStatus = "none"
	StartupUpdateRecovered StartupUpdateStatus = "recovered"
	StartupUpdateConfirmed StartupUpdateStatus = "confirmed"
	StartupUpdatePending   StartupUpdateStatus = "pending-confirmation"
)

// RecoverAtStartup repairs an interrupted update without making update health
// a prerequisite for launching the offline launcher. A committed transaction
// is confirmed only when the executable that started is the committed version.
func RecoverAtStartup(installRoot, currentVersion string) (StartupUpdateStatus, error) {
	layout, err := NewUpdateLayout(installRoot)
	if err != nil {
		return StartupUpdateNone, err
	}
	if err := layout.Ensure(); err != nil {
		return StartupUpdateNone, err
	}
	journal, err := loadJournal(layout.Journal)
	if errors.Is(err, os.ErrNotExist) {
		return StartupUpdateNone, nil
	}
	if err != nil {
		return StartupUpdateNone, err
	}
	if journal.Phase == "committed" || journal.Phase == "restarting" {
		if journal.Version != currentVersion {
			return StartupUpdatePending, nil
		}
		if err := ConfirmTransaction(layout, journal.Version, journal.ManifestSHA256); err != nil {
			return StartupUpdatePending, err
		}
		return StartupUpdateConfirmed, nil
	}
	if err := RecoverTransaction(context.Background(), layout); err != nil {
		return StartupUpdateNone, err
	}
	return StartupUpdateRecovered, nil
}
